package proxy

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
	"github.com/flexinfer/flexinfer/pkg/envutil"
	"github.com/flexinfer/flexinfer/pkg/modelmeta"
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

	// lastAccessHeartbeatInterval keeps serverless models warm while a single
	// long-running request is still in flight.
	lastAccessHeartbeatInterval = 30 * time.Second

	// defaultBackendPort is used when a model's backend port cannot be determined.
	defaultBackendPort int32 = 8000

	// readyPollInterval is the polling interval when waiting for a model to become ready.
	readyPollInterval = 1 * time.Second

	// proxyTTL is how long a cached httputil.ReverseProxy is valid before eviction.
	// After this duration, the proxy is recreated to pick up backend pod IP changes.
	proxyTTL = 5 * time.Minute

	// defaultGracefulShutdownTimeout bounds proxy HTTP draining during rollout
	// termination while leaving enough room for long-context completions.
	defaultGracefulShutdownTimeout = 10 * time.Minute

	// defaultShutdownDrainDelay is the in-process pause between flipping the
	// /readyz probe to 503 and closing the HTTP listener. It gives Kubernetes
	// time to remove this pod from the Service endpoints so no new request
	// lands on a listener that is about to close. See issue #65.
	defaultShutdownDrainDelay = 5 * time.Second
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
	DirectRuntimeEnabled             bool // Enable direct proxy-to-runtime fast path
	GracefulShutdownTimeout          time.Duration
	// ShutdownDrainDelay is the pause between flipping /readyz to 503 and
	// closing the HTTP listener during graceful shutdown. It lets endpoint
	// removal propagate before the listener stops accepting. See issue #65.
	ShutdownDrainDelay           time.Duration
	MaxTokensClampEnabled        bool
	MaxTokensClampPromptReserve  int
	AdmissionEnabled             bool
	AdmissionSafetyMarginPercent int
	AdmissionDefaultMaxTokens    int
	// LabelGroupRouting controls how pickReadyMember picks within a shared
	// service-label group when more than one member is Ready.
	// Empty / "round-robin": legacy per-label RR (default).
	// "least-loaded": fewest active proxy connections; RR among ties.
	// "prefix-or-rr": extract routing prefix key; consistent-hash to a
	// candidate when present, else RR.
	// "session-or-rr": session key, else RR.
	// "prefix-session-or-rr": prefix first, then session, else RR.
	LabelGroupRouting string
	// PyannoteUpstream is the base URL of the pyannote diarization sibling
	// service. When set, POST /diarize is reverse-proxied to it so ICC has a
	// single base URL for ASR + diarization. Empty → /diarize returns 503.
	PyannoteUpstream string
	// KokoroUpstream is the base URL of the Kokoro TTS sibling service. When
	// set, POST /v1/audio/speech is reverse-proxied to it so the voice stack
	// exposes ASR + diarization + TTS under one base URL. Empty → 503.
	KokoroUpstream string
	// CodebaseAnswerUpstream is the base URL of the codebase-answer read-path
	// sibling service. When set, POST /v1/rag is reverse-proxied to it so
	// retrieval-augmented codebase Q&A is reachable through the proxy front
	// door. Empty → /v1/rag returns 503.
	CodebaseAnswerUpstream string
}

