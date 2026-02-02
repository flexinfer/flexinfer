// mcp-time is a blazing fast time MCP server written in Go.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-time", "version", version)

	server := mcp.NewServer("mcp-time", version)
	server.SetInstructions("Fast Go-native time server. Tools: get_current_time, convert_timezone, add_duration, list_timezones, wait")

	// get_current_time - Get current time in a timezone
	server.AddTool(mcp.Tool{
		Name:        "get_current_time",
		Description: "Get the current time in a specified timezone",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"timezone": map[string]any{
					"type":        "string",
					"description": "IANA timezone name (e.g., America/New_York, Europe/London, Asia/Tokyo). Defaults to UTC.",
				},
			},
		},
	}, handleGetCurrentTime)

	// convert_timezone - Convert time between timezones
	server.AddTool(mcp.Tool{
		Name:        "convert_timezone",
		Description: "Convert a time from one timezone to another",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"time": map[string]any{
					"type":        "string",
					"description": "Time to convert in RFC3339 format (e.g., 2024-01-15T14:30:00Z)",
				},
				"from_timezone": map[string]any{
					"type":        "string",
					"description": "Source IANA timezone name",
				},
				"to_timezone": map[string]any{
					"type":        "string",
					"description": "Target IANA timezone name",
				},
			},
			Required: []string{"time", "to_timezone"},
		},
	}, handleConvertTimezone)

	// add_duration - Add or subtract duration from a time
	server.AddTool(mcp.Tool{
		Name:        "add_duration",
		Description: "Add or subtract a duration from a time",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"time": map[string]any{
					"type":        "string",
					"description": "Base time in RFC3339 format. Defaults to current time.",
				},
				"duration": map[string]any{
					"type":        "string",
					"description": "Duration to add (e.g., '2h30m', '-1h', '24h', '7d'). Use negative for subtraction.",
				},
				"timezone": map[string]any{
					"type":        "string",
					"description": "Timezone for result. Defaults to UTC.",
				},
			},
			Required: []string{"duration"},
		},
	}, handleAddDuration)

	// list_timezones - List common timezones
	server.AddTool(mcp.Tool{
		Name:        "list_timezones",
		Description: "List common IANA timezone names",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListTimezones)

	// wait - Sleep for a duration
	server.AddTool(mcp.Tool{
		Name:        "wait",
		Description: "Wait (sleep) for a duration",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"duration": map[string]any{
					"type":        "string",
					"description": "How long to wait (e.g., '250ms', '10s', '2m', '1h', '7d').",
				},
			},
			Required: []string{"duration"},
		},
	}, handleWait)

	return server.Run(ctx)
}

func parseDurationWithDays(durationStr string) (time.Duration, error) {
	if durationStr == "" {
		return 0, fmt.Errorf("duration is required")
	}
	if strings.HasSuffix(durationStr, "d") {
		raw := strings.TrimSuffix(durationStr, "d")
		days, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid day duration %q: expected integer days like '7d'", durationStr)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(durationStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", durationStr, err)
	}
	return d, nil
}

func handleGetCurrentTime(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	tzName := v.String("timezone", "UTC")

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid timezone %q: %w", tzName, err)), nil
	}

	now := time.Now().In(loc)
	result := map[string]any{
		"time":     now.Format(time.RFC3339),
		"timezone": tzName,
		"unix":     now.Unix(),
		"weekday":  now.Weekday().String(),
		"iso_week": func() int { _, w := now.ISOWeek(); return w }(),
	}

	return mcp.JSONResult(result)
}

func handleConvertTimezone(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	timeStr := v.Required("time")
	toTZ := v.Required("to_timezone")
	fromTZ := v.String("from_timezone", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse time
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		// Try other formats
		t, err = time.Parse("2006-01-02 15:04:05", timeStr)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("cannot parse time %q: use RFC3339 format", timeStr)), nil
		}
	}

	// Apply source timezone if specified
	if fromTZ != "" {
		loc, err := time.LoadLocation(fromTZ)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("invalid from_timezone %q: %w", fromTZ, err)), nil
		}
		t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
	}

	// Convert to target timezone
	toLoc, err := time.LoadLocation(toTZ)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid to_timezone %q: %w", toTZ, err)), nil
	}

	converted := t.In(toLoc)
	result := map[string]any{
		"original":  t.Format(time.RFC3339),
		"converted": converted.Format(time.RFC3339),
		"timezone":  toTZ,
	}

	return mcp.JSONResult(result)
}

func handleAddDuration(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	durationStr := v.Required("duration")
	timeStr := v.String("time", "")
	tzName := v.String("timezone", "UTC")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	d, err := parseDurationWithDays(durationStr)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse base time or use now
	var t time.Time
	if timeStr != "" {
		t, err = time.Parse(time.RFC3339, timeStr)
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("cannot parse time %q: use RFC3339 format", timeStr)), nil
		}
	} else {
		t = time.Now()
	}

	// Apply timezone
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid timezone %q: %w", tzName, err)), nil
	}

	result := t.Add(d).In(loc)
	output := map[string]any{
		"original": t.Format(time.RFC3339),
		"duration": durationStr,
		"result":   result.Format(time.RFC3339),
		"timezone": tzName,
	}

	return mcp.JSONResult(output)
}

func handleListTimezones(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	timezones := []string{
		// Americas
		"America/New_York",
		"America/Chicago",
		"America/Denver",
		"America/Los_Angeles",
		"America/Toronto",
		"America/Vancouver",
		"America/Sao_Paulo",
		// Europe
		"Europe/London",
		"Europe/Paris",
		"Europe/Berlin",
		"Europe/Amsterdam",
		"Europe/Moscow",
		// Asia
		"Asia/Tokyo",
		"Asia/Shanghai",
		"Asia/Hong_Kong",
		"Asia/Singapore",
		"Asia/Seoul",
		"Asia/Mumbai",
		"Asia/Dubai",
		// Pacific
		"Pacific/Auckland",
		"Pacific/Sydney",
		"Australia/Melbourne",
		// UTC
		"UTC",
	}

	return mcp.JSONResult(map[string]any{
		"timezones": timezones,
		"note":      "Use IANA timezone names. See https://en.wikipedia.org/wiki/List_of_tz_database_time_zones",
	})
}

func handleWait(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	durationStr := v.Required("duration")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	d, err := parseDurationWithDays(durationStr)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	if d < 0 {
		return mcp.ErrorResult(fmt.Errorf("duration must be non-negative")), nil
	}

	start := time.Now()
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline) - 100*time.Millisecond
		if d > remaining {
			return mcp.ErrorResult(fmt.Errorf("requested duration %s exceeds time remaining %s; increase the server timeout or use a shorter wait", d, remaining)), nil
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}

	end := time.Now()
	return mcp.JSONResult(map[string]any{
		"requested_duration": durationStr,
		"waited_duration_ms": end.Sub(start).Milliseconds(),
		"started_at":         start.UTC().Format(time.RFC3339Nano),
		"ended_at":           end.UTC().Format(time.RFC3339Nano),
	})
}
