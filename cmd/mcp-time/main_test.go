package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseDurationWithDays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"7d", 7 * 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"0d", 0, false},
		{"2h", 2 * time.Hour, false},
		{"30m", 30 * time.Minute, false},
		{"500ms", 500 * time.Millisecond, false},
		{"1h30m", 90 * time.Minute, false},
		{"-1h", -1 * time.Hour, false},
		{"1.5d", 0, true},    // non-integer days
		{"", 0, true},        // empty
		{"invalid", 0, true}, // invalid format
		{"abc", 0, true},     // not parseable
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := parseDurationWithDays(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDurationWithDays(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && d != tt.want {
				t.Errorf("parseDurationWithDays(%q) = %v, want %v", tt.input, d, tt.want)
			}
		})
	}
}

func TestHandleWaitRespectsDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	result, err := handleWait(ctx, map[string]any{"duration": "200ms"})
	if err != nil {
		// Context cancellation returns a Go error
		return
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected deadline-related error result")
	}
}

func TestHandleWait_ShortDuration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result, err := handleWait(ctx, map[string]any{"duration": "10ms"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
}

func TestHandleWait_NegativeDuration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result, err := handleWait(ctx, map[string]any{"duration": "-1s"})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for negative duration")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "non-negative") {
		t.Errorf("error should mention non-negative, got: %v", result.Content)
	}
}

func TestHandleWait_MissingDuration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	result, err := handleWait(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected error result for missing duration")
	}
}

func TestHandleGetCurrentTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		timezone  string
		wantIsErr bool
	}{
		{"default UTC", "", false},
		{"explicit UTC", "UTC", false},
		{"New York", "America/New_York", false},
		{"Tokyo", "Asia/Tokyo", false},
		{"invalid timezone", "Invalid/Zone", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{}
			if tt.timezone != "" {
				args["timezone"] = tt.timezone
			}

			result, err := handleGetCurrentTime(context.Background(), args)
			if err != nil {
				t.Errorf("handleGetCurrentTime() unexpected Go error = %v", err)
				return
			}
			if result.IsError != tt.wantIsErr {
				t.Errorf("handleGetCurrentTime() IsError = %v, want %v", result.IsError, tt.wantIsErr)
				return
			}
			if !tt.wantIsErr && len(result.Content) == 0 {
				t.Error("expected content in result")
			}
		})
	}
}

func TestHandleGetCurrentTime_ResponseFields(t *testing.T) {
	t.Parallel()

	result, err := handleGetCurrentTime(context.Background(), map[string]any{"timezone": "UTC"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text := result.Content[0].Text

	// Check expected fields in response (YAML format)
	expectedFields := []string{"time:", "timezone:", "unix:", "weekday:", "iso_week:"}
	for _, field := range expectedFields {
		if !strings.Contains(text, field) {
			t.Errorf("response should contain %q, got: %s", field, text)
		}
	}
}

func TestHandleConvertTimezone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         map[string]any
		wantIsErr    bool
		errSubstring string
	}{
		{
			name: "valid conversion",
			args: map[string]any{
				"time":        "2024-01-15T14:30:00Z",
				"to_timezone": "America/New_York",
			},
			wantIsErr: false,
		},
		{
			name: "with from_timezone",
			args: map[string]any{
				"time":          "2024-01-15T14:30:00Z",
				"from_timezone": "UTC",
				"to_timezone":   "Asia/Tokyo",
			},
			wantIsErr: false,
		},
		{
			name: "alternative date format",
			args: map[string]any{
				"time":        "2024-01-15 14:30:00",
				"to_timezone": "UTC",
			},
			wantIsErr: false,
		},
		{
			name: "missing time",
			args: map[string]any{
				"to_timezone": "UTC",
			},
			wantIsErr:    true,
			errSubstring: "time",
		},
		{
			name: "missing to_timezone",
			args: map[string]any{
				"time": "2024-01-15T14:30:00Z",
			},
			wantIsErr:    true,
			errSubstring: "to_timezone",
		},
		{
			name: "invalid time format",
			args: map[string]any{
				"time":        "invalid",
				"to_timezone": "UTC",
			},
			wantIsErr:    true,
			errSubstring: "cannot parse time",
		},
		{
			name: "invalid from_timezone",
			args: map[string]any{
				"time":          "2024-01-15T14:30:00Z",
				"from_timezone": "Invalid/Zone",
				"to_timezone":   "UTC",
			},
			wantIsErr:    true,
			errSubstring: "invalid from_timezone",
		},
		{
			name: "invalid to_timezone",
			args: map[string]any{
				"time":        "2024-01-15T14:30:00Z",
				"to_timezone": "Invalid/Zone",
			},
			wantIsErr:    true,
			errSubstring: "invalid to_timezone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handleConvertTimezone(context.Background(), tt.args)
			if err != nil {
				t.Errorf("handleConvertTimezone() unexpected Go error = %v", err)
				return
			}
			if result.IsError != tt.wantIsErr {
				t.Errorf("handleConvertTimezone() IsError = %v, want %v", result.IsError, tt.wantIsErr)
				return
			}
			if tt.wantIsErr && tt.errSubstring != "" {
				if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, tt.errSubstring) {
					t.Errorf("error should contain %q, got: %v", tt.errSubstring, result.Content)
				}
			}
			if !tt.wantIsErr && result == nil {
				t.Error("expected non-nil result")
			}
		})
	}
}

func TestHandleAddDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         map[string]any
		wantIsErr    bool
		errSubstring string
	}{
		{
			name: "add to current time",
			args: map[string]any{
				"duration": "1h",
			},
			wantIsErr: false,
		},
		{
			name: "add to specific time",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "2h30m",
			},
			wantIsErr: false,
		},
		{
			name: "subtract duration",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "-1h",
			},
			wantIsErr: false,
		},
		{
			name: "with timezone",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "3h",
				"timezone": "America/New_York",
			},
			wantIsErr: false,
		},
		{
			name: "add days",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "7d",
			},
			wantIsErr: false,
		},
		{
			name: "missing duration",
			args: map[string]any{
				"time": "2024-01-15T14:30:00Z",
			},
			wantIsErr:    true,
			errSubstring: "duration",
		},
		{
			name: "invalid time format",
			args: map[string]any{
				"time":     "invalid",
				"duration": "1h",
			},
			wantIsErr:    true,
			errSubstring: "cannot parse time",
		},
		{
			name: "invalid timezone",
			args: map[string]any{
				"duration": "1h",
				"timezone": "Invalid/Zone",
			},
			wantIsErr:    true,
			errSubstring: "invalid timezone",
		},
		{
			name: "invalid duration",
			args: map[string]any{
				"duration": "invalid",
			},
			wantIsErr:    true,
			errSubstring: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handleAddDuration(context.Background(), tt.args)
			if err != nil {
				t.Errorf("handleAddDuration() unexpected Go error = %v", err)
				return
			}
			if result.IsError != tt.wantIsErr {
				t.Errorf("handleAddDuration() IsError = %v, want %v", result.IsError, tt.wantIsErr)
				return
			}
			if tt.wantIsErr && tt.errSubstring != "" {
				if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, tt.errSubstring) {
					t.Errorf("error should contain %q, got: %v", tt.errSubstring, result.Content)
				}
			}
			if !tt.wantIsErr && result == nil {
				t.Error("expected non-nil result")
			}
		})
	}
}

func TestHandleListTimezones(t *testing.T) {
	t.Parallel()

	result, err := handleListTimezones(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text := result.Content[0].Text

	// Verify some expected timezones exist in response
	expectedTZs := []string{"UTC", "America/New_York", "Asia/Tokyo", "Europe/London"}
	for _, tz := range expectedTZs {
		if !strings.Contains(text, tz) {
			t.Errorf("expected timezone %q in response, got: %s", tz, text)
		}
	}

	// Verify note is present
	if !strings.Contains(text, "note:") {
		t.Error("response should contain note field")
	}
}
