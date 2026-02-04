// mcp-slack is a Slack MCP server for searching and posting messages.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type slackServer struct {
	token      string
	httpClient *httpclient.Client
}

type slackError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	token := os.Getenv("SLACK_BOT_TOKEN")
	if token == "" {
		token = os.Getenv("SLACK_TOKEN")
	}

	srv := &slackServer{
		token:      token,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-slack", "version", version)

	server := mcp.NewServer("mcp-slack", version)
	server.SetInstructions("Slack MCP server. Search messages, list channels, post messages. Requires SLACK_BOT_TOKEN with appropriate scopes.")

	// search_messages
	server.AddTool(mcp.Tool{
		Name:        "search_messages",
		Description: "Search for messages in Slack. Requires search:read scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query. Supports Slack search modifiers like 'from:@user', 'in:#channel', 'before:2024-01-01'",
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "Number of results to return (max 100, default 20)",
				},
				"sort": map[string]any{
					"type":        "string",
					"description": "Sort order: score (relevance) or timestamp",
				},
				"sort_dir": map[string]any{
					"type":        "string",
					"description": "Sort direction: asc or desc",
				},
			},
			Required: []string{"query"},
		},
	}, srv.handleSearchMessages)

	// list_channels
	server.AddTool(mcp.Tool{
		Name:        "list_channels",
		Description: "List channels in the workspace. Requires channels:read scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"types": map[string]any{
					"type":        "string",
					"description": "Comma-separated channel types: public_channel, private_channel, mpim, im (default: public_channel)",
				},
				"exclude_archived": map[string]any{
					"type":        "boolean",
					"description": "Exclude archived channels (default: true)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of channels to return (max 1000, default 100)",
				},
			},
		},
	}, srv.handleListChannels)

	// get_channel_history
	server.AddTool(mcp.Tool{
		Name:        "get_channel_history",
		Description: "Get message history for a channel. Requires channels:history or groups:history scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"channel": map[string]any{
					"type":        "string",
					"description": "Channel ID (e.g., C1234567890)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Number of messages to return (max 1000, default 100)",
				},
				"oldest": map[string]any{
					"type":        "string",
					"description": "Only messages after this Unix timestamp",
				},
				"latest": map[string]any{
					"type":        "string",
					"description": "Only messages before this Unix timestamp",
				},
			},
			Required: []string{"channel"},
		},
	}, srv.handleGetChannelHistory)

	// post_message
	server.AddTool(mcp.Tool{
		Name:        "post_message",
		Description: "Post a message to a channel. Requires chat:write scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"channel": map[string]any{
					"type":        "string",
					"description": "Channel ID or name (e.g., C1234567890 or #general)",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "Message text. Supports Slack mrkdwn formatting.",
				},
				"thread_ts": map[string]any{
					"type":        "string",
					"description": "Thread timestamp to reply in a thread",
				},
				"unfurl_links": map[string]any{
					"type":        "boolean",
					"description": "Enable unfurling of primarily text-based content (default: true)",
				},
				"unfurl_media": map[string]any{
					"type":        "boolean",
					"description": "Enable unfurling of media content (default: true)",
				},
			},
			Required: []string{"channel", "text"},
		},
	}, srv.handlePostMessage)

	// list_users
	server.AddTool(mcp.Tool{
		Name:        "list_users",
		Description: "List users in the workspace. Requires users:read scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of users to return (max 1000, default 100)",
				},
				"include_locale": map[string]any{
					"type":        "boolean",
					"description": "Include locale information",
				},
			},
		},
	}, srv.handleListUsers)

	// get_user_info
	server.AddTool(mcp.Tool{
		Name:        "get_user_info",
		Description: "Get information about a user. Requires users:read scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"user": map[string]any{
					"type":        "string",
					"description": "User ID (e.g., U1234567890)",
				},
			},
			Required: []string{"user"},
		},
	}, srv.handleGetUserInfo)

	// get_channel_info
	server.AddTool(mcp.Tool{
		Name:        "get_channel_info",
		Description: "Get information about a channel. Requires channels:read scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"channel": map[string]any{
					"type":        "string",
					"description": "Channel ID (e.g., C1234567890)",
				},
			},
			Required: []string{"channel"},
		},
	}, srv.handleGetChannelInfo)

	// add_reaction
	server.AddTool(mcp.Tool{
		Name:        "add_reaction",
		Description: "Add a reaction to a message. Requires reactions:write scope.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"channel": map[string]any{
					"type":        "string",
					"description": "Channel ID where the message is",
				},
				"timestamp": map[string]any{
					"type":        "string",
					"description": "Message timestamp (ts)",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Reaction emoji name (without colons, e.g., 'thumbsup')",
				},
			},
			Required: []string{"channel", "timestamp", "name"},
		},
	}, srv.handleAddReaction)

	// get_permalink
	server.AddTool(mcp.Tool{
		Name:        "get_permalink",
		Description: "Get a permalink URL for a message",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"channel": map[string]any{
					"type":        "string",
					"description": "Channel ID",
				},
				"message_ts": map[string]any{
					"type":        "string",
					"description": "Message timestamp",
				},
			},
			Required: []string{"channel", "message_ts"},
		},
	}, srv.handleGetPermalink)

	return server.Run(ctx)
}

