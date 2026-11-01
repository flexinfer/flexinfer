package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"sigs.k8s.io/yaml"
)

// Caller QoS classes (D3, .loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md §6.3).
//
// Every inference request is resolved to one of three classes. The class
// labels the usage log and the per-class metrics; lanes that opt in also
// enforce it (proxy seat gate, vLLM per-request priority). Resolution is
// first match wins:
//
//  1. the X-FlexInfer-QoS request header;
//  2. the caller-identity map in ConfigMap flexinfer-proxy-qos: the sha256 of
//     the bearer key, then the caller name (X-FlexInfer-Caller, else the
//     User-Agent product token);
//  3. a LiteLLM alias suffix on the requested model (workhorse-background);
//  4. interactive.
//
// The class is orthogonal to the proxy-internal workload class
// (X-FlexInfer-Internal-Workload-Class, isBackgroundRequest): that header
// decides whether a request may create cold-start demand, this one decides
// its place in the lane's queue. A background-QoS request still activates a
// cold lane.

type qosClass string

const (
	qosInteractive qosClass = "interactive"
	qosBackground  qosClass = "background"
	qosBatch       qosClass = "batch"
)

const (
	// headerQoS selects the class for one request. The proxy strips it before
	// forwarding.
	headerQoS = "X-FlexInfer-QoS"
	// headerCaller names the calling workload for the ConfigMap caller map. It
	// takes precedence over the User-Agent product token. Stripped before
	// forwarding.
	headerCaller = "X-FlexInfer-Caller"
)

// parseQoSClass accepts a class name case-insensitively.
func parseQoSClass(s string) (qosClass, bool) {
	switch qosClass(strings.ToLower(strings.TrimSpace(s))) {
	case qosInteractive:
		return qosInteractive, true
	case qosBackground:
		return qosBackground, true
	case qosBatch:
		return qosBatch, true
	}
	return "", false
}

// vllmPriority is the vLLM per-request `priority` for the class. vLLM
// schedules lower values first (vllm/v1/core/sched/request_queue.py,
// PriorityRequestQueue orders by (priority, arrival_time)).
func (c qosClass) vllmPriority() int {
	switch c {
	case qosBackground:
		return 10
	case qosBatch:
		return 20
	}
	return 0
}

// qosSource names the rule that resolved a request's class.
type qosSource string

const (
	qosSourceHeader  qosSource = "header"
	qosSourceAPIKey  qosSource = "api_key"
	qosSourceCaller  qosSource = "caller"
	qosSourceAlias   qosSource = "alias"
	qosSourceDefault qosSource = "default"
)

// qosAliasSuffixes are the model-name suffixes a LiteLLM alias may carry to
// select a class (workhorse-background → workhorse, background).
var qosAliasSuffixes = []struct {
	suffix string
	class  qosClass
}{
	{"-background", qosBackground},
	{"-batch", qosBatch},
}

// splitQoSAliasSuffix returns the base model name and the class a QoS alias
// suffix selects. ok is false when name carries no suffix or nothing precedes it.
func splitQoSAliasSuffix(name string) (base string, class qosClass, ok bool) {
	for _, s := range qosAliasSuffixes {
		if strings.HasSuffix(name, s.suffix) && len(name) > len(s.suffix) {
			return strings.TrimSuffix(name, s.suffix), s.class, true
		}
	}
	return name, "", false
}

// qosDecision is one request's resolved class. handleRequest stores it in the
// request context; lane is filled in once the model name resolves to a Model.
type qosDecision struct {
	class  qosClass
	source qosSource
	caller string
	lane   string
}

type qosDecisionKey struct{}

func withQoSDecision(ctx context.Context, d *qosDecision) context.Context {
	return context.WithValue(ctx, qosDecisionKey{}, d)
}

func qosDecisionFrom(ctx context.Context) *qosDecision {
	d, _ := ctx.Value(qosDecisionKey{}).(*qosDecision)
	return d
}

