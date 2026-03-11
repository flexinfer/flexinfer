package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace"
	calendar "google.golang.org/api/calendar/v3"
	docsapi "google.golang.org/api/docs/v1"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/people/v1"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/googleworkspace"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/secrets"
	"github.com/crb2nu/loom/pkg/strutil"
	"github.com/crb2nu/loom/pkg/validate"
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

func (s *googleWorkspaceServer) handleAuthStatus(ctx context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	creds, err := googleworkspace.LoadRuntimeCredentials(s.secrets)
	if err != nil {
		return mcp.JSONResult(map[string]any{
			"configured": false,
			"error":      err.Error(),
			"hint":       "Run `loom auth google login` to populate the Loom secret store.",
		})
	}

	result := map[string]any{
		"configured":        true,
		"account_email":     creds.AccountEmail,
		"scopes":            creds.Scopes,
		"refresh_token_set": creds.RefreshToken != "",
		"client_id_present": creds.ClientID != "",
		"client_secret_set": creds.ClientSecret != "",
	}

	token, tokenErr := creds.AccessToken(ctx, s.httpClient.HTTP())
	if tokenErr == nil && token != nil {
		result["access_token_expiry"] = token.Expiry.Format(time.RFC3339)
		result["token_refresh_ok"] = true
	} else if tokenErr != nil {
		result["token_refresh_ok"] = false
		result["token_refresh_error"] = tokenErr.Error()
	}

	if info, infoErr := googleworkspace.FetchUserInfo(ctx, s.httpClient.HTTP(), creds); infoErr == nil {
		result["userinfo"] = info
		if creds.AccountEmail == "" {
			result["account_email"] = info.Email
		}
	} else {
		result["userinfo_error"] = infoErr.Error()
	}

	return mcp.JSONResult(result)
}

func (s *googleWorkspaceServer) handleGmailListMessages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.String("query", "")
	maxResults := validate.NormalizePerPage(v.Int("max_results", 20), 20, 50)
	pageToken := v.String("page_token", "")
	labelIDs := v.StringSlice("label_ids")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, creds, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	call := clients.gmail.Users.Messages.List("me").MaxResults(int64(maxResults))
	if query != "" {
		call = call.Q(query)
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	if len(labelIDs) > 0 {
		call = call.LabelIds(labelIDs...)
	}
	list, err := call.Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Gmail", err)), nil
	}

	items := make([]map[string]any, 0, len(list.Messages))
	for _, item := range list.Messages {
		msg, getErr := clients.gmail.Users.Messages.Get("me", item.Id).
			Format("metadata").
			MetadataHeaders("From", "To", "Subject", "Date").
			Do()
		if getErr != nil {
			items = append(items, map[string]any{
				"id":    item.Id,
				"error": s.wrapGoogleError("Gmail", getErr).Error(),
			})
			continue
		}
		items = append(items, simplifyGmailMessage(msg, false))
	}

	return mcp.JSONResult(map[string]any{
		"account_email":        creds.AccountEmail,
		"messages":             items,
		"next_page_token":      list.NextPageToken,
		"result_size_estimate": list.ResultSizeEstimate,
	})
}

func (s *googleWorkspaceServer) handleGmailGetMessage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	messageID := v.Required("message_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	msg, err := clients.gmail.Users.Messages.Get("me", messageID).Format("full").Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Gmail", err)), nil
	}
	return mcp.JSONResult(simplifyGmailMessage(msg, true))
}

func (s *googleWorkspaceServer) handleGmailSendMessage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	to := v.RequiredStringSlice("to")
	cc := v.StringSlice("cc")
	bcc := v.StringSlice("bcc")
	subject := v.String("subject", "")
	bodyText := v.String("body_text", "")
	bodyHTML := v.String("body_html", "")
	threadID := v.String("thread_id", "")
	replyTo := v.String("reply_to", "")
	if bodyText == "" && bodyHTML == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("body_text or body_html")), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	raw, err := buildRFC822Message(to, cc, bcc, subject, bodyText, bodyHTML, replyTo)
	if err != nil {
		return mcp.ErrorResult(mcperror.InvalidInput(err.Error())), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	resp, err := clients.gmail.Users.Messages.Send("me", &gmail.Message{
		Raw:      raw,
		ThreadId: threadID,
	}).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Gmail", err)), nil
	}
	return mcp.JSONResult(map[string]any{
		"id":        resp.Id,
		"thread_id": resp.ThreadId,
		"label_ids": resp.LabelIds,
	})
}

