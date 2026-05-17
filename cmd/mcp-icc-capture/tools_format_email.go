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

// formatEmailExtractSchema declares the input contract for
// icc_format_email_extract. Mirrors formatSlackPasteSchema's shape so
// reviewers can read both formatters side-by-side.
func formatEmailExtractSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_slug"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Raw email text (RFC 822, Gmail-style paste, or web-rendered headers)",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "ICC project slug; '_inbox' if not yet attributed",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to detected Date header or now in local TZ",
			},
			"subject": map[string]any{
				"type":        "string",
				"description": "Optional override for the detected Subject",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug for the filename; defaults to slugged subject",
			},
		},
	}
}

// formatEmailExtractResult is the JSON payload returned from the tool.
// Surfaces detected fields + warnings so callers can decide whether to
// re-format with overrides.
type formatEmailExtractResult struct {
	Markdown          string   `json:"markdown"`
	SuggestedFilename string   `json:"suggested_filename"`
	SuggestedPath     string   `json:"suggested_path"`
	DetectedSubject   string   `json:"detected_subject"`
	DetectedFrom      []string `json:"detected_from"`
	Warnings          []string `json:"warnings,omitempty"`
}

// emailMessage is one reply in the parsed thread. The first entry is
// the top-level message; subsequent entries are quoted replies.
type emailMessage struct {
	From    string
	Date    string
	Subject string
	Body    string
}

// formatEmailExtractInput is the typed input bundle shared by the
// format tool and the icc_capture_email composer.
type formatEmailExtractInput struct {
	Text        string
	ProjectSlug string
	CapturedAt  string
	Subject     string
	Topic       string
}

// formatEmailExtract is the pure formatter (no MCP types, no I/O).
// Both icc_format_email_extract and icc_capture_email call this so the
// two surfaces stay byte-identical.
func formatEmailExtract(in formatEmailExtractInput) (formatEmailExtractResult, error) {
	if strings.TrimSpace(in.Text) == "" {
		return formatEmailExtractResult{}, errors.New("text is required and must not be empty")
	}

	var warnings []string

	messages := parseEmailThread(in.Text)
	if len(messages) == 0 {
		// No headers at all; preserve content as a single anonymous body.
		messages = []emailMessage{{Body: strings.TrimSpace(in.Text)}}
		warnings = append(warnings, "no email headers detected; treating entire text as body")
	}

	top := messages[0]

	subject := in.Subject
	if subject == "" {
		subject = top.Subject
	}

	capturedAt := in.CapturedAt
	if capturedAt == "" {
		if top.Date != "" {
			if parsed, ok := parseEmailDate(top.Date); ok {
				capturedAt = parsed
			}
		}
	}
	if capturedAt == "" {
		capturedAt = time.Now().Format(time.RFC3339)
	}

	senders := collectEmailSenders(messages)

	topic := in.Topic
	if topic == "" {
		topic = slugify(subject, 40)
		if topic == "note" {
			// Fall back to first body line if subject was empty/unslugged.
			for _, m := range messages {
				if line := firstNonEmptyLine(m.Body); line != "" {
					topic = slugify(line, 40)
					break
				}
			}
		}
	}

	captureDate := dateForFilename(capturedAt)
	filename := buildEmailFilename(captureDate, topic)
	suggestedPath := buildEmailPath(in.ProjectSlug, filename)

	markdown := renderEmailMarkdown(in.ProjectSlug, capturedAt, subject, senders, messages)

	return formatEmailExtractResult{
		Markdown:          markdown,
		SuggestedFilename: filename,
		SuggestedPath:     suggestedPath,
		DetectedSubject:   top.Subject,
		DetectedFrom:      senders,
		Warnings:          warnings,
	}, nil
}

func handleFormatEmailExtract(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	in := formatEmailExtractInput{
		Text:        v.Required("text"),
		ProjectSlug: v.Required("project_slug"),
		CapturedAt:  v.String("captured_at", ""),
		Subject:     v.String("subject", ""),
		Topic:       v.String("topic", ""),
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	out, err := formatEmailExtract(in)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(out)
}

// emailHeaderRE matches a single header line like "From: alice@x.com"
// or "Subject: Re: stuff". The header key is whatever appears before
// the first colon; we only act on a fixed allowlist below.
var emailHeaderRE = regexp.MustCompile(`^(From|To|Cc|Subject|Date|Sent)\s*:\s*(.+)$`)

// quotedReplyRE matches the "On <date>, <person> wrote:" boundary
// Gmail and most other clients emit between a reply and its quoted
// parent. The trailing colon may be absent on some clients; we accept
// either. We grab the whole "stuff between On and wrote" in one group
// and split on the LAST comma in parseQuotedReplyHeader — dates with
// internal commas ("Wed, 13 May 2026 ...") would otherwise confuse a
// first-comma split.
var quotedReplyRE = regexp.MustCompile(`^On (.+?) wrote\s*:?\s*$`)

// parseEmailThread splits a raw email paste into ordered messages.
// First message is the top reply; subsequent are quoted parents.
// Best-effort: when no headers are found we return a single message
// with everything in Body.
func parseEmailThread(text string) []emailMessage {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil
	}

	// Phase 1: pull headers off the top of the message. Look in the
	// first 20 lines so headers separated by a brief preamble (e.g. a
	// quoted "Forwarded message" banner) still attach to message 0.
	top := emailMessage{}
	bodyStart := 0
	consumed := map[int]bool{}
	headerWindow := 20
	if len(lines) < headerWindow {
		headerWindow = len(lines)
	}
	for i := 0; i < headerWindow; i++ {
		line := strings.TrimRight(lines[i], " \t\r")
		if line == "" {
			continue
		}
		m := emailHeaderRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		val := strings.TrimSpace(m[2])
		switch key {
		case "From":
			top.From = val
		case "Subject":
			top.Subject = val
		case "Date", "Sent":
			top.Date = val
		}
		consumed[i] = true
		bodyStart = i + 1
	}

	// If no headers fired, signal an empty result to the caller so it
	// can fall back to the whole-text-as-body path.
	if top.From == "" && top.Subject == "" && top.Date == "" {
		return nil
	}

	// Body for top message: everything after the headers, until we hit
	// a quoted-reply boundary.
	var bodyLines []string
	cursor := bodyStart
	for cursor < len(lines) {
		line := lines[cursor]
		if quotedReplyRE.MatchString(strings.TrimSpace(line)) {
			break
		}
		bodyLines = append(bodyLines, line)
		cursor++
	}
	top.Body = strings.TrimSpace(strings.Join(bodyLines, "\n"))
	messages := []emailMessage{top}

	// Phase 2: walk remaining lines, splitting on quoted-reply markers.
	current := emailMessage{}
	var currentBody []string
	flush := func() {
		if current.From == "" && current.Date == "" && len(currentBody) == 0 {
			return
		}
		current.Body = stripQuoteMarkers(strings.TrimSpace(strings.Join(currentBody, "\n")))
		messages = append(messages, current)
		current = emailMessage{}
		currentBody = nil
	}
	for cursor < len(lines) {
		line := lines[cursor]
		trimmed := strings.TrimSpace(line)
		if m := quotedReplyRE.FindStringSubmatch(trimmed); m != nil {
			flush()
			date, from := splitQuotedReplyHeader(m[1])
			current.Date = date
			current.From = from
			cursor++
			continue
		}
		currentBody = append(currentBody, line)
		cursor++
	}
	flush()

	return messages
}

