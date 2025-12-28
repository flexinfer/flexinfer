// mcp-youtube provides YouTube transcript extraction via MCP.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"github.com/kkdai/youtube/v2"
)

var version = "dev"

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

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

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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
	urlStr, _ := args["url"].(string)
	if urlStr == "" {
		return nil, fmt.Errorf("url is required")
	}

	language, _ := args["language"].(string)
	if language == "" {
		language = "en"
	}

	includeTimestamps, _ := args["include_timestamps"].(bool)

	videoID := extractVideoID(urlStr)

	client := youtube.Client{}
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
	urlStr, _ := args["url"].(string)
	if urlStr == "" {
		return nil, fmt.Errorf("url is required")
	}

	videoID := extractVideoID(urlStr)

	client := youtube.Client{}
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
