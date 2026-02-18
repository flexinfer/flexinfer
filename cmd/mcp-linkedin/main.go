// mcp-linkedin provides LinkedIn personal account operations via MCP.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

const (
	defaultLinkedInBaseURL = "https://www.linkedin.com/voyager/api"
	maxResponseBytes       = 2 * 1024 * 1024 // 2MB cap to keep responses bounded.
)

var version = "0.1.0"

type linkedInServer struct {
	baseURL      string
	accessToken  string
	sessionToken string
	jsessionID   string
	httpClient   *httpclient.Client
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
	accessToken := env.StringWithFallbacks("LINKEDIN_ACCESS_TOKEN", "LINKEDIN_TOKEN")
	sessionToken := env.StringWithFallbacks("LINKEDIN_SESSION_COOKIE", "LINKEDIN_LI_AT")
	jsessionID := env.String("LINKEDIN_JSESSIONID", "")

	if accessToken == "" && sessionToken == "" {
		return mcperror.NotConfigured(
			"LINKEDIN_ACCESS_TOKEN or LINKEDIN_SESSION_COOKIE",
			"set LINKEDIN_ACCESS_TOKEN for API auth or LINKEDIN_SESSION_COOKIE/LINKEDIN_LI_AT for Voyager auth",
		)
	}

	if sessionToken != "" && jsessionID == "" {
		logger.Warn("LINKEDIN_JSESSIONID is not set; write operations may fail due to missing csrf-token header")
	}

	ls := &linkedInServer{
		baseURL:      baseURL,
		accessToken:  accessToken,
		sessionToken: sessionToken,
		jsessionID:   jsessionID,
		httpClient:   httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-linkedin", "version", version, "base_url", baseURL)

	server := mcp.NewServer("mcp-linkedin", version)
	server.SetInstructions("LinkedIn personal account management. Supports profile reads and messaging operations. Configure via LINKEDIN_ACCESS_TOKEN or LINKEDIN_SESSION_COOKIE.")

	server.AddTool(mcp.Tool{
		Name:        "linkedin_get_profile",
		Description: "Get the authenticated LinkedIn profile",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
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
			},
			Required: []string{"text"},
		},
	}, ls.handleSendMessage)

	return server.Run(ctx)
}

func (s *linkedInServer) handleGetProfile(ctx context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	data, err := s.requestJSON(ctx, http.MethodGet, "/me", nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(data)
}

func (s *linkedInServer) handleListConversations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	start := v.Int("start", 0)
	count := v.Int("count", 20)

	if err := validateNonNegative("start", start); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := validateRange("count", count, 1, 50); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/messaging/conversations?keyVersion=LEGACY_INBOX&start=%d&count=%d", start, count)
	data, err := s.requestJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(data)
}

func (s *linkedInServer) handleGetConversationMessages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	conversationURN := strings.TrimSpace(v.Required("conversation_urn"))
	start := v.Int("start", 0)
	count := v.Int("count", 20)

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

	path := fmt.Sprintf("/messaging/conversations/%s/events?start=%d&count=%d", url.PathEscape(conversationURN), start, count)
	data, err := s.requestJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(data)
}

func (s *linkedInServer) handleSendMessage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	text := strings.TrimSpace(v.Required("text"))
	subject := strings.TrimSpace(v.String("subject", ""))
	conversationURN := strings.TrimSpace(v.String("conversation_urn", ""))
	recipients := readStringSliceArg(args["recipients"])

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

	return mcp.JSONResult(map[string]any{
		"ok":               true,
		"conversation_urn": conversationURN,
		"response":         data,
	})
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
	if resp.StatusCode >= 400 {
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
