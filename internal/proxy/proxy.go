package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Proxy operational constants
const (
	// staleQueueCleanupInterval is how often stale queues are checked and removed.
	staleQueueCleanupInterval = 30 * time.Second

	// endpointWatchInterval is how often the endpoint watcher refreshes pod addresses.
	endpointWatchInterval = 10 * time.Second

	// serviceLabelCacheTTL is how long the service label cache is valid before refresh.
	serviceLabelCacheTTL = 5 * time.Second

	// lastAccessThrottleInterval prevents excessive K8s API calls by skipping
	// LastAccessTime updates if the last update was within this duration.
	lastAccessThrottleInterval = 1 * time.Minute

	// defaultBackendPort is used when a model's backend port cannot be determined.
	defaultBackendPort int32 = 8000

	// gpuGroupMinTimeout is the minimum timeout for GPUGroup model swaps.
	gpuGroupMinTimeout = 120 * time.Second

	// gpuGroupPollInterval is the polling interval when waiting for GPUGroup activation.
	gpuGroupPollInterval = 1 * time.Second

	// readyPollInterval is the polling interval when waiting for a model to become ready.
	readyPollInterval = 1 * time.Second
)

// Scheme is the runtime scheme used by the proxy for K8s API types.
var Scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(Scheme))
	utilruntime.Must(aiv1alpha1.AddToScheme(Scheme))
	utilruntime.Must(aiv1alpha2.AddToScheme(Scheme))
}

// Config holds all proxy configuration.
type Config struct {
	Namespace                        string
	Client                           client.Client
	MaxQueueSize                     int
	QueueTimeout                     time.Duration
	ColdStartTimeout                 time.Duration
	RoutingEnabled                   bool
	RoutingExplicitCacheKeyMaxLength int
	RoutingSystemSegmentMaxLength    int
	RoutingDocSegmentMaxLength       int
	ValidateRequests                 bool
	BackoffEnabled                   bool
	BackoffMaxRetries                int
	BackoffInitialWait               time.Duration
	BackoffMaxWait                   time.Duration
	RateLimitEnabled                 bool
	RateLimitPerModel                float64
	RateLimitBurst                   int
	RateLimitGlobal                  float64
	RateLimitGlobalBurst             int
	AuthEnabled                      bool
	AuthToken                        string
}

// ConfigFromEnv constructs a Config from environment variables and the provided client/namespace.
func ConfigFromEnv(k8sClient client.Client, namespace string) Config {
	defaultRoutingConfig := routing.DefaultPrefixKeyConfig()
	cfg := Config{
		Namespace:                        namespace,
		Client:                           k8sClient,
		MaxQueueSize:                     getEnvInt("PROXY_MAX_QUEUE_SIZE", 100),
		QueueTimeout:                     getEnvDuration("PROXY_QUEUE_TIMEOUT", 60*time.Second),
		ColdStartTimeout:                 getEnvDuration("PROXY_COLD_START_TIMEOUT", 60*time.Second),
		RoutingEnabled:                   getEnvBool("PROXY_ROUTING_ENABLED", true),
		RoutingExplicitCacheKeyMaxLength: getEnvInt("PROXY_ROUTING_EXPLICIT_KEY_MAX_LENGTH", defaultRoutingConfig.ExplicitCacheKeyMaxLength),
		RoutingSystemSegmentMaxLength:    getEnvInt("PROXY_ROUTING_SYSTEM_SEGMENT_MAX_LENGTH", defaultRoutingConfig.SystemSegmentMaxLength),
		RoutingDocSegmentMaxLength:       getEnvInt("PROXY_ROUTING_DOCUMENT_SEGMENT_MAX_LENGTH", defaultRoutingConfig.DocSegmentMaxLength),
		ValidateRequests:                 getEnvBool("PROXY_VALIDATE_REQUESTS", false),
		BackoffEnabled:                   getEnvBool("PROXY_BACKOFF_ENABLED", false),
		BackoffMaxRetries:                getEnvInt("PROXY_BACKOFF_MAX_RETRIES", 3),
		BackoffInitialWait:               getEnvDuration("PROXY_BACKOFF_INITIAL_WAIT", 5*time.Second),
		BackoffMaxWait:                   getEnvDuration("PROXY_BACKOFF_MAX_WAIT", 30*time.Second),
		RateLimitEnabled:                 getEnvBool("PROXY_RATE_LIMIT_ENABLED", false),
		RateLimitPerModel:                getEnvFloat("PROXY_RATE_LIMIT_PER_MODEL", 100.0),
		RateLimitBurst:                   getEnvInt("PROXY_RATE_LIMIT_BURST", 50),
		RateLimitGlobal:                  getEnvFloat("PROXY_RATE_LIMIT_GLOBAL", 1000.0),
		RateLimitGlobalBurst:             getEnvInt("PROXY_RATE_LIMIT_GLOBAL_BURST", 200),
		AuthEnabled:                      getEnvBool("PROXY_AUTH_ENABLED", false),
		AuthToken:                        os.Getenv("PROXY_AUTH_TOKEN"),
	}

	if cfg.AuthEnabled && cfg.AuthToken == "" {
		slog.Warn("PROXY_AUTH_ENABLED=true but PROXY_AUTH_TOKEN is empty — auth will reject all requests")
	}

	return cfg
}

