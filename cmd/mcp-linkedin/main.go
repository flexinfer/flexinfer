// mcp-linkedin provides LinkedIn personal account operations via MCP.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/secrets"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

const (
	defaultLinkedInBaseURL = "https://www.linkedin.com/voyager/api"
	maxResponseBytes       = 2 * 1024 * 1024 // 2MB cap to keep responses bounded.

	linkedinModeAuto         = "auto"
	linkedinModeOfficial     = "official"
	linkedinModeExperimental = "experimental"

	defaultMessengerConversationsQueryID = "messengerConversations.0d5e6781bbee71c3e51c8843c6519f48"
	defaultMessengerMessagesQueryID      = "messengerMessages.5846eeb71c981f11e0134cb6626cc314"
)

var version = "0.1.0"

type linkedInServer struct {
	baseURL      string
	mode         string
	accessToken  string
	sessionToken string
	jsessionID   string
	httpClient   *httpclient.Client

	logger           *slog.Logger
	browserKit       linkedInBrowserKitConfig
	browserKitRunner linkedInBrowserKitRunner
	secretStore      secretSetter
	loginUsername    string
	loginPassword    string
	mailboxURN       string
	conversationsQID string
	messagesQID      string

	stateMu            sync.Mutex
	lastHealthCheckAt  time.Time
	lastRecoveryAt     time.Time
	lastSessionState   string
	recoveryInProgress bool

	now func() time.Time
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	baseURL := strings.TrimSuffix(env.String("LINKEDIN_BASE_URL", defaultLinkedInBaseURL), "/")
	mode, err := parseLinkedInMode(env.String("LINKEDIN_MODE", linkedinModeAuto))
	if err != nil {
		return err
	}
	accessToken := env.StringWithFallbacks("LINKEDIN_ACCESS_TOKEN", "LINKEDIN_TOKEN")
	sessionToken := env.StringWithFallbacks("LINKEDIN_SESSION_COOKIE", "LINKEDIN_LI_AT", "LI_AT")
	jsessionID := env.StringWithFallbacks("LINKEDIN_JSESSIONID", "JSESSIONID")
	loginUsername := strings.TrimSpace(env.StringWithFallbacks("LINKEDIN_LOGIN_USERNAME", "LINKEDIN_USERNAME"))
	loginPassword := strings.TrimSpace(env.StringWithFallbacks("LINKEDIN_LOGIN_PASSWORD", "LINKEDIN_PASSWORD"))
	mailboxURN := strings.TrimSpace(env.String("LINKEDIN_MESSAGING_MAILBOX_URN", ""))
	conversationsQID := strings.TrimSpace(env.String("LINKEDIN_MESSENGER_CONVERSATIONS_QUERY_ID", defaultMessengerConversationsQueryID))
	messagesQID := strings.TrimSpace(env.String("LINKEDIN_MESSENGER_MESSAGES_QUERY_ID", defaultMessengerMessagesQueryID))

	browserKitMode, err := parseLinkedInBrowserKitMode(env.String("LINKEDIN_BROWSERKIT_MODE", linkedInBrowserKitModeAuto))
	if err != nil {
		return err
	}
	browserKitPython := defaultLinkedInBrowserKitPython()
	browserKitStorageDir := strings.TrimSpace(env.String("LINKEDIN_BROWSERKIT_STORAGE_DIR", defaultLinkedInBrowserKitStorageDir()))
	if browserKitStorageDir == "" {
		browserKitStorageDir = defaultLinkedInBrowserKitStorageDir()
	}
	browserKitSessionID := strings.TrimSpace(env.String("LINKEDIN_BROWSERKIT_SESSION_ID", "primary"))
	if browserKitSessionID == "" {
		browserKitSessionID = "primary"
	}
	healthTTL := time.Duration(env.Int("LINKEDIN_SESSION_HEALTH_TTL_SECONDS", 1200)) * time.Second
	recoveryCooldown := time.Duration(env.Int("LINKEDIN_SESSION_RECOVERY_COOLDOWN_SECONDS", 300)) * time.Second

	if browserKitMode == linkedInBrowserKitModeRequired {
		if err := verifyBrowserKitDeps(browserKitPython); err != nil {
			return err
		}
	}
	canBootstrapSession := browserKitMode != linkedInBrowserKitModeOff && loginUsername != "" && loginPassword != ""

	if accessToken == "" && sessionToken == "" && !canBootstrapSession {
		return mcperror.NotConfigured(
			"LINKEDIN_ACCESS_TOKEN or LINKEDIN_SESSION_COOKIE",
			"set LINKEDIN_ACCESS_TOKEN, LINKEDIN_SESSION_COOKIE/LINKEDIN_LI_AT/LI_AT, or LINKEDIN_LOGIN_USERNAME+LINKEDIN_LOGIN_PASSWORD with BrowserKit enabled",
		)
	}
	if mode == linkedinModeOfficial && accessToken == "" {
		return mcperror.NotConfigured("LINKEDIN_ACCESS_TOKEN", "required when LINKEDIN_MODE=official")
	}
	if mode == linkedinModeExperimental && sessionToken == "" && !canBootstrapSession {
		return mcperror.NotConfigured(
			"LINKEDIN_SESSION_COOKIE",
			"required when LINKEDIN_MODE=experimental (or provide LINKEDIN_LOGIN_USERNAME+LINKEDIN_LOGIN_PASSWORD with BrowserKit enabled)",
		)
	}
	if sessionToken != "" && jsessionID == "" {
		logger.Warn("LINKEDIN_JSESSIONID is not set; write operations may fail due to missing csrf-token header")
	}

	var secretStore secretSetter
	if mgr, err := secrets.DefaultManager(); err != nil {
		logger.Warn("unable to initialize secret manager", "error", err)
	} else {
		secretStore = mgr
	}

	ls := &linkedInServer{
		baseURL:      baseURL,
		mode:         mode,
		accessToken:  accessToken,
		sessionToken: sessionToken,
		jsessionID:   jsessionID,
		httpClient:   httpclient.NewDefault(),
		logger:       logger,
		browserKit: linkedInBrowserKitConfig{
			mode:             browserKitMode,
			python:           browserKitPython,
			storageDir:       browserKitStorageDir,
			sessionID:        browserKitSessionID,
			healthTTL:        healthTTL,
			recoveryCooldown: recoveryCooldown,
		},
		browserKitRunner: nil,
		secretStore:      secretStore,
		loginUsername:    loginUsername,
		loginPassword:    loginPassword,
		mailboxURN:       mailboxURN,
		conversationsQID: conversationsQID,
		messagesQID:      messagesQID,
		lastSessionState: linkedInSessionStateUnknown,
		now:              time.Now,
	}
	ls.browserKitRunner = ls.runBrowserKitHelper

	logger.Info(
		"starting server",
		"name", "mcp-linkedin",
		"version", version,
		"base_url", baseURL,
		"mode", mode,
		"browserkit_mode", browserKitMode,
	)

	server := mcp.NewServer("mcp-linkedin", version)
	server.SetInstructions("LinkedIn personal account management. Supports profile reads and messaging operations. Configure via LINKEDIN_ACCESS_TOKEN or LINKEDIN_SESSION_COOKIE.")

	server.AddTool(mcp.Tool{
		Name:        "linkedin_auth_status",
		Description: "Get LinkedIn auth, BrowserKit, and session-recovery status for this MCP server",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, ls.handleAuthStatus)

	server.AddTool(mcp.Tool{
		Name:        "linkedin_session_health",
		Description: "Run an on-demand LinkedIn session health probe via BrowserKit stealth session",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"refresh": map[string]any{
					"type":        "boolean",
					"description": "Force a live health check instead of using TTL cache (default: true)",
				},
			},
		},
	}, ls.handleSessionHealth)

	server.AddTool(mcp.Tool{
		Name:        "linkedin_session_recover",
		Description: "Attempt LinkedIn session recovery using BrowserKit stealth flow (interactive or silent)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"mode": map[string]any{
					"type":        "string",
					"description": "Recovery mode: interactive or silent (default: interactive)",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "Bypass cooldown guard for emergency recovery (default: false)",
				},
			},
		},
	}, ls.handleSessionRecover)

	server.AddTool(mcp.Tool{
		Name:        "linkedin_get_profile",
		Description: "Get the authenticated LinkedIn profile",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"include_raw": map[string]any{
					"type":        "boolean",
					"description": "Include raw upstream payload in response (default: false)",
				},
			},
		},
	}, ls.handleGetProfile)

	server.AddTool(mcp.Tool{
		Name:        "linkedin_list_conversations",
		Description: "List messaging conversations from LinkedIn inbox",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"start": map[string]any{
					"type":        "integer",
					"description": "Pagination start offset (default: 0)",
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "Number of conversations (default: 20, max: 50)",
				},
				"include_raw": map[string]any{
					"type":        "boolean",
					"description": "Include raw upstream payload in response (default: false)",
				},
			},
		},
	}, ls.handleListConversations)

	server.AddTool(mcp.Tool{
		Name:        "linkedin_get_conversation_messages",
		Description: "Get messages/events for a LinkedIn conversation URN",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"conversation_urn": map[string]any{
					"type":        "string",
					"description": "Conversation URN (example: urn:li:msg_conversation:1234567890)",
				},
				"start": map[string]any{
					"type":        "integer",
					"description": "Pagination start offset (default: 0)",
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "Number of messages/events (default: 20, max: 100)",
				},
				"include_raw": map[string]any{
					"type":        "boolean",
					"description": "Include raw upstream payload in response (default: false)",
				},
			},
			Required: []string{"conversation_urn"},
		},
	}, ls.handleGetConversationMessages)

	server.AddTool(mcp.Tool{
		Name:        "linkedin_send_message",
		Description: "Send a LinkedIn message. Reply to an existing conversation or create a new one with recipients.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"conversation_urn": map[string]any{
					"type":        "string",
					"description": "Conversation URN to reply to. If omitted, recipients is required to create a new conversation.",
				},
				"recipients": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Recipient profile URNs for new conversation (example: urn:li:fs_miniProfile:ACoAAA...)",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Message text body",
				},
				"subject": map[string]any{
					"type":        "string",
					"description": "Optional message subject",
				},
				"include_raw": map[string]any{
					"type":        "boolean",
					"description": "Include raw upstream payload in response (default: false)",
				},
			},
			Required: []string{"text"},
		},
	}, ls.handleSendMessage)

	return server.Run(ctx)
}

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