// Validate checks the Config for conflicting or invalid settings. Returns a
// joined error describing all issues found, or nil if the config is valid.
func (c Config) Validate() error {
	var errs []error

	if c.MaxQueueSize <= 0 {
		errs = append(errs, fmt.Errorf("PROXY_MAX_QUEUE_SIZE must be > 0 (got %d)", c.MaxQueueSize))
	}
	if c.QueueTimeout <= 0 {
		errs = append(errs, fmt.Errorf("PROXY_QUEUE_TIMEOUT must be > 0 (got %s)", c.QueueTimeout))
	}
	if c.ColdStartTimeout <= 0 {
		errs = append(errs, fmt.Errorf("PROXY_COLD_START_TIMEOUT must be > 0 (got %s)", c.ColdStartTimeout))
	}
	if c.GracefulShutdownTimeout < 0 {
		errs = append(errs, fmt.Errorf("PROXY_GRACEFUL_SHUTDOWN_TIMEOUT must be >= 0 (got %s)", c.GracefulShutdownTimeout))
	}
	if c.ShutdownDrainDelay < 0 {
		errs = append(errs, fmt.Errorf("PROXY_SHUTDOWN_DRAIN_DELAY must be >= 0 (got %s)", c.ShutdownDrainDelay))
	}
	if c.BackoffEnabled {
		if c.BackoffMaxRetries <= 0 {
			errs = append(errs, fmt.Errorf("PROXY_BACKOFF_MAX_RETRIES must be > 0 when backoff is enabled (got %d)", c.BackoffMaxRetries))
		}
		if c.BackoffInitialWait > c.BackoffMaxWait {
			errs = append(errs, fmt.Errorf("PROXY_BACKOFF_INITIAL_WAIT (%s) must be <= PROXY_BACKOFF_MAX_WAIT (%s)", c.BackoffInitialWait, c.BackoffMaxWait))
		}
	}
	if c.RateLimitEnabled {
		if c.RateLimitPerModel <= 0 {
			errs = append(errs, fmt.Errorf("PROXY_RATE_LIMIT_PER_MODEL must be > 0 when rate limiting is enabled (got %f)", c.RateLimitPerModel))
		}
		if c.RateLimitBurst <= 0 {
			errs = append(errs, fmt.Errorf("PROXY_RATE_LIMIT_BURST must be > 0 when rate limiting is enabled (got %d)", c.RateLimitBurst))
		}
	}
	if c.AuthEnabled && c.AuthToken == "" {
		errs = append(errs, fmt.Errorf("PROXY_AUTH_TOKEN must be set when PROXY_AUTH_ENABLED=true"))
	}
	if c.MaxTokensClampEnabled && c.MaxTokensClampPromptReserve <= 0 {
		errs = append(errs, fmt.Errorf("PROXY_MAX_TOKENS_CLAMP_PROMPT_RESERVE_TOKENS must be > 0 when max_tokens clamp is enabled (got %d)", c.MaxTokensClampPromptReserve))
	}
	if c.AdmissionEnabled {
		if c.AdmissionSafetyMarginPercent < 0 || c.AdmissionSafetyMarginPercent > 50 {
			errs = append(errs, fmt.Errorf("PROXY_ADMISSION_SAFETY_MARGIN_PERCENT must be in [0,50] when admission enabled (got %d)", c.AdmissionSafetyMarginPercent))
		}
		if c.AdmissionDefaultMaxTokens <= 0 {
			errs = append(errs, fmt.Errorf("PROXY_ADMISSION_DEFAULT_MAX_TOKENS must be > 0 when admission enabled (got %d)", c.AdmissionDefaultMaxTokens))
		}
	}
	if !isValidLabelGroupRoutingMode(c.LabelGroupRouting) {
		errs = append(errs, fmt.Errorf(
			"FLEXINFER_PROXY_LABEL_GROUP_ROUTING must be one of %q,%q,%q,%q,%q,%q (got %q)",
			labelGroupRoutingRR, labelGroupRoutingRRAlias,
			labelGroupRoutingLeastLoaded,
			labelGroupRoutingPrefixOrRR, labelGroupRoutingSessionOrRR,
			labelGroupRoutingPrefixSessionOrRR, c.LabelGroupRouting,
		))
	}

	return stderrors.Join(errs...)
}

// ConfigFromEnv constructs a Config from environment variables and the provided client/namespace.
func ConfigFromEnv(k8sClient client.Client, namespace string) Config {
	defaultRoutingConfig := routing.DefaultPrefixKeyConfig()
	cfg := Config{
		Namespace:                        namespace,
		Client:                           k8sClient,
		MaxQueueSize:                     envutil.IntOrDefault("PROXY_MAX_QUEUE_SIZE", 100),
		QueueTimeout:                     envutil.DurationOrDefault("PROXY_QUEUE_TIMEOUT", 60*time.Second),
		ColdStartTimeout:                 envutil.DurationOrDefault("PROXY_COLD_START_TIMEOUT", 60*time.Second),
		RoutingEnabled:                   envutil.BoolOrDefault("PROXY_ROUTING_ENABLED", true),
		RoutingExplicitCacheKeyMaxLength: envutil.IntOrDefault("PROXY_ROUTING_EXPLICIT_KEY_MAX_LENGTH", defaultRoutingConfig.ExplicitCacheKeyMaxLength),
		RoutingSystemSegmentMaxLength:    envutil.IntOrDefault("PROXY_ROUTING_SYSTEM_SEGMENT_MAX_LENGTH", defaultRoutingConfig.SystemSegmentMaxLength),
		RoutingDocSegmentMaxLength:       envutil.IntOrDefault("PROXY_ROUTING_DOCUMENT_SEGMENT_MAX_LENGTH", defaultRoutingConfig.DocSegmentMaxLength),
		ValidateRequests:                 envutil.BoolOrDefault("PROXY_VALIDATE_REQUESTS", false),
		BackoffEnabled:                   envutil.BoolOrDefault("PROXY_BACKOFF_ENABLED", false),
		BackoffMaxRetries:                envutil.IntOrDefault("PROXY_BACKOFF_MAX_RETRIES", 3),
		BackoffInitialWait:               envutil.DurationOrDefault("PROXY_BACKOFF_INITIAL_WAIT", 5*time.Second),
		BackoffMaxWait:                   envutil.DurationOrDefault("PROXY_BACKOFF_MAX_WAIT", 30*time.Second),
		RateLimitEnabled:                 envutil.BoolOrDefault("PROXY_RATE_LIMIT_ENABLED", false),
		RateLimitPerModel:                envutil.Float64OrDefault("PROXY_RATE_LIMIT_PER_MODEL", 100.0),
		RateLimitBurst:                   envutil.IntOrDefault("PROXY_RATE_LIMIT_BURST", 50),
		RateLimitGlobal:                  envutil.Float64OrDefault("PROXY_RATE_LIMIT_GLOBAL", 1000.0),
		RateLimitGlobalBurst:             envutil.IntOrDefault("PROXY_RATE_LIMIT_GLOBAL_BURST", 200),
		AuthEnabled:                      envutil.BoolOrDefault("PROXY_AUTH_ENABLED", false),
		AuthToken:                        os.Getenv("PROXY_AUTH_TOKEN"),
		DirectRuntimeEnabled:             envutil.BoolOrDefault("PROXY_DIRECT_RUNTIME_ENABLED", true),
		GracefulShutdownTimeout:          envutil.DurationOrDefault("PROXY_GRACEFUL_SHUTDOWN_TIMEOUT", defaultGracefulShutdownTimeout),
		ShutdownDrainDelay:               envutil.DurationOrDefault("PROXY_SHUTDOWN_DRAIN_DELAY", defaultShutdownDrainDelay),
		MaxTokensClampEnabled:            envutil.BoolOrDefault("PROXY_MAX_TOKENS_CLAMP_ENABLED", true),
		MaxTokensClampPromptReserve:      envutil.IntOrDefault("PROXY_MAX_TOKENS_CLAMP_PROMPT_RESERVE_TOKENS", defaultPromptReserveTokens),
		AdmissionEnabled:                 envutil.BoolOrDefault("PROXY_ADMISSION_ENABLED", false),
		AdmissionSafetyMarginPercent:     envutil.IntOrDefault("PROXY_ADMISSION_SAFETY_MARGIN_PERCENT", 5),
		AdmissionDefaultMaxTokens:        envutil.IntOrDefault("PROXY_ADMISSION_DEFAULT_MAX_TOKENS", defaultAdmissionMaxTokens),
		LabelGroupRouting:                os.Getenv("FLEXINFER_PROXY_LABEL_GROUP_ROUTING"),
		PyannoteUpstream:                 os.Getenv("FLEXINFER_PYANNOTE_UPSTREAM"),
		KokoroUpstream:                   os.Getenv("FLEXINFER_KOKORO_UPSTREAM"),
		CodebaseAnswerUpstream:           os.Getenv("FLEXINFER_CODEBASE_ANSWER_UPSTREAM"),
	}

	return cfg
}

