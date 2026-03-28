// knowledge_graph_reasoning.go — reasoning chain storage and retrieval for KnowledgeGraph.
package agentcontext

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AddReasoningChain stores a reasoning chain
func (g *KnowledgeGraph) AddReasoningChain(chain *ReasoningChain) error {
	if chain.Query == "" {
		return fmt.Errorf("query is required")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if chain.ID == "" {
		chain.ID = uuid.New().String()[:12]
	}
	if chain.CreatedAt.IsZero() {
		chain.CreatedAt = time.Now().UTC()
	}

	g.reasoningChains[chain.ID] = chain
	return nil
}

// GetReasoningChain retrieves a reasoning chain
func (g *KnowledgeGraph) GetReasoningChain(id string) (*ReasoningChain, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	chain, ok := g.reasoningChains[id]
	if !ok {
		return nil, fmt.Errorf("reasoning chain not found: %s", id)
	}
	return chain, nil
}

// ListReasoningChains lists reasoning chains
func (g *KnowledgeGraph) ListReasoningChains(sessionID, agentID string, limit int) []*ReasoningChain {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var result []*ReasoningChain
	for _, chain := range g.reasoningChains {
		if sessionID != "" && chain.SessionID != sessionID {
			continue
		}
		if agentID != "" && chain.AgentID != agentID {
			continue
		}
		result = append(result, chain)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}
