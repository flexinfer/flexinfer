package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

func (s *linkedInServer) handleAuthStatus(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	activeAuth := "none"
	switch {
	case s.accessToken != "":
		activeAuth = "access_token"
	case s.sessionToken != "":
		activeAuth = "session_cookie"
	}

	canUseMessaging := s.ensureMessagingAllowed() == nil
	canSendMessaging := s.ensureSendAllowed() == nil

	now := s.now()
	remainingCooldown := s.cooldownRemaining(now)

	s.stateMu.Lock()
	lastState := s.lastSessionState
	lastHealth := s.lastHealthCheckAt
	lastRecovery := s.lastRecoveryAt
	recoveryInProgress := s.recoveryInProgress
	s.stateMu.Unlock()

	if lastState == "" {
		lastState = linkedInSessionStateUnknown
	}

	resp := map[string]any{
		"mode": s.mode,
		"auth": map[string]any{
			"active_auth":        activeAuth,
			"has_access_token":   s.accessToken != "",
			"session_cookie":     redactedDescriptor(s.sessionToken),
			"jsessionid":         redactedDescriptor(s.jsessionID),
			"has_session_cookie": s.sessionToken != "",
			"has_jsessionid":     s.jsessionID != "",
		},
		"capabilities": map[string]any{
			"profile_read":      true,
			"messaging_read":    canUseMessaging,
			"messaging_send":    canSendMessaging,
			"experimental_mode": s.mode != linkedinModeOfficial,
		},
		"browserkit": map[string]any{
			"mode":                  s.browserKit.mode,
			"enabled":               s.browserKit.mode != linkedInBrowserKitModeOff,
			"python":                s.browserKit.python,
			"has_login_credentials": strings.TrimSpace(s.loginUsername) != "" && strings.TrimSpace(s.loginPassword) != "",
			"storage_dir":           s.browserKit.storageDir,
			"session_id":            s.browserKit.sessionID,
			"storage_state_file":    filepath.Join(s.browserKit.storageDir, s.browserKit.sessionID+".json"),
			"storage_state_present": s.hasBrowserKitStorageState(),
		},
		"messaging_graphql": map[string]any{
			"mailbox_urn_configured":       strings.TrimSpace(s.mailboxURN) != "",
			"conversations_query_id":       s.conversationsQID,
			"conversation_messages_qid":    s.messagesQID,
			"legacy_messaging_path_active": s.conversationsQID == "" || s.messagesQID == "",
		},
		"session": map[string]any{
			"state":                               lastState,
			"last_health_check_at":                formatRFC3339(lastHealth),
			"last_recovery_at":                    formatRFC3339(lastRecovery),
			"health_ttl_seconds":                  int(s.browserKit.healthTTL.Seconds()),
			"recovery_cooldown_seconds":           int(s.browserKit.recoveryCooldown.Seconds()),
			"recovery_cooldown_remaining_seconds": int(remainingCooldown.Seconds()),
			"recovery_in_progress":                recoveryInProgress,
		},
	}
	return mcp.JSONResult(resp)
}

func (s *linkedInServer) handleSessionHealth(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	refresh := v.Bool("refresh", true)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	res, err := s.runSessionHealth(ctx, refresh)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(s.sessionResultPayload("health", res, false))
}

func (s *linkedInServer) handleSessionRecover(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	mode, err := parseLinkedInRecoveryMode(v.String("mode", linkedInRecoveryModeInteractive))
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	force := v.Bool("force", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	res, err := s.recoverSession(ctx, mode, force)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(s.sessionResultPayload("recover", res, true))
}

func (s *linkedInServer) sessionResultPayload(operation string, res *linkedInBrowserKitResponse, includeMode bool) map[string]any {
	if res == nil {
		res = &linkedInBrowserKitResponse{OK: false, State: linkedInSessionStateError}
	}
	payload := map[string]any{
		"ok":        res.OK,
		"operation": operation,
		"state":     res.State,
		"final_url": res.FinalURL,
		"auth": map[string]any{
			"session_cookie": redactedDescriptor(res.LIAt),
			"jsessionid":     redactedDescriptor(res.JSessionID),
			"has_li_at":      res.HasLIAt,
			"has_jsessionid": res.HasJSession,
		},
		"warnings": res.Warnings,
	}
	if includeMode {
		payload["mode"] = s.browserKit.mode
	}
	return payload
}

func (s *linkedInServer) ensureMessagingAllowed() error {
	if s.mode == linkedinModeOfficial {
		return mcperror.Forbidden("messaging tools are disabled when LINKEDIN_MODE=official")
	}
	if strings.TrimSpace(s.sessionToken) == "" && !s.canBootstrapSessionViaRecovery() {
		return mcperror.NotConfigured("LINKEDIN_SESSION_COOKIE", "required for LinkedIn messaging tools")
	}
	return nil
}

func (s *linkedInServer) canBootstrapSessionViaRecovery() bool {
	return s.browserKit.mode != linkedInBrowserKitModeOff &&
		strings.TrimSpace(s.loginUsername) != "" &&
		strings.TrimSpace(s.loginPassword) != ""
}

func (s *linkedInServer) ensureSendAllowed() error {
	if err := s.ensureMessagingAllowed(); err != nil {
		return err
	}
	if strings.TrimSpace(s.jsessionID) == "" {
		return mcperror.NotConfigured("LINKEDIN_JSESSIONID", "required for LinkedIn messaging send operations")
	}
	return nil
}

func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