// proxyEntry holds a cached httputil.ReverseProxy with its creation timestamp
// so stale entries can be evicted after proxyTTL.
type proxyEntry struct {
	proxy   *httputil.ReverseProxy
	created time.Time
}

// Proxy is the flexinfer reverse proxy that routes requests to model backends.
type Proxy struct {
	client       client.Client
	namespace    string
	proxyMap     TypedSyncMap[string, proxyEntry] // cache of ReverseProxy by target URL, with TTL
	requestGroup singleflight.Group               // coalescing activation requests

	// Request queues per model during cold start
	queues   TypedSyncMap[string, *RequestQueue]
	queuesMu sync.Mutex

	// Extracted subsystems
	resolver  *ModelResolver // name resolution: service labels, aliases, LoRA
	activator ModelActivator // K8s activation: scale-up, demand signals, cold-start

	// Configuration (can be overridden by env vars)
	maxQueueSize       int                          // Default: 100
	queueTimeout       time.Duration                // Default: 60s (how long request can wait in queue)
	coldStartTimeout   time.Duration                // Default: 60s (how long to wait for model to become ready)
	connectionTracking TypedSyncMap[string, *int64] // tracking active connections per model

	// activationNotFoundGrace bounds how long waitForReady tolerates a fully
	// missing Model/ModelDeployment before fast-failing the activation.
	// Zero means defaultActivationNotFoundGrace; overridable in tests.
	activationNotFoundGrace time.Duration

	// Routing for multi-replica models
	router             *routing.Router
	routingEnabled     bool                         // Enable advanced routing (session affinity, prefix-based)
	podConnectionCount TypedSyncMap[string, *int64] // tracking connections per pod address

	// Request validation
	validateRequests bool // Enable OpenAI request schema validation

	// Endpoint tracking for metrics
	endpointCache TypedSyncMap[string, []string]           // model name -> list of endpoint addresses
	routingKeySet TypedSyncMap[string, *routingKeyTracker] // model|strategy|key_source -> tracker

	// Backoff configuration for failed activations
	backoffEnabled     bool          // Enable exponential backoff for failed activations
	backoffMaxRetries  int           // Maximum retry attempts (default: 3)
	backoffInitialWait time.Duration // Initial wait time (default: 5s)
	backoffMaxWait     time.Duration // Maximum wait time (default: 30s)

	// Rate limiting
	rateLimitEnabled     bool                                // Enable per-model rate limiting
	rateLimitPerModel    float64                             // Requests per second per model (0 = unlimited)
	rateLimitBurst       int                                 // Max burst size per model
	rateLimitGlobal      float64                             // Global requests per second (0 = unlimited)
	rateLimitGlobalBurst int                                 // Global burst size
	modelLimiters        TypedSyncMap[string, *rate.Limiter] // per-model rate limiters
	globalLimiter        *rate.Limiter                       // global rate limiter (nil if disabled)

	// Authentication
	authEnabled bool   // Enable bearer token authentication
	authToken   string // Expected bearer token (from Secret)

	// max_tokens clamping
	maxTokensClampEnabled       bool
	maxTokensClampPromptReserve int

	// context-bounded admission (CC-6a-2): refuses requests whose
	// estimated_prompt_tokens + max_tokens would exceed the per-Model
	// context ceiling. Opt-in per Model via the flexinfer.ai/admission
	// annotation; default off globally. See
	// docs/planning/context-bounded-admission-spec.md.
	admission *admissionFilter

	// Direct runtime communication (fast path)
	runtimeCache         *RuntimeCache                // cached runtime pod endpoints
	directRuntimeEnabled bool                         // enable direct proxy-to-runtime loading
	directLoadTargets    TypedSyncMap[string, string] // modelName -> "http://podIP:backendPort"

	// Last-observed Service port per model. Populated on every successful
	// getServicePort read; consulted when a subsequent read fails so we don't
	// silently fall through to the backend's default port. Without this,
	// llamacpp Models whose Service exposes port 8000 can be dialed at port
	// 8080 (LlamaCppBackend.Port(), the runtime control-plane port) during
	// the brief windows when the controller's reconcile churn evicts the
	// Service from the proxy's informer cache — producing intermittent 502
	// Bad Gateway with 30s TCP timeouts. See MR !491 follow-up.
	lastKnownServicePorts TypedSyncMap[string, int32]

	// Round-robin counters for shared service-label routing. Keyed by label
	// (e.g. "quality-chat"). Used by pickReadyMember to distribute requests
	// across multiple models that claim the same label (two-instance fleets,
	// horizontal capacity). Counters are atomic and ride alongside the
	// resolver's labelGroupCache; they survive proxy lifetime, reset on
	// Proxy reconstruction (every proxyTTL).
	labelRRCounters TypedSyncMap[string, *atomic.Uint64]

	// Configured label-group routing mode. Validated at startup. Default
	// empty = legacy per-label round-robin. See F4-proxy-prefix-pinning in
	// `.loom/brainstorm-f4-long-context-agent-2026-05-25.md`.
	labelGroupRouting string

	// pyannoteUpstream is the base URL of the pyannote diarization sibling
	// service; empty disables the /diarize route (returns 503).
	pyannoteUpstream string

	// kokoroUpstream is the base URL of the Kokoro TTS sibling service; empty
	// disables the /v1/audio/speech route (returns 503).
	kokoroUpstream string

	// codebaseAnswerUpstream is the base URL of the codebase-answer read-path
	// sibling service; empty disables the /v1/rag route (returns 503).
	codebaseAnswerUpstream string

	// Lifecycle context for background goroutines
	ctx    context.Context
	cancel context.CancelFunc

	// gracefulShutdownTimeout bounds http.Server shutdown after SIGTERM/SIGINT.
	gracefulShutdownTimeout time.Duration

	// shutdownDrainDelay is the pause between flipping /readyz to 503 and
	// calling server.Shutdown. It gives Kubernetes time to remove this pod
	// from the Service endpoints before the listener closes. Zero means no
	// delay. See readiness.go and issue #65.
	shutdownDrainDelay time.Duration

	// shuttingDown flips to true the moment graceful shutdown begins so
	// /readyz reports 503 (endpoint drain) while the listener still accepts
	// in-flight completions. See readiness.go.
	shuttingDown atomic.Bool

	// Stored config for debug endpoint (secrets redacted)
	debugConfig debugConfigView
}