func (s *googleWorkspaceServer) handleCalendarListCalendars(ctx context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	resp, err := clients.calendar.CalendarList.List().MinAccessRole("reader").Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Calendar", err)), nil
	}
	items := make([]map[string]any, 0, len(resp.Items))
	for _, item := range resp.Items {
		items = append(items, map[string]any{
			"id":          item.Id,
			"summary":     item.Summary,
			"description": item.Description,
			"time_zone":   item.TimeZone,
			"primary":     item.Primary,
			"access_role": item.AccessRole,
		})
	}
	return mcp.JSONResult(map[string]any{"calendars": items})
}

func (s *googleWorkspaceServer) handleCalendarListEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	calendarID := v.String("calendar_id", "primary")
	timeMin := v.String("time_min", "")
	timeMax := v.String("time_max", "")
	query := v.String("query", "")
	maxResults := validate.NormalizePerPage(v.Int("max_results", 20), 20, 50)
	pageToken := v.String("page_token", "")
	if err := validateOptionalRFC3339("time_min", timeMin); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := validateOptionalRFC3339("time_max", timeMax); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	call := clients.calendar.Events.List(calendarID).
		MaxResults(int64(maxResults)).
		SingleEvents(true)
	if query != "" {
		call = call.Q(query)
	}
	if timeMin != "" {
		call = call.TimeMin(timeMin)
	}
	if timeMax != "" {
		call = call.TimeMax(timeMax)
	}
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	if query == "" {
		call = call.OrderBy("startTime")
	}
	resp, err := call.Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Calendar", err)), nil
	}
	events := make([]map[string]any, 0, len(resp.Items))
	for _, item := range resp.Items {
		events = append(events, simplifyCalendarEvent(item))
	}
	return mcp.JSONResult(map[string]any{
		"calendar_id":     calendarID,
		"events":          events,
		"next_page_token": resp.NextPageToken,
	})
}

func (s *googleWorkspaceServer) handleCalendarCreateEvent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	calendarID := v.String("calendar_id", "primary")
	summary := v.Required("summary")
	description := v.String("description", "")
	location := v.String("location", "")
	start := v.Required("start")
	end := v.Required("end")
	timezone := v.String("timezone", "")
	attendees := v.StringSlice("attendees")
	if err := validateOptionalRFC3339("start", start); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := validateOptionalRFC3339("end", end); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	event := &calendar.Event{
		Summary:     summary,
		Description: description,
		Location:    location,
		Start:       &calendar.EventDateTime{DateTime: start, TimeZone: timezone},
		End:         &calendar.EventDateTime{DateTime: end, TimeZone: timezone},
	}
	if len(attendees) > 0 {
		event.Attendees = make([]*calendar.EventAttendee, 0, len(attendees))
		for _, attendee := range attendees {
			event.Attendees = append(event.Attendees, &calendar.EventAttendee{Email: attendee})
		}
	}

	created, err := clients.calendar.Events.Insert(calendarID, event).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Calendar", err)), nil
	}
	return mcp.JSONResult(simplifyCalendarEvent(created))
}

func (s *googleWorkspaceServer) handleCalendarGetEvent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	calendarID := v.String("calendar_id", "primary")
	eventID := v.Required("event_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	event, err := clients.calendar.Events.Get(calendarID, eventID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Calendar", err)), nil
	}
	return mcp.JSONResult(simplifyCalendarEvent(event))
}

