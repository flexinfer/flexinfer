// mcp-linkedin provides LinkedIn personal account operations via MCP.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/secrets"
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
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-linkedin", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-linkedin")

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
	}, mcpotel.TracedToolHandler(tracer, "linkedin_auth_status", ls.handleAuthStatus))

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
	}, mcpotel.TracedToolHandler(tracer, "linkedin_session_health", ls.handleSessionHealth))

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
	}, mcpotel.TracedToolHandler(tracer, "linkedin_session_recover", ls.handleSessionRecover))

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
	}, mcpotel.TracedToolHandler(tracer, "linkedin_get_profile", ls.handleGetProfile))

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
	}, mcpotel.TracedToolHandler(tracer, "linkedin_list_conversations", ls.handleListConversations))

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
	}, mcpotel.TracedToolHandler(tracer, "linkedin_get_conversation_messages", ls.handleGetConversationMessages))

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
	}, mcpotel.TracedToolHandler(tracer, "linkedin_send_message", ls.handleSendMessage))

	return server.Run(ctx)
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