// qosClassLabel is the metric/log label for a request: its class, or
// interactive when no decision was recorded (requests that bypassed
// handleRequest).
func qosClassLabel(ctx context.Context) string {
	if d := qosDecisionFrom(ctx); d != nil {
		return string(d.class)
	}
	return string(qosInteractive)
}

// qosPolicy is the parsed caller-identity map.
type qosPolicy struct {
	callers map[string]qosClass // lower-cased caller name → class
	apiKeys map[string]qosClass // lower-case hex sha256 of the bearer key → class
}

// qosPolicyFile is the ConfigMap flexinfer-proxy-qos data key qos.yaml:
//
//	callers:
//	  loom-mills: background
//	apiKeys:
//	  "sha256:<hex of sha256(key)>": background
//
// API keys are stored as hashes because a ConfigMap is not a Secret.
type qosPolicyFile struct {
	Callers map[string]string `json:"callers"`
	APIKeys map[string]string `json:"apiKeys"`
}

func parseQoSPolicy(data []byte) (*qosPolicy, error) {
	var f qosPolicyFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse qos policy: %w", err)
	}
	p := &qosPolicy{callers: map[string]qosClass{}, apiKeys: map[string]qosClass{}}
	for name, v := range f.Callers {
		class, ok := parseQoSClass(v)
		if !ok {
			return nil, fmt.Errorf("caller %q: unknown qos class %q", name, v)
		}
		p.callers[strings.ToLower(strings.TrimSpace(name))] = class
	}
	for key, v := range f.APIKeys {
		class, ok := parseQoSClass(v)
		if !ok {
			return nil, fmt.Errorf("api key %q: unknown qos class %q", key, v)
		}
		hash := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(key), "sha256:"))
		if len(hash) != sha256.Size*2 {
			return nil, fmt.Errorf("api key %q: want sha256:<64 hex chars>", key)
		}
		p.apiKeys[hash] = class
	}
	return p, nil
}

// qosResolver holds the current caller-identity map and reloads it from the
// mounted ConfigMap file. A nil resolver, an unset path or a missing file
// mean an empty map: every request falls through to the alias suffix and the
// interactive default.
type qosResolver struct {
	path   string
	policy atomic.Pointer[qosPolicy]
	last   []byte
}

// qosPolicyReloadInterval is how often the mounted file is re-read. A
// ConfigMap volume update reaches the pod within the kubelet sync period
// (about a minute), so this adds at most 30 s.
const qosPolicyReloadInterval = 30 * time.Second

func newQoSResolver(path string) *qosResolver {
	q := &qosResolver{path: path}
	q.reload()
	return q
}

// reload re-reads the policy file. A parse error keeps the previous map so a
// bad edit cannot silently drop every mapping.
func (q *qosResolver) reload() {
	if q == nil || q.path == "" {
		return
	}
	data, err := os.ReadFile(q.path)
	if err != nil {
		if os.IsNotExist(err) {
			data = nil
		} else {
			slog.Warn("qos: failed to read caller policy", "path", q.path, "error", err)
			return
		}
	}
	if q.policy.Load() != nil && bytes.Equal(data, q.last) {
		return
	}
	policy, err := parseQoSPolicy(data)
	if err != nil {
		slog.Error("qos: keeping previous caller policy", "path", q.path, "error", err)
		return
	}
	q.policy.Store(policy)
	q.last = data
	slog.Info("qos: loaded caller policy", "path", q.path,
		"callers", len(policy.callers), "api_keys", len(policy.apiKeys))
}

func (q *qosResolver) watch(ctx context.Context) {
	if q == nil || q.path == "" {
		return
	}
	ticker := time.NewTicker(qosPolicyReloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.reload()
		}
	}
}

func (q *qosResolver) current() *qosPolicy {
	if q == nil {
		return nil
	}
	return q.policy.Load()
}

