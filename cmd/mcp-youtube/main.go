// mcp-youtube provides YouTube transcript extraction via MCP.
package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/kkdai/youtube/v2"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcpscaffold"
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
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-youtube", version,
		mcpscaffold.WithInstructions("YouTube transcript server. Use get_transcript to extract video transcripts."),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	// get_transcript - Get transcript from a YouTube video
	srv.AddTracedTool(mcp.Tool{
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
	srv.AddTracedTool(mcp.Tool{
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

	return srv.Run(ctx)
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
		// Library transcript API failed — try caption track fallback
		text, fbErr := transcriptViaCaptionTracks(ctx, video, language, includeTimestamps)
		if fbErr != nil {
			return mcp.ErrorResult(fmt.Errorf("failed to get transcript: %w (caption fallback: %v)", err, fbErr)), nil
		}
		return mcp.JSONResult(map[string]any{
			"video_id":   videoID,
			"title":      video.Title,
			"language":   language,
			"source":     "caption-tracks",
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

// transcriptViaCaptionTracks fetches a transcript using the video's caption
// track URLs directly. This is a pure Go fallback that works in hub mode
// without requiring yt-dlp.
func transcriptViaCaptionTracks(ctx context.Context, video *youtube.Video, language string, includeTimestamps bool) (string, error) {
	if len(video.CaptionTracks) == 0 {
		return "", fmt.Errorf("no caption tracks available")
	}

	// Find matching caption track: prefer exact language match, then "en" fallback.
	var track *youtube.CaptionTrack
	for i := range video.CaptionTracks {
		if video.CaptionTracks[i].LanguageCode == language {
			track = &video.CaptionTracks[i]
			break
		}
	}
	if track == nil && language != "en" {
		for i := range video.CaptionTracks {
			if video.CaptionTracks[i].LanguageCode == "en" {
				track = &video.CaptionTracks[i]
				break
			}
		}
	}
	if track == nil {
		// Use first available track.
		track = &video.CaptionTracks[0]
	}

	// Force json3 format in the caption track URL.
	captionURL := setCaptionFormat(track.BaseURL, "json3")

	body, err := fetchURL(ctx, captionURL)
	if err != nil {
		return "", err
	}

	// Try json3 first, fall back to XML (timedtext srv3).
	if text, err := parseJSON3Captions(body, includeTimestamps); err == nil && text != "" {
		return text, nil
	}

	// Retry with XML format if json3 didn't work.
	captionURL = setCaptionFormat(track.BaseURL, "srv3")
	body, err = fetchURL(ctx, captionURL)
	if err != nil {
		return "", err
	}
	return parseXMLCaptions(body, includeTimestamps)
}

// setCaptionFormat sets or replaces the fmt parameter in a caption URL.
func setCaptionFormat(rawURL, format string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("fmt", format)
	u.RawQuery = q.Encode()
	return u.String()
}

func fetchURL(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch captions: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("caption fetch returned %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func parseJSON3Captions(body []byte, includeTimestamps bool) (string, error) {
	var subData struct {
		Events []struct {
			TStartMs int `json:"tStartMs"`
			Segs     []struct {
				UTF8 string `json:"utf8"`
			} `json:"segs"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &subData); err != nil {
		return "", err
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

// timedText is the XML structure returned by YouTube's timedtext API (srv3 format).
type timedText struct {
	XMLName xml.Name      `xml:"timedtext"`
	Body    timedTextBody `xml:"body"`
}

type timedTextBody struct {
	Paragraphs []timedTextParagraph `xml:"p"`
}

type timedTextParagraph struct {
	StartMs  int            `xml:"t,attr"`
	Duration int            `xml:"d,attr"`
	Segments []timedTextSeg `xml:"s"`
}

type timedTextSeg struct {
	Text string `xml:",chardata"`
}

func parseXMLCaptions(body []byte, includeTimestamps bool) (string, error) {
	var tt timedText
	if err := xml.Unmarshal(body, &tt); err != nil {
		return "", fmt.Errorf("parse caption xml: %w", err)
	}

	var builder strings.Builder
	for _, p := range tt.Body.Paragraphs {
		var text string
		for _, seg := range p.Segments {
			text += seg.Text
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if includeTimestamps {
			ms := p.StartMs
			m, s := ms/60000, (ms%60000)/1000
			builder.WriteString(fmt.Sprintf("[%d:%02d] %s\n", m, s, text))
		} else {
			builder.WriteString(text + " ")
		}
	}

	result := strings.TrimSpace(builder.String())
	if result == "" {
		return "", fmt.Errorf("no transcript text in caption track")
	}
	return result, nil
}