func (s *slackServer) request(ctx context.Context, method, endpoint string, params url.Values, jsonBody any) ([]byte, error) {
	var req *http.Request
	var err error

	apiURL := "https://slack.com/api/" + endpoint

	if jsonBody != nil {
		data, err := json.Marshal(jsonBody)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		req, err = http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	} else if params != nil {
		if method == "GET" {
			apiURL += "?" + params.Encode()
			req, err = http.NewRequestWithContext(ctx, method, apiURL, nil)
		} else {
			req, err = http.NewRequestWithContext(ctx, method, apiURL, strings.NewReader(params.Encode()))
			if err == nil {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, method, apiURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
	}

	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.httpClient.HTTP().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Check for Slack API error
	var slackErr slackError
	if err := json.Unmarshal(body, &slackErr); err == nil && !slackErr.OK {
		return nil, fmt.Errorf("slack API error: %s", slackErr.Error)
	}

	return body, nil
}

func (s *slackServer) handleSearchMessages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.Required("query")
	count := v.Int("count", 20)
	sort := v.String("sort", "")
	sortDir := v.String("sort_dir", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("count", strconv.Itoa(count))
	if sort != "" {
		params.Set("sort", sort)
	}
	if sortDir != "" {
		params.Set("sort_dir", sortDir)
	}

	data, err := s.request(ctx, "GET", "search.messages", params, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK       bool `json:"ok"`
		Messages struct {
			Total   int `json:"total"`
			Matches []struct {
				Type     string `json:"type"`
				User     string `json:"user"`
				Username string `json:"username"`
				Text     string `json:"text"`
				Ts       string `json:"ts"`
				Channel  struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"channel"`
				Permalink string `json:"permalink"`
			} `json:"matches"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d messages:\n\n", result.Messages.Total))
	for _, m := range result.Messages.Matches {
		sb.WriteString(fmt.Sprintf("- #%s | @%s | %s\n", m.Channel.Name, m.Username, formatTimestamp(m.Ts)))
		sb.WriteString(fmt.Sprintf("  %s\n", truncateText(m.Text, 200)))
		sb.WriteString(fmt.Sprintf("  %s\n\n", m.Permalink))
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *slackServer) handleListChannels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	types := v.String("types", "public_channel")
	excludeArchived := v.Bool("exclude_archived", true)
	limit := v.Int("limit", 100)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("types", types)
	params.Set("exclude_archived", strconv.FormatBool(excludeArchived))
	params.Set("limit", strconv.Itoa(limit))

	data, err := s.request(ctx, "GET", "conversations.list", params, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK       bool `json:"ok"`
		Channels []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			IsPrivate  bool   `json:"is_private"`
			IsArchived bool   `json:"is_archived"`
			NumMembers int    `json:"num_members"`
			Topic      struct {
				Value string `json:"value"`
			} `json:"topic"`
			Purpose struct {
				Value string `json:"value"`
			} `json:"purpose"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d channels:\n\n", len(result.Channels)))
	for _, c := range result.Channels {
		private := ""
		if c.IsPrivate {
			private = " [private]"
		}
		archived := ""
		if c.IsArchived {
			archived = " [archived]"
		}
		sb.WriteString(fmt.Sprintf("- #%s (ID: %s)%s%s\n", c.Name, c.ID, private, archived))
		sb.WriteString(fmt.Sprintf("  members: %d\n", c.NumMembers))
		if c.Purpose.Value != "" {
			sb.WriteString(fmt.Sprintf("  purpose: %s\n", truncateText(c.Purpose.Value, 100)))
		}
		sb.WriteString("\n")
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *slackServer) handleGetChannelHistory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	channel := v.Required("channel")
	limit := v.Int("limit", 100)
	oldest := v.String("oldest", "")
	latest := v.String("latest", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("channel", channel)
	params.Set("limit", strconv.Itoa(limit))
	if oldest != "" {
		params.Set("oldest", oldest)
	}
	if latest != "" {
		params.Set("latest", latest)
	}

	data, err := s.request(ctx, "GET", "conversations.history", params, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK       bool `json:"ok"`
		Messages []struct {
			Type      string `json:"type"`
			User      string `json:"user"`
			Text      string `json:"text"`
			Ts        string `json:"ts"`
			ThreadTs  string `json:"thread_ts"`
			Reactions []struct {
				Name  string   `json:"name"`
				Count int      `json:"count"`
				Users []string `json:"users"`
			} `json:"reactions"`
		} `json:"messages"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Messages in channel (showing %d):\n\n", len(result.Messages)))
	for _, m := range result.Messages {
		isThread := ""
		if m.ThreadTs != "" && m.ThreadTs != m.Ts {
			isThread = " [thread reply]"
		}
		sb.WriteString(fmt.Sprintf("- %s | %s%s\n", m.User, formatTimestamp(m.Ts), isThread))
		sb.WriteString(fmt.Sprintf("  %s\n", truncateText(m.Text, 300)))
		if len(m.Reactions) > 0 {
			reactions := []string{}
			for _, r := range m.Reactions {
				reactions = append(reactions, fmt.Sprintf(":%s: %d", r.Name, r.Count))
			}
			sb.WriteString(fmt.Sprintf("  reactions: %s\n", strings.Join(reactions, " ")))
		}
		sb.WriteString("\n")
	}
	if result.HasMore {
		sb.WriteString("[more messages available]\n")
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *slackServer) handlePostMessage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	channel := v.Required("channel")
	text := v.Required("text")
	threadTs := v.String("thread_ts", "")
	unfurlLinks := v.Bool("unfurl_links", true)
	unfurlMedia := v.Bool("unfurl_media", true)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"channel":      channel,
		"text":         text,
		"unfurl_links": unfurlLinks,
		"unfurl_media": unfurlMedia,
	}
	if threadTs != "" {
		body["thread_ts"] = threadTs
	}

	data, err := s.request(ctx, "POST", "chat.postMessage", nil, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK      bool   `json:"ok"`
		Channel string `json:"channel"`
		Ts      string `json:"ts"`
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	return mcp.TextResult(fmt.Sprintf("Message posted to %s at %s", result.Channel, result.Ts)), nil
}

func (s *slackServer) handleListUsers(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	limit := v.Int("limit", 100)
	includeLocale := v.Bool("include_locale", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	if includeLocale {
		params.Set("include_locale", "true")
	}

	data, err := s.request(ctx, "GET", "users.list", params, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK      bool `json:"ok"`
		Members []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			IsBot    bool   `json:"is_bot"`
			IsAdmin  bool   `json:"is_admin"`
			Deleted  bool   `json:"deleted"`
			Profile  struct {
				Title       string `json:"title"`
				DisplayName string `json:"display_name"`
				Email       string `json:"email"`
			} `json:"profile"`
		} `json:"members"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d users:\n\n", len(result.Members)))
	for _, u := range result.Members {
		if u.Deleted {
			continue
		}
		flags := []string{}
		if u.IsBot {
			flags = append(flags, "bot")
		}
		if u.IsAdmin {
			flags = append(flags, "admin")
		}
		flagStr := ""
		if len(flags) > 0 {
			flagStr = " [" + strings.Join(flags, ", ") + "]"
		}
		sb.WriteString(fmt.Sprintf("- @%s (ID: %s)%s\n", u.Name, u.ID, flagStr))
		if u.RealName != "" {
			sb.WriteString(fmt.Sprintf("  name: %s\n", u.RealName))
		}
		if u.Profile.Title != "" {
			sb.WriteString(fmt.Sprintf("  title: %s\n", u.Profile.Title))
		}
		sb.WriteString("\n")
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *slackServer) handleGetUserInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	user := v.Required("user")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("user", user)

	data, err := s.request(ctx, "GET", "users.info", params, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK   bool `json:"ok"`
		User struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			IsBot    bool   `json:"is_bot"`
			IsAdmin  bool   `json:"is_admin"`
			Deleted  bool   `json:"deleted"`
			TZ       string `json:"tz"`
			TZLabel  string `json:"tz_label"`
			Profile  struct {
				Title            string `json:"title"`
				Phone            string `json:"phone"`
				DisplayName      string `json:"display_name"`
				RealName         string `json:"real_name"`
				Email            string `json:"email"`
				StatusText       string `json:"status_text"`
				StatusEmoji      string `json:"status_emoji"`
				StatusExpiration int64  `json:"status_expiration"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	u := result.User
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("User: @%s (ID: %s)\n", u.Name, u.ID))
	sb.WriteString(fmt.Sprintf("Name: %s\n", u.RealName))
	if u.Profile.DisplayName != "" {
		sb.WriteString(fmt.Sprintf("Display Name: %s\n", u.Profile.DisplayName))
	}
	if u.Profile.Title != "" {
		sb.WriteString(fmt.Sprintf("Title: %s\n", u.Profile.Title))
	}
	if u.Profile.Email != "" {
		sb.WriteString(fmt.Sprintf("Email: %s\n", u.Profile.Email))
	}
	if u.Profile.StatusText != "" {
		sb.WriteString(fmt.Sprintf("Status: %s %s\n", u.Profile.StatusEmoji, u.Profile.StatusText))
	}
	sb.WriteString(fmt.Sprintf("Timezone: %s\n", u.TZLabel))
	if u.IsBot {
		sb.WriteString("Type: Bot\n")
	}
	if u.IsAdmin {
		sb.WriteString("Role: Admin\n")
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *slackServer) handleGetChannelInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	channel := v.Required("channel")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("channel", channel)

	data, err := s.request(ctx, "GET", "conversations.info", params, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK      bool `json:"ok"`
		Channel struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			IsPrivate  bool   `json:"is_private"`
			IsArchived bool   `json:"is_archived"`
			IsMember   bool   `json:"is_member"`
			NumMembers int    `json:"num_members"`
			Created    int64  `json:"created"`
			Creator    string `json:"creator"`
			Topic      struct {
				Value   string `json:"value"`
				Creator string `json:"creator"`
			} `json:"topic"`
			Purpose struct {
				Value   string `json:"value"`
				Creator string `json:"creator"`
			} `json:"purpose"`
		} `json:"channel"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	c := result.Channel
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Channel: #%s (ID: %s)\n", c.Name, c.ID))
	if c.IsPrivate {
		sb.WriteString("Type: Private\n")
	} else {
		sb.WriteString("Type: Public\n")
	}
	if c.IsArchived {
		sb.WriteString("Status: Archived\n")
	}
	sb.WriteString(fmt.Sprintf("Members: %d\n", c.NumMembers))
	sb.WriteString(fmt.Sprintf("Created: %s by %s\n", time.Unix(c.Created, 0).Format(time.RFC3339), c.Creator))
	if c.Topic.Value != "" {
		sb.WriteString(fmt.Sprintf("Topic: %s\n", c.Topic.Value))
	}
	if c.Purpose.Value != "" {
		sb.WriteString(fmt.Sprintf("Purpose: %s\n", c.Purpose.Value))
	}

	return mcp.TextResult(sb.String()), nil
}

func (s *slackServer) handleAddReaction(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	channel := v.Required("channel")
	timestamp := v.Required("timestamp")
	name := v.Required("name")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	body := map[string]any{
		"channel":   channel,
		"timestamp": timestamp,
		"name":      name,
	}

	_, err := s.request(ctx, "POST", "reactions.add", nil, body)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.TextResult(fmt.Sprintf("Added :%s: reaction", name)), nil
}

func (s *slackServer) handleGetPermalink(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	channel := v.Required("channel")
	messageTs := v.Required("message_ts")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	params := url.Values{}
	params.Set("channel", channel)
	params.Set("message_ts", messageTs)

	data, err := s.request(ctx, "GET", "chat.getPermalink", params, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var result struct {
		OK        bool   `json:"ok"`
		Permalink string `json:"permalink"`
		Channel   string `json:"channel"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcp.ErrorResult(fmt.Errorf("parse response: %w", err)), nil
	}

	return mcp.TextResult(result.Permalink), nil
}

func truncateText(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatTimestamp(ts string) string {
	// Slack timestamps are Unix timestamps with microseconds as decimal
	parts := strings.Split(ts, ".")
	if len(parts) == 0 {
		return ts
	}
	secs, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return ts
	}
	return time.Unix(secs, 0).Format("2006-01-02 15:04:05")
}
