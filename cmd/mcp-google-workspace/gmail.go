package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"google.golang.org/api/gmail/v1"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
)

func (s *googleWorkspaceServer) handleGmailListMessages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.String("query", "")
	maxResults := validate.NormalizePerPage(v.Int("max_results", 20), 20, 50)
	pageToken := v.String("page_token", "")
	labelIDs := v.StringSlice("label_ids")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, creds, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	call := clients.gmail.Users.Messages.List("me").MaxResults(int64(maxResults))
	if query != "" {
		call = call.Q(query)
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	if len(labelIDs) > 0 {
		call = call.LabelIds(labelIDs...)
	}
	list, err := call.Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Gmail", err)), nil
	}

	items := make([]map[string]any, 0, len(list.Messages))
	for _, item := range list.Messages {
		msg, getErr := clients.gmail.Users.Messages.Get("me", item.Id).
			Format("metadata").
			MetadataHeaders("From", "To", "Subject", "Date").
			Do()
		if getErr != nil {
			items = append(items, map[string]any{
				"id":    item.Id,
				"error": s.wrapGoogleError("Gmail", getErr).Error(),
			})
			continue
		}
		items = append(items, simplifyGmailMessage(msg, false))
	}

	return mcp.JSONResult(map[string]any{
		"account_email":        creds.AccountEmail,
		"messages":             items,
		"next_page_token":      list.NextPageToken,
		"result_size_estimate": list.ResultSizeEstimate,
	})
}

func (s *googleWorkspaceServer) handleGmailGetMessage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	messageID := v.Required("message_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	msg, err := clients.gmail.Users.Messages.Get("me", messageID).Format("full").Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Gmail", err)), nil
	}
	return mcp.JSONResult(simplifyGmailMessage(msg, true))
}

func (s *googleWorkspaceServer) handleGmailSendMessage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	to := v.RequiredStringSlice("to")
	cc := v.StringSlice("cc")
	bcc := v.StringSlice("bcc")
	subject := v.String("subject", "")
	bodyText := v.String("body_text", "")
	bodyHTML := v.String("body_html", "")
	threadID := v.String("thread_id", "")
	replyTo := v.String("reply_to", "")
	if bodyText == "" && bodyHTML == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("body_text or body_html")), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	raw, err := buildRFC822Message(to, cc, bcc, subject, bodyText, bodyHTML, replyTo)
	if err != nil {
		return mcp.ErrorResult(mcperror.InvalidInput(err.Error())), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := clients.gmail.Users.Messages.Send("me", &gmail.Message{
		Raw:      raw,
		ThreadId: threadID,
	}).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Gmail", err)), nil
	}
	return mcp.JSONResult(map[string]any{
		"id":        resp.Id,
		"thread_id": resp.ThreadId,
		"label_ids": resp.LabelIds,
	})
}

func simplifyGmailMessage(msg *gmail.Message, includeBody bool) map[string]any {
	headers := gmailHeaders(msg.Payload)
	result := map[string]any{
		"id":            msg.Id,
		"thread_id":     msg.ThreadId,
		"label_ids":     msg.LabelIds,
		"snippet":       msg.Snippet,
		"history_id":    msg.HistoryId,
		"subject":       headers["Subject"],
		"from":          headers["From"],
		"to":            headers["To"],
		"date":          headers["Date"],
		"internal_date": gmailInternalDate(msg.InternalDate),
	}
	if includeBody {
		result["body_text"] = strutil.Truncate(extractMessageBody(msg.Payload), 8000)
		result["headers"] = headers
	}
	return result
}

func gmailHeaders(part *gmail.MessagePart) map[string]string {
	headers := make(map[string]string)
	if part == nil {
		return headers
	}
	for _, header := range part.Headers {
		headers[header.Name] = header.Value
	}
	return headers
}

func gmailInternalDate(raw int64) string {
	if raw <= 0 {
		return ""
	}
	return time.UnixMilli(raw).Format(time.RFC3339)
}

func extractMessageBody(part *gmail.MessagePart) string {
	if part == nil {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(part.MimeType), "text/plain") && part.Body != nil && part.Body.Data != "" {
		if body := decodeBase64URL(part.Body.Data); body != "" {
			return body
		}
	}
	for _, child := range part.Parts {
		if body := extractMessageBody(child); body != "" {
			return body
		}
	}
	if part.Body != nil && part.Body.Data != "" {
		return decodeBase64URL(part.Body.Data)
	}
	return ""
}

func decodeBase64URL(value string) string {
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(decoded))
}

func buildRFC822Message(to, cc, bcc []string, subject, bodyText, bodyHTML, replyTo string) (string, error) {
	if len(to) == 0 {
		return "", fmt.Errorf("at least one recipient is required")
	}
	if bodyText == "" && bodyHTML == "" {
		return "", fmt.Errorf("body_text or body_html is required")
	}

	var buf bytes.Buffer
	writeHeader := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		buf.WriteString(name)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}

	writeHeader("To", strings.Join(to, ", "))
	writeHeader("Cc", strings.Join(cc, ", "))
	writeHeader("Bcc", strings.Join(bcc, ", "))
	writeHeader("Subject", subject)
	writeHeader("Reply-To", replyTo)
	writeHeader("MIME-Version", "1.0")
	if bodyHTML != "" {
		writeHeader("Content-Type", `text/html; charset="UTF-8"`)
		buf.WriteString("\r\n")
		buf.WriteString(bodyHTML)
	} else {
		writeHeader("Content-Type", `text/plain; charset="UTF-8"`)
		buf.WriteString("\r\n")
		buf.WriteString(bodyText)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(buf.Bytes()), nil
}
