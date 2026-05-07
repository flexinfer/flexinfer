package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	cost "github.com/crb2nu/loom/internal/visibility/contracts/cost"
)

func sampleCostSnapshot() *cost.CostStatsResult {
	return &cost.CostStatsResult{
		Enabled:   true,
		Timestamp: "2026-05-06T12:00:00Z",
		ByAgent: []cost.CostAgentUsage{
			{AgentID: "claude-code", CallCount: 100, ErrorCount: 2, DeniedCount: 1, CachedCount: 5, TotalDuration: 5000},
			{AgentID: "codex", CallCount: 50, ErrorCount: 0, DeniedCount: 0, CachedCount: 0, TotalDuration: 1000},
		},
		ByServer: []cost.CostServerUsage{
			{Server: "github", CallCount: 80, ErrorCount: 1, TotalDuration: 4000},
			{Server: "gitlab", CallCount: 70, ErrorCount: 1, TotalDuration: 2000},
		},
		Totals: cost.CostTotals{
			CallCount:     150,
			ErrorCount:    2,
			DeniedCount:   1,
			CachedCount:   5,
			TotalDuration: 6000,
		},
	}
}

func TestParseCostGroupBy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    costGroupBy
		wantErr bool
	}{
		{"", costGroupByAgent, false},
		{"agent", costGroupByAgent, false},
		{"AGENT", costGroupByAgent, false},
		{"server", costGroupByServer, false},
		{"day", costGroupByDay, false},
		{"DAY", costGroupByDay, false},
		{"week", "", true},
		{"foo", "", true},
	}
	for _, tc := range cases {
		got, err := parseCostGroupBy(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseCostGroupBy(%q) = %q, expected error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseCostGroupBy(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("parseCostGroupBy(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRunCostCommand_JSONRoundTrips(t *testing.T) {
	t.Parallel()

	want := sampleCostSnapshot()
	fetch := func(_ string) (*cost.CostStatsResult, error) { return want, nil }

	var buf bytes.Buffer
	if err := runCostCommand(context.Background(), &buf, "/dev/null", costGroupByAgent, true, 0, fetch); err != nil {
		t.Fatalf("runCostCommand: %v", err)
	}

	var got cost.CostStatsResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal output: %v\noutput: %s", err, buf.String())
	}
	if got.Totals != want.Totals {
		t.Errorf("totals = %+v, want %+v", got.Totals, want.Totals)
	}
	if len(got.ByAgent) != len(want.ByAgent) {
		t.Errorf("by_agent len = %d, want %d", len(got.ByAgent), len(want.ByAgent))
	}
	if len(got.ByServer) != len(want.ByServer) {
		t.Errorf("by_server len = %d, want %d", len(got.ByServer), len(want.ByServer))
	}
	if !got.Enabled {
		t.Errorf("expected enabled=true")
	}
}

func TestRunCostCommand_TextRendersGroups(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		groupBy costGroupBy
		mustHas []string
	}{
		{
			name:    "agent",
			groupBy: costGroupByAgent,
			mustHas: []string{"AGENT", "claude-code", "codex", "Totals:"},
		},
		{
			name:    "server",
			groupBy: costGroupByServer,
			mustHas: []string{"SERVER", "github", "gitlab", "Totals:"},
		},
		{
			name:    "day",
			groupBy: costGroupByDay,
			mustHas: []string{"DAY", "2026-05-06", "Totals:"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			fetch := func(_ string) (*cost.CostStatsResult, error) { return sampleCostSnapshot(), nil }
			if err := runCostCommand(context.Background(), &buf, "/dev/null", tc.groupBy, false, 0, fetch); err != nil {
				t.Fatalf("runCostCommand: %v", err)
			}
			got := buf.String()
			for _, want := range tc.mustHas {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\nfull output:\n%s", want, got)
				}
			}
		})
	}
}

func TestRunCostCommand_DisabledShowsReason(t *testing.T) {
	t.Parallel()

	fetch := func(_ string) (*cost.CostStatsResult, error) {
		return &cost.CostStatsResult{Enabled: false, Reason: "feature-flag off"}, nil
	}
	var buf bytes.Buffer
	if err := runCostCommand(context.Background(), &buf, "/dev/null", costGroupByAgent, false, 0, fetch); err != nil {
		t.Fatalf("runCostCommand: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "disabled") || !strings.Contains(got, "feature-flag off") {
		t.Errorf("expected disabled+reason in output, got:\n%s", got)
	}
}

func TestRunCostCommand_FetchErrorIsNonZero(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("daemon offline")
	fetch := func(_ string) (*cost.CostStatsResult, error) { return nil, wantErr }

	var buf bytes.Buffer
	err := runCostCommand(context.Background(), &buf, "/dev/null", costGroupByAgent, false, 0, fetch)
	if err == nil {
		t.Fatalf("expected error from fetch failure")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error chain missing fetch error: %v", err)
	}
	if !strings.Contains(err.Error(), "daemon unreachable") {
		t.Errorf("error message missing daemon-unreachable hint: %v", err)
	}
}

func TestRunCostCommand_WatchClampsAndCancels(t *testing.T) {
	t.Parallel()

	calls := 0
	fetch := func(_ string) (*cost.CostStatsResult, error) {
		calls++
		return sampleCostSnapshot(), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Cancel after a short delay so Watch exits cleanly.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var buf bytes.Buffer
	// Pass an unrealistically short watch interval; it must be clamped, not panic.
	err := runCostCommand(ctx, &buf, "/dev/null", costGroupByAgent, true, time.Millisecond, fetch)
	if err != nil {
		t.Fatalf("runCostCommand watch: %v", err)
	}
	if calls < 1 {
		t.Errorf("expected fetch to be called at least once, got %d", calls)
	}
}

func TestDayKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"", "today"},
		{"2026-05-06T12:00:00Z", "2026-05-06"},
		{"2026-05-06T12:00:00.123456789Z", "2026-05-06"},
		{"not-a-timestamp", "not-a-time"},
		{"short", "short"},
	}
	for _, tc := range cases {
		if got := dayKey(tc.in); got != tc.want {
			t.Errorf("dayKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCostAvgMs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		total int64
		calls int64
		want  string
	}{
		{0, 0, "-"},
		{100, 0, "-"},
		{100, 10, "10.0"},
		{55, 4, "13.8"},
	}
	for _, tc := range cases {
		if got := costAvgMs(tc.total, tc.calls); got != tc.want {
			t.Errorf("costAvgMs(%d,%d) = %q, want %q", tc.total, tc.calls, got, tc.want)
		}
	}
}
