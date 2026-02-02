/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commands

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"one minute", 60 * time.Second, "1m"},
		{"minutes", 5 * time.Minute, "5m"},
		{"one hour", 60 * time.Minute, "1h0m"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h30m"},
		{"one day", 24 * time.Hour, "1d"},
		{"multiple days", 72 * time.Hour, "3d"},
		{"zero", 0, "0s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.duration)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"no truncation needed", "short", 10, "short"},
		{"exact length", "exact", 5, "exact"},
		{"truncate long string", "this is a very long string", 10, "this is..."},
		{"truncate at boundary", "hello world", 8, "hello..."},
		{"empty string", "", 5, ""},
		{"single char", "a", 5, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}
