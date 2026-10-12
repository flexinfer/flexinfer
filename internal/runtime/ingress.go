package runtime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
)

// ErrModelBusy means an unload or swap has begun draining a model whose
// accepted inference requests have not finished. Callers must retry the
// lifecycle operation without terminating the backend.
var ErrModelBusy = pkgrt.ErrModelBusy

// ErrInsufficientVRAM means another model must release capacity before retrying
// the load. It does not indicate a failed backend startup.
var ErrInsufficientVRAM = pkgrt.ErrInsufficientVRAM

// modelIngress is the sole advertised HTTP entrance to a managed backend.
// Admission and drain share a mutex, so once beginDrain returns no new request
// can enter the backend. Requests remain counted until their response body,
// including an SSE stream, has finished forwarding.
type modelIngress struct {
	mu              sync.Mutex
	active          int64
	draining        bool
	lastRequestTime time.Time
	drained         chan struct{}
	server          *http.Server
	listener        net.Listener
	proxy           *httputil.ReverseProxy
	transport       *http.Transport
	closeOnce       sync.Once
}

func newModelIngress(port, backendPort int32) (*modelIngress, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(int(port))))
	if err != nil {
		return nil, fmt.Errorf("listen on model ingress port %d: %w", port, err)
	}
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(backendPort)))}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	ingress := &modelIngress{
		drained:   make(chan struct{}),
		listener:  listener,
		proxy:     httputil.NewSingleHostReverseProxy(target),
		transport: transport,
	}
	ingress.proxy.Transport = transport
	// Flush each upstream write so a stream is delivered while it is running,
	// rather than buffered until the backend finishes.
	ingress.proxy.FlushInterval = -1
	ingress.server = &http.Server{Handler: ingress, ReadHeaderTimeout: 10 * time.Second}
	return ingress, nil
}

func (i *modelIngress) start() {
	go func() { _ = i.server.Serve(i.listener) }()
}

func (i *modelIngress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	recordActivity := !isBackendProbe(r)
	i.mu.Lock()
	if i.draining {
		i.mu.Unlock()
		w.Header().Set("Retry-After", "1")
		http.Error(w, "model is draining; retry after activation", http.StatusServiceUnavailable)
		return
	}
	i.active++
	if recordActivity {
		i.lastRequestTime = time.Now()
	}
	i.mu.Unlock()
	defer func() {
		i.mu.Lock()
		i.active--
		if recordActivity {
			i.lastRequestTime = time.Now()
		}
		if i.draining && i.active == 0 {
			close(i.drained)
		}
		i.mu.Unlock()
	}()
	i.proxy.ServeHTTP(w, r)
}

// Read-only health, discovery, and metrics probes are accepted requests for
// draining purposes, but are not evidence that inference is active. Background
// inference still records activity exactly like a foreground request.
func isBackendProbe(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/", "/health", "/healthz", "/readyz", "/metrics", "/slots", "/props", "/v1/models", "/api/ps", "/api/tags", "/api/version":
		return true
	default:
		return false
	}
}

func (i *modelIngress) snapshot() (int64, bool, time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.active, i.draining, i.lastRequestTime
}

func modelIsDraining(model *LoadedModel) bool {
	if model.ingress == nil {
		return false
	}
	_, draining, _ := model.ingress.snapshot()
	return draining
}

func modelBackendPort(model *LoadedModel) int32 {
	if model.backendPort != 0 {
		return model.backendPort
	}
	return model.Port
}

func (i *modelIngress) beginDrain() int64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.draining {
		i.draining = true
		if i.active == 0 {
			close(i.drained)
		}
	}
	return i.active
}

func (i *modelIngress) waitForDrain(ctx context.Context) error {
	i.beginDrain()
	select {
	case <-i.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *modelIngress) close() {
	if i == nil {
		return
	}
	i.closeOnce.Do(func() {
		i.beginDrain()
		_ = i.server.Close()
		// Close the listener separately as it may not have been handed to Serve
		// yet when subprocess startup fails.
		_ = i.listener.Close()
		i.transport.CloseIdleConnections()
	})
}

// configureIngressBackend changes only runtime subprocess bindings. Gaming
// backends use several non-HTTP ports and retain their existing lifecycle.
// Python image servers consume PORT but currently hardcode their bind address;
// their private port is never advertised by the runtime.
func configureIngressBackend(name string, port int32, args []string, env []corev1.EnvVar) ([]string, []corev1.EnvVar) {
	switch name {
	case backend.NameLlamaCpp, backend.NameVLLM, backend.NameVLLMOmni, backend.NameMLCLLM:
		args = setRuntimeArg(args, "--host", "127.0.0.1")
		args = setRuntimeArg(args, "--port", strconv.Itoa(int(port)))
	case backend.NameOllama:
		env = overlayEnvVars(env, []corev1.EnvVar{{Name: "OLLAMA_HOST", Value: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port)))}})
	case backend.NameDiffusers, backend.NameSNAC:
		env = overlayEnvVars(env, []corev1.EnvVar{{Name: "PORT", Value: strconv.Itoa(int(port))}})
	}
	return args, env
}

func setRuntimeArg(args []string, flag, value string) []string {
	for n := 0; n+1 < len(args); n++ {
		if args[n] == flag {
			args[n+1] = value
			return args
		}
	}
	return append(args, flag, value)
}

func supportsRequestIngress(name string) bool {
	switch name {
	case backend.NameLlamaCpp, backend.NameVLLM, backend.NameVLLMOmni, backend.NameMLCLLM, backend.NameOllama, backend.NameDiffusers, backend.NameSNAC:
		return true
	default:
		return false
	}
}

// reserveBackendPort selects a kernel-available private port and keeps it
// reserved until the child is about to start. Manager.opMu serializes loads,
// and the used set also excludes every existing ingress and subprocess port.
func reserveBackendPort(used map[int32]bool) (net.Listener, int32, error) {
	for attempt := 0; attempt < 16; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, 0, fmt.Errorf("reserve private backend port: %w", err)
		}
		port := int32(listener.Addr().(*net.TCPAddr).Port)
		if !used[port] {
			return listener, port, nil
		}
		_ = listener.Close()
	}
	return nil, 0, fmt.Errorf("could not reserve an unused private backend port")
}