// Proxy is the flexinfer reverse proxy that routes requests to model backends.
type Proxy struct {
	client       client.Client
	namespace    string
	proxyMap     sync.Map           // cache of httputil.ReverseProxy by model name
	requestGroup singleflight.Group // coalescing activation requests

	// Request queues per model during cold start
	queues   sync.Map // map[string]*RequestQueue
	queuesMu sync.Mutex

	// Service label to model name cache
	serviceLabelCache   sync.Map // map[string]string: service label -> model name
	serviceLabelCacheMu sync.Mutex
	lastCacheRefresh    time.Time

	// Configuration (can be overridden by env vars)
	maxQueueSize       int           // Default: 100
	queueTimeout       time.Duration // Default: 60s (how long request can wait in queue)
	coldStartTimeout   time.Duration // Default: 60s (how long to wait for model to become ready)
	connectionTracking sync.Map      // map[string]*int64 for tracking active connections per model

	// Routing for multi-replica models
	router             *routing.Router
	routingEnabled     bool     // Enable advanced routing (session affinity, prefix-based)
	podConnectionCount sync.Map // map[string]*int64 for tracking connections per pod address

	// Request validation
	validateRequests bool // Enable OpenAI request schema validation

	// Endpoint tracking for metrics
	endpointCache sync.Map // map[string][]string - model name -> list of endpoint addresses
	routingKeySet sync.Map // map[string]*routingKeyTracker keyed by model|strategy|key_source

	// Backoff configuration for failed activations
	backoffEnabled     bool          // Enable exponential backoff for failed activations
	backoffMaxRetries  int           // Maximum retry attempts (default: 3)
	backoffInitialWait time.Duration // Initial wait time (default: 5s)
	backoffMaxWait     time.Duration // Maximum wait time (default: 30s)

	// Rate limiting
	rateLimitEnabled     bool          // Enable per-model rate limiting
	rateLimitPerModel    float64       // Requests per second per model (0 = unlimited)
	rateLimitBurst       int           // Max burst size per model
	rateLimitGlobal      float64       // Global requests per second (0 = unlimited)
	rateLimitGlobalBurst int           // Global burst size
	modelLimiters        sync.Map      // map[string]*rate.Limiter per-model rate limiters
	globalLimiter        *rate.Limiter // global rate limiter (nil if disabled)

	// Authentication
	authEnabled bool   // Enable bearer token authentication
	authToken   string // Expected bearer token (from Secret)
}

// New creates a new Proxy from the given Config.
func New(cfg Config) *Proxy {
	RegisterMetrics()
	routing.SetPrefixKeyConfig(routing.PrefixKeyConfig{
		ExplicitCacheKeyMaxLength: cfg.RoutingExplicitCacheKeyMaxLength,
		SystemSegmentMaxLength:    cfg.RoutingSystemSegmentMaxLength,
		DocSegmentMaxLength:       cfg.RoutingDocSegmentMaxLength,
	})

	p := &Proxy{
		client:               cfg.Client,
		namespace:            cfg.Namespace,
		maxQueueSize:         cfg.MaxQueueSize,
		queueTimeout:         cfg.QueueTimeout,
		coldStartTimeout:     cfg.ColdStartTimeout,
		router:               routing.NewRouter(),
		routingEnabled:       cfg.RoutingEnabled,
		validateRequests:     cfg.ValidateRequests,
		backoffEnabled:       cfg.BackoffEnabled,
		backoffMaxRetries:    cfg.BackoffMaxRetries,
		backoffInitialWait:   cfg.BackoffInitialWait,
		backoffMaxWait:       cfg.BackoffMaxWait,
		rateLimitEnabled:     cfg.RateLimitEnabled,
		rateLimitPerModel:    cfg.RateLimitPerModel,
		rateLimitBurst:       cfg.RateLimitBurst,
		rateLimitGlobal:      cfg.RateLimitGlobal,
		rateLimitGlobalBurst: cfg.RateLimitGlobalBurst,
		authEnabled:          cfg.AuthEnabled,
		authToken:            cfg.AuthToken,
	}

	// Initialize global rate limiter
	if cfg.RateLimitEnabled && cfg.RateLimitGlobal > 0 {
		p.globalLimiter = rate.NewLimiter(rate.Limit(cfg.RateLimitGlobal), cfg.RateLimitGlobalBurst)
	}

	return p
}

