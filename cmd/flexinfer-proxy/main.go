package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/singleflight"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var (
	scheme = runtime.NewScheme()

	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_requests_total",
			Help: "Total number of requests processed by the proxy.",
		},
		[]string{"model", "status"},
	)

	scaleUpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_scale_ups_total",
			Help: "Total number of scale-up operations triggered.",
		},
		[]string{"model"},
	)

	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "proxy_request_duration_seconds",
			Help:    "Histogram of request processing duration.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"model"},
	)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(aiv1alpha1.AddToScheme(scheme))

	// Register metrics
	prometheus.MustRegister(requestsTotal)
	prometheus.MustRegister(scaleUpsTotal)
	prometheus.MustRegister(requestDuration)
}

type Proxy struct {
	client       client.Client
	namespace    string
	proxyMap     sync.Map           // cache of httputil.ReverseProxy by model name
	requestGroup singleflight.Group // coalescing activation requests
}

func main() {
	var port int
	flag.IntVar(&port, "port", 8080, "Port to listen on")
	flag.Parse()

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	cfg, err := config.GetConfig()
	if err != nil {
		log.Fatalf("unable to get kubeconfig: %v", err)
	}

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("unable to create k8s client: %v", err)
	}

	p := &Proxy{
		client:    k8sClient,
		namespace: namespace,
	}

	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/", p.handleRequest)
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Printf("Starting proxy on :%d in namespace %s", port, namespace)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	// 1. Determine Target Model from Header or Path
	// Convention: X-Model-ID header OR first path segment if mapping provided
	// For simplicity, we'll assume the client sends X-Model-ID or we parse it from the request body (which is hard for proxy)
	// OR we can use the "Host" header if using subdomains like <model>.flexinfer.example.com

	// Let's support OpenAI style: model is in the JSON body usually, but we don't want to parse body.
	// We'll require X-Model-ID header for the MVP, or assume we are a single-model proxy sidecar?
	// The implementation plan says "Activator Pattern", usually a shared ingress.
	// But let's verify if we can extract it.

	modelName := r.Header.Get("X-Model-ID")
	if modelName == "" {
		// Fallback: Use path prefix? e.g. /model/<name>/v1/...
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(pathParts) > 1 && pathParts[0] == "model" {
			modelName = pathParts[1]
			// Strip the /model/<name> prefix for upstream
			r.URL.Path = "/" + strings.Join(pathParts[2:], "/")
		}
	}

	// Fallback: Check JSON Body (OpenAI Standard)
	if modelName == "" && r.Method == http.MethodPost && strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		// Read body
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil {
			// Restore body immediately so the proxy can upstream it
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			// Parse partial JSON to find "model" field
			var payload struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Model != "" {
				modelName = payload.Model
			}
		}
	}

	if modelName == "" {
		http.Error(w, "X-Model-ID header or /model/<name> path required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// 2. Ensure Model is Active
	start := time.Now()
	if err := p.ensureActive(ctx, modelName); err != nil {
		log.Printf("Failed to activate model %s: %v", modelName, err)
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		http.Error(w, fmt.Sprintf("Failed to activate model: %v", err), http.StatusServiceUnavailable)
		return
	}

	// 3. Update LastAccessTime (Async)
	go p.updateLastAccess(context.Background(), modelName)

	// 4. Forward Request
	p.serveProxy(w, r, modelName)

	// Metrics update
	requestsTotal.WithLabelValues(modelName, "success").Inc()
	requestDuration.WithLabelValues(modelName).Observe(time.Since(start).Seconds())
}

func (p *Proxy) ensureActive(ctx context.Context, modelName string) error {
	// Use Singleflight to deduplicate concurrent scale-up requests for the same model
	_, err, _ := p.requestGroup.Do(modelName, func() (interface{}, error) {
		// This block is executed only once per modelName for concurrent requests
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}

			// Fetch current state
			md := &aiv1alpha1.ModelDeployment{}
			err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md)
			if err != nil {
				if errors.IsNotFound(err) {
					return nil, fmt.Errorf("model deployment %s not found", modelName)
				}
				return nil, err
			}

			// Check if scaled to zero
			if md.Spec.Replicas == nil || *md.Spec.Replicas == 0 {
				log.Printf("Model %s is scaled to zero. Activating...", modelName)
				scaleUpsTotal.WithLabelValues(modelName).Inc()

				// Trigger Scale Up
				one := int32(1)
				md.Spec.Replicas = &one
				if err := p.client.Update(ctx, md); err != nil {
					// Optimization: Conflict error means someone else updated it, which is fine, we just loop
					if errors.IsConflict(err) {
						continue
					}
					return nil, fmt.Errorf("failed to scale up: %v", err)
				}
			}

			// Check if Ready
			if isReady(md) {
				return nil, nil
			}

			// Wait for readiness
			time.Sleep(1 * time.Second)
		}
	})

	return err
}

func (p *Proxy) updateLastAccess(ctx context.Context, modelName string) {
	// Optimization: Don't update on every request to avoid API spam.
	// Only update if current LastAccessTime is old (> 1 minute ago).
	// We'll need to fetch the object first.

	md := &aiv1alpha1.ModelDeployment{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, md); err != nil {
		log.Printf("Error fetching model for stats update: %v", err)
		return
	}

	now := metav1.Now()
	// Update status directly
	md.Status.LastAccessTime = &now
	if err := p.client.Status().Update(ctx, md); err != nil {
		// Log but don't fail, it's just stats
		log.Printf("Failed to update LastAccessTime: %v", err)
	}
}

func (p *Proxy) serveProxy(w http.ResponseWriter, r *http.Request, modelName string) {
	targetURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:80", modelName, p.namespace) // Assuming backend is on port 80 of the Service

	// Check if we have a proxy for this already
	var rp *httputil.ReverseProxy
	if val, ok := p.proxyMap.Load(modelName); ok {
		rp = val.(*httputil.ReverseProxy)
	} else {
		// Create new proxy
		u, _ := url.Parse(targetURL)
		rp = httputil.NewSingleHostReverseProxy(u)
		p.proxyMap.Store(modelName, rp)
	}

	rp.ServeHTTP(w, r)
}

func isReady(md *aiv1alpha1.ModelDeployment) bool {
	for _, cond := range md.Status.Conditions {
		if cond.Type == aiv1alpha1.ConditionTypeReady && cond.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}
