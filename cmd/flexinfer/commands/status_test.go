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

func TestFormatAge(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero time", time.Time{}, "-"},
		{"seconds ago", now.Add(-30 * time.Second), "30s"},
		{"one minute ago", now.Add(-90 * time.Second), "1m"},
		{"minutes ago", now.Add(-5 * time.Minute), "5m"},
		{"one hour ago", now.Add(-70 * time.Minute), "1h"},
		{"hours ago", now.Add(-3 * time.Hour), "3h"},
		{"one day ago", now.Add(-25 * time.Hour), "1d"},
		{"days ago", now.Add(-72 * time.Hour), "3d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAge(tt.t)
			if got != tt.want {
				t.Errorf("formatAge(%v) = %q, want %q", tt.t, got, tt.want)
			}
		})
	}
}

func TestFormatDurationShort(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"negative", -5 * time.Second, "0s"},
		{"zero", 0, "0s"},
		{"seconds", 30 * time.Second, "30s"},
		{"one minute", 60 * time.Second, "1m0s"},
		{"minute and seconds", 90 * time.Second, "1m30s"},
		{"minutes", 5*time.Minute + 15*time.Second, "5m15s"},
		{"one hour", 60 * time.Minute, "1h0m"},
		{"hours and minutes", 2*time.Hour + 30*time.Minute, "2h30m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDurationShort(tt.duration)
			if got != tt.want {
				t.Errorf("formatDurationShort(%v) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}