// splitQuotedReplyHeader breaks the captured text from "On <X> wrote:"
// into (date, from). Real-world headers use commas inside the date
// ("Wed, 13 May 2026 17:00:00 -0400, bob@example.com") so we split on
// the LAST comma rather than the first. We also strip a trailing
// "<addr>" form ("Bob Smith <bob@example.com>") to a bare display name
// when the addr is the same.
func splitQuotedReplyHeader(s string) (string, string) {
	s = strings.TrimSpace(s)
	idx := strings.LastIndex(s, ",")
	if idx == -1 {
		// No comma: treat the whole thing as the from field.
		return "", s
	}
	date := strings.TrimSpace(s[:idx])
	from := strings.TrimSpace(s[idx+1:])
	// Strip "<addr>" suffix: "Bob Smith <bob@x.com>" → "Bob Smith".
	if i := strings.LastIndex(from, "<"); i != -1 && strings.HasSuffix(from, ">") {
		bare := strings.TrimSpace(from[:i])
		if bare != "" {
			from = bare
		}
	}
	return date, from
}

// stripQuoteMarkers removes leading "> " characters so quoted parents
// render as plain text in the final markdown.
func stripQuoteMarkers(body string) string {
	out := make([]string, 0, 16)
	for _, raw := range strings.Split(body, "\n") {
		line := raw
		for strings.HasPrefix(line, ">") {
			line = strings.TrimPrefix(line, ">")
			line = strings.TrimPrefix(line, " ")
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// collectEmailSenders returns unique From values in thread order.
func collectEmailSenders(messages []emailMessage) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(messages))
	for _, m := range messages {
		s := strings.TrimSpace(m.From)
		if s == "" {
			continue
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// parseEmailDate makes a best effort to parse common email Date header
// formats into RFC 3339. We accept RFC 1123Z (the RFC 822 canonical
// shape Gmail emits) plus a couple of close variants.
func parseEmailDate(s string) (string, bool) {
	formats := []string{
		time.RFC1123Z,
		time.RFC1123,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 MST",
		"2 Jan 2006 15:04:05 -0700",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t.Format(time.RFC3339), true
		}
	}
	return "", false
}

func firstNonEmptyLine(s string) string {
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line != "" {
			return line
		}
	}
	return ""
}

func buildEmailFilename(date, topic string) string {
	if topic == "" {
		topic = "note"
	}
	return fmt.Sprintf("%s-%s.md", date, topic)
}

func buildEmailPath(projectSlug, filename string) string {
	if projectSlug == "" {
		projectSlug = "_inbox"
	}
	return fmt.Sprintf(
		"/workspace/icc-project-workspaces/projects/%s/email/%s",
		projectSlug, filename,
	)
}

// renderEmailMarkdown builds the final markdown document. Frontmatter
// shape matches STRUCTURE.md's email spec — keep field names and order
// stable.
func renderEmailMarkdown(
	projectSlug, capturedAt, subject string,
	participants []string,
	messages []emailMessage,
) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "project: %s\n", projectSlug)
	b.WriteString("source: email\n")
	b.WriteString("classification: possible_phi\n")
	fmt.Fprintf(&b, "captured_at: %s\n", capturedAt)
	fmt.Fprintf(&b, "subject: %s\n", subject)
	b.WriteString("participants: [")
	b.WriteString(joinQuoted(participants))
	b.WriteString("]\n")
	b.WriteString("---\n\n")

	heading := strings.TrimSpace(subject)
	if heading == "" {
		heading = "Email"
	}
	fmt.Fprintf(&b, "# %s\n\n", heading)

	for i, m := range messages {
		from := m.From
		if from == "" {
			from = "unknown"
		}
		date := strings.TrimSpace(m.Date)
		if date != "" {
			fmt.Fprintf(&b, "### From %s · %s\n\n", from, date)
		} else {
			fmt.Fprintf(&b, "### From %s\n\n", from)
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