func (s *linkedInServer) handleGetProfile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	includeRaw := v.Bool("include_raw", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	data, err := s.requestJSON(ctx, http.MethodGet, "/me", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(formatProfileResult(data, includeRaw))
}

func (s *linkedInServer) handleListConversations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := s.ensureMessagingAllowed(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := s.ensureFreshSession(ctx); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	start := v.Int("start", 0)
	count := v.Int("count", 20)
	includeRaw := v.Bool("include_raw", false)

	if err := validateNonNegative("start", start); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := validateRange("count", count, 1, 50); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := legacyConversationsPath(start, count)
	if modernPath, err := s.messagingConversationsPath(ctx); err == nil && strings.TrimSpace(modernPath) != "" {
		path = modernPath
	} else if err != nil {
		s.logger.Warn("linkedin: falling back to legacy conversations path", "error", err)
	}
	data, err := s.requestJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	source := "legacy"
	if strings.HasPrefix(strings.ToLower(path), "/voyagermessaginggraphql/") {
		source = "graphql"
	}
	return mcp.JSONResult(formatConversationsResult(data, start, count, includeRaw, source))
}

func (s *linkedInServer) handleGetConversationMessages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := s.ensureMessagingAllowed(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := s.ensureFreshSession(ctx); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	conversationURN := strings.TrimSpace(v.Required("conversation_urn"))
	start := v.Int("start", 0)
	count := v.Int("count", 20)
	includeRaw := v.Bool("include_raw", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := validateNonNegative("start", start); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := validateRange("count", count, 1, 100); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if conversationURN == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("conversation_urn")), nil
	}

	path := legacyConversationMessagesPath(conversationURN, start, count)
	if modernPath, err := s.messagingMessagesPath(conversationURN); err == nil && strings.TrimSpace(modernPath) != "" {
		path = modernPath
	} else if err != nil {
		s.logger.Warn("linkedin: falling back to legacy conversation messages path", "error", err)
	}
	data, err := s.requestJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	source := "legacy"
	if strings.HasPrefix(strings.ToLower(path), "/voyagermessaginggraphql/") {
		source = "graphql"
	}
	return mcp.JSONResult(formatConversationMessagesResult(data, conversationURN, start, count, includeRaw, source))
}