func (s *googleWorkspaceServer) handleCalendarUpdateEvent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	calendarID := v.String("calendar_id", "primary")
	eventID := v.Required("event_id")
	summary, hasSummary := optionalStringArg(args, "summary")
	description, hasDescription := optionalStringArg(args, "description")
	location, hasLocation := optionalStringArg(args, "location")
	start, hasStart := optionalStringArg(args, "start")
	end, hasEnd := optionalStringArg(args, "end")
	timezone, hasTimezone := optionalStringArg(args, "timezone")
	attendees, hasAttendees := optionalStringSliceArg(args, "attendees")

	if hasStart {
		if err := validateOptionalRFC3339("start", start); err != nil {
			return mcp.ErrorResult(err), nil
		}
	}
	if hasEnd {
		if err := validateOptionalRFC3339("end", end); err != nil {
			return mcp.ErrorResult(err), nil
		}
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if !hasSummary && !hasDescription && !hasLocation && !hasStart && !hasEnd && !hasTimezone && !hasAttendees {
		return mcp.ErrorResult(mcperror.RequiredParam("at least one update field")), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	event, err := clients.calendar.Events.Get(calendarID, eventID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Calendar", err)), nil
	}

	if hasSummary {
		event.Summary = summary
	}
	if hasDescription {
		event.Description = description
	}
	if hasLocation {
		event.Location = location
	}
	if hasStart {
		if event.Start == nil {
			event.Start = &calendar.EventDateTime{}
		}
		event.Start.DateTime = start
		event.Start.Date = ""
	}
	if hasEnd {
		if event.End == nil {
			event.End = &calendar.EventDateTime{}
		}
		event.End.DateTime = end
		event.End.Date = ""
	}
	if hasTimezone {
		if event.Start != nil {
			event.Start.TimeZone = timezone
		}
		if event.End != nil {
			event.End.TimeZone = timezone
		}
	}
	if hasAttendees {
		event.Attendees = make([]*calendar.EventAttendee, 0, len(attendees))
		for _, attendee := range attendees {
			event.Attendees = append(event.Attendees, &calendar.EventAttendee{Email: attendee})
		}
	}

	updated, err := clients.calendar.Events.Update(calendarID, eventID, event).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Calendar", err)), nil
	}
	return mcp.JSONResult(simplifyCalendarEvent(updated))
}

func (s *googleWorkspaceServer) handleCalendarDeleteEvent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	calendarID := v.String("calendar_id", "primary")
	eventID := v.Required("event_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := clients.calendar.Events.Delete(calendarID, eventID).Do(); err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Calendar", err)), nil
	}
	return mcp.JSONResult(map[string]any{
		"deleted":     true,
		"calendar_id": calendarID,
		"event_id":    eventID,
	})
}

func (s *googleWorkspaceServer) handleDocsGetDocument(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	documentID := v.Required("document_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	doc, err := clients.docs.Documents.Get(documentID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	return mcp.JSONResult(simplifyDocument(doc))
}

func (s *googleWorkspaceServer) handleDocsCreateDocument(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	title := v.Required("title")
	initialText := v.String("initial_text", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	doc, err := clients.docs.Documents.Create(&docsapi.Document{Title: title}).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	if initialText != "" {
		if _, err := clients.docs.Documents.BatchUpdate(doc.DocumentId, &docsapi.BatchUpdateDocumentRequest{
			Requests: []*docsapi.Request{
				{
					InsertText: &docsapi.InsertTextRequest{
						Location: &docsapi.Location{Index: 1},
						Text:     initialText,
					},
				},
			},
		}).Do(); err != nil {
			return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
		}
		doc, err = clients.docs.Documents.Get(doc.DocumentId).Do()
		if err != nil {
			return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
		}
	}
	return mcp.JSONResult(simplifyDocument(doc))
}

func (s *googleWorkspaceServer) handleDocsAppendText(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	documentID := v.Required("document_id")
	text := v.Required("text")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	doc, err := clients.docs.Documents.Get(documentID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	index := documentEndIndex(doc)
	if index < 1 {
		index = 1
	}
	if _, err := clients.docs.Documents.BatchUpdate(documentID, &docsapi.BatchUpdateDocumentRequest{
		Requests: []*docsapi.Request{
			{
				InsertText: &docsapi.InsertTextRequest{
					Location: &docsapi.Location{Index: index},
					Text:     text,
				},
			},
		},
	}).Do(); err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	doc, err = clients.docs.Documents.Get(documentID).Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Docs", err)), nil
	}
	return mcp.JSONResult(simplifyDocument(doc))
}

func (s *googleWorkspaceServer) handleDriveSearchFiles(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	query := v.String("query", "")
	mimeType := v.String("mime_type", "")
	maxResults := validate.NormalizePerPage(v.Int("max_results", 20), 20, 50)
	pageToken := v.String("page_token", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	driveQuery := strings.TrimSpace(query)
	if mimeType != "" {
		filter := fmt.Sprintf("mimeType = '%s'", strings.ReplaceAll(mimeType, "'", "\\'"))
		if driveQuery == "" {
			driveQuery = filter
		} else {
			driveQuery = fmt.Sprintf("(%s) and %s", driveQuery, filter)
		}
	}
	if driveQuery == "" {
		driveQuery = "trashed = false"
	} else {
		driveQuery = fmt.Sprintf("(%s) and trashed = false", driveQuery)
	}

	call := clients.drive.Files.List().
		Q(driveQuery).
		PageSize(int64(maxResults)).
		Fields("nextPageToken, files(id,name,mimeType,modifiedTime,webViewLink,owners(emailAddress,displayName),driveId,parents,size)").
		OrderBy("modifiedTime desc")
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}

	resp, err := call.Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Drive", err)), nil
	}
	files := make([]map[string]any, 0, len(resp.Files))
	for _, file := range resp.Files {
		files = append(files, simplifyDriveFile(file))
	}
	return mcp.JSONResult(map[string]any{
		"files":           files,
		"next_page_token": resp.NextPageToken,
		"query":           driveQuery,
	})
}

func (s *googleWorkspaceServer) handleDriveGetFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	fileID := v.Required("file_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	clients, _, err := s.newClients(ctx)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	file, err := clients.drive.Files.Get(fileID).
		Fields("id,name,mimeType,modifiedTime,webViewLink,owners(emailAddress,displayName),driveId,parents,size").
		Do()
	if err != nil {
		return mcp.ErrorResult(s.wrapGoogleError("Drive", err)), nil
	}
	return mcp.JSONResult(simplifyDriveFile(file))
}

func (s *googleWorkspaceServer) newClients(ctx context.Context) (*googleClients, *googleworkspace.Credentials, error) {
	creds, err := googleworkspace.LoadRuntimeCredentials(s.secrets)
	if err != nil {
		return nil, nil, mcperror.NotConfigured(
			googleworkspace.SecretRefreshToken,
			"run `loom auth google login` (and ensure client credentials are stored in Loom secrets)",
		)
	}
	authHTTPClient, err := creds.NewHTTPClient(ctx, s.httpClient.HTTP())
	if err != nil {
		return nil, nil, s.wrapGoogleError("Google OAuth", err)
	}
	gmailSvc, err := gmail.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Gmail", err)
	}
	calendarSvc, err := calendar.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Calendar", err)
	}
	docsSvc, err := docsapi.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Docs", err)
	}
	driveSvc, err := drive.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		return nil, nil, s.wrapGoogleError("Drive", err)
	}
	peopleSvc, err := people.NewService(ctx, option.WithHTTPClient(authHTTPClient))
	if err != nil {
		// People API is not required for the current toolset.
		peopleSvc = nil
	}
	return &googleClients{
		http:     authHTTPClient,
		gmail:    gmailSvc,
		calendar: calendarSvc,
		docs:     docsSvc,
		drive:    driveSvc,
		people:   peopleSvc,
	}, creds, nil
}

