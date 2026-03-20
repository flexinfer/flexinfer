package proxy

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
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

	// readyPollInterval is the polling interval when waiting for a model to become ready.
	readyPollInterval = 1 * time.Second

	// proxyTTL is how long a cached httputil.ReverseProxy is valid before eviction.
	// After this duration, the proxy is recreated to pick up backend pod IP changes.
	proxyTTL = 5 * time.Minute
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

	return stderrors.Join(errs...)
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
		DirectRuntimeEnabled:             getEnvBool("PROXY_DIRECT_RUNTIME_ENABLED", true),
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

	// Direct runtime communication (fast path)
	runtimeCache         *RuntimeCache                // cached runtime pod endpoints
	directRuntimeEnabled bool                         // enable direct proxy-to-runtime loading
	directLoadTargets    TypedSyncMap[string, string] // modelName -> "http://podIP:backendPort"

	// Lifecycle context for background goroutines
	ctx    context.Context
	cancel context.CancelFunc

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
		client:               cfg.Client,
		namespace:            cfg.Namespace,
		resolver:             NewModelResolver(cfg.Client, cfg.Namespace),
		activator:            NewK8sModelActivator(cfg.Client, cfg.Namespace, cfg.ColdStartTimeout),
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
		directRuntimeEnabled: cfg.DirectRuntimeEnabled,
		ctx:                  ctx,
		cancel:               cancel,
		debugConfig:          newDebugConfigView(cfg),
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

// Run starts the proxy HTTP server and background goroutines.
func (p *Proxy) Run(port int) error {
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

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/v1/models", p.handleModels)
	mux.HandleFunc("/debug/config", p.handleDebugConfig)
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
		"rate_limit_global", p.rateLimitGlobal,
		"direct_runtime_enabled", p.directRuntimeEnabled)

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

	// 2. Try to resolve service labels (e.g., "textgen" -> "qwen3-8b-fast")
	resolvedName := p.resolver.ResolveServiceLabel(ctx, modelName)
	if resolvedName != modelName {
		slog.Debug("resolved service label", "label", modelName, "model", resolvedName, "request_id", requestID)
		modelName = resolvedName
		span.SetAttributes(attribute.String("flexinfer.model", modelName))
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
		p.trackAndServe(w, r, modelName, start)
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
	Namespace            string  `json:"namespace"`
	MaxQueueSize         int     `json:"maxQueueSize"`
	QueueTimeout         string  `json:"queueTimeout"`
	ColdStartTimeout     string  `json:"coldStartTimeout"`
	RoutingEnabled       bool    `json:"routingEnabled"`
	ValidateRequests     bool    `json:"validateRequests"`
	BackoffEnabled       bool    `json:"backoffEnabled"`
	BackoffMaxRetries    int     `json:"backoffMaxRetries"`
	BackoffInitialWait   string  `json:"backoffInitialWait"`
	BackoffMaxWait       string  `json:"backoffMaxWait"`
	RateLimitEnabled     bool    `json:"rateLimitEnabled"`
	RateLimitPerModel    float64 `json:"rateLimitPerModel"`
	RateLimitBurst       int     `json:"rateLimitBurst"`
	RateLimitGlobal      float64 `json:"rateLimitGlobal"`
	RateLimitGlobalBurst int     `json:"rateLimitGlobalBurst"`
	AuthEnabled          bool    `json:"authEnabled"`
	AuthToken            string  `json:"authToken"` // always redacted
	DirectRuntimeEnabled bool    `json:"directRuntimeEnabled"`
}

func newDebugConfigView(cfg Config) debugConfigView {
	tokenDisplay := ""
	if cfg.AuthToken != "" {
		tokenDisplay = "***redacted***"
	}
	return debugConfigView{
		Namespace:            cfg.Namespace,
		MaxQueueSize:         cfg.MaxQueueSize,
		QueueTimeout:         cfg.QueueTimeout.String(),
		ColdStartTimeout:     cfg.ColdStartTimeout.String(),
		RoutingEnabled:       cfg.RoutingEnabled,
		ValidateRequests:     cfg.ValidateRequests,
		BackoffEnabled:       cfg.BackoffEnabled,
		BackoffMaxRetries:    cfg.BackoffMaxRetries,
		BackoffInitialWait:   cfg.BackoffInitialWait.String(),
		BackoffMaxWait:       cfg.BackoffMaxWait.String(),
		RateLimitEnabled:     cfg.RateLimitEnabled,
		RateLimitPerModel:    cfg.RateLimitPerModel,
		RateLimitBurst:       cfg.RateLimitBurst,
		RateLimitGlobal:      cfg.RateLimitGlobal,
		RateLimitGlobalBurst: cfg.RateLimitGlobalBurst,
		AuthEnabled:          cfg.AuthEnabled,
		AuthToken:            tokenDisplay,
		DirectRuntimeEnabled: cfg.DirectRuntimeEnabled,
	}
}

func (p *Proxy) handleDebugConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(p.debugConfig); err != nil {
		slog.Warn("debug config write failed", "error", err)
	}
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
