package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/flexinfer/flexinfer/pkg/observability"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// RuntimeReconciler watches runtime pods and provides runtime discovery
// for the ModelReconciler. When a runtime pod exists on a GPU node,
// models can be loaded via the runtime API instead of creating separate
// Deployments.
type RuntimeReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// RuntimeEndpoint represents a discovered runtime pod's API endpoint.
type RuntimeEndpoint struct {
	// PodName is the runtime pod name.
	PodName string

	// PodIP is the pod's cluster IP.
	PodIP string

	// Port is the runtime API port (default 8080).
	Port int32

	// NodeName is the node where the runtime pod runs.
	NodeName string

	// Ready indicates whether the runtime pod is ready.
	Ready bool
}

// URL returns the HTTP URL for the runtime API.
func (e *RuntimeEndpoint) URL() string {
	return fmt.Sprintf("http://%s:%d", e.PodIP, e.Port)
}

const (
	runtimeComponentLabel = "flexinfer-runtime"
)

// Reconcile handles runtime pod lifecycle events.
func (r *RuntimeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, _, endSpan := observability.StartReconcileSpan(ctx, "runtime", req.Namespace, req.Name)
	defer endSpan()
	logger := log.FromContext(ctx)

	pod := &corev1.Pod{}
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			logger.V(1).Info("Runtime pod deleted", "pod", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Runtime pod reconciled",
		"pod", pod.Name,
		"node", pod.Spec.NodeName,
		"phase", pod.Status.Phase,
		"ready", isPodReady(pod),
	)

	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

// FindRuntimeForNode returns the runtime endpoint for a node, if one exists.
// Returns Running pods preferentially. If no Running pod matches, returns a
// Pending/starting pod with Ready=false so callers can wait instead of
// falling back to Deployment (which would deadlock on GPU resources).
func (r *RuntimeReconciler) FindRuntimeForNode(ctx context.Context, namespace string, nodeSelector map[string]string) (*RuntimeEndpoint, error) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(namespace),
		client.MatchingLabels{"app.kubernetes.io/component": runtimeComponentLabel},
	); err != nil {
		return nil, fmt.Errorf("listing runtime pods: %w", err)
	}

	var pendingMatch *RuntimeEndpoint

	for _, pod := range pods.Items {
		// For Pending pods, nodeSelector matching uses the DaemonSet's
		// nodeSelector (stored on the pod spec) rather than the assigned node.
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			// Pending pod: check if its own nodeSelector matches the model's.
			if !podNodeSelectorMatches(pod.Spec.NodeSelector, nodeSelector) {
				continue
			}
		} else {
			if !nodeMatchesSelector(ctx, r.Client, nodeName, nodeSelector) {
				continue
			}
		}

		if pod.Status.Phase == corev1.PodRunning {
			return &RuntimeEndpoint{
				PodName:  pod.Name,
				PodIP:    pod.Status.PodIP,
				Port:     pkgrt.RuntimeAPIPort,
				NodeName: nodeName,
				Ready:    isPodReady(&pod),
			}, nil
		}

		// Track non-Running match as fallback.
		if pendingMatch == nil {
			pendingMatch = &RuntimeEndpoint{
				PodName:  pod.Name,
				NodeName: nodeName,
				Port:     pkgrt.RuntimeAPIPort,
				Ready:    false,
			}
		}
	}

	return pendingMatch, nil
}

// podNodeSelectorMatches checks if a pod's nodeSelector is a superset of
// the required selector (i.e., they target the same or more specific node).
func podNodeSelectorMatches(podSelector, required map[string]string) bool {
	for k, v := range required {
		if podSelector[k] != v {
			return false
		}
	}
	return true
}

// LoadModel sends a load request to a runtime endpoint.
func (r *RuntimeReconciler) LoadModel(ctx context.Context, endpoint *RuntimeEndpoint, name string, payload []byte) error {
	url := fmt.Sprintf("%s/api/v1/models/%s/load", endpoint.URL(), name)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("creating load request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(toReader(payload))

	client := &http.Client{Timeout: httpClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending load request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("runtime load failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// UnloadModel sends an unload request to a runtime endpoint.
func (r *RuntimeReconciler) UnloadModel(ctx context.Context, endpoint *RuntimeEndpoint, name string) error {
	url := fmt.Sprintf("%s/api/v1/models/%s", endpoint.URL(), name)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("creating unload request: %w", err)
	}

	client := &http.Client{Timeout: httpClientTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("sending unload request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("runtime unload failed (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}

// RuntimeModelStatus is the health response from the runtime API.
type RuntimeModelStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error"`
}

// CheckModelHealth queries the runtime for a model's current state.
// Returns nil if the model is not loaded on the runtime.
func (r *RuntimeReconciler) CheckModelHealth(ctx context.Context, endpoint *RuntimeEndpoint, name string) (*RuntimeModelStatus, error) {
	url := fmt.Sprintf("%s/api/v1/models/%s/health", endpoint.URL(), name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating health request: %w", err)
	}

	httpClient := &http.Client{Timeout: httpClientShort}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("health request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Model not loaded
	}

	var status RuntimeModelStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decoding health response: %w", err)
	}

	return &status, nil
}

// isPodReady returns true if all containers in the pod are ready.
func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// nodeMatchesSelector checks if a node matches the given label selector.
func nodeMatchesSelector(ctx context.Context, c client.Client, nodeName string, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		return false
	}

	for k, v := range selector {
		if node.Labels[k] != v {
			return false
		}
	}
	return true
}

// toReader converts a byte slice to an io.Reader.
func toReader(b []byte) io.Reader {
	return io.NopCloser(newBytesReader(b))
}

// newBytesReader wraps bytes.NewReader.
func newBytesReader(b []byte) *bytesReader {
	return &bytesReader{data: b}
}

type bytesReader struct {
	data []byte
	pos  int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

func (r *bytesReader) Close() error { return nil }

// SetupWithManager registers the RuntimeReconciler.
func (r *RuntimeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Named("runtime").
		Complete(r)
}