func (s *googleWorkspaceServer) wrapGoogleError(service string, err error) error {
	if err == nil {
		return nil
	}
	if gErr, ok := err.(*googleapi.Error); ok {
		return mcperror.APIError(service, gErr.Code, gErr.Message)
	}
	if strings.Contains(strings.ToLower(err.Error()), "deadline") || strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return mcperror.Timeout(service)
	}
	return mcperror.WrapAPI(service, err)
}

func simplifyGmailMessage(msg *gmail.Message, includeBody bool) map[string]any {
	headers := gmailHeaders(msg.Payload)
	result := map[string]any{
		"id":            msg.Id,
		"thread_id":     msg.ThreadId,
		"label_ids":     msg.LabelIds,
		"snippet":       msg.Snippet,
		"history_id":    msg.HistoryId,
		"subject":       headers["Subject"],
		"from":          headers["From"],
		"to":            headers["To"],
		"date":          headers["Date"],
		"internal_date": gmailInternalDate(msg.InternalDate),
	}
	if includeBody {
		result["body_text"] = strutil.Truncate(extractMessageBody(msg.Payload), 8000)
		result["headers"] = headers
	}
	return result
}

func gmailHeaders(part *gmail.MessagePart) map[string]string {
	headers := make(map[string]string)
	if part == nil {
		return headers
	}
	for _, header := range part.Headers {
		headers[header.Name] = header.Value
	}
	return headers
}

func gmailInternalDate(raw int64) string {
	if raw <= 0 {
		return ""
	}
	return time.UnixMilli(raw).Format(time.RFC3339)
}

func extractMessageBody(part *gmail.MessagePart) string {
	if part == nil {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(part.MimeType), "text/plain") && part.Body != nil && part.Body.Data != "" {
		if body := decodeBase64URL(part.Body.Data); body != "" {
			return body
		}
	}
	for _, child := range part.Parts {
		if body := extractMessageBody(child); body != "" {
			return body
		}
	}
	if part.Body != nil && part.Body.Data != "" {
		return decodeBase64URL(part.Body.Data)
	}
	return ""
}

