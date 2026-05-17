package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// formatSlackPasteSchema declares the input contract for
// icc_format_slack_paste. Kept here (next to the handler) so reviewers
// can read schema and parsing in one place.
func formatSlackPasteSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_slug", "channel"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Raw Slack paste",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "ICC project slug; '_inbox' if not yet attributed",
			},
			"channel": map[string]any{
				"type":        "string",
				"description": "Slack channel name without leading #",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to now in local TZ",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug for the filename",
			},
			"participants": map[string]any{
				"type":        "array",
				"description": "Optional; inferred from paste if absent",
				"items":       map[string]any{"type": "string"},
			},
		},
	}
}

// formatSlackPasteResult is the JSON payload returned from the tool.
type formatSlackPasteResult struct {
	Markdown          string `json:"markdown"`
	SuggestedFilename string `json:"suggested_filename"`
	SuggestedPath     string `json:"suggested_path"`
}

// slackMessage is one parsed entry from the Slack paste.
type slackMessage struct {
	User string
	Time string
	Body string
}

func handleFormatSlackPaste(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	text := v.Required("text")
	projectSlug := v.Required("project_slug")
	channel := v.Required("channel")
	capturedAt := v.String("captured_at", "")
	topic := v.String("topic", "")
	explicitParticipants := v.StringSlice("participants")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if strings.TrimSpace(text) == "" {
		return mcp.ErrorResult(errors.New("text is required and must not be empty")), nil
	}

	if capturedAt == "" {
		capturedAt = time.Now().Format(time.RFC3339)
	}

	messages := parseSlackPaste(text)

	participants := explicitParticipants
	if len(participants) == 0 {
		participants = inferParticipants(messages)
	}

	if topic == "" {
		topic = deriveTopicSlug(messages, text)
	}

	captureDate := dateForFilename(capturedAt)
	filename := buildSlackFilename(captureDate, topic)
	suggestedPath := buildSlackPath(projectSlug, filename)

	markdown := renderSlackMarkdown(
		projectSlug, channel, capturedAt, participants, messages, text,
	)

	return jsonResult(formatSlackPasteResult{
		Markdown:          markdown,
		SuggestedFilename: filename,
		SuggestedPath:     suggestedPath,
	})
}

// slackHeaderRE matches a Slack message header line. Slack pastes
// typically render headers as `username  10:14 AM` or
// `username  Yesterday at 10:14 AM`. We split on two-or-more spaces
// between user and timestamp.
var slackHeaderRE = regexp.MustCompile(
	`^(?P<user>[^\s][^\t]*?)\s{2,}(?P<time>(?:Today|Yesterday|[A-Z][a-z]+ \d{1,2}(?:,\s*\d{4})?)\s+at\s+\d{1,2}:\d{2}\s*(?:AM|PM)|\d{1,2}:\d{2}\s*(?:AM|PM))\s*$`,
)

// parseSlackPaste splits a raw Slack thread paste into individual
// messages. Best-effort: lines that don't match a header attach to
// the most recent message body.
func parseSlackPaste(text string) []slackMessage {
	var messages []slackMessage
	var cur *slackMessage

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if line == "" {
			if cur != nil {
				cur.Body = strings.TrimRight(cur.Body+"\n", "\n")
			}
			continue
		}
		if m := slackHeaderRE.FindStringSubmatch(line); m != nil {
			user := strings.TrimSpace(m[slackHeaderRE.SubexpIndex("user")])
			ts := strings.TrimSpace(m[slackHeaderRE.SubexpIndex("time")])
			messages = append(messages, slackMessage{User: user, Time: ts})
			cur = &messages[len(messages)-1]
			continue
		}
		if cur == nil {
			// Body before any header: treat as a single unattributed
			// pseudo-message so callers don't lose content.
			messages = append(messages, slackMessage{User: "unknown", Time: ""})
			cur = &messages[len(messages)-1]
		}
		if cur.Body == "" {
			cur.Body = line
		} else {
			cur.Body += "\n" + line
		}
	}

	// Trim trailing whitespace from each body.
	for i := range messages {
		messages[i].Body = strings.TrimSpace(messages[i].Body)
	}
	return messages
}

// inferParticipants returns unique users in first-seen order.
func inferParticipants(messages []slackMessage) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range messages {
		u := strings.TrimSpace(m.User)
		if u == "" || u == "unknown" {
			continue
		}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// deriveTopicSlug builds a filesystem-friendly slug from the first
// non-empty body line in the paste, capped at 40 chars.
func deriveTopicSlug(messages []slackMessage, fallbackText string) string {
	first := ""
	for _, m := range messages {
		body := strings.TrimSpace(m.Body)
		if body != "" {
			first = body
			break
		}
	}
	if first == "" {
		// Fall back to first non-empty raw line.
		for _, raw := range strings.Split(fallbackText, "\n") {
			line := strings.TrimSpace(raw)
			if line != "" {
				first = line
				break
			}
		}
	}
	return slugify(first, 40)
}

var nonSlugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 40
	}
	if len(s) > maxLen*4 {
		s = s[:maxLen*4]
	}
	s = strings.ToLower(s)
	s = nonSlugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > maxLen {
		s = s[:maxLen]
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "note"
	}
	return s
}

// dateForFilename parses capturedAt and returns YYYY-MM-DD, falling
// back to today if parsing fails.
func dateForFilename(capturedAt string) string {
	if t, err := time.Parse(time.RFC3339, capturedAt); err == nil {
		return t.Format("2006-01-02")
	}
	if t, err := time.Parse("2006-01-02", capturedAt); err == nil {
		return t.Format("2006-01-02")
	}
	return time.Now().Format("2006-01-02")
}

func buildSlackFilename(date, topic string) string {
	if topic == "" {
		topic = "note"
	}
	return fmt.Sprintf("%s-%s.md", date, topic)
}

func buildSlackPath(projectSlug, filename string) string {
	if projectSlug == "" {
		projectSlug = "_inbox"
	}
	return fmt.Sprintf(
		"/workspace/icc-project-workspaces/projects/%s/slack/%s",
		projectSlug, filename,
	)
}

// renderSlackMarkdown builds the final markdown document. The
// frontmatter shape is the contract with Slice A — keep these field
// names and order stable.
func renderSlackMarkdown(
	projectSlug, channel, capturedAt string,
	participants []string,
	messages []slackMessage,
	originalText string,
) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "project: %s\n", projectSlug)
	b.WriteString("source: slack\n")
	b.WriteString("classification: possible_phi\n")
	fmt.Fprintf(&b, "captured_at: %s\n", capturedAt)
	fmt.Fprintf(&b, "channel: %s\n", channel)
	b.WriteString("participants: [")
	b.WriteString(joinQuoted(participants))
	b.WriteString("]\n")
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# Slack thread — #%s\n\n", channel)

	if len(messages) == 0 {
		b.WriteString("```\n")
		b.WriteString(strings.TrimSpace(originalText))
		b.WriteString("\n```\n")
		return b.String()
	}

	for i, m := range messages {
		user := m.User
		if user == "" {
			user = "unknown"
		}
		if m.Time != "" {
			fmt.Fprintf(&b, "### %s · %s\n\n", user, m.Time)
		} else {
			fmt.Fprintf(&b, "### %s\n\n", user)
		}
		if m.Body != "" {
			b.WriteString(m.Body)
			b.WriteString("\n")
		}
		if i < len(messages)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func joinQuoted(items []string) string {
	if len(items) == 0 {
		return ""
	}
	// Sort? No — preserve first-seen order for participants.
	parts := make([]string, 0, len(items))
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%q", s))
	}
	return strings.Join(parts, ", ")
}