// New creates a new Proxy from the given Config.
func New(cfg Config) *Proxy {
	RegisterMetrics()
	routing.SetPrefixKeyConfig(routing.PrefixKeyConfig{
		ExplicitCacheKeyMaxLength: cfg.RoutingExplicitCacheKeyMaxLength,
		SystemSegmentMaxLength:    cfg.RoutingSystemSegmentMaxLength,
		DocSegmentMaxLength:       cfg.RoutingDocSegmentMaxLength,
	})

	ctx, cancel := context.WithCancel(context.Background())

	p := &Proxy{
		client:                      cfg.Client,
		namespace:                   cfg.Namespace,
		resolver:                    NewModelResolver(cfg.Client, cfg.Namespace),
		activator:                   NewK8sModelActivator(cfg.Client, cfg.Namespace, cfg.ColdStartTimeout),
		maxQueueSize:                cfg.MaxQueueSize,
		queueTimeout:                cfg.QueueTimeout,
		coldStartTimeout:            cfg.ColdStartTimeout,
		router:                      routing.NewRouter(),
		routingEnabled:              cfg.RoutingEnabled,
		validateRequests:            cfg.ValidateRequests,
		backoffEnabled:              cfg.BackoffEnabled,
		backoffMaxRetries:           cfg.BackoffMaxRetries,
		backoffInitialWait:          cfg.BackoffInitialWait,
		backoffMaxWait:              cfg.BackoffMaxWait,
		rateLimitEnabled:            cfg.RateLimitEnabled,
		rateLimitPerModel:           cfg.RateLimitPerModel,
		rateLimitBurst:              cfg.RateLimitBurst,
		rateLimitGlobal:             cfg.RateLimitGlobal,
		rateLimitGlobalBurst:        cfg.RateLimitGlobalBurst,
		authEnabled:                 cfg.AuthEnabled,
		authToken:                   cfg.AuthToken,
		maxTokensClampEnabled:       cfg.MaxTokensClampEnabled,
		maxTokensClampPromptReserve: cfg.MaxTokensClampPromptReserve,
		admission: &admissionFilter{
			Enabled:             cfg.AdmissionEnabled,
			SafetyMarginPercent: cfg.AdmissionSafetyMarginPercent,
			DefaultMaxTokens:    cfg.AdmissionDefaultMaxTokens,
		},
		directRuntimeEnabled:    cfg.DirectRuntimeEnabled,
		gracefulShutdownTimeout: cfg.GracefulShutdownTimeout,
		shutdownDrainDelay:      cfg.ShutdownDrainDelay,
		labelGroupRouting:       canonicalLabelGroupRoutingMode(cfg.LabelGroupRouting),
		pyannoteUpstream:        cfg.PyannoteUpstream,
		kokoroUpstream:          cfg.KokoroUpstream,
		codebaseAnswerUpstream:  cfg.CodebaseAnswerUpstream,
		ctx:                     ctx,
		cancel:                  cancel,
		debugConfig:             newDebugConfigView(cfg),
	}

	if cfg.DirectRuntimeEnabled {
		p.runtimeCache = NewRuntimeCache(cfg.Client, cfg.Namespace, endpointWatchInterval)
	}

	// Initialize global rate limiter
	if cfg.RateLimitEnabled && cfg.RateLimitGlobal > 0 {
		p.globalLimiter = rate.NewLimiter(rate.Limit(cfg.RateLimitGlobal), cfg.RateLimitGlobalBurst)
	}

	return p
}

