// mcp-time is a blazing fast time MCP server written in Go.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var version = "1.0.0"

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

	server := mcp.NewServer("mcp-time", version)
	server.SetInstructions("Fast Go-native time server. Tools: get_current_time, convert_timezone, add_duration")

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

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleGetCurrentTime(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	tzName := "UTC"
	if tz, ok := args["timezone"].(string); ok && tz != "" {
		tzName = tz
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tzName, err)
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
	timeStr, _ := args["time"].(string)
	fromTZ, _ := args["from_timezone"].(string)
	toTZ, _ := args["to_timezone"].(string)

	if timeStr == "" {
		return nil, fmt.Errorf("time is required")
	}
	if toTZ == "" {
		return nil, fmt.Errorf("to_timezone is required")
	}

	// Parse time
	t, err := time.Parse(time.RFC3339, timeStr)
	if err != nil {
		// Try other formats
		t, err = time.Parse("2006-01-02 15:04:05", timeStr)
		if err != nil {
			return nil, fmt.Errorf("cannot parse time %q: use RFC3339 format", timeStr)
		}
	}

	// Apply source timezone if specified
	if fromTZ != "" {
		loc, err := time.LoadLocation(fromTZ)
		if err != nil {
			return nil, fmt.Errorf("invalid from_timezone %q: %w", fromTZ, err)
		}
		t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
	}

	// Convert to target timezone
	toLoc, err := time.LoadLocation(toTZ)
	if err != nil {
		return nil, fmt.Errorf("invalid to_timezone %q: %w", toTZ, err)
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
	durationStr, _ := args["duration"].(string)
	timeStr, _ := args["time"].(string)
	tzName, _ := args["timezone"].(string)

	if durationStr == "" {
		return nil, fmt.Errorf("duration is required")
	}

	// Parse duration (support 'd' for days)
	var d time.Duration
	if len(durationStr) > 0 && durationStr[len(durationStr)-1] == 'd' {
		days := 0
		fmt.Sscanf(durationStr, "%dd", &days)
		d = time.Duration(days) * 24 * time.Hour
	} else {
		var err error
		d, err = time.ParseDuration(durationStr)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q: %w", durationStr, err)
		}
	}

	// Parse base time or use now
	var t time.Time
	if timeStr != "" {
		var err error
		t, err = time.Parse(time.RFC3339, timeStr)
		if err != nil {
			return nil, fmt.Errorf("cannot parse time %q: use RFC3339 format", timeStr)
		}
	} else {
		t = time.Now()
	}

	// Apply timezone
	if tzName == "" {
		tzName = "UTC"
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tzName, err)
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
