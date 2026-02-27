package agentcontext

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// File claims: advisory locks for coordinating file edits between agents.

// HandleFileClaimAcquire claims a file for editing/review
func (s *Service) HandleFileClaimAcquire(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
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

	// Check for existing claims from other agents
	var conflicts []map[string]any
	s.fileClaimsMu.RLock()
	if agents, ok := s.fileClaims[filePath]; ok {
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
	s.fileClaimsMu.RUnlock()

	// Store the claim (advisory — we always store even if conflicts exist)
	s.fileClaimsMu.Lock()
	if s.fileClaims[filePath] == nil {
		s.fileClaims[filePath] = make(map[string]*FileClaim)
	}
	s.fileClaims[filePath][agentID] = claim
	s.fileClaimsMu.Unlock()

	result := map[string]any{
		"ok":        true,
		"claim_id":  claim.ID,
		"file_path": filePath,
		"agent_id":  agentID,
	}

	// Persist to Qdrant (non-fatal)
	if err := s.persistFileClaim(ctx, claim); err != nil {
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

// HandleFileClaimRelease releases file claims
func (s *Service) HandleFileClaimRelease(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.Required("agent_id")
	filePath := v.Required("file_path")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	released := 0

	if filePath == "all" {
		released = s.releaseAllClaimsForAgent(agentID)
	} else {
		s.fileClaimsMu.Lock()
		if agents, ok := s.fileClaims[filePath]; ok {
			if _, ok := agents[agentID]; ok {
				delete(agents, agentID)
				if len(agents) == 0 {
					delete(s.fileClaims, filePath)
				}
				released = 1
			}
		}
		s.fileClaimsMu.Unlock()

		// Remove from Qdrant
		if err := s.qdrant.Get(CollFileClaims).DeleteByFilter(ctx, FilterMust(
			Match("agent_id", agentID),
			Match("file_path", filePath),
		)); err != nil {
			s.logger.Warn("failed to delete file claim from Qdrant", "agent_id", agentID, "file_path", filePath, "error", err)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"agent_id": agentID,
		"released": released,
	})
}

// HandleFileClaimQuery checks who holds claims on specific files
func (s *Service) HandleFileClaimQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	filePaths := v.StringSlice("file_paths")
	excludeAgent := v.String("exclude_agent", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if len(filePaths) == 0 {
		return mcp.ErrorResult(fmt.Errorf("file_paths is required")), nil
	}

	s.fileClaimsMu.RLock()
	defer s.fileClaimsMu.RUnlock()

	now := time.Now()
	results := make(map[string][]map[string]any)

	for _, fp := range filePaths {
		if agents, ok := s.fileClaims[fp]; ok {
			var claims []map[string]any
			for agentID, claim := range agents {
				if agentID == excludeAgent {
					continue
				}
				if claim.ExpiresAt != nil && now.After(*claim.ExpiresAt) {
					continue
				}
				claims = append(claims, map[string]any{
					"agent_id":   agentID,
					"claim_type": string(claim.ClaimType),
					"reason":     claim.Reason,
					"created_at": claim.CreatedAt.Format(time.RFC3339),
				})
			}
			if len(claims) > 0 {
				results[fp] = claims
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"claims": results,
	})
}

// HandleFileClaimList lists claims by agent
func (s *Service) HandleFileClaimList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	agentID := v.String("agent_id", "")
	claimTypeStr := v.String("claim_type", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	s.fileClaimsMu.RLock()
	defer s.fileClaimsMu.RUnlock()

	now := time.Now()
	var claims []map[string]any

	for filePath, agents := range s.fileClaims {
		for claimAgent, claim := range agents {
			if agentID != "" && claimAgent != agentID {
				continue
			}
			if claimTypeStr != "" && string(claim.ClaimType) != claimTypeStr {
				continue
			}
			if claim.ExpiresAt != nil && now.After(*claim.ExpiresAt) {
				continue
			}
			claims = append(claims, map[string]any{
				"claim_id":   claim.ID,
				"file_path":  filePath,
				"agent_id":   claimAgent,
				"session_id": claim.SessionID,
				"claim_type": string(claim.ClaimType),
				"reason":     claim.Reason,
				"created_at": claim.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"claims": claims,
		"count":  len(claims),
	})
}

// releaseAllClaimsForAgent removes all claims held by an agent from in-memory map and Qdrant.
// Returns count released.
func (s *Service) releaseAllClaimsForAgent(agentID string) int {
	s.fileClaimsMu.Lock()

	released := 0
	for filePath, agents := range s.fileClaims {
		if _, ok := agents[agentID]; ok {
			delete(agents, agentID)
			released++
			if len(agents) == 0 {
				delete(s.fileClaims, filePath)
			}
		}
	}
	s.fileClaimsMu.Unlock()

	// Also clean up Qdrant (best-effort, non-blocking)
	if released > 0 && s.qdrant.Get(CollFileClaims) != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.qdrant.Get(CollFileClaims).DeleteByFilter(ctx, FilterMust(Match("agent_id", agentID))); err != nil {
				s.logger.Warn("failed to delete file claims from Qdrant", "agent_id", agentID, "error", err)
			}
		}()
	}
	return released
}

// persistFileClaim stores a claim to Qdrant
func (s *Service) persistFileClaim(ctx context.Context, claim *FileClaim) error {
	if err := s.qdrant.Get(CollFileClaims).EnsureCollection(ctx, sessionsVectorSize); err != nil {
		return err
	}

	point := Point{
		ID:      claim.ID,
		Vector:  make([]float64, sessionsVectorSize),
		Payload: fileClaimToPayload(claim),
	}

	return s.qdrant.Get(CollFileClaims).Upsert(ctx, []Point{point}, true)
}

// loadFileClaimsFromQdrant loads file claims from Qdrant on startup
func (s *Service) loadFileClaimsFromQdrant(ctx context.Context) error {
	points, err := s.qdrant.Get(CollFileClaims).ScrollPoints(ctx, nil, 1000, false)
	if err != nil {
		return err
	}

	s.fileClaimsMu.Lock()
	defer s.fileClaimsMu.Unlock()

	now := time.Now()
	for _, p := range points {
		claim := payloadToFileClaim(p.Payload)
		if claim == nil {
			continue
		}
		// Skip expired claims
		if claim.ExpiresAt != nil && now.After(*claim.ExpiresAt) {
			continue
		}
		if s.fileClaims[claim.FilePath] == nil {
			s.fileClaims[claim.FilePath] = make(map[string]*FileClaim)
		}
		s.fileClaims[claim.FilePath][claim.AgentID] = claim
	}
	return nil
}

// Payload converters

func fileClaimToPayload(c *FileClaim) map[string]any {
	payload := map[string]any{
		"id":         c.ID,
		"agent_id":   c.AgentID,
		"session_id": c.SessionID,
		"file_path":  c.FilePath,
		"claim_type": string(c.ClaimType),
		"reason":     c.Reason,
		"created_at": c.CreatedAt.Format(time.RFC3339Nano),
	}
	if c.ExpiresAt != nil {
		payload["expires_at"] = c.ExpiresAt.Format(time.RFC3339Nano)
	}
	return payload
}

func payloadToFileClaim(payload map[string]any) *FileClaim {
	if payload == nil {
		return nil
	}
	c := &FileClaim{
		ID:        toString(payload["id"]),
		AgentID:   toString(payload["agent_id"]),
		SessionID: toString(payload["session_id"]),
		FilePath:  toString(payload["file_path"]),
		ClaimType: ClaimType(toString(payload["claim_type"])),
		Reason:    toString(payload["reason"]),
	}
	if ts := toString(payload["created_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			c.CreatedAt = t
		}
	}
	if ts := toString(payload["expires_at"]); ts != "" {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			c.ExpiresAt = &t
		}
	}
	return c
}