func decodeBase64URL(value string) string {
	decoded, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(decoded))
}

func buildRFC822Message(to, cc, bcc []string, subject, bodyText, bodyHTML, replyTo string) (string, error) {
	if len(to) == 0 {
		return "", fmt.Errorf("at least one recipient is required")
	}
	if bodyText == "" && bodyHTML == "" {
		return "", fmt.Errorf("body_text or body_html is required")
	}

	var buf bytes.Buffer
	writeHeader := func(name, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		buf.WriteString(name)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}

	writeHeader("To", strings.Join(to, ", "))
	writeHeader("Cc", strings.Join(cc, ", "))
	writeHeader("Bcc", strings.Join(bcc, ", "))
	writeHeader("Subject", subject)
	writeHeader("Reply-To", replyTo)
	writeHeader("MIME-Version", "1.0")
	if bodyHTML != "" {
		writeHeader("Content-Type", `text/html; charset="UTF-8"`)
		buf.WriteString("\r\n")
		buf.WriteString(bodyHTML)
	} else {
		writeHeader("Content-Type", `text/plain; charset="UTF-8"`)
		buf.WriteString("\r\n")
		buf.WriteString(bodyText)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(buf.Bytes()), nil
}

func simplifyCalendarEvent(event *calendar.Event) map[string]any {
	attendees := make([]string, 0, len(event.Attendees))
	for _, attendee := range event.Attendees {
		attendees = append(attendees, attendee.Email)
	}
	return map[string]any{
		"id":          event.Id,
		"status":      event.Status,
		"summary":     event.Summary,
		"description": event.Description,
		"location":    event.Location,
		"html_link":   event.HtmlLink,
		"start":       eventDateTimeValue(event.Start),
		"end":         eventDateTimeValue(event.End),
		"attendees":   attendees,
	}
}

func eventDateTimeValue(v *calendar.EventDateTime) string {
	if v == nil {
		return ""
	}
	if v.DateTime != "" {
		return v.DateTime
	}
	return v.Date
}

func simplifyDocument(doc *docsapi.Document) map[string]any {
	return map[string]any{
		"document_id": doc.DocumentId,
		"title":       doc.Title,
		"revision_id": doc.RevisionId,
		"url":         "https://docs.google.com/document/d/" + doc.DocumentId + "/edit",
		"text":        strutil.Truncate(documentText(doc), 12000),
	}
}

func documentText(doc *docsapi.Document) string {
	if doc == nil || doc.Body == nil {
		return ""
	}
	var parts []string
	for _, content := range doc.Body.Content {
		if content.Paragraph == nil {
			continue
		}
		for _, elem := range content.Paragraph.Elements {
			if elem.TextRun == nil {
				continue
			}
			parts = append(parts, elem.TextRun.Content)
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func documentEndIndex(doc *docsapi.Document) int64 {
	if doc == nil || doc.Body == nil || len(doc.Body.Content) == 0 {
		return 1
	}
	last := doc.Body.Content[len(doc.Body.Content)-1]
	if last.EndIndex <= 1 {
		return 1
	}
	return last.EndIndex - 1
}

func simplifyDriveFile(file *drive.File) map[string]any {
	owners := make([]map[string]any, 0, len(file.Owners))
	for _, owner := range file.Owners {
		owners = append(owners, map[string]any{
			"email": owner.EmailAddress,
			"name":  owner.DisplayName,
		})
	}
	return map[string]any{
		"id":            file.Id,
		"name":          file.Name,
		"mime_type":     file.MimeType,
		"modified_time": file.ModifiedTime,
		"web_view_link": file.WebViewLink,
		"parents":       file.Parents,
		"size":          file.Size,
		"owners":        owners,
	}
}

func validateOptionalRFC3339(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return mcperror.InvalidParam(field, "must be RFC3339")
	}
	return nil
}

func optionalStringArg(args map[string]any, key string) (string, bool) {
	value, ok := args[key]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprint(value)), true
}

func optionalStringSliceArg(args map[string]any, key string) ([]string, bool) {
	value, ok := args[key]
	if !ok {
		return nil, false
	}
	raw, ok := value.([]any)
	if !ok {
		typed, ok := value.([]string)
		if !ok {
			return []string{}, true
		}
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, strings.TrimSpace(item))
		}
		return result, true
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		result = append(result, strings.TrimSpace(fmt.Sprint(item)))
	}
	return result, true
}
