// mcp-youtube provides YouTube transcript extraction via MCP.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/kkdai/youtube/v2"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "dev"

// Singleton YouTube client for connection reuse
var (
	ytClient     *youtube.Client
	ytClientOnce sync.Once
)

func getYouTubeClient() *youtube.Client {
	ytClientOnce.Do(func() {
		ytClient = &youtube.Client{}
	})
	return ytClient
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-youtube", "version", version)

	server := mcp.NewServer("mcp-youtube", version)
	server.SetInstructions("YouTube transcript server. Use get_transcript to extract video transcripts.")

	// get_transcript - Get transcript from a YouTube video
	server.AddTool(mcp.Tool{
		Name:        "get_transcript",
		Description: "Get the transcript/captions from a YouTube video",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "YouTube video URL or video ID",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Language code (e.g., 'en', 'es', 'fr'). Defaults to 'en'",
				},
				"include_timestamps": map[string]any{
					"type":        "boolean",
					"description": "Include timestamps in output. Defaults to false",
				},
			},
			Required: []string{"url"},
		},
	}, handleGetTranscript)

	// get_video_info - Get video metadata
	server.AddTool(mcp.Tool{
		Name:        "get_video_info",
		Description: "Get metadata about a YouTube video (title, duration, author, etc.)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "YouTube video URL or video ID",
				},
			},
			Required: []string{"url"},
		},
	}, handleGetVideoInfo)

	return server.Run(ctx)
}

// extractVideoID extracts the video ID from various YouTube URL formats
func extractVideoID(input string) string {
	// Already a video ID (11 chars, alphanumeric with - and _)
	if matched, _ := regexp.MatchString(`^[a-zA-Z0-9_-]{11}$`, input); matched {
		return input
	}

	// Standard watch URL: youtube.com/watch?v=VIDEO_ID
	re := regexp.MustCompile(`(?:youtube\.com/watch\?v=|youtu\.be/|youtube\.com/embed/|youtube\.com/v/|youtube\.com/shorts/)([a-zA-Z0-9_-]{11})`)
	matches := re.FindStringSubmatch(input)
	if len(matches) > 1 {
		return matches[1]
	}

	return input
}

func handleGetTranscript(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	urlStr := v.Required("url")
	language := v.String("language", "en")
	includeTimestamps := v.Bool("include_timestamps", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	videoID := extractVideoID(urlStr)

	client := getYouTubeClient()
	video, err := client.GetVideoContext(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video: %w", err)
	}

	// Get transcript
	transcript, err := client.GetTranscript(video, language)
	if err != nil {
		// Try without language specification
		transcript, err = client.GetTranscript(video, "")
		if err != nil {
			return nil, fmt.Errorf("failed to get transcript: %w", err)
		}
	}

	var builder strings.Builder
	for _, segment := range transcript {
		if includeTimestamps {
			builder.WriteString(fmt.Sprintf("[%s] %s\n", segment.OffsetText, segment.Text))
		} else {
			builder.WriteString(segment.Text + " ")
		}
	}

	text := strings.TrimSpace(builder.String())

	return mcp.JSONResult(map[string]any{
		"video_id":   videoID,
		"title":      video.Title,
		"language":   language,
		"segments":   len(transcript),
		"transcript": text,
	})
}

func handleGetVideoInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	urlStr := v.Required("url")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	videoID := extractVideoID(urlStr)

	client := getYouTubeClient()
	video, err := client.GetVideoContext(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to get video: %w", err)
	}

	return mcp.JSONResult(map[string]any{
		"video_id":    videoID,
		"title":       video.Title,
		"author":      video.Author,
		"duration":    video.Duration.String(),
		"description": truncateString(video.Description, 500),
		"views":       video.Views,
		"publish_date": func() string {
			if video.PublishDate.IsZero() {
				return ""
			}
			return video.PublishDate.Format("2006-01-02")
		}(),
	})
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
