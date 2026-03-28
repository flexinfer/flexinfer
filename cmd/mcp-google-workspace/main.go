package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace"
	calendar "google.golang.org/api/calendar/v3"
	docsapi "google.golang.org/api/docs/v1"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/people/v1"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/secrets"
)

var version = "0.1.0"

type googleWorkspaceServer struct {
	logger     *slog.Logger
	httpClient *httpclient.Client
	secrets    *secrets.Manager
	timeout    time.Duration
}

type googleClients struct {
	http     *http.Client
	gmail    *gmail.Service
	calendar *calendar.Service
	docs     *docsapi.Service
	drive    *drive.Service
	people   *people.Service
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-google-workspace", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-google-workspace")

	var secretMgr *secrets.Manager
	if mgr, err := secrets.DefaultManager(); err != nil {
		logger.Warn("unable to initialize secret manager", "error", err)
	} else {
		secretMgr = mgr
	}

	srv := &googleWorkspaceServer{
		logger:     logger,
		httpClient: httpclient.NewDefault(),
		secrets:    secretMgr,
		timeout:    time.Duration(env.Int("GOOGLE_WORKSPACE_TIMEOUT_SECONDS", 30)) * time.Second,
	}

	server := mcp.NewServer("mcp-google-workspace", version)
	server.SetInstructions("Google Workspace MCP. Uses Loom-managed Google OAuth secrets (client ID, client secret, refresh token) configured via `loom auth google login`.")
	registerTools(server, srv, tracer)

	logger.Info("starting server", "name", "mcp-google-workspace", "version", version, "timeout", srv.timeout.String())
	return server.Run(ctx)
}

