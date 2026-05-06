package render

import (
	"strings"
	"testing"
)

func TestParseFilter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		spec    string
		want    Filter
		wantErr bool
	}{
		{
			name: "empty string yields empty filter",
			spec: "",
			want: Filter{},
		},
		{
			name: "whitespace-only yields empty filter",
			spec: "   ",
			want: Filter{},
		},
		{
			name: "single pair",
			spec: "agent=claude",
			want: Filter{"agent": "claude"},
		},
		{
			name: "multiple pairs",
			spec: "a=1,b=2",
			want: Filter{"a": "1", "b": "2"},
		},
		{
			name: "whitespace tolerant",
			spec: "  a = 1 , b = 2 ",
			want: Filter{"a": "1", "b": "2"},
		},
		{
			name: "case-insensitive keys and values",
			spec: "Agent=Claude",
			want: Filter{"agent": "claude"},
		},
		{
			name: "trailing comma ignored",
			spec: "a=1,",
			want: Filter{"a": "1"},
		},
		{
			name: "duplicate keys last write wins",
			spec: "a=1,a=2",
			want: Filter{"a": "2"},
		},
		{
			name: "empty value is allowed",
			spec: "a=",
			want: Filter{"a": ""},
		},
		{
			name:    "missing equals is rejected",
			spec:    "bad",
			wantErr: true,
		},
		{
			name:    "empty key is rejected",
			spec:    "=value",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseFilter(tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for spec %q, got nil", tc.spec)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for spec %q: %v", tc.spec, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch for spec %q: got %d want %d (%v vs %v)", tc.spec, len(got), len(tc.want), got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("key %q: got %q want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestParseFilter_ErrorMessageMentionsBadInput(t *testing.T) {
	t.Parallel()

	_, err := ParseFilter("nope")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected error to quote bad input, got %q", err.Error())
	}
}

func TestFilter_Match(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		filter Filter
		row    map[string]string
		want   bool
	}{
		{
			name:   "empty filter matches anything",
			filter: Filter{},
			row:    map[string]string{"a": "1"},
			want:   true,
		},
		{
			name:   "empty filter matches nil row",
			filter: Filter{},
			row:    nil,
			want:   true,
		},
		{
			name:   "all keys match",
			filter: Filter{"a": "1", "b": "2"},
			row:    map[string]string{"a": "1", "b": "2", "c": "3"},
			want:   true,
		},
		{
			name:   "missing filter key on row",
			filter: Filter{"a": "1", "missing": "x"},
			row:    map[string]string{"a": "1"},
			want:   false,
		},
		{
			name:   "value mismatch",
			filter: Filter{"a": "1"},
			row:    map[string]string{"a": "2"},
			want:   false,
		},
		{
			name:   "case-insensitive value match",
			filter: Filter{"agent": "claude"},
			row:    map[string]string{"Agent": "CLAUDE"},
			want:   true,
		},
		{
			name:   "empty filter value matches empty row value",
			filter: Filter{"a": ""},
			row:    map[string]string{"a": ""},
			want:   true,
		},
		{
			name:   "empty filter value rejects non-empty row value",
			filter: Filter{"a": ""},
			row:    map[string]string{"a": "x"},
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.filter.Match(tc.row)
			if got != tc.want {
				t.Fatalf("Match got %v, want %v", got, tc.want)
			}
		})
	}
}