// Shutdown cancels all background goroutines started by Run.
func (p *Proxy) Shutdown() {
	if p.cancel != nil {
		p.cancel()
	}
}

// Run starts the proxy HTTP server and background goroutines until ctx is
// canceled. Cancellation drains in-flight requests before stopping background
// work.
func (p *Proxy) Run(ctx context.Context, port int) error {
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: p.newServeMux(),
	}
	return p.runServer(ctx, server)
}

func (p *Proxy) runServer(ctx context.Context, server *http.Server) error {
	// Start queue cleanup goroutine
	go p.cleanupStaleQueues()

	// Start endpoint watcher for routing
	if p.routingEnabled {
		go p.watchEndpoints(p.ctx)
	}

	// Start runtime cache for direct fast path
	if p.runtimeCache != nil {
		p.runtimeCache.StartRefreshLoop(p.ctx)

		// Recover direct load targets from running runtime pods.
		recoveryCtx, recoveryCancel := context.WithTimeout(p.ctx, 10*time.Second)
		p.recoverDirectLoadTargets(recoveryCtx)
		recoveryCancel()
	}

	slog.Info("starting proxy",
		"addr", server.Addr,
		"namespace", p.namespace,
		"queue_size", p.maxQueueSize,
		"queue_timeout", p.queueTimeout.String(),
		"cold_start_timeout", p.coldStartTimeout.String(),
		"graceful_shutdown_timeout", p.shutdownTimeout().String(),
		"shutdown_drain_delay", p.shutdownDrainDelay.String(),
		"validate_requests", p.validateRequests,
		"backoff_enabled", p.backoffEnabled,
		"rate_limit_enabled", p.rateLimitEnabled,
		"rate_limit_per_model", p.rateLimitPerModel,
		"rate_limit_global", p.rateLimitGlobal,
		"max_tokens_clamp_enabled", p.maxTokensClampEnabled,
		"max_tokens_clamp_prompt_reserve", p.maxTokensClampPromptReserve,
		"admission_enabled", p.admission.Enabled,
		"admission_safety_margin_percent", p.admission.SafetyMarginPercent,
		"admission_default_max_tokens", p.admission.DefaultMaxTokens,
		"direct_runtime_enabled", p.directRuntimeEnabled)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	return p.waitForServer(ctx, server, errCh)
}

