package agentcontext

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// ClaimSvc manages advisory file locks for coordinating edits between agents.
type ClaimSvc struct {
	mu     sync.RWMutex
	claims map[string]map[string]*FileClaim // filePath -> agentID -> claim

	qdrant  *QdrantClient
	logger  *slog.Logger
	metrics *Metrics
}

// NewClaimSvc creates a new ClaimSvc.
func NewClaimSvc(qdrant *QdrantClient, logger *slog.Logger, metrics *Metrics) *ClaimSvc {
	return &ClaimSvc{
		claims:  make(map[string]map[string]*FileClaim),
		qdrant:  qdrant,
		logger:  logger,
		metrics: metrics,
	}
}

// Acquire claims a file for editing/review.
func (c *ClaimSvc) Acquire(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	sessionID := v.Required("session_id")
	filePath := v.Required("file_path")
	claimTypeStr := v.String("claim_type", string(ClaimTypeEdit))
	reason := v.String("reason", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	claimType := ClaimType(claimTypeStr)
	now := time.Now()

	claim := &FileClaim{
		ID:        GenerateID(agentID, filePath, "claim", now),
		AgentID:   agentID,
		SessionID: sessionID,
		FilePath:  filePath,
		ClaimType: claimType,
		Reason:    reason,
		CreatedAt: now,
	}

	var conflicts []map[string]any
	c.mu.RLock()
	if agents, ok := c.claims[filePath]; ok {
		for otherAgent, otherClaim := range agents {
			if otherAgent == agentID {
				continue
			}
			if otherClaim.ExpiresAt != nil && now.After(*otherClaim.ExpiresAt) {
				continue
			}
			conflicts = append(conflicts, map[string]any{
				"agent_id":   otherAgent,
				"claim_type": string(otherClaim.ClaimType),
				"reason":     otherClaim.Reason,
				"created_at": otherClaim.CreatedAt.Format(time.RFC3339),
			})
		}
	}
	c.mu.RUnlock()

	c.mu.Lock()
	if c.claims[filePath] == nil {
		c.claims[filePath] = make(map[string]*FileClaim)
	}
	c.claims[filePath][agentID] = claim
	c.mu.Unlock()

	result := map[string]any{
		"ok":        true,
		"claim_id":  claim.ID,
		"file_path": filePath,
		"agent_id":  agentID,
	}

	if err := c.persist(ctx, claim); err != nil {
		result["_warning"] = fmt.Sprintf("failed to persist claim: %v", err)
	}
	if len(conflicts) > 0 {
		result["has_conflicts"] = true
		result["conflicts"] = conflicts
	} else {
		result["has_conflicts"] = false
	}

	return mcp.JSONResult(result)
}

// Release releases file claims.
func (c *ClaimSvc) Release(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	filePath := v.Required("file_path")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	released := 0

	if filePath == "all" {
		released = c.ReleaseAllForAgent(agentID)
	} else {
		c.mu.Lock()
		if agents, ok := c.claims[filePath]; ok {
			if _, ok := agents[agentID]; ok {
				delete(agents, agentID)
				if len(agents) == 0 {
					delete(c.claims, filePath)
				}
				released = 1
			}
		}
		c.mu.Unlock()

		if c.qdrant != nil {
			if err := c.qdrant.DeleteByFilter(ctx, FilterMust(
				Match("agent_id", agentID),
				Match("file_path", filePath),
			)); err != nil {
				c.logger.Warn("failed to delete file claim from Qdrant", "agent_id", agentID, "file_path", filePath, "error", err)
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"agent_id": agentID,
		"released": released,
	})
}

// Query checks who holds claims on specific files.
func (c *ClaimSvc) Query(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	filePaths := v.StringSlice("file_paths")
	excludeAgent := v.String("exclude_agent", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if len(filePaths) == 0 {
		return mcp.ErrorResult(fmt.Errorf("file_paths is required")), nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	results := make(map[string][]map[string]any)

	for _, fp := range filePaths {
		if agents, ok := c.claims[fp]; ok {
			var claimList []map[string]any
			for claimAgent, cl := range agents {
				if claimAgent == excludeAgent {
					continue
				}
				if cl.ExpiresAt != nil && now.After(*cl.ExpiresAt) {
					continue
				}
				claimList = append(claimList, map[string]any{
					"agent_id":   claimAgent,
					"claim_type": string(cl.ClaimType),
					"reason":     cl.Reason,
					"created_at": cl.CreatedAt.Format(time.RFC3339),
				})
			}
			if len(claimList) > 0 {
				results[fp] = claimList
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"claims": results,
	})
}

// List lists claims optionally filtered by agent or type.
func (c *ClaimSvc) List(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	claimTypeStr := v.String("claim_type", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	var claimList []map[string]any

	for filePath, agents := range c.claims {
		for claimAgent, cl := range agents {
			if agentID != "" && claimAgent != agentID {
				continue
			}
			if claimTypeStr != "" && string(cl.ClaimType) != claimTypeStr {
				continue
			}
			if cl.ExpiresAt != nil && now.After(*cl.ExpiresAt) {
				continue
			}
			claimList = append(claimList, map[string]any{
				"claim_id":   cl.ID,
				"file_path":  filePath,
				"agent_id":   claimAgent,
				"session_id": cl.SessionID,
				"claim_type": string(cl.ClaimType),
				"reason":     cl.Reason,
				"created_at": cl.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"claims": claimList,
		"count":  len(claimList),
	})
}

// ReleaseAllForAgent removes all claims held by an agent. Returns count released.
func (c *ClaimSvc) ReleaseAllForAgent(agentID string) int {
	c.mu.Lock()

	released := 0
	for filePath, agents := range c.claims {
		if _, ok := agents[agentID]; ok {
			delete(agents, agentID)
			released++
			if len(agents) == 0 {
				delete(c.claims, filePath)
			}
		}
	}
	c.mu.Unlock()

	if released > 0 && c.qdrant != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := c.qdrant.DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID))); err != nil {
				c.logger.Warn("failed to delete file claims from Qdrant", "agent_id", agentID, "error", err)
			}
		}()
	}
	return released
}

// DetectConflicts checks if any files overlap with other agents' claims.
func (c *ClaimSvc) DetectConflicts(agentID string, files []string) []map[string]any {
	if len(files) == 0 {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	var conflicts []map[string]any
	for _, f := range files {
		if agents, ok := c.claims[f]; ok {
			for claimAgent, claim := range agents {
				if claimAgent == agentID {
					continue
				}
				if claim.ExpiresAt != nil && time.Now().After(*claim.ExpiresAt) {
					continue
				}
				conflicts = append(conflicts, map[string]any{
					"file":       f,
					"agent_id":   claimAgent,
					"claim_type": string(claim.ClaimType),
					"source":     "file_claim",
				})
			}
		}
	}

	return conflicts
}

func (c *ClaimSvc) persist(ctx context.Context, claim *FileClaim) error {
	if c.qdrant == nil {
		return nil
	}
	if err := c.qdrant.EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      claim.ID,
		Vector:  make([]float64, sessionsVectorSize),
		Payload: fileClaimToPayload(claim),
	}

	return c.qdrant.Upsert(ctx, []Point{point}, true)
}

// LoadFromQdrant loads file claims from Qdrant on startup.
func (c *ClaimSvc) LoadFromQdrant(ctx context.Context) error {
	if c.qdrant == nil {
		return nil
	}
	points, err := c.qdrant.ScrollPoints(ctx, nil, 1000, false)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for _, p := range points {
		claim := payloadToFileClaim(p.Payload)
		if claim == nil {
			continue
		}
		if claim.ExpiresAt != nil && now.After(*claim.ExpiresAt) {
			continue
		}
		if c.claims[claim.FilePath] == nil {
			c.claims[claim.FilePath] = make(map[string]*FileClaim)
		}
		c.claims[claim.FilePath][claim.AgentID] = claim
	}
	return nil
}
