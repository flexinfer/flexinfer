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

// formatMeetingNotesSchema declares the input contract for
// icc_format_meeting_notes. Participants is required because meetings
// have known attendees (unlike Slack threads where the formatter can
// infer from message headers, or email where senders fall out of the
// thread).
func formatMeetingNotesSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_slug", "participants"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Meeting notes (Gemini auto-notes structure or freeform)",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "ICC project slug; '_inbox' if not yet attributed",
			},
			"participants": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Required; meetings have known attendees",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to now in local TZ",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug (e.g. '1on1', 'sprint-review')",
			},
		},
	}
}

type formatMeetingNotesResult struct {
	Markdown          string `json:"markdown"`
	SuggestedFilename string `json:"suggested_filename"`
	SuggestedPath     string `json:"suggested_path"`
}

// formatMeetingNotesInput is the typed input bundle shared by the
// format tool and the icc_capture_meeting composer.
type formatMeetingNotesInput struct {
	Text         string
	ProjectSlug  string
	Participants []string
	CapturedAt   string
	Topic        string
}

// formatMeetingNotes is the pure formatter (no MCP types, no I/O).
func formatMeetingNotes(in formatMeetingNotesInput) (formatMeetingNotesResult, error) {
	if strings.TrimSpace(in.Text) == "" {
		return formatMeetingNotesResult{}, errors.New("text is required and must not be empty")
	}
	if len(cleanParticipants(in.Participants)) == 0 {
		return formatMeetingNotesResult{}, errors.New("participants is required and must not be empty")
	}

	capturedAt := in.CapturedAt
	if capturedAt == "" {
		capturedAt = time.Now().Format(time.RFC3339)
	}

	participants := cleanParticipants(in.Participants)

	topic := in.Topic
	if topic == "" {
		topic = deriveMeetingTopic(in.Text)
	}

	captureDate := dateForFilename(capturedAt)
	filename := buildMeetingFilename(captureDate, participants, topic)
	suggestedPath := buildMeetingPath(in.ProjectSlug, filename)

	markdown := renderMeetingMarkdown(in.ProjectSlug, capturedAt, participants, in.Text, topic)

	return formatMeetingNotesResult{
		Markdown:          markdown,
		SuggestedFilename: filename,
		SuggestedPath:     suggestedPath,
	}, nil
}

func handleFormatMeetingNotes(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	in := formatMeetingNotesInput{
		Text:         v.Required("text"),
		ProjectSlug:  v.Required("project_slug"),
		Participants: v.RequiredStringSlice("participants"),
		CapturedAt:   v.String("captured_at", ""),
		Topic:        v.String("topic", ""),
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	out, err := formatMeetingNotes(in)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(out)
}

// h1RE matches a markdown H1 line ("# title"). Used to decide whether
// freeform notes already have a heading.
var h1RE = regexp.MustCompile(`(?m)^#\s+\S`)

// deriveMeetingTopic best-effort pulls a topic slug from the first H1
// of the notes (Gemini auto-notes usually start with one). Falls back
// to the first non-empty line, then to a generic "meeting" slug.
func deriveMeetingTopic(text string) string {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			return slugify(strings.TrimPrefix(line, "# "), 40)
		}
		// Non-heading first line — slug it but fall back to "meeting"
		// if it produces nothing usable.
		slug := slugify(line, 40)
		if slug == "" || slug == "note" {
			return "meeting"
		}
		return slug
	}
	return "meeting"
}

// cleanParticipants trims whitespace and drops empty entries while
// preserving caller-supplied order.
func cleanParticipants(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// participantFilenameSlug joins participants for the filename. We slug
// each name (first token, lowercase) and join with "-". For 1:1
// meetings this yields e.g. "cody-nadia"; for larger meetings it
// truncates the list at 3 names to keep filenames readable.
func participantFilenameSlug(participants []string) string {
	if len(participants) == 0 {
		return ""
	}
	parts := make([]string, 0, len(participants))
	limit := len(participants)
	if limit > 3 {
		limit = 3
	}
	for i := 0; i < limit; i++ {
		// Take first whitespace-delimited token so "Nadia Patel" → "nadia".
		first := strings.Fields(participants[i])
		if len(first) == 0 {
			continue
		}
		s := slugify(first[0], 20)
		if s != "" && s != "note" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "-")
}

func buildMeetingFilename(date string, participants []string, topic string) string {
	pSlug := participantFilenameSlug(participants)
	if topic == "" {
		topic = "meeting"
	}
	switch {
	case pSlug != "" && topic != "":
		return fmt.Sprintf("%s-%s-%s.md", date, pSlug, topic)
	case pSlug != "":
		return fmt.Sprintf("%s-%s.md", date, pSlug)
	default:
		return fmt.Sprintf("%s-%s.md", date, topic)
	}
}

func buildMeetingPath(projectSlug, filename string) string {
	if projectSlug == "" {
		projectSlug = "_inbox"
	}
	return fmt.Sprintf(
		"/workspace/icc-project-workspaces/projects/%s/meetings/%s",
		projectSlug, filename,
	)
}

// renderMeetingMarkdown emits frontmatter + body. If the body has no
// H1, we synthesize one from the topic so every meeting note has a
// title.
func renderMeetingMarkdown(
	projectSlug, capturedAt string,
	participants []string,
	body string,
	topic string,
) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "project: %s\n", projectSlug)
	b.WriteString("source: meeting\n")
	b.WriteString("classification: possible_phi\n")
	fmt.Fprintf(&b, "captured_at: %s\n", capturedAt)
	b.WriteString("participants: [")
	b.WriteString(joinQuoted(participants))
	b.WriteString("]\n")
	b.WriteString("---\n\n")

	trimmed := strings.TrimSpace(body)
	if !h1RE.MatchString(trimmed) {
		title := topic
		if title == "" || title == "meeting" {
			title = "Meeting notes"
		} else {
			title = strings.ReplaceAll(title, "-", " ")
		}
		fmt.Fprintf(&b, "# %s\n\n", title)
	}
	b.WriteString(trimmed)
	b.WriteString("\n")
	return b.String()
}