func (p *Proxy) runServerOnListener(ctx context.Context, server *http.Server, listener net.Listener) error {
	go p.cleanupStaleQueues()
	if p.routingEnabled {
		go p.watchEndpoints(p.ctx)
	}
	if p.runtimeCache != nil {
		p.runtimeCache.StartRefreshLoop(p.ctx)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	return p.waitForServer(ctx, server, errCh)
}

func (p *Proxy) waitForServer(ctx context.Context, server *http.Server, errCh <-chan error) error {
	select {
	case err := <-errCh:
		if stderrors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		start := time.Now()
		timeout := p.shutdownTimeout()
		drainDelay := p.shutdownDrainDelay

		// Flip /readyz to 503 first so the kubelet pulls this pod out of the
		// Service endpoints. The listener still accepts during the drain delay,
		// so requests that raced the endpoint update still reach a live backend.
		p.shuttingDown.Store(true)
		proxyShutdownsTotal.WithLabelValues("started").Inc()
		slog.Info("proxy graceful shutdown started",
			"timeout", timeout.String(),
			"drain_delay", drainDelay.String(),
			"in_flight_connections", p.totalActiveConnections())

		// Wait for endpoint removal to propagate before closing the listener so
		// no new request lands on a pod that is about to stop accepting.
		if drainDelay > 0 {
			time.Sleep(drainDelay)
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		err := server.Shutdown(shutdownCtx)
		duration := time.Since(start)
		proxyShutdownDuration.Observe(duration.Seconds())
		p.Shutdown()

		if err != nil {
			proxyShutdownsTotal.WithLabelValues("timeout").Inc()
			slog.Error("proxy graceful shutdown timed out", "duration", duration.String(), "timeout", timeout.String(), "error", err)
			return fmt.Errorf("proxy graceful shutdown timed out after %s: %w", timeout, err)
		}

		proxyShutdownsTotal.WithLabelValues("completed").Inc()
		slog.Info("proxy graceful shutdown completed", "duration", duration.String())

		if err := <-errCh; err != nil && !stderrors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (p *Proxy) shutdownTimeout() time.Duration {
	if p.gracefulShutdownTimeout > 0 {
		return p.gracefulShutdownTimeout
	}
	return defaultGracefulShutdownTimeout
}

func (p *Proxy) newServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/v1/models", p.handleModels)
	mux.HandleFunc("/debug/config", p.handleDebugConfig)
	mux.HandleFunc("/diarize", p.handleDiarize)
	mux.HandleFunc("/v1/audio/speech", p.handleSpeech)
	mux.HandleFunc("/v1/rag", p.handleCodebaseAnswer)
	mux.HandleFunc("/", p.handleRequest)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("ok")); err != nil {
			slog.Warn("healthz write failed", "error", err)
		}
	})
	// /readyz gates rollout draining: 200 while serving, 503 the moment
	// shutdown begins so the kubelet removes this pod from Service endpoints
	// before the listener closes. /healthz stays 200 throughout so a long
	// drain is never SIGKILLed by a liveness probe. See readiness.go, issue #65.
	mux.HandleFunc("/readyz", p.handleReadyz)

	return mux
}

