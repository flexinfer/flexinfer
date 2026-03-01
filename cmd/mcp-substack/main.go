package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "0.1.0"

type substackServer struct {
	baseURL    string
	userID     int
	httpClient *httpclient.Client
	logger     *slog.Logger
	configErr  error
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp,
		shutdownTracer,
		err := mcpotel.InitTracer(
		ctx, "mcp-substack",

		logger)
	if err !=
		nil {
		logger.Warn("OTel tracer init failed",

			"error", err)
	}
	defer func() {
		_ = shutdownTracer(ctx)
	}()
	tracer := mcpotel.Tracer(tp, "mcp-substack")

	client := httpclient.NewDefault()
	s := &substackServer{
		httpClient: client,
		logger:     logger,
	}
	token := env.StringWithFallbacks("SUBSTACK_SESSION_TOKEN", "SUBSTACK_TOKEN", "SUBSTACK_COOKIE")
	subdomain := env.String("SUBSTACK_SUBDOMAIN", "")
	if token == "" || subdomain == "" {
		s.configErr = mcperror.NotConfigured(
			"SUBSTACK_SESSION_TOKEN/SUBSTACK_SUBDOMAIN",
			"set SUBSTACK_SESSION_TOKEN (or SUBSTACK_TOKEN/SUBSTACK_COOKIE) and SUBSTACK_SUBDOMAIN",
		)
		logger.Warn("substack server running in degraded mode", "error", s.configErr.Error())
	} else {
		cookieName := env.String("SUBSTACK_COOKIE_NAME", "substack.sid")
		client.SetHeader("Cookie", cookieName+"="+token)
		s.baseURL = fmt.Sprintf("https://%s.substack.com/api/v1", subdomain)
	}

	// Fetch user ID from a published post for draft byline attribution.
	if s.configErr == nil {
		if err := s.resolveUserID(ctx); err != nil {
			logger.Warn("could not resolve user ID (draft creation may fail)", "error", err)
		}
	}

	logger.Info("starting server", "name", "mcp-substack", "version", version,
		"subdomain", subdomain, "user_id", s.userID)

	server := mcp.NewServer("mcp-substack", version)
	server.SetInstructions("Substack publication management. Create drafts, publish posts, and read archives for your newsletter.")

	server.AddTool(mcp.Tool{
		Name:        "substack_list_drafts",
		Description: "List draft posts, ordered by most recently updated",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"offset": map[string]any{"type": "integer", "description": "Pagination offset (default: 0)"},
				"limit":  map[string]any{"type": "integer", "description": "Number of drafts to return (default: 25)"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "substack_list_drafts", s.handleListDrafts))

	server.AddTool(mcp.Tool{
		Name:        "substack_create_draft",
		Description: "Create a new draft post on Substack",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"title":      map[string]any{"type": "string", "description": "Post title"},
				"subtitle":   map[string]any{"type": "string", "description": "Post subtitle"},
				"body":       map[string]any{"type": "string", "description": "Post body (HTML)"},
				"section_id": map[string]any{"type": "integer", "description": "Section ID to file under"},
				"audience":   map[string]any{"type": "string", "description": "Audience: everyone, only_paid, founding, only_free (default: everyone)"},
			},
			Required: []string{"title", "body"},
		},
	}, mcpotel.TracedToolHandler(tracer, "substack_create_draft", s.handleCreateDraft))

	server.AddTool(mcp.Tool{
		Name:        "substack_update_draft",
		Description: "Update an existing draft post",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"draft_id":   map[string]any{"type": "integer", "description": "Draft ID to update"},
				"title":      map[string]any{"type": "string", "description": "New title"},
				"subtitle":   map[string]any{"type": "string", "description": "New subtitle"},
				"body":       map[string]any{"type": "string", "description": "New body (HTML)"},
				"section_id": map[string]any{"type": "integer", "description": "Section ID"},
				"audience":   map[string]any{"type": "string", "description": "Audience level"},
			},
			Required: []string{"draft_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "substack_update_draft", s.handleUpdateDraft))

	server.AddTool(mcp.Tool{
		Name:        "substack_publish",
		Description: "Publish a draft post. Runs prepublish validation then publishes.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"draft_id":   map[string]any{"type": "integer", "description": "Draft ID to publish"},
				"send_email": map[string]any{"type": "boolean", "description": "Send email to subscribers (default: true)"},
				"audience":   map[string]any{"type": "string", "description": "Audience: everyone, only_paid, founding, only_free (default: everyone)"},
			},
			Required: []string{"draft_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "substack_publish", s.handlePublish))

	server.AddTool(mcp.Tool{
		Name:        "substack_list_posts",
		Description: "List published posts from the archive",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"offset": map[string]any{"type": "integer", "description": "Pagination offset (default: 0)"},
				"limit":  map[string]any{"type": "integer", "description": "Number of posts to return (default: 25)"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "substack_list_posts", s.handleListPosts))

	server.AddTool(mcp.Tool{
		Name:        "substack_get_post",
		Description: "Get a single post by slug or ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"slug": map[string]any{"type": "string", "description": "Post slug (URL path)"},
				"id":   map[string]any{"type": "integer", "description": "Post ID"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "substack_get_post", s.handleGetPost))

	return server.Run(ctx)
}

// resolveUserID fetches the authenticated user's ID from a published post.
func (s *substackServer) resolveUserID(ctx context.Context) error {
	data, err := s.doRequest(ctx, "GET", "/archive?limit=1&sort=new", nil)
	if err != nil {
		return fmt.Errorf("fetch archive: %w", err)
	}

	var posts []struct {
		PublishedBylines []struct {
			ID int `json:"id"`
		} `json:"publishedBylines"`
	}
	if err := json.Unmarshal(data, &posts); err != nil {
		return fmt.Errorf("parse archive: %w", err)
	}
	if len(posts) == 0 || len(posts[0].PublishedBylines) == 0 {
		return fmt.Errorf("no published posts found (publish at least one post first)")
	}

	s.userID = posts[0].PublishedBylines[0].ID
	return nil
}

// doRequest performs an HTTP request against the Substack API and returns the raw response body.
func (s *substackServer) doRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	if s.configErr != nil {
		return nil, s.configErr
	}

	reqURL := s.baseURL + path

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}

// doJSON performs an HTTP request and returns the parsed JSON response.
func (s *substackServer) doJSON(ctx context.Context, method, path string, body any) (any, error) {
	data, err := s.doRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}

	var result any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return result, nil
}

// --- Tool handlers ---

func (s *substackServer) handleListDrafts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	offset := v.Int("offset", 0)
	limit := v.Int("limit", 25)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/post_management/drafts?offset=%d&limit=%d&order_by=draft_updated_at&order_direction=desc", offset, limit)
	result, err := s.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"drafts": result})
}