func (s *linkedInServer) handleSendMessage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := s.ensureMessagingAllowed(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := s.ensureFreshSession(ctx); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if strings.TrimSpace(s.jsessionID) == "" {
		return mcp.ErrorResult(mcperror.NotConfigured("LINKEDIN_JSESSIONID", "required for LinkedIn messaging send operations")), nil
	}

	v := validate.NewArgs(args)
	text := strings.TrimSpace(v.Required("text"))
	subject := strings.TrimSpace(v.String("subject", ""))
	conversationURN := strings.TrimSpace(v.String("conversation_urn", ""))
	recipients := readStringSliceArg(args["recipients"])
	includeRaw := v.Bool("include_raw", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if text == "" {
		return mcp.ErrorResult(mcperror.InvalidParam("text", "must not be empty")), nil
	}
	if conversationURN == "" && len(recipients) == 0 {
		return mcp.ErrorResult(mcperror.InvalidParam("recipients", "required when conversation_urn is not provided")), nil
	}
	if len(recipients) > 20 {
		return mcp.ErrorResult(mcperror.InvalidParam("recipients", "maximum 20 recipients")), nil
	}

	eventCreate := buildEventCreate(text, subject)

	path := ""
	payload := map[string]any{}

	if conversationURN != "" {
		path = fmt.Sprintf("/messaging/conversations/%s/events?action=create", url.PathEscape(conversationURN))
		payload["eventCreate"] = eventCreate
	} else {
		path = "/messaging/conversations?action=create"
		payload["keyVersion"] = "LEGACY_INBOX"
		payload["conversationCreate"] = map[string]any{
			"recipients":  recipients,
			"subtype":     "MEMBER_TO_MEMBER",
			"eventCreate": eventCreate,
		}
	}

	data, err := s.requestJSON(ctx, http.MethodPost, path, payload)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(formatSendMessageResult(data, conversationURN, recipients, text, subject, includeRaw))
}

func buildEventCreate(text, subject string) map[string]any {
	messageCreate := map[string]any{
		"body": text,
		"attributedBody": map[string]any{
			"text": text,
		},
	}
	if subject != "" {
		messageCreate["subject"] = subject
	}
	return map[string]any{
		"value": map[string]any{
			"com.linkedin.voyager.messaging.create.MessageCreate": messageCreate,
		},
	}
}

func (s *linkedInServer) requestJSON(ctx context.Context, method, path string, body any) (any, error) {
	raw, err := s.request(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, mcperror.ParseError("LinkedIn API response JSON", err)
	}
	return out, nil
}

func (s *linkedInServer) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	if s.shouldUseBrowserKitTransport(path) {
		payload, err := s.requestViaBrowserKit(ctx, method, path, body)
		if err == nil {
			return payload, nil
		}
		s.logger.Warn("linkedin browserkit primary transport failed; falling back to http", "path", path, "error", err)
	}
	return s.requestWithRecovery(ctx, method, path, body, true)
}