func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	background := isBackgroundRequest(r)
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("flexinfer/proxy").Start(ctx, "proxy.handle_request")
	defer span.End()
	span.SetAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.path", r.URL.Path),
		attribute.Bool("flexinfer.workload.background", background),
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

	span.SetAttributes(attribute.String("flexinfer.model", modelName))

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

	// 2. Try to resolve service labels (e.g., "textgen" -> "qwen3-8b-fast").
	// For shared labels with multiple claimants (two-instance fleets), pick
	// among Ready members in round-robin; for single-claimant labels this
	// reduces to the same behavior as the legacy ResolveServiceLabel path.
	if members, isLabel := p.resolver.ResolveServiceLabelGroup(ctx, modelName); isLabel && len(members) > 0 {
		chosen := p.pickReadyMemberRouted(ctx, modelName, members, r, bodyBytes)
		if chosen != modelName {
			slog.Debug("resolved service label",
				"label", modelName, "model", chosen,
				"members", members, "request_id", requestID)
			modelName = chosen
			span.SetAttributes(attribute.String("flexinfer.model", modelName))
		}
	}

	// 2b. Try to resolve model aliases (servedModelName / litellm aliases -> K8s name)
	resolvedAlias := p.resolver.ResolveModelAlias(ctx, modelName)
	if resolvedAlias != modelName {
		slog.Debug("resolved model alias", "alias", modelName, "model", resolvedAlias, "request_id", requestID)
		modelName = resolvedAlias
		span.SetAttributes(attribute.String("flexinfer.model", modelName))
	}

	// 3. Fetch v1alpha2 Model first (preferred)
	m, err := p.getModel(ctx, modelName)
	if err == nil {
		// 3-pre. Endpoint guard: if the Model declares which inference paths it
		// serves (flexinfer.ai/serve-paths) and this request isn't one of them,
		// reject immediately — do NOT serve, cold-start, or touch demand. This
		// stops e.g. chat-completion probes from warming an ASR-only model
		// (whisper, /v1/audio/transcriptions only) and preempting its shared GPU.
		if !modelServesPath(m, r.URL.Path) {
			slog.Debug("rejecting request: model does not serve path",
				"model", modelName, "path", r.URL.Path, "request_id", requestID)
			validation.WriteModelNotFound(w, modelName)
			requestsTotal.WithLabelValues(modelName, "endpoint_unsupported").Inc()
			return
		}

		// 3a. Context-bounded admission (CC-6a-2): refuse over-budget
		// requests at the proxy edge instead of forwarding and waiting
		// 30s for the runtime to reject. Opt-in per Model via the
		// flexinfer.ai/admission annotation; no-op when not opted in or
		// the feature flag is off.
		if decision := p.admission.checkAdmission(
			m.Annotations,
			modelmeta.ResolveTokenLimits(&m.Spec),
			bodyBytes,
		); decision.Enforced {
			logAdmission(ctx, modelName, decision)
			admissionDecisionsTotal.WithLabelValues(
				modelName, decision.Reason, strconv.FormatBool(decision.Allow),
			).Inc()
			if !decision.Allow {
				writeAdmissionRejection(w, modelName, decision)
				requestsTotal.WithLabelValues(modelName, "admission_rejected").Inc()
				return
			}
		}

		// If model is ready, serve directly.
		if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
			if background {
				p.trackAndServeBackground(w, r, modelName, start)
			} else {
				p.trackAndServe(w, r, modelName, start)
			}
			return
		}

		// Background work may consume only an already-warm model. It must never
		// create demand, enter the cold-start queue, or activate a parked model.
		if background {
			slog.Debug("rejecting background request: model is not ready",
				"model", modelName, "phase", m.Status.Phase, "request_id", requestID)
			validation.WriteServiceUnavailable(w,
				fmt.Sprintf("background request requires model %q to already be Ready", modelName))
			requestsTotal.WithLabelValues(modelName, "background_not_ready").Inc()
			return
		}

		// Parked / de-advertised models (spec.litellm.enabled=false) are not
		// part of the servable fleet: never cold-start them by name. Return 404
		// immediately WITHOUT touching demand, so a stale client or prober
		// hitting a model we do not intend to serve cannot build an unbounded
		// queue or trigger shared-GPU preemption of the warm leader. (A warm
		// minReplicas>=1 parked model would have served via the Ready branch.)
		if litellmExplicitlyDisabled(m) {
			slog.Debug("rejecting cold start: model is parked (litellm disabled)",
				"model", modelName, "request_id", requestID)
			validation.WriteModelNotFound(w, modelName)
			requestsTotal.WithLabelValues(modelName, "parked").Inc()
			return
		}

		// Statically parked behind a warm primary it can never outrank: the
		// controller has determined this shared-group member is not promotable by
		// demand (the warm leader never idles and outranks it). Cold-starting it is
		// doomed — the election parks the backend the instant it spawns, so the
		// request would burn a 10-25m timeout and churn the GPU runtime. Fast-fail
		// 503 WITHOUT queueing, cold-starting, or touching demand. Unlike the
		// litellm-disabled park (404, model removed from the fleet), the model is
		// still advertised and could serve once contention clears, so 503 is the
		// honest status. The gate self-heals when the controller clears the prefix.
		if parkedBehindPrimary(m) {
			slog.Debug("rejecting cold start: model is parked behind a warm primary",
				"model", modelName, "preemptedBy", m.Status.SharedGroup.PreemptedBy,
				"request_id", requestID)
			validation.WriteServiceUnavailable(w,
				fmt.Sprintf("model %q is parked behind a higher-priority primary on its shared GPU and is not currently servable", modelName))
			requestsTotal.WithLabelValues(modelName, "parked_behind_primary").Inc()
			return
		}

		// Model is scaled to zero or not ready - use queue.
		if err := p.handleColdStart(ctx, w, r, modelName, start); err != nil {
			slog.Error("cold start failed", "model", modelName, "error", err)
			requestsTotal.WithLabelValues(modelName, "error").Inc()
		}
		return
	}

	if !errors.IsNotFound(err) {
		slog.Error("error fetching model", "model", modelName, "error", err)
		validation.WriteInternalError(w, "Internal error fetching model")
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		return
	}

	// v1alpha1 fallback (deprecated)
	md, err := p.getModelDeployment(ctx, modelName)
	if err != nil {
		if errors.IsNotFound(err) {
			validation.WriteModelNotFound(w, modelName)
		} else {
			slog.Error("error fetching model deployment", "model", modelName, "error", err)
			validation.WriteInternalError(w, "Internal error fetching model deployment")
		}
		requestsTotal.WithLabelValues(modelName, "error").Inc()
		return
	}

	slog.Warn("serving request via deprecated v1alpha1 ModelDeployment, please migrate to v1alpha2 Model",
		"model", modelName)

	// If model is ready, serve directly.
	if isReady(md) && (md.Spec.Replicas != nil && *md.Spec.Replicas > 0) {
		if background {
			p.trackAndServeBackground(w, r, modelName, start)
		} else {
			p.trackAndServe(w, r, modelName, start)
		}
		return
	}

	if background {
		slog.Debug("rejecting background request: legacy model is not ready",
			"model", modelName, "request_id", requestID)
		validation.WriteServiceUnavailable(w,
			fmt.Sprintf("background request requires model %q to already be Ready", modelName))
		requestsTotal.WithLabelValues(modelName, "background_not_ready").Inc()
		return
	}

	// Check if this model was loaded via the direct runtime path — the
	// controller hasn't backfilled the CRD status yet, but we know it's
	// serving on the runtime pod.
	if _, ok := p.directLoadTargets.Load(modelName); ok {
		p.trackAndServe(w, r, modelName, start)
		return
	}

	// Model is scaled to zero or not ready - use queue.
	if err := p.handleColdStart(ctx, w, r, modelName, start); err != nil {
		slog.Error("cold start failed", "model", modelName, "error", err)
		requestsTotal.WithLabelValues(modelName, "error").Inc()
	}
}

