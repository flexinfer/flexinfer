package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// Strategy defines the routing strategy for a model.
type Strategy string

const (
	// StrategyDefault uses Kubernetes Service load balancing (round-robin).
	StrategyDefault Strategy = ""

	// StrategySessionAffinity routes requests with the same session ID to the same pod.
	StrategySessionAffinity Strategy = "session-affinity"

	// StrategyPrefix routes requests with the same system prompt prefix to the same pod.
	StrategyPrefix Strategy = "prefix"

	// StrategyLeastLoaded routes to the pod with the lowest current load.
	StrategyLeastLoaded Strategy = "least-loaded"
)

// KeySource identifies where a routing key came from.
type KeySource string

const (
	KeySourceNone            KeySource = "none"
	KeySourceSessionHeader   KeySource = "session-header"
	KeySourceConversation    KeySource = "conversation-header"
	KeySourceSessionBody     KeySource = "session-body"
	KeySourceSessionMessages KeySource = "session-messages"
	KeySourceExplicitHeader  KeySource = "explicit-header"
	KeySourceExplicitBody    KeySource = "explicit-body"
	KeySourcePrefixField     KeySource = "prefix-field"
	KeySourceCanonical       KeySource = "canonical"
	KeySourceSessionFallback KeySource = "session-fallback"
)

const (
	headerCacheKey                   = "X-Flexinfer-Cache-Key"
	defaultExplicitCacheKeyMaxLength = 128
	defaultSystemSegmentMaxLength    = 512
	defaultDocSegmentMaxLength       = 256
)

var explicitCacheKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:/=-]+$`)

// PrefixKeyConfig controls strictness for prefix/canonical key extraction.
type PrefixKeyConfig struct {
	ExplicitCacheKeyMaxLength int
	SystemSegmentMaxLength    int
	DocSegmentMaxLength       int
}

var prefixKeyConfig = DefaultPrefixKeyConfig()

// DefaultPrefixKeyConfig returns default keying bounds.
func DefaultPrefixKeyConfig() PrefixKeyConfig {
	return PrefixKeyConfig{
		ExplicitCacheKeyMaxLength: defaultExplicitCacheKeyMaxLength,
		SystemSegmentMaxLength:    defaultSystemSegmentMaxLength,
		DocSegmentMaxLength:       defaultDocSegmentMaxLength,
	}
}

// CurrentPrefixKeyConfig returns the current keying bounds.
func CurrentPrefixKeyConfig() PrefixKeyConfig {
	return prefixKeyConfig
}

// SetPrefixKeyConfig sets keying bounds, falling back to defaults for invalid values.
func SetPrefixKeyConfig(cfg PrefixKeyConfig) {
	prefixKeyConfig = sanitizePrefixKeyConfig(cfg)
}

func sanitizePrefixKeyConfig(cfg PrefixKeyConfig) PrefixKeyConfig {
	sanitized := DefaultPrefixKeyConfig()
	if cfg.ExplicitCacheKeyMaxLength > 0 {
		sanitized.ExplicitCacheKeyMaxLength = cfg.ExplicitCacheKeyMaxLength
	}
	if cfg.SystemSegmentMaxLength > 0 {
		sanitized.SystemSegmentMaxLength = cfg.SystemSegmentMaxLength
	}
	if cfg.DocSegmentMaxLength > 0 {
		sanitized.DocSegmentMaxLength = cfg.DocSegmentMaxLength
	}
	return sanitized
}

// defaultVirtualNodes is the number of virtual nodes per real node on the hash ring.
// Higher values give more even distribution but use more memory (150 is typical for <100 backends).
const defaultVirtualNodes = 150

// Router handles routing decisions for multi-replica models.
type Router struct {
	mu    sync.RWMutex
	rings map[string]*HashRing // model name -> hash ring
}

// RouteDecision captures the selected route target and key metadata.
type RouteDecision struct {
	Target    string
	Key       string
	KeySource KeySource
}

// NewRouter creates a new router instance.
func NewRouter() *Router {
	return &Router{
		rings: make(map[string]*HashRing),
	}
}

// GetRing returns the hash ring for a model, creating one if needed.
func (r *Router) GetRing(model string) *HashRing {
	r.mu.RLock()
	ring, ok := r.rings[model]
	r.mu.RUnlock()

	if ok {
		return ring
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if ring, ok = r.rings[model]; ok {
		return ring
	}

	ring = NewHashRing(defaultVirtualNodes)
	r.rings[model] = ring
	return ring
}

// UpdateEndpoints updates the endpoints for a model.
func (r *Router) UpdateEndpoints(model string, endpoints []string) {
	ring := r.GetRing(model)
	ring.SetNodes(endpoints)
}

// RemoveModel removes a model's routing state.
func (r *Router) RemoveModel(model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rings, model)
}

// LoadFunc is a function that returns the current load for a pod address.
// Higher values indicate more load.
type LoadFunc func(podAddr string) int64

// Route returns the target pod address for a request based on the routing strategy.
// Returns empty string if no routing is available (should fall back to Service DNS).
func (r *Router) Route(model string, strategy Strategy, req *http.Request, body []byte) string {
	return r.RouteWithLoad(model, strategy, req, body, nil)
}

// RouteWithLoad returns the target pod address, using load information for least-loaded routing.
func (r *Router) RouteWithLoad(model string, strategy Strategy, req *http.Request, body []byte, loadFn LoadFunc) string {
	return r.RouteWithDecision(model, strategy, req, body, loadFn).Target
}

// RouteWithDecision returns route target plus key metadata for observability.
func (r *Router) RouteWithDecision(model string, strategy Strategy, req *http.Request, body []byte, loadFn LoadFunc) RouteDecision {
	ring := r.GetRing(model)
	if ring.Size() == 0 {
		return RouteDecision{} // No endpoints, fall back to Service DNS
	}

	var (
		key    string
		source KeySource
	)

	switch strategy {
	case StrategySessionAffinity:
		key, source = ExtractSessionKey(req, body)
	case StrategyPrefix:
		key, source = ExtractPrefixKey(req, body)
		if key == "" {
			if fallbackKey, fallbackSource := ExtractSessionKey(req, body); fallbackKey != "" {
				key = fallbackKey
				source = KeySourceSessionFallback
				if fallbackSource == KeySourceNone {
					source = KeySourceNone
				}
			}
		}
	case StrategyLeastLoaded:
		return RouteDecision{
			Target:    r.selectLeastLoaded(model, loadFn),
			KeySource: KeySourceNone,
		}
	default:
		// Default strategy - no affinity
		return RouteDecision{}
	}

	if key == "" {
		return RouteDecision{KeySource: source} // No key available, fall back to default routing
	}

	return RouteDecision{
		Target:    ring.Get(key),
		Key:       key,
		KeySource: source,
	}
}

// selectLeastLoaded returns the pod with the lowest load.
func (r *Router) selectLeastLoaded(model string, loadFn LoadFunc) string {
	ring := r.GetRing(model)
	nodes := ring.Nodes()

	if len(nodes) == 0 {
		return ""
	}

	if loadFn == nil {
		// No load function provided, return first node (arbitrary but consistent)
		return nodes[0]
	}

	// Find node with minimum load
	minLoad := int64(1<<63 - 1) // MaxInt64
	var minNode string

	for _, node := range nodes {
		load := loadFn(node)
		if load < minLoad {
			minLoad = load
			minNode = node
		}
	}

	return minNode
}

// ExtractSessionID extracts a session identifier from a request.
// Priority:
// 1. X-Session-ID header (explicit)
// 2. Hash of first message content (implicit, for chat continuity)
func ExtractSessionID(req *http.Request, body []byte) string {
	key, _ := ExtractSessionKey(req, body)
	return key
}

// ExtractSessionKey extracts a session key and key source.
func ExtractSessionKey(req *http.Request, body []byte) (string, KeySource) {
	// Check explicit session header
	if sessionID := req.Header.Get("X-Session-ID"); sessionID != "" {
		return sessionID, KeySourceSessionHeader
	}

	// Check for conversation ID (common in chat applications)
	if convID := req.Header.Get("X-Conversation-ID"); convID != "" {
		return convID, KeySourceConversation
	}

	// Try to extract from request body
	if len(body) == 0 {
		return "", KeySourceNone
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", KeySourceNone
	}

	// Check for explicit session_id in body
	if sessionID, ok := data["session_id"].(string); ok && sessionID != "" {
		return sessionID, KeySourceSessionBody
	}

	// For chat completions, hash the messages to create implicit session ID
	// This provides affinity for requests with the same conversation history
	if messages, ok := data["messages"].([]interface{}); ok && len(messages) > 0 {
		// Hash first few messages (conversation context)
		// Limit to avoid hashing huge histories
		maxMessages := 5
		if len(messages) < maxMessages {
			maxMessages = len(messages)
		}

		h := sha256.New()
		for i := 0; i < maxMessages; i++ {
			if msg, ok := messages[i].(map[string]interface{}); ok {
				if content, ok := msg["content"].(string); ok {
					h.Write([]byte(content))
				}
			}
		}
		return "msg:" + hex.EncodeToString(h.Sum(nil))[:16], KeySourceSessionMessages
	}

	return "", KeySourceNone
}

// ExtractPrefix extracts the system prompt prefix for prefix-based routing.
// This enables KV-cache sharing for requests with the same system prompt.
func ExtractPrefix(body []byte) string {
	key, _ := ExtractPrefixKey(nil, body)
	return key
}

// ExtractPrefixKey extracts the prefix key and key source with precedence:
// 1) X-Flexinfer-Cache-Key header
// 2) cache_key / cacheKey body field
// 3) legacy prefix field
// 4) canonicalized system/document context hash
func ExtractPrefixKey(req *http.Request, body []byte) (string, KeySource) {
	if req != nil {
		if explicit, ok := normalizeExplicitCacheKey(req.Header.Get(headerCacheKey)); ok {
			return hashKey("exp:", explicit), KeySourceExplicitHeader
		}
	}

	if len(body) == 0 {
		return "", KeySourceNone
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", KeySourceNone
	}

	if explicit, ok := extractExplicitBodyKey(data); ok {
		return hashKey("exp:", explicit), KeySourceExplicitBody
	}

	// Check for legacy explicit prefix.
	if prefix, ok := data["prefix"].(string); ok && strings.TrimSpace(prefix) != "" {
		cfg := CurrentPrefixKeyConfig()
		return hashKey("pfx:", normalizeText(prefix, cfg.SystemSegmentMaxLength)), KeySourcePrefixField
	}

	canonical := extractCanonicalPrefixMaterial(data)
	if canonical == "" {
		return "", KeySourceNone
	}
	return hashKey("sys:", canonical), KeySourceCanonical
}

func normalizeExplicitCacheKey(in string) (string, bool) {
	cfg := CurrentPrefixKeyConfig()
	trimmed := strings.ToLower(strings.TrimSpace(in))
	if trimmed == "" || len(trimmed) > cfg.ExplicitCacheKeyMaxLength {
		return "", false
	}
	if !explicitCacheKeyPattern.MatchString(trimmed) {
		return "", false
	}
	return trimmed, true
}

func extractExplicitBodyKey(data map[string]interface{}) (string, bool) {
	for _, field := range []string{"cache_key", "cacheKey"} {
		if raw, ok := data[field].(string); ok {
			if normalized, valid := normalizeExplicitCacheKey(raw); valid {
				return normalized, true
			}
		}
	}
	return "", false
}

func extractCanonicalPrefixMaterial(data map[string]interface{}) string {
	system := extractSystemContext(data)
	document := extractDocumentContext(data)

	switch {
	case system != "" && document != "":
		return "system=" + system + "|doc=" + document
	case system != "":
		return "system=" + system
	case document != "":
		return "doc=" + document
	default:
		return ""
	}
}

func extractSystemContext(data map[string]interface{}) string {
	cfg := CurrentPrefixKeyConfig()
	messages, ok := data["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		return ""
	}

	parts := make([]string, 0, 3)
	for _, rawMsg := range messages {
		msg, ok := rawMsg.(map[string]interface{})
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if !strings.EqualFold(role, "system") {
			continue
		}
		content := normalizeText(extractTextPayload(msg["content"]), cfg.SystemSegmentMaxLength)
		if content == "" {
			continue
		}
		parts = append(parts, content)
		if len(parts) >= 3 {
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func extractDocumentContext(data map[string]interface{}) string {
	cfg := CurrentPrefixKeyConfig()
	for _, key := range []string{"document_context", "documentContext", "context"} {
		if normalized := normalizeText(extractTextPayload(data[key]), cfg.DocSegmentMaxLength); normalized != "" {
			return normalized
		}
	}

	if docs, ok := data["documents"].([]interface{}); ok && len(docs) > 0 {
		if normalized := normalizeText(extractTextPayload(docs[0]), cfg.DocSegmentMaxLength); normalized != "" {
			return normalized
		}
		if first, ok := docs[0].(map[string]interface{}); ok {
			for _, key := range []string{"content", "text", "value"} {
				if normalized := normalizeText(extractTextPayload(first[key]), cfg.DocSegmentMaxLength); normalized != "" {
					return normalized
				}
			}
		}
	}

	return ""
}

func extractTextPayload(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := strings.TrimSpace(extractTextPayload(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]interface{}:
		for _, key := range []string{"text", "content", "value"} {
			if text := strings.TrimSpace(extractTextPayload(v[key])); text != "" {
				return text
			}
		}
		if text := strings.TrimSpace(extractTextPayload(v["parts"])); text != "" {
			return text
		}
		return ""
	default:
		return ""
	}
}

func normalizeText(in string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(in)), " "))
	if normalized == "" {
		return ""
	}
	if len(normalized) > maxLen {
		return normalized[:maxLen]
	}
	return normalized
}

func hashKey(prefix, material string) string {
	h := sha256.Sum256([]byte(material))
	return prefix + hex.EncodeToString(h[:])[:16]
}