func (s *linkedInServer) requestWithRecovery(ctx context.Context, method, path string, body any, allowRecovery bool) ([]byte, error) {
	payload, err := s.doRequest(ctx, method, path, body)
	if err == nil {
		return payload, nil
	}

	if (!allowRecovery || !isAuthChallengeErr(err)) && isAuthChallengeErr(err) && s.shouldUseBrowserKitTransport(path) {
		s.logger.Warn("linkedin auth challenge detected; attempting browserkit transport fallback", "path", path)
		return s.requestViaBrowserKit(ctx, method, path, body)
	}

	if !allowRecovery || !isAuthChallengeErr(err) {
		return nil, err
	}

	s.logger.Warn("linkedin auth challenge detected; attempting one-time recovery", "path", path)
	if recErr := s.maybeRecoverAfterChallenge(ctx, path); recErr != nil {
		return nil, recErr
	}
	return s.requestWithRecovery(ctx, method, path, body, false)
}

func (s *linkedInServer) doRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reqBody *bytes.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, mcperror.ParseError("request body", err)
		}
		reqBody = bytes.NewReader(jsonBody)
	} else {
		reqBody = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reqBody)
	if err != nil {
		return nil, mcperror.OperationFailed("create request", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-RestLi-Protocol-Version", "2.0.0")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.accessToken)
	}
	if s.sessionToken != "" {
		cookieValue := "li_at=" + s.sessionToken
		if normalized := normalizeJSessionID(s.jsessionID); normalized != "" {
			cookieValue += "; JSESSIONID=" + normalized
		}
		req.Header.Set("Cookie", cookieValue)
	}
	if csrfToken := csrfTokenFromJSessionID(s.jsessionID); csrfToken != "" {
		req.Header.Set("csrf-token", csrfToken)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "too many redirects") || strings.Contains(msg, "stopped after 10 redirects") {
			return nil, &authChallengeError{statusCode: 0, body: err.Error()}
		}
		return nil, mcperror.WrapAPI("LinkedIn", err)
	}
	defer resp.Body.Close()

	payload, truncated, err := httpclient.ReadBodyWithLimit(resp.Body, maxResponseBytes)
	if err != nil {
		return nil, mcperror.OperationFailed("read LinkedIn API response", err)
	}
	if truncated {
		return nil, mcperror.ServerError("LinkedIn response exceeded 2MB limit")
	}
	if isLinkedInSessionInvalidation(resp, payload) {
		return nil, &authChallengeError{statusCode: resp.StatusCode, body: "LinkedIn invalidated session cookies"}
	}
	if resp.StatusCode >= 400 {
		if isLinkedInAuthChallenge(resp.StatusCode, payload) {
			return nil, &authChallengeError{statusCode: resp.StatusCode, body: string(payload)}
		}
		return nil, mcperror.APIError("LinkedIn", resp.StatusCode, string(payload))
	}

	return payload, nil
}

