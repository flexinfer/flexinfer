package main

import (
	"context"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
)

// Token verification handler.
func (g *gitlabServer) handleVerifyToken(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(g.token) == "" {
		return nil, mcperror.NotConfigured("GITLAB_PERSONAL_ACCESS_TOKEN", "set via environment variable or GITLAB_TOKEN")
	}
	if strings.Contains(g.token, "${") {
		return nil, mcperror.InvalidParam("token", "appears to be unexpanded - check your Loom secrets/keychain resolution")
	}

	result := map[string]any{
		"ok":      false,
		"api_url": g.apiURL,
		"token": map[string]any{
			"present": true,
		},
	}

	// Best-effort: PAT metadata (scopes, expiry). Not all GitLab versions expose this endpoint.
	if tok, err := g.request(ctx, "GET", "/personal_access_tokens/self", nil); err == nil {
		// Never return the actual token; the endpoint doesn't include it anyway, but keep future-proof.
		delete(tok, "token")
		result["personal_access_token"] = tok
	} else if err != nil {
		if mcpErr, ok := err.(*mcperror.Error); ok && mcpErr.Code == mcperror.CodeNotFound {
			// Older GitLab instances may not support this endpoint; fall back to /user.
		} else {
			// If it's not a 404, bubble up (401/403/5xx/etc).
			return nil, err
		}
	}

	user, err := g.request(ctx, "GET", "/user", nil)
	if err != nil {
		return nil, err
	}
	result["user"] = user
	result["ok"] = true
	return mcp.JSONResult(result)
}
