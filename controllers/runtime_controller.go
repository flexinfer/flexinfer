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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

// CanAcceptLoad reports whether the runtime management API has a routable pod
// IP. A runtime pod can be Running with Ready=false while no backend model is
// ready yet; the controller must still be able to send load/unload requests in
// that state.
func (e *RuntimeEndpoint) CanAcceptLoad() bool {
	return e != nil && e.PodIP != ""
}

// URL returns the HTTP URL for the runtime API.
func (e *RuntimeEndpoint) URL() string {
	return fmt.Sprintf("http://%s:%d", e.PodIP, e.Port)
}

const (
	runtimeComponentLabel = "flexinfer-runtime"
)

// runtimeControllerPodMeaningfulChange keeps the runtime discovery controller
// scoped to the pods it actually discovers. The previous unfiltered For(Pod)
// watch reconciled every pod status update in the cluster, even though
// Reconcile only logs runtime discovery state. Runtime pods still enqueue on
// create/delete and on the IP, phase, readiness, or deletion transitions used
// by model routing.
var runtimeControllerPodMeaningfulChange = predicate.Funcs{
	CreateFunc: func(e event.CreateEvent) bool {
		return isRuntimeComponentPod(e.Object)
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return isRuntimeComponentPod(e.Object)
	},
	GenericFunc: func(event.GenericEvent) bool { return false },
	UpdateFunc: func(e event.UpdateEvent) bool {
		oldRuntime := isRuntimeComponentPod(e.ObjectOld)
		newRuntime := isRuntimeComponentPod(e.ObjectNew)
		if oldRuntime != newRuntime {
			return true
		}
		if !newRuntime {
			return false
		}
		return runtimePodMeaningfulChange.Update(e)
	},
}

func isRuntimeComponentPod(obj client.Object) bool {
	return obj != nil && obj.GetLabels()["app.kubernetes.io/component"] == runtimeComponentLabel
}

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
	if !isRuntimeComponentPod(pod) {
		// A runtime label removal is intentionally admitted by the predicate so
		// any outstanding scheduled requeue is drained without being renewed.
		return ctrl.Result{}, nil
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
	Port  int32  `json:"port,omitempty"`
}

type RuntimeStatusResponse struct {
	GPUVendor   string              `json:"gpuVendor"`
	GPUArch     string              `json:"gpuArch"`
	ActiveModel *RuntimeModelStatus `json:"activeModel,omitempty"`
}

type RuntimeModeStatus struct {
	Mode string `json:"mode"`
	// Degraded is set when the runtime is in Mode but the mode's backing
	// subprocess is not running (e.g. the gaming backend crashed and a
	// supervised restart is pending). Detail explains why.
	Degraded bool   `json:"degraded,omitempty"`
	Detail   string `json:"detail,omitempty"`
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

// GetStatus queries the runtime process for node-level state, including the
// currently active model when one is loaded.
func (r *RuntimeReconciler) GetStatus(ctx context.Context, endpoint *RuntimeEndpoint) (*RuntimeStatusResponse, error) {
	url := fmt.Sprintf("%s/api/v1/status", endpoint.URL())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating status request: %w", err)
	}

	httpClient := &http.Client{Timeout: httpClientShort}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("status request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("runtime status failed (status %d): %s", resp.StatusCode, string(body))
	}

	var status RuntimeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decoding status response: %w", err)
	}

	return &status, nil
}

func (r *RuntimeReconciler) GetMode(ctx context.Context, endpoint *RuntimeEndpoint) (string, error) {
	status, err := r.GetModeStatus(ctx, endpoint)
	return status.Mode, err
}

// GetModeStatus returns the runtime's full mode report, including the degraded
// flag the runtime sets when the mode's backing subprocess (e.g. Sunshine in
// gaming mode) has crashed and is awaiting a supervised restart.
func (r *RuntimeReconciler) GetModeStatus(ctx context.Context, endpoint *RuntimeEndpoint) (RuntimeModeStatus, error) {
	url := fmt.Sprintf("%s/api/v1/mode", endpoint.URL())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return RuntimeModeStatus{}, fmt.Errorf("creating mode request: %w", err)
	}

	httpClient := &http.Client{Timeout: httpClientShort}
	resp, err := httpClient.Do(req)
	if err != nil {
		return RuntimeModeStatus{}, fmt.Errorf("mode request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var status RuntimeModeStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return RuntimeModeStatus{}, fmt.Errorf("decoding mode response: %w", err)
	}
	return status, nil
}

// SetMode switches a node's runtime between "inference" and "gaming" via the
// runtime management API (PUT /api/v1/mode). Gaming mode drains all loaded
// models on the node and starts the gaming backend; inference mode unloads the
// gaming backend and returns the node to the servable fleet. The runtime
// performs the actual drain — this call is idempotent (a no-op when the node is
// already in the target mode).
func (r *RuntimeReconciler) SetMode(ctx context.Context, endpoint *RuntimeEndpoint, mode string) error {
	url := fmt.Sprintf("%s/api/v1/mode", endpoint.URL())

	body, err := json.Marshal(RuntimeModeStatus{Mode: mode})
	if err != nil {
		return fmt.Errorf("encoding mode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return fmt.Errorf("creating set-mode request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(toReader(body))

	httpClient := &http.Client{Timeout: httpClientTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("set-mode request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("runtime set-mode failed (status %d): %s", resp.StatusCode, string(b))
	}
	return nil
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
		For(&corev1.Pod{}, builder.WithPredicates(runtimeControllerPodMeaningfulChange)).
		Named("runtime").
		Complete(r)
}