// qosCallerName is the caller name the ConfigMap maps: X-FlexInfer-Caller,
// else the User-Agent's first product token ("loom-mills/1.4 (go)" →
// "loom-mills"), lower-cased.
func qosCallerName(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get(headerCaller)); v != "" {
		return strings.ToLower(v)
	}
	ua := strings.TrimSpace(r.Header.Get("User-Agent"))
	if i := strings.IndexAny(ua, "/ "); i >= 0 {
		ua = ua[:i]
	}
	return strings.ToLower(ua)
}

// bearerKeyHash is the hex sha256 of the request's bearer key, or "" when it
// carries none.
func bearerKeyHash(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) < len("Bearer ") || !strings.EqualFold(auth[:len("Bearer ")], "Bearer ") {
		return ""
	}
	key := strings.TrimSpace(auth[len("Bearer "):])
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// resolveQoS resolves the request's class and strips the QoS headers so they
// never reach a model server. aliasClass is the class a QoS alias suffix on
// the requested model selected ("" when none).
func (p *Proxy) resolveQoS(r *http.Request, aliasClass qosClass) *qosDecision {
	raw := r.Header.Get(headerQoS)
	caller := qosCallerName(r)
	r.Header.Del(headerQoS)
	r.Header.Del(headerCaller)

	d := &qosDecision{caller: caller}
	if raw != "" {
		if class, ok := parseQoSClass(raw); ok {
			d.class, d.source = class, qosSourceHeader
			return d
		}
		slog.Debug("qos: ignoring unknown class header", "value", raw)
	}
	if policy := p.qos.current(); policy != nil {
		if hash := bearerKeyHash(r); hash != "" {
			if class, ok := policy.apiKeys[hash]; ok {
				d.class, d.source = class, qosSourceAPIKey
				return d
			}
		}
		if class, ok := policy.callers[caller]; ok && caller != "" {
			d.class, d.source = class, qosSourceCaller
			return d
		}
	}
	if aliasClass != "" {
		d.class, d.source = aliasClass, qosSourceAlias
		return d
	}
	d.class, d.source = qosInteractive, qosSourceDefault
	return d
}

// stripQoSAliasSuffix returns the routable model name for a requested name
// that carries a QoS alias suffix, and the class the suffix selects. The
// suffix is honoured only when the full name is not itself a Model, service
// label, alias or LoRA adapter, so an existing name ending in -batch keeps
// routing to itself.
func (p *Proxy) stripQoSAliasSuffix(ctx context.Context, name string) (string, qosClass) {
	base, class, ok := splitQoSAliasSuffix(name)
	if !ok || p.modelNameResolves(ctx, name) {
		return name, ""
	}
	return base, class
}

// modelNameResolves reports whether name routes as given: a Model, a service
// label, a model alias or a LoRA adapter.
func (p *Proxy) modelNameResolves(ctx context.Context, name string) bool {
	if !isValidModelResourceName(name) {
		return false
	}
	if _, err := p.getModel(ctx, name); err == nil {
		return true
	}
	if p.resolver == nil {
		return false
	}
	if members, isLabel := p.resolver.ResolveServiceLabelGroup(ctx, name); isLabel && len(members) > 0 {
		return true
	}
	if p.resolver.ResolveModelAlias(ctx, name) != name {
		return true
	}
	if _, isLoRA := p.resolver.ResolveLoRAAdapter(ctx, name); isLoRA {
		return true
	}
	return false
}

// qosOutcomeUnenforced is the decision outcome on a lane that does not
// enforce classes: the request is served exactly as before.
const qosOutcomeUnenforced = "unenforced"

// observeQoSServed records the decision counter and the per-class duration
// for one served request.
func observeQoSServed(d *qosDecision, outcome string, elapsed time.Duration) {
	if d == nil || d.lane == "" {
		return
	}
	qosDecisionsTotal.WithLabelValues(string(d.class), d.lane, outcome).Inc()
	qosRequestDuration.WithLabelValues(string(d.class), d.lane).Observe(elapsed.Seconds())
}