func normalizeJSessionID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	trimmed := strings.Trim(v, "\"")
	return `"` + trimmed + `"`
}

func csrfTokenFromJSessionID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	return strings.Trim(v, "\"")
}

func validateNonNegative(field string, n int) error {
	if n < 0 {
		return mcperror.InvalidParam(field, "must be >= 0")
	}
	return nil
}

func validateRange(field string, n, min, max int) error {
	if n < min || n > max {
		return mcperror.InvalidParam(field, fmt.Sprintf("must be between %d and %d", min, max))
	}
	return nil
}

func readStringSliceArg(v any) []string {
	var out []string
	switch values := v.(type) {
	case []string:
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
	case []any:
		for _, value := range values {
			if s, ok := value.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
	}
	return out
}

func formatConversationsResult(raw any, start, count int, includeRaw bool, source string) map[string]any {
	elements, syncToken := extractConversationElements(raw)
	total := len(elements)
	sliced := paginateAnySlice(elements, start, count)
	conversations := make([]map[string]any, 0, len(sliced))
	for _, item := range sliced {
		if m, ok := item.(map[string]any); ok {
			conversations = append(conversations, summarizeConversation(m))
		}
	}

	out := map[string]any{
		"source":        source,
		"conversations": conversations,
		"pagination": map[string]any{
			"requested_start":        start,
			"requested_count":        count,
			"returned_count":         len(conversations),
			"available_count":        total,
			"client_side_pagination": source == "graphql",
		},
	}
	if syncToken != "" {
		out["sync_token"] = syncToken
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func formatConversationMessagesResult(raw any, conversationURN string, start, count int, includeRaw bool, source string) map[string]any {
	elements, syncToken := extractMessageElements(raw)
	total := len(elements)
	sliced := paginateAnySlice(elements, start, count)
	messages := make([]map[string]any, 0, len(sliced))
	for _, item := range sliced {
		if m, ok := item.(map[string]any); ok {
			messages = append(messages, summarizeMessage(m, conversationURN))
		}
	}

	out := map[string]any{
		"source":           source,
		"conversation_urn": conversationURN,
		"messages":         messages,
		"pagination": map[string]any{
			"requested_start":        start,
			"requested_count":        count,
			"returned_count":         len(messages),
			"available_count":        total,
			"client_side_pagination": source == "graphql",
		},
	}
	if syncToken != "" {
		out["sync_token"] = syncToken
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func formatProfileResult(raw any, includeRaw bool) map[string]any {
	profile, _ := raw.(map[string]any)
	mini, _ := profile["miniProfile"].(map[string]any)

	firstName := attributedText(mini["firstName"])
	lastName := attributedText(mini["lastName"])
	out := map[string]any{
		"profile": map[string]any{
			"entity_urn":        stringValue(profile["entityUrn"]),
			"first_name":        firstName,
			"last_name":         lastName,
			"full_name":         strings.TrimSpace(firstName + " " + lastName),
			"headline":          attributedText(mini["headline"]),
			"occupation":        attributedText(profile["occupation"]),
			"public_identifier": stringValue(mini["publicIdentifier"]),
			"profile_url":       firstNonEmpty(stringValue(mini["publicProfileUrl"]), stringValue(mini["profileUrl"])),
			"dash_entity_urn":   stringValue(mini["dashEntityUrn"]),
			"memorialized":      boolValue(profile["memorialized"]),
		},
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func formatSendMessageResult(raw any, conversationURN string, recipients []string, text, subject string, includeRaw bool) map[string]any {
	out := map[string]any{
		"ok": true,
		"request": map[string]any{
			"conversation_urn": conversationURN,
			"recipients":       recipients,
			"recipients_count": len(recipients),
			"subject":          subject,
			"text_preview":     strutil.TruncateSingleLine(text, 240),
		},
		"result": summarizeSendMutationResult(raw, conversationURN),
	}
	if includeRaw {
		out["raw"] = raw
	}
	return out
}

func summarizeSendMutationResult(raw any, fallbackConversationURN string) map[string]any {
	out := map[string]any{
		"conversation_urn": strings.TrimSpace(fallbackConversationURN),
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return out
	}

	if conversationURN := firstNonEmpty(
		nestedString(root, "conversation", "entityUrn"),
		nestedString(root, "value", "conversation", "entityUrn"),
		nestedString(root, "event", "conversation", "entityUrn"),
	); conversationURN != "" {
		out["conversation_urn"] = conversationURN
	}
	if eventURN := firstNonEmpty(
		nestedString(root, "event", "entityUrn"),
		nestedString(root, "event", "backendUrn"),
		stringValue(root["entityUrn"]),
		stringValue(root["backendUrn"]),
	); eventURN != "" {
		out["event_urn"] = eventURN
	}
	if createdAt := firstNonZero(
		nestedInt64(root, "event", "createdAt"),
		int64Value(root["createdAt"]),
	); createdAt > 0 {
		out["created_at"] = createdAt
	}

	return out
}

func extractConversationElements(raw any) ([]any, string) {
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, ""
	}

	if data, ok := root["data"].(map[string]any); ok {
		if coll, ok := data["messengerConversationsBySyncToken"].(map[string]any); ok {
			elements := toAnySlice(coll["elements"])
			syncToken := extractSyncToken(coll)
			if syncToken == "" {
				syncToken = extractSyncToken(data)
			}
			return elements, syncToken
		}
	}

	return toAnySlice(root["elements"]), extractSyncToken(root)
}

func extractMessageElements(raw any) ([]any, string) {
	root, ok := raw.(map[string]any)
	if !ok {
		return nil, ""
	}

	if data, ok := root["data"].(map[string]any); ok {
		if coll, ok := data["messengerMessagesBySyncToken"].(map[string]any); ok {
			elements := toAnySlice(coll["elements"])
			syncToken := extractSyncToken(coll)
			if syncToken == "" {
				syncToken = extractSyncToken(data)
			}
			return elements, syncToken
		}
	}

	return toAnySlice(root["elements"]), extractSyncToken(root)
}

func summarizeConversation(in map[string]any) map[string]any {
	conversationURN := stringValue(in["entityUrn"])
	backendURN := stringValue(in["backendUrn"])
	if conversationURN == "" {
		conversationURN = backendURN
	}

	out := map[string]any{
		"conversation_urn": conversationURN,
		"entity_urn":       stringValue(in["entityUrn"]),
		"backend_urn":      backendURN,
		"conversation_url": stringValue(in["conversationUrl"]),
		"title":            stringValue(in["title"]),
		"state":            stringValue(in["state"]),
		"read":             boolValue(in["read"]),
		"unread_count":     intValue(in["unreadCount"]),
		"last_activity_at": int64Value(in["lastActivityAt"]),
		"created_at":       int64Value(in["createdAt"]),
		"categories":       stringSliceValue(in["categories"]),
		"participants":     summarizeParticipants(in["conversationParticipants"]),
	}
	if latest := summarizeLatestMessage(in["messages"]); latest != nil {
		out["latest_message"] = latest
	}
	return out
}

func summarizeParticipants(raw any) []map[string]any {
	items := toAnySlice(raw)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		memberInfo := map[string]any{}
		if participantType, ok := m["participantType"].(map[string]any); ok {
			if member, ok := participantType["member"].(map[string]any); ok {
				memberInfo = member
			}
		}
		name := strings.TrimSpace(strings.TrimSpace(attributedText(memberInfo["firstName"])) + " " + strings.TrimSpace(attributedText(memberInfo["lastName"])))
		out = append(out, map[string]any{
			"entity_urn":        stringValue(m["entityUrn"]),
			"host_identity_urn": stringValue(m["hostIdentityUrn"]),
			"name":              name,
			"profile_url":       stringValue(memberInfo["profileUrl"]),
			"headline":          attributedText(memberInfo["headline"]),
			"distance":          stringValue(memberInfo["distance"]),
			"member_badge_type": stringValue(m["memberBadgeType"]),
		})
	}
	return out
}

func summarizeLatestMessage(raw any) map[string]any {
	messages, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	elements := toAnySlice(messages["elements"])
	if len(elements) == 0 {
		return nil
	}
	msg, ok := elements[0].(map[string]any)
	if !ok {
		return nil
	}
	body := attributedText(msg["body"])
	out := map[string]any{
		"entity_urn":    stringValue(msg["entityUrn"]),
		"backend_urn":   stringValue(msg["backendUrn"]),
		"subject":       stringValue(msg["subject"]),
		"body":          body,
		"body_preview":  strutil.TruncateSingleLine(body, 240),
		"delivered_at":  int64Value(msg["deliveredAt"]),
		"sender_entity": extractSenderEntityURN(msg),
	}
	return out
}

func summarizeMessage(in map[string]any, requestedConversationURN string) map[string]any {
	body := attributedText(in["body"])
	conversationURN := requestedConversationURN
	if conversationURN == "" {
		if conversation, ok := in["conversation"].(map[string]any); ok {
			conversationURN = stringValue(conversation["entityUrn"])
		}
	}
	return map[string]any{
		"message_urn":       firstNonEmpty(stringValue(in["entityUrn"]), stringValue(in["backendUrn"])),
		"entity_urn":        stringValue(in["entityUrn"]),
		"backend_urn":       stringValue(in["backendUrn"]),
		"conversation_urn":  conversationURN,
		"sender_entity_urn": extractSenderEntityURN(in),
		"subject":           stringValue(in["subject"]),
		"body":              body,
		"body_preview":      strutil.TruncateSingleLine(body, 240),
		"delivered_at":      int64Value(in["deliveredAt"]),
		"origin_token":      stringValue(in["originToken"]),
	}
}

func extractSenderEntityURN(in map[string]any) string {
	if sender, ok := in["sender"].(map[string]any); ok {
		if value := stringValue(sender["entityUrn"]); value != "" {
			return value
		}
	}
	if actor, ok := in["actor"].(map[string]any); ok {
		if value := stringValue(actor["entityUrn"]); value != "" {
			return value
		}
	}
	return ""
}

func extractSyncToken(m map[string]any) string {
	if meta, ok := m["metadata"].(map[string]any); ok {
		if token := stringValue(meta["newSyncToken"]); token != "" {
			return token
		}
	}
	return ""
}

func paginateAnySlice(values []any, start, count int) []any {
	if len(values) == 0 {
		return []any{}
	}
	if start < 0 {
		start = 0
	}
	if start >= len(values) {
		return []any{}
	}
	if count <= 0 {
		return []any{}
	}
	end := start + count
	if end > len(values) {
		end = len(values)
	}
	return values[start:end]
}

func toAnySlice(v any) []any {
	switch items := v.(type) {
	case []any:
		return items
	default:
		return []any{}
	}
}

func attributedText(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return strings.TrimSpace(stringValue(typed["text"]))
	default:
		return ""
	}
}

func stringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func stringSliceValue(v any) []string {
	items := toAnySlice(v)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s := stringValue(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func int64Value(v any) int64 {
	switch typed := v.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return n
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func intValue(v any) int {
	return int(int64Value(v))
}

func boolValue(v any) bool {
	switch typed := v.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func nestedValue(root map[string]any, path ...string) any {
	if len(path) == 0 {
		return nil
	}
	var current any = root
	for _, segment := range path {
		node, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = node[segment]
	}
	return current
}

func nestedString(root map[string]any, path ...string) string {
	return stringValue(nestedValue(root, path...))
}

func nestedInt64(root map[string]any, path ...string) int64 {
	return int64Value(nestedValue(root, path...))
}

func legacyConversationsPath(start, count int) string {
	return fmt.Sprintf("/messaging/conversations?keyVersion=LEGACY_INBOX&start=%d&count=%d", start, count)
}

func legacyConversationMessagesPath(conversationURN string, start, count int) string {
	return fmt.Sprintf("/messaging/conversations/%s/events?start=%d&count=%d", url.PathEscape(conversationURN), start, count)
}

func (s *linkedInServer) messagingConversationsPath(ctx context.Context) (string, error) {
	queryID := strings.TrimSpace(s.conversationsQID)
	if queryID == "" {
		return "", nil
	}
	mailboxURN, err := s.resolveMailboxURN(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(mailboxURN) == "" {
		return "", errors.New("mailbox urn is empty")
	}
	return fmt.Sprintf(
		"/voyagerMessagingGraphQL/graphql?queryId=%s&variables=%s",
		url.QueryEscape(queryID),
		fmt.Sprintf("(mailboxUrn:%s)", url.QueryEscape(mailboxURN)),
	), nil
}

func (s *linkedInServer) messagingMessagesPath(conversationURN string) (string, error) {
	queryID := strings.TrimSpace(s.messagesQID)
	if queryID == "" {
		return "", nil
	}
	conversationURN = strings.TrimSpace(conversationURN)
	if conversationURN == "" {
		return "", errors.New("conversation urn is empty")
	}
	return fmt.Sprintf(
		"/voyagerMessagingGraphQL/graphql?queryId=%s&variables=%s",
		url.QueryEscape(queryID),
		fmt.Sprintf("(conversationUrn:%s)", url.QueryEscape(conversationURN)),
	), nil
}

func (s *linkedInServer) resolveMailboxURN(ctx context.Context) (string, error) {
	if configured := strings.TrimSpace(s.mailboxURN); configured != "" {
		return configured, nil
	}

	var (
		profileRaw any
		bkErr      error
	)
	if s.browserKit.mode != linkedInBrowserKitModeOff && s.mode != linkedinModeOfficial {
		if raw, err := s.requestViaBrowserKit(ctx, http.MethodGet, "/me", nil); err == nil {
			if len(raw) == 0 {
				profileRaw = map[string]any{}
			} else {
				if parseErr := json.Unmarshal(raw, &profileRaw); parseErr != nil {
					bkErr = mcperror.ParseError("LinkedIn /me browserkit response JSON", parseErr)
				}
			}
		} else {
			bkErr = err
		}
		if bkErr != nil {
			s.logger.Warn("linkedin: browserkit /me lookup failed; falling back to HTTP", "error", bkErr)
		}
	}
	if profileRaw == nil {
		var err error
		profileRaw, err = s.requestJSON(ctx, http.MethodGet, "/me", nil)
		if err != nil {
			if bkErr != nil {
				return "", mcperror.OperationFailed("resolve linkedin mailbox urn", fmt.Errorf("browserkit /me failed (%v); http /me failed (%w)", bkErr, err))
			}
			return "", err
		}
	}

	mailboxURN, err := mailboxURNFromProfile(profileRaw)
	if err != nil {
		return "", err
	}
	s.mailboxURN = mailboxURN
	return mailboxURN, nil
}

func mailboxURNFromProfile(profileRaw any) (string, error) {
	profile, ok := profileRaw.(map[string]any)
	if !ok {
		return "", errors.New("unexpected /me response shape")
	}
	miniProfile, ok := profile["miniProfile"].(map[string]any)
	if !ok {
		return "", errors.New("missing miniProfile in /me response")
	}
	dashEntityURN, _ := miniProfile["dashEntityUrn"].(string)
	dashEntityURN = strings.TrimSpace(dashEntityURN)
	if dashEntityURN == "" {
		return "", errors.New("missing miniProfile.dashEntityUrn in /me response")
	}
	return dashEntityURN, nil
}

func parseLinkedInMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = linkedinModeAuto
	}
	switch mode {
	case linkedinModeAuto, linkedinModeOfficial, linkedinModeExperimental:
		return mode, nil
	default:
		return "", mcperror.InvalidParam("LINKEDIN_MODE", "must be one of: auto, official, experimental")
	}
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

func defaultLinkedInBrowserKitPython() string {
	if configured := strings.TrimSpace(env.StringWithFallbacks("LINKEDIN_BROWSERKIT_PYTHON", "BROWSERKIT_PYTHON")); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		candidate := filepath.Join(home, ".config", "loom", "browserkit-venv", "bin", "python")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate
		}
	}
	return "python3"
}
