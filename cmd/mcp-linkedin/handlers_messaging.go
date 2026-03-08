package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

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