// Run starts the proxy HTTP server and background goroutines.
func (p *Proxy) Run(port int) error {
	// Start queue cleanup goroutine
	go p.cleanupStaleQueues()

	// Start endpoint watcher for routing
	if p.routingEnabled {
		go p.watchEndpoints(context.Background())
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/v1/models", p.handleModels)
	mux.HandleFunc("/", p.handleRequest)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			slog.Warn("healthz write failed", "error", err)
		}
	})

	slog.Info("starting proxy",
		"port", port,
		"namespace", p.namespace,
		"queue_size", p.maxQueueSize,
		"queue_timeout", p.queueTimeout.String(),
		"cold_start_timeout", p.coldStartTimeout.String(),
		"validate_requests", p.validateRequests,
		"backoff_enabled", p.backoffEnabled,
		"rate_limit_enabled", p.rateLimitEnabled,
		"rate_limit_per_model", p.rateLimitPerModel,
		"rate_limit_global", p.rateLimitGlobal)

	return http.ListenAndServe(fmt.Sprintf(":%d", port), mux)
}

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("flexinfer/proxy").Start(ctx, "proxy.handle_request")
	defer span.End()
	span.SetAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.path", r.URL.Path),
	)

	// 0a. Generate/propagate request ID
	requestID := generateRequestID(r)
	w.Header().Set("X-Request-ID", requestID)
	span.SetAttributes(attribute.String("request.id", requestID))
	r = r.WithContext(context.WithValue(ctx, requestIDKey{}, requestID))

	// 0b. Authentication check
	if !p.checkAuth(r) {
		validation.WriteUnauthorized(w, "Invalid or missing bearer token")
		return
	}

	// 1. Extract model name and body from request
	modelName, bodyBytes := p.extractModelNameAndBody(r)
	if modelName == "" {
		validation.WriteBadRequestWithCode(w, "X-Model-ID header, /model/<name> path, or 'model' field in request body required", validation.CodeMissingRequiredField)
		return
	}

	// 1b. Rate limit check
	if !p.checkRateLimit(modelName) {
		validation.WriteRateLimited(w, 1)
		requestsTotal.WithLabelValues(modelName, "rate_limited").Inc()
		return
	}

	// 2. Validate request if enabled
	if p.validateRequests && len(bodyBytes) > 0 {
		if result := validation.ValidateRequest(r.URL.Path, bodyBytes); result != nil && !result.Valid {
			validation.WriteValidationErrors(w, result)
			return
		}
	}

	ctx = r.Context()

	// 2. Try to resolve service labels (e.g., "textgen" -> "qwen3-8b-fast")
	resolvedName := p.resolveServiceLabel(ctx, modelName)
	if resolvedName != modelName {
		slog.Debug("resolved service label", "label", modelName, "model", resolvedName, "request_id", requestID)
		modelName = resolvedName
		span.SetAttributes(attribute.String("flexinfer.model", modelName))
	}

	// 3. Fetch the model deployment
	md, err := p.getModelDeployment(ctx, modelName)
	if err == nil {
		// 4. Check if model belongs to a GPUGroup (v1alpha1 only)
		if md.Spec.GPUGroupRef != nil && *md.Spec.GPUGroupRef != "" {
			if err := p.handleGPUGroupRequest(ctx, w, r, modelName, md, start); err != nil {
				slog.Error("GPUGroup request failed", "model", modelName, "error", err)
				requestsTotal.WithLabelValues(modelName, "error").Inc()
			}
			return
		}

		// 5. If model is ready, serve directly (non-GPUGroup path)
		if isReady(md) && (md.Spec.Replicas != nil && *md.Spec.Replicas > 0) {
			p.trackAndServe(w, r, modelName, start)
			return
		}

		// 6. Model is scaled to zero or not ready - use queue
		if err := p.handleColdStart(ctx, w, r, modelName, start); err != nil {
			slog.Error("cold start failed", "model", modelName, "error", err)
			requestsTotal.WithLabelValues(modelName, "error").Inc()
			// Error response already sent by handleColdStart
		}
		return
	}

	if !errors.IsNotFound(err) {
		slog.Error("error fetching model deployment", "model", modelName, "error", err)
		validation.WriteInternalError(w, "Internal error fetching model deployment")
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		return
	}

	// v1alpha2 fallback
	m, err := p.getModel(ctx, modelName)
	if err != nil {
		if errors.IsNotFound(err) {
			validation.WriteModelNotFound(w, modelName)
		} else {
			slog.Error("error fetching model", "model", modelName, "error", err)
			validation.WriteInternalError(w, "Internal error fetching model")
		}
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		return
	}

	// If model is ready, serve directly.
	if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
		p.trackAndServe(w, r, modelName, start)
		return
	}

	// Model is scaled to zero or not ready - use queue.
	if err := p.handleColdStart(ctx, w, r, modelName, start); err != nil {
		slog.Error("cold start failed", "model", modelName, "error", err)
		requestsTotal.WithLabelValues(modelName, "error").Inc()
	}
}

// isReady checks if a ModelDeployment has the Ready condition set to True.
func isReady(md *aiv1alpha1.ModelDeployment) bool {
	for _, cond := range md.Status.Conditions {
		if cond.Type == aiv1alpha1.ConditionTypeReady && cond.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// getEnvInt returns an integer from environment variable or default.
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

// getEnvDuration returns a duration from environment variable or default.
func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

// getEnvBool returns a boolean from environment variable or default.
func getEnvBool(key string, defaultVal bool) bool {
	if val := os.Getenv(key); val != "" {
		switch strings.ToLower(val) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return defaultVal
}

// getEnvFloat returns a float64 from environment variable or default.
func getEnvFloat(key string, defaultVal float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return defaultVal
}
