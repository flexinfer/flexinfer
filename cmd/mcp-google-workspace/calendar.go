package main

import (
	"context"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	calendar "google.golang.org/api/calendar/v3"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

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
	attendeesSlice, hasAttendees := optionalStringSliceArg(args, "attendees")

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
		event.Attendees = make([]*calendar.EventAttendee, 0, len(attendeesSlice))
		for _, attendee := range attendeesSlice {
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
