package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// formatStandupSchema declares the input contract for icc_format_standup.
// Per STRUCTURE.md, standups go under projects/<slug>/research/ since
// there is no standup/ source folder — standups are treated as a kind
// of research artifact.
func formatStandupSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"text", "project_slug"},
		Properties: map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Standup notes (personal prep or team standup transcript)",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "ICC project slug; use '_inbox' for personal prep",
			},
			"captured_at": map[string]any{
				"type":        "string",
				"description": "ISO 8601 timestamp; defaults to now",
			},
			"topic": map[string]any{
				"type":        "string",
				"description": "Optional short topic slug; defaults to 'standup' or 'standup-prep'",
			},
			"team": map[string]any{
				"type":        "string",
				"description": "Optional team name for team standups",
			},
			"participants": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional list of attendees",
			},
		},
	}
}

type formatStandupResult struct {
	Markdown          string `json:"markdown"`
	SuggestedFilename string `json:"suggested_filename"`
	SuggestedPath     string `json:"suggested_path"`
}

// formatStandupInput is the typed input bundle shared by the format
// tool and the icc_capture_standup composer.
type formatStandupInput struct {
	Text         string
	ProjectSlug  string
	CapturedAt   string
	Topic        string
	Team         string
	Participants []string
}

// formatStandup is the pure formatter (no MCP types, no I/O).
func formatStandup(in formatStandupInput) (formatStandupResult, error) {
	if strings.TrimSpace(in.Text) == "" {
		return formatStandupResult{}, errors.New("text is required and must not be empty")
	}

	capturedAt := in.CapturedAt
	if capturedAt == "" {
		capturedAt = time.Now().Format(time.RFC3339)
	}

	participants := cleanParticipants(in.Participants)

	// Topic defaults differ by flavor: personal prep (no team, _inbox
	// project) gets "standup-prep"; team standups get "standup" with
	// the team name folded into the filename.
	isPersonal := strings.TrimSpace(in.Team) == "" && (in.ProjectSlug == "" || in.ProjectSlug == "_inbox")
	topic := in.Topic
	if topic == "" {
		if isPersonal {
			topic = "standup-prep"
		} else {
			topic = "standup"
		}
	}

	captureDate := dateForFilename(capturedAt)
	filename := buildStandupFilename(captureDate, in.Team, topic)
	suggestedPath := buildStandupPath(in.ProjectSlug, filename)

	markdown := renderStandupMarkdown(in.ProjectSlug, capturedAt, participants, in.Team, topic, in.Text)

	return formatStandupResult{
		Markdown:          markdown,
		SuggestedFilename: filename,
		SuggestedPath:     suggestedPath,
	}, nil
}

func handleFormatStandup(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	in := formatStandupInput{
		Text:         v.Required("text"),
		ProjectSlug:  v.Required("project_slug"),
		CapturedAt:   v.String("captured_at", ""),
		Topic:        v.String("topic", ""),
		Team:         v.String("team", ""),
		Participants: v.StringSlice("participants"),
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	out, err := formatStandup(in)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return jsonResult(out)
}

func buildStandupFilename(date, team, topic string) string {
	teamSlug := slugify(team, 30)
	if teamSlug != "" && teamSlug != "note" {
		return fmt.Sprintf("%s-%s-%s.md", date, teamSlug, topic)
	}
	return fmt.Sprintf("%s-%s.md", date, topic)
}

// buildStandupPath returns the path under projects/<slug>/research/.
// STRUCTURE.md does not define a standup/ folder, so we treat standup
// notes as a kind of research artifact (long-form notes that inform
// project context).
func buildStandupPath(projectSlug, filename string) string {
	if projectSlug == "" {
		projectSlug = "_inbox"
	}
	return fmt.Sprintf(
		"/workspace/icc-project-workspaces/projects/%s/research/%s",
		projectSlug, filename,
	)
}

// renderStandupMarkdown emits frontmatter (source: standup) + body.
// Synthesizes a heading from team/topic when the body lacks an H1.
func renderStandupMarkdown(
	projectSlug, capturedAt string,
	participants []string,
	team, topic, body string,
) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "project: %s\n", projectSlug)
	b.WriteString("source: standup\n")
	b.WriteString("classification: possible_phi\n")
	fmt.Fprintf(&b, "captured_at: %s\n", capturedAt)
	b.WriteString("participants: [")
	b.WriteString(joinQuoted(participants))
	b.WriteString("]\n")
	b.WriteString("---\n\n")

	trimmed := strings.TrimSpace(body)
	if !h1RE.MatchString(trimmed) {
		title := composeStandupTitle(team, topic)
		fmt.Fprintf(&b, "# %s\n\n", title)
	}
	b.WriteString(trimmed)
	b.WriteString("\n")
	return b.String()
}

func composeStandupTitle(team, topic string) string {
	team = strings.TrimSpace(team)
	topicHuman := strings.ReplaceAll(topic, "-", " ")
	if topicHuman == "" {
		topicHuman = "standup"
	}
	if team != "" {
		return fmt.Sprintf("%s %s", team, topicHuman)
	}
	return strings.ToUpper(topicHuman[:1]) + topicHuman[1:]
}
