package agentcontext

import (
	"context"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// File claims: advisory locks for coordinating file edits between agents.
// This file contains thin delegation methods on Service that forward to ClaimSvc,
// plus payload conversion functions shared by both layers.

// HandleFileClaimAcquire claims a file for editing/review
func (s *Service) HandleFileClaimAcquire(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.claims.Acquire(ctx, args)
}

// HandleFileClaimRelease releases file claims
func (s *Service) HandleFileClaimRelease(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.claims.Release(ctx, args)
}

// HandleFileClaimQuery checks who holds claims on specific files
func (s *Service) HandleFileClaimQuery(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.claims.Query(ctx, args)
}

// HandleFileClaimList lists claims by agent
func (s *Service) HandleFileClaimList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	return s.claims.List(ctx, args)
}

// releaseAllClaimsForAgent removes all claims held by an agent.
func (s *Service) releaseAllClaimsForAgent(agentID string) int {
	return s.claims.ReleaseAllForAgent(agentID)
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