func registerTools(server *mcp.Server, srv *googleWorkspaceServer, tracer trace.Tracer) {
	wrap := func(name string, h mcp.ToolHandler) mcp.ToolHandler {
		return mcpotel.TracedToolHandler(tracer, name, h)
	}

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_auth_status",
		Description: "Show whether Google Workspace OAuth is configured and refreshable for this MCP server",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, wrap("google_workspace_auth_status", srv.handleAuthStatus))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_gmail_list_messages",
		Description: "List Gmail messages from the authenticated mailbox",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":       map[string]any{"type": "string", "description": "Optional Gmail search query"},
				"label_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional Gmail label IDs"},
				"max_results": map[string]any{"type": "integer", "description": "Maximum messages to return (default 20, max 50)"},
				"page_token":  map[string]any{"type": "string", "description": "Pagination token returned by a previous call"},
			},
		},
	}, wrap("google_workspace_gmail_list_messages", srv.handleGmailListMessages))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_gmail_get_message",
		Description: "Get a Gmail message with a simplified body/header view",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"message_id": map[string]any{"type": "string", "description": "Gmail message ID"},
			},
			Required: []string{"message_id"},
		},
	}, wrap("google_workspace_gmail_get_message", srv.handleGmailGetMessage))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_gmail_send_message",
		Description: "Send a Gmail message from the authenticated mailbox",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"to":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Required recipient email addresses"},
				"cc":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional CC recipients"},
				"bcc":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional BCC recipients"},
				"subject":   map[string]any{"type": "string", "description": "Message subject"},
				"body_text": map[string]any{"type": "string", "description": "Plain text body"},
				"body_html": map[string]any{"type": "string", "description": "Optional HTML body"},
				"thread_id": map[string]any{"type": "string", "description": "Optional existing Gmail thread ID"},
				"reply_to":  map[string]any{"type": "string", "description": "Optional Reply-To header"},
			},
			Required: []string{"to"},
		},
	}, wrap("google_workspace_gmail_send_message", srv.handleGmailSendMessage))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_calendar_list_calendars",
		Description: "List calendars available to the authenticated Google account",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, wrap("google_workspace_calendar_list_calendars", srv.handleCalendarListCalendars))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_calendar_list_events",
		Description: "List events from a Google Calendar",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"calendar_id": map[string]any{"type": "string", "description": "Calendar ID (default: primary)"},
				"time_min":    map[string]any{"type": "string", "description": "RFC3339 lower bound"},
				"time_max":    map[string]any{"type": "string", "description": "RFC3339 upper bound"},
				"query":       map[string]any{"type": "string", "description": "Free-text event query"},
				"max_results": map[string]any{"type": "integer", "description": "Maximum events to return (default 20, max 50)"},
				"page_token":  map[string]any{"type": "string", "description": "Pagination token returned by a previous call"},
			},
		},
	}, wrap("google_workspace_calendar_list_events", srv.handleCalendarListEvents))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_calendar_create_event",
		Description: "Create a Google Calendar event",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"calendar_id": map[string]any{"type": "string", "description": "Calendar ID (default: primary)"},
				"summary":     map[string]any{"type": "string", "description": "Event summary"},
				"description": map[string]any{"type": "string", "description": "Event description"},
				"location":    map[string]any{"type": "string", "description": "Event location"},
				"start":       map[string]any{"type": "string", "description": "RFC3339 event start"},
				"end":         map[string]any{"type": "string", "description": "RFC3339 event end"},
				"timezone":    map[string]any{"type": "string", "description": "Optional explicit timezone"},
				"attendees":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional attendee email addresses"},
			},
			Required: []string{"summary", "start", "end"},
		},
	}, wrap("google_workspace_calendar_create_event", srv.handleCalendarCreateEvent))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_calendar_get_event",
		Description: "Get a single event from a Google Calendar",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"calendar_id": map[string]any{"type": "string", "description": "Calendar ID (default: primary)"},
				"event_id":    map[string]any{"type": "string", "description": "Google Calendar event ID"},
			},
			Required: []string{"event_id"},
		},
	}, wrap("google_workspace_calendar_get_event", srv.handleCalendarGetEvent))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_calendar_update_event",
		Description: "Update an existing Google Calendar event",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"calendar_id": map[string]any{"type": "string", "description": "Calendar ID (default: primary)"},
				"event_id":    map[string]any{"type": "string", "description": "Google Calendar event ID"},
				"summary":     map[string]any{"type": "string", "description": "Event summary"},
				"description": map[string]any{"type": "string", "description": "Event description"},
				"location":    map[string]any{"type": "string", "description": "Event location"},
				"start":       map[string]any{"type": "string", "description": "RFC3339 event start"},
				"end":         map[string]any{"type": "string", "description": "RFC3339 event end"},
				"timezone":    map[string]any{"type": "string", "description": "Optional timezone for start/end when updating times"},
				"attendees":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional attendee email addresses"},
			},
			Required: []string{"event_id"},
		},
	}, wrap("google_workspace_calendar_update_event", srv.handleCalendarUpdateEvent))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_calendar_delete_event",
		Description: "Delete an event from a Google Calendar",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"calendar_id": map[string]any{"type": "string", "description": "Calendar ID (default: primary)"},
				"event_id":    map[string]any{"type": "string", "description": "Google Calendar event ID"},
			},
			Required: []string{"event_id"},
		},
	}, wrap("google_workspace_calendar_delete_event", srv.handleCalendarDeleteEvent))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_docs_get_document",
		Description: "Fetch a Google Docs document by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"document_id": map[string]any{"type": "string", "description": "Google Docs document ID"},
			},
			Required: []string{"document_id"},
		},
	}, wrap("google_workspace_docs_get_document", srv.handleDocsGetDocument))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_docs_create_document",
		Description: "Create a Google Docs document and optionally seed it with text",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"title":        map[string]any{"type": "string", "description": "Document title"},
				"initial_text": map[string]any{"type": "string", "description": "Optional initial document text"},
			},
			Required: []string{"title"},
		},
	}, wrap("google_workspace_docs_create_document", srv.handleDocsCreateDocument))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_docs_append_text",
		Description: "Append text to the end of a Google Docs document",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"document_id": map[string]any{"type": "string", "description": "Google Docs document ID"},
				"text":        map[string]any{"type": "string", "description": "Text to append"},
			},
			Required: []string{"document_id", "text"},
		},
	}, wrap("google_workspace_docs_append_text", srv.handleDocsAppendText))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_drive_search_files",
		Description: "Search Google Drive file metadata",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query":       map[string]any{"type": "string", "description": "Optional raw Drive query expression"},
				"mime_type":   map[string]any{"type": "string", "description": "Optional MIME type filter"},
				"max_results": map[string]any{"type": "integer", "description": "Maximum files to return (default 20, max 50)"},
				"page_token":  map[string]any{"type": "string", "description": "Pagination token returned by a previous call"},
			},
		},
	}, wrap("google_workspace_drive_search_files", srv.handleDriveSearchFiles))

	server.AddTool(mcp.Tool{
		Name:        "google_workspace_drive_get_file",
		Description: "Get Google Drive file metadata by ID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file_id": map[string]any{"type": "string", "description": "Google Drive file ID"},
			},
			Required: []string{"file_id"},
		},
	}, wrap("google_workspace_drive_get_file", srv.handleDriveGetFile))
}