func (s *substackServer) handleCreateDraft(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	title := v.Required("title")
	body := v.Required("body")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	draft := map[string]any{
		"draft_title":   title,
		"draft_body":    body,
		"draft_bylines": []map[string]any{{"id": s.userID, "is_guest": false}},
		"audience":      v.String("audience", "everyone"),
		"type":          "newsletter",
	}
	if subtitle := v.String("subtitle", ""); subtitle != "" {
		draft["draft_subtitle"] = subtitle
	}
	if sectionID := v.Int("section_id", 0); sectionID > 0 {
		draft["section_id"] = sectionID
	}

	result, err := s.doJSON(ctx, "POST", "/drafts", draft)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"draft": result})
}

func (s *substackServer) handleUpdateDraft(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	draftID := v.RequiredInt("draft_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	update := map[string]any{}
	if title := v.String("title", ""); title != "" {
		update["draft_title"] = title
	}
	if subtitle := v.String("subtitle", ""); subtitle != "" {
		update["draft_subtitle"] = subtitle
	}
	if body := v.String("body", ""); body != "" {
		update["draft_body"] = body
	}
	if sectionID := v.Int("section_id", 0); sectionID > 0 {
		update["section_id"] = sectionID
	}
	if audience := v.String("audience", ""); audience != "" {
		update["audience"] = audience
	}

	path := fmt.Sprintf("/drafts/%d", draftID)
	result, err := s.doJSON(ctx, "PUT", path, update)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"draft": result})
}

func (s *substackServer) handlePublish(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	draftID := v.RequiredInt("draft_id")
	sendEmail := v.Bool("send_email", true)
	audience := v.String("audience", "everyone")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Step 1: prepublish validation
	prepubPath := fmt.Sprintf("/drafts/%d/prepublish", draftID)
	if _, err := s.doJSON(ctx, "POST", prepubPath, map[string]any{}); err != nil {
		return mcp.ErrorResult(fmt.Errorf("prepublish failed: %w", err)), nil
	}

	// Step 2: publish
	pubPath := fmt.Sprintf("/drafts/%d/publish", draftID)
	result, err := s.doJSON(ctx, "POST", pubPath, map[string]any{
		"send_email": sendEmail,
		"audience":   audience,
	})
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("publish failed (prepublish succeeded): %w", err)), nil
	}

	return mcp.JSONResult(map[string]any{"post": result})
}

func (s *substackServer) handleListPosts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	offset := v.Int("offset", 0)
	limit := v.Int("limit", 25)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/archive?offset=%d&limit=%d&sort=new", offset, limit)
	result, err := s.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"posts": result})
}

func (s *substackServer) handleGetPost(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	slug := v.String("slug", "")
	id := v.Int("id", 0)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var path string
	switch {
	case slug != "":
		path = "/posts/" + slug
	case id > 0:
		path = fmt.Sprintf("/posts/by-id/%d", id)
	default:
		return mcp.ErrorResult(fmt.Errorf("either slug or id is required")), nil
	}

	result, err := s.doJSON(ctx, "GET", path, nil)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{"post": result})
}
