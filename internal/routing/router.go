package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
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

// Router handles routing decisions for multi-replica models.
type Router struct {
	mu    sync.RWMutex
	rings map[string]*HashRing // model name -> hash ring
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

	ring = NewHashRing(150)
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
	ring := r.GetRing(model)
	if ring.Size() == 0 {
		return "" // No endpoints, fall back to Service DNS
	}

	var key string

	switch strategy {
	case StrategySessionAffinity:
		key = ExtractSessionID(req, body)
	case StrategyPrefix:
		key = ExtractPrefix(body)
	case StrategyLeastLoaded:
		return r.selectLeastLoaded(model, loadFn)
	default:
		// Default strategy - no affinity
		return ""
	}

	if key == "" {
		return "" // No key available, fall back to default routing
	}

	return ring.Get(key)
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
	// Check explicit session header
	if sessionID := req.Header.Get("X-Session-ID"); sessionID != "" {
		return sessionID
	}

	// Check for conversation ID (common in chat applications)
	if convID := req.Header.Get("X-Conversation-ID"); convID != "" {
		return convID
	}

	// Try to extract from request body
	if len(body) == 0 {
		return ""
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return ""
	}

	// Check for explicit session_id in body
	if sessionID, ok := data["session_id"].(string); ok && sessionID != "" {
		return sessionID
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
		return "msg:" + hex.EncodeToString(h.Sum(nil))[:16]
	}

	return ""
}

// ExtractPrefix extracts the system prompt prefix for prefix-based routing.
// This enables KV-cache sharing for requests with the same system prompt.
func ExtractPrefix(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return ""
	}

	// Check for explicit prefix
	if prefix, ok := data["prefix"].(string); ok && prefix != "" {
		h := sha256.Sum256([]byte(prefix))
		return "pfx:" + hex.EncodeToString(h[:])[:16]
	}

	// Extract system prompt from messages
	if messages, ok := data["messages"].([]interface{}); ok && len(messages) > 0 {
		if firstMsg, ok := messages[0].(map[string]interface{}); ok {
			if role, ok := firstMsg["role"].(string); ok && role == "system" {
				if content, ok := firstMsg["content"].(string); ok && content != "" {
					h := sha256.Sum256([]byte(content))
					return "sys:" + hex.EncodeToString(h[:])[:16]
				}
			}
		}
	}

	return ""
}
