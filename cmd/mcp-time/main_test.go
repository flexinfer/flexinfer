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
		{"1.5d", 0, true},     // non-integer days
		{"", 0, true},         // empty
		{"invalid", 0, true},  // invalid format
		{"abc", 0, true},      // not parseable
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

	_, err := handleWait(ctx, map[string]any{"duration": "200ms"})
	if err == nil {
		t.Fatalf("expected deadline-related error")
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
	_, err := handleWait(ctx, map[string]any{"duration": "-1s"})
	if err == nil {
		t.Fatal("expected error for negative duration")
	}
	if !strings.Contains(err.Error(), "non-negative") {
		t.Errorf("error should mention non-negative, got: %v", err)
	}
}

func TestHandleWait_MissingDuration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, err := handleWait(ctx, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing duration")
	}
}

func TestHandleGetCurrentTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		timezone string
		wantErr  bool
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
			if (err != nil) != tt.wantErr {
				t.Errorf("handleGetCurrentTime() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && result == nil {
				t.Error("expected non-nil result")
			}
			if !tt.wantErr && len(result.Content) == 0 {
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
		wantErr      bool
		errSubstring string
	}{
		{
			name: "valid conversion",
			args: map[string]any{
				"time":        "2024-01-15T14:30:00Z",
				"to_timezone": "America/New_York",
			},
			wantErr: false,
		},
		{
			name: "with from_timezone",
			args: map[string]any{
				"time":          "2024-01-15T14:30:00Z",
				"from_timezone": "UTC",
				"to_timezone":   "Asia/Tokyo",
			},
			wantErr: false,
		},
		{
			name: "alternative date format",
			args: map[string]any{
				"time":        "2024-01-15 14:30:00",
				"to_timezone": "UTC",
			},
			wantErr: false,
		},
		{
			name: "missing time",
			args: map[string]any{
				"to_timezone": "UTC",
			},
			wantErr:      true,
			errSubstring: "time is required",
		},
		{
			name: "missing to_timezone",
			args: map[string]any{
				"time": "2024-01-15T14:30:00Z",
			},
			wantErr:      true,
			errSubstring: "to_timezone is required",
		},
		{
			name: "invalid time format",
			args: map[string]any{
				"time":        "invalid",
				"to_timezone": "UTC",
			},
			wantErr:      true,
			errSubstring: "cannot parse time",
		},
		{
			name: "invalid from_timezone",
			args: map[string]any{
				"time":          "2024-01-15T14:30:00Z",
				"from_timezone": "Invalid/Zone",
				"to_timezone":   "UTC",
			},
			wantErr:      true,
			errSubstring: "invalid from_timezone",
		},
		{
			name: "invalid to_timezone",
			args: map[string]any{
				"time":        "2024-01-15T14:30:00Z",
				"to_timezone": "Invalid/Zone",
			},
			wantErr:      true,
			errSubstring: "invalid to_timezone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handleConvertTimezone(context.Background(), tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleConvertTimezone() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errSubstring != "" {
				if !strings.Contains(err.Error(), tt.errSubstring) {
					t.Errorf("error should contain %q, got: %v", tt.errSubstring, err)
				}
			}
			if !tt.wantErr && result == nil {
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
		wantErr      bool
		errSubstring string
	}{
		{
			name: "add to current time",
			args: map[string]any{
				"duration": "1h",
			},
			wantErr: false,
		},
		{
			name: "add to specific time",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "2h30m",
			},
			wantErr: false,
		},
		{
			name: "subtract duration",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "-1h",
			},
			wantErr: false,
		},
		{
			name: "with timezone",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "3h",
				"timezone": "America/New_York",
			},
			wantErr: false,
		},
		{
			name: "add days",
			args: map[string]any{
				"time":     "2024-01-15T14:30:00Z",
				"duration": "7d",
			},
			wantErr: false,
		},
		{
			name: "missing duration",
			args: map[string]any{
				"time": "2024-01-15T14:30:00Z",
			},
			wantErr:      true,
			errSubstring: "duration is required",
		},
		{
			name: "invalid time format",
			args: map[string]any{
				"time":     "invalid",
				"duration": "1h",
			},
			wantErr:      true,
			errSubstring: "cannot parse time",
		},
		{
			name: "invalid timezone",
			args: map[string]any{
				"duration": "1h",
				"timezone": "Invalid/Zone",
			},
			wantErr:      true,
			errSubstring: "invalid timezone",
		},
		{
			name: "invalid duration",
			args: map[string]any{
				"duration": "invalid",
			},
			wantErr:      true,
			errSubstring: "invalid", // error message may vary
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := handleAddDuration(context.Background(), tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleAddDuration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errSubstring != "" {
				if !strings.Contains(err.Error(), tt.errSubstring) {
					t.Errorf("error should contain %q, got: %v", tt.errSubstring, err)
				}
			}
			if !tt.wantErr && result == nil {
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
