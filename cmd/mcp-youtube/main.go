// mcp-youtube provides YouTube transcript extraction via MCP.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/kkdai/youtube/v2"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/strutil"
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
		return mcp.ErrorResult(fmt.Errorf("failed to get video: %w", err)), nil
	}

	// Get transcript via library first, then fall back to yt-dlp
	transcript, err := client.GetTranscript(video, language)
	if err != nil {
		transcript, err = client.GetTranscript(video, "")
	}
	if err != nil {
		// Library transcript API failed — try yt-dlp fallback
		text, fbErr := transcriptViaYTDLP(ctx, videoID, language, includeTimestamps)
		if fbErr != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to get transcript: %w (yt-dlp fallback: %v)", err, fbErr)), nil
		}
		return mcp.JSONResult(map[string]any{
			"video_id":   videoID,
			"title":      video.Title,
			"language":   language,
			"source":     "yt-dlp",
			"transcript": text,
		})
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
		return mcp.ErrorResult(fmt.Errorf("failed to get video: %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"video_id":    videoID,
		"title":       video.Title,
		"author":      video.Author,
		"duration":    video.Duration.String(),
		"description": strutil.Truncate(video.Description, 500),
		"views":       video.Views,
		"publish_date": func() string {
			if video.PublishDate.IsZero() {
				return ""
			}
			return video.PublishDate.Format("2006-01-02")
		}(),
	})
}

// transcriptViaYTDLP fetches a transcript using yt-dlp as a fallback when the
// Go library's GetTranscript API fails (e.g., YouTube internal API changes).
func transcriptViaYTDLP(ctx context.Context, videoID, language string, includeTimestamps bool) (string, error) {
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return "", fmt.Errorf("yt-dlp not found in PATH")
	}

	url := "https://www.youtube.com/watch?v=" + videoID
	args := []string{
		"--skip-download",
		"--write-subs",
		"--write-auto-subs",
		"--sub-format", "json3",
		"--sub-langs", language,
		"--dump-json",
		url,
	}

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp failed: %w", err)
	}

	// Parse the JSON output for subtitle URL
	var info struct {
		Subtitles    map[string][]subtitleEntry `json:"subtitles"`
		AutoCaptions map[string][]subtitleEntry `json:"automatic_captions"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("failed to parse yt-dlp output: %w", err)
	}

	// Prefer manual subtitles, fall back to auto-captions
	subs := info.Subtitles[language]
	if len(subs) == 0 {
		subs = info.AutoCaptions[language]
	}
	if len(subs) == 0 {
		// Try "en" fallback if requested language unavailable
		subs = info.Subtitles["en"]
		if len(subs) == 0 {
			subs = info.AutoCaptions["en"]
		}
	}
	if len(subs) == 0 {
		return "", fmt.Errorf("no subtitles available for language %q", language)
	}

	// Find json3 format subtitle URL and fetch it
	var subURL string
	for _, s := range subs {
		if s.Ext == "json3" {
			subURL = s.URL
			break
		}
	}
	if subURL == "" {
		return "", fmt.Errorf("no json3 subtitle format available")
	}

	// Fetch subtitle content
	subCmd := exec.CommandContext(ctx, "yt-dlp", "--no-warnings", "-o", "-", subURL)
	subOut, err := subCmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to fetch subtitle content: %w", err)
	}

	// Parse json3 subtitle format
	var subData struct {
		Events []struct {
			TStartMs int `json:"tStartMs"`
			Segs     []struct {
				UTF8 string `json:"utf8"`
			} `json:"segs"`
		} `json:"events"`
	}
	if err := json.Unmarshal(subOut, &subData); err != nil {
		return "", fmt.Errorf("failed to parse subtitle json3: %w", err)
	}

	var builder strings.Builder
	for _, event := range subData.Events {
		var text string
		for _, seg := range event.Segs {
			text += seg.UTF8
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if includeTimestamps {
			ms := event.TStartMs
			m, s := ms/60000, (ms%60000)/1000
			builder.WriteString(fmt.Sprintf("[%d:%02d] %s\n", m, s, text))
		} else {
			builder.WriteString(text + " ")
		}
	}

	return strings.TrimSpace(builder.String()), nil
}

type subtitleEntry struct {
	Ext  string `json:"ext"`
	URL  string `json:"url"`
	Name string `json:"name"`
}