// isBackgroundRequest consumes the proxy-internal workload-class header. The
// header is removed regardless of value so internal scheduling metadata can
// never leak to model servers. Only the recognized background value changes
// request behavior; missing or unknown values remain foreground-compatible.
func isBackgroundRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	raw := r.Header.Get(benchmarkconfig.HeaderInternalWorkloadClass)
	r.Header.Del(benchmarkconfig.HeaderInternalWorkloadClass)
	return strings.EqualFold(strings.TrimSpace(raw), benchmarkconfig.WorkloadClassBackground)
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

// debugConfigView is a JSON-safe, redacted view of the proxy configuration
// exposed via the /debug/config endpoint.
type debugConfigView struct {
	Namespace                    string  `json:"namespace"`
	MaxQueueSize                 int     `json:"maxQueueSize"`
	QueueTimeout                 string  `json:"queueTimeout"`
	ColdStartTimeout             string  `json:"coldStartTimeout"`
	RoutingEnabled               bool    `json:"routingEnabled"`
	ValidateRequests             bool    `json:"validateRequests"`
	BackoffEnabled               bool    `json:"backoffEnabled"`
	BackoffMaxRetries            int     `json:"backoffMaxRetries"`
	BackoffInitialWait           string  `json:"backoffInitialWait"`
	BackoffMaxWait               string  `json:"backoffMaxWait"`
	RateLimitEnabled             bool    `json:"rateLimitEnabled"`
	RateLimitPerModel            float64 `json:"rateLimitPerModel"`
	RateLimitBurst               int     `json:"rateLimitBurst"`
	RateLimitGlobal              float64 `json:"rateLimitGlobal"`
	RateLimitGlobalBurst         int     `json:"rateLimitGlobalBurst"`
	AuthEnabled                  bool    `json:"authEnabled"`
	AuthToken                    string  `json:"authToken"` // always redacted
	DirectRuntimeEnabled         bool    `json:"directRuntimeEnabled"`
	GracefulShutdownTimeout      string  `json:"gracefulShutdownTimeout"`
	MaxTokensClampEnabled        bool    `json:"maxTokensClampEnabled"`
	MaxTokensClampPromptReserve  int     `json:"maxTokensClampPromptReserve"`
	AdmissionEnabled             bool    `json:"admissionEnabled"`
	AdmissionSafetyMarginPercent int     `json:"admissionSafetyMarginPercent"`
	AdmissionDefaultMaxTokens    int     `json:"admissionDefaultMaxTokens"`
	LabelGroupRouting            string  `json:"labelGroupRouting"`
}

func newDebugConfigView(cfg Config) debugConfigView {
	tokenDisplay := ""
	if cfg.AuthToken != "" {
		tokenDisplay = "***redacted***"
	}
	labelGroupRouting := canonicalLabelGroupRoutingMode(cfg.LabelGroupRouting)
	if labelGroupRouting == labelGroupRoutingRR {
		labelGroupRouting = labelGroupRoutingRRAlias
	}
	gracefulShutdownTimeout := cfg.GracefulShutdownTimeout
	if gracefulShutdownTimeout == 0 {
		gracefulShutdownTimeout = defaultGracefulShutdownTimeout
	}
	return debugConfigView{
		Namespace:                    cfg.Namespace,
		MaxQueueSize:                 cfg.MaxQueueSize,
		QueueTimeout:                 cfg.QueueTimeout.String(),
		ColdStartTimeout:             cfg.ColdStartTimeout.String(),
		RoutingEnabled:               cfg.RoutingEnabled,
		ValidateRequests:             cfg.ValidateRequests,
		BackoffEnabled:               cfg.BackoffEnabled,
		BackoffMaxRetries:            cfg.BackoffMaxRetries,
		BackoffInitialWait:           cfg.BackoffInitialWait.String(),
		BackoffMaxWait:               cfg.BackoffMaxWait.String(),
		RateLimitEnabled:             cfg.RateLimitEnabled,
		RateLimitPerModel:            cfg.RateLimitPerModel,
		RateLimitBurst:               cfg.RateLimitBurst,
		RateLimitGlobal:              cfg.RateLimitGlobal,
		RateLimitGlobalBurst:         cfg.RateLimitGlobalBurst,
		AuthEnabled:                  cfg.AuthEnabled,
		AuthToken:                    tokenDisplay,
		DirectRuntimeEnabled:         cfg.DirectRuntimeEnabled,
		GracefulShutdownTimeout:      gracefulShutdownTimeout.String(),
		MaxTokensClampEnabled:        cfg.MaxTokensClampEnabled,
		MaxTokensClampPromptReserve:  cfg.MaxTokensClampPromptReserve,
		AdmissionEnabled:             cfg.AdmissionEnabled,
		AdmissionSafetyMarginPercent: cfg.AdmissionSafetyMarginPercent,
		AdmissionDefaultMaxTokens:    cfg.AdmissionDefaultMaxTokens,
		LabelGroupRouting:            labelGroupRouting,
	}
}

func (p *Proxy) handleDebugConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(p.debugConfig); err != nil {
		slog.Warn("debug config write failed", "error", err)
	}
}
