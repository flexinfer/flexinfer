package backend

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStallSequences(t *testing.T) {
	for _, tc := range []struct {
		name    string
		samples []VLLMStallSample
		want    bool
	}{
		{"flat", []VLLMStallSample{{Running: 1}, {Running: 1}}, true},
		{"idle", []VLLMStallSample{{}, {}}, false},
		{"moving", []VLLMStallSample{{Running: 1}, {Running: 1, Generation: 1}}, false},
		{"waiting", []VLLMStallSample{{Waiting: 1}, {Waiting: 1}}, true},
		{"new work", []VLLMStallSample{{}, {Running: 1}}, false},
		{"reset counter", []VLLMStallSample{{Running: 1, Prompt: 5}, {Running: 1}}, false},
		// A chunked prefill longer than the window: counters flat, KV usage rising.
		{"long prefill", []VLLMStallSample{{Running: 1, KVUsage: 0.17, HasKVUsage: true}, {Running: 1, KVUsage: 0.35, HasKVUsage: true}}, false},
		{"kv frozen", []VLLMStallSample{{Running: 1, KVUsage: 0.74, HasKVUsage: true}, {Running: 1, KVUsage: 0.74, HasKVUsage: true}}, true},
		{"kv freed", []VLLMStallSample{{Running: 1, KVUsage: 0.74, HasKVUsage: true}, {Running: 1, KVUsage: 0.2, HasKVUsage: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := StallDetector{Window: time.Minute}
			now := time.Now()
			d.Observe(tc.samples[0], now)
			if got := d.Observe(tc.samples[1], now.Add(time.Minute)); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// TestStallLongPrefillThenHang replays the 2026-11-21 shape: a 120K-token
// prefill moves only the KV gauge for longer than the window and is never a
// stall; once the gauge freezes too, the window runs from the last change.
func TestStallLongPrefillThenHang(t *testing.T) {
	d := StallDetector{Window: 5 * time.Minute}
	start := time.Now()
	kv := []float64{0.17, 0.35, 0.47, 0.56, 0.66, 0.74, 0.80}
	for i, v := range kv {
		at := start.Add(time.Duration(i) * time.Minute)
		if d.Observe(VLLMStallSample{Running: 1, Prompt: 428923, Generation: 4432, KVUsage: v, HasKVUsage: true}, at) {
			t.Fatalf("minute %d: long prefill declared stalled", i)
		}
	}
	frozen := VLLMStallSample{Running: 1, Prompt: 428923, Generation: 4432, KVUsage: 0.80, HasKVUsage: true}
	last := start.Add(time.Duration(len(kv)-1) * time.Minute)
	if d.Observe(frozen, last.Add(4*time.Minute)) {
		t.Fatal("stalled before the window elapsed")
	}
	if !d.Observe(frozen, last.Add(5*time.Minute)) {
		t.Fatal("frozen gauge and counters not declared stalled after the window")
	}
}

func TestStallMetricsKVUsage(t *testing.T) {
	base := "vllm:num_requests_running 1\nvllm:num_requests_waiting 0\nvllm:prompt_tokens_total 20\nvllm:generation_tokens_total 30\n"
	for _, tc := range []struct {
		name  string
		extra string
		want  float64
		hasKV bool
	}{
		{"absent", "", 0, false},
		{"current name", "vllm:kv_cache_usage_perc{engine=\"0\",model_name=\"m\"} 0.25\n", 0.25, true},
		{"legacy name", "vllm:gpu_cache_usage_perc{model_name=\"m\"} 0.5\n", 0.5, true},
		{"current wins", "vllm:gpu_cache_usage_perc 0.5\nvllm:kv_cache_usage_perc 0.25\n", 0.25, true},
		{"invalid ignored", "vllm:kv_cache_usage_perc NaN\n", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := ParseVLLMStallSample(base + tc.extra)
			if err != nil {
				t.Fatal(err)
			}
			if s.HasKVUsage != tc.hasKV || s.KVUsage != tc.want || s.Running != 1 || s.Generation != 30 {
				t.Fatalf("got %+v", s)
			}
		})
	}
}

func TestStallMetrics(t *testing.T) {
	text := "vllm:num_requests_running{model_name=\"a b\"} 1\nvllm:num_requests_running{model_name=\"c\"} 2\nvllm:num_requests_waiting 0\nvllm:prompt_tokens_total 20\nvllm:generation_tokens_total 30\n"
	s, err := ParseVLLMStallSample(text)
	if err != nil || s.Running != 3 || s.Generation != 30 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{"", text + "vllm:prompt_tokens_total NaN\n"} {
		if _, err := ParseVLLMStallSample(bad); err == nil {
			t.Fatal("accepted invalid scrape")
		}
	}
}

func TestStallProbeDefinition(t *testing.T) {
	b := &VLLMBackend{}
	p := b.ReadinessProbeForSpec(&ModelSpec{Config: map[string]any{"stallWindow": "2m"}})
	if p.Exec == nil || p.PeriodSeconds != 30 || p.FailureThreshold != 3 {
		t.Fatalf("bad probe: %+v", p)
	}
	script := p.Exec.Command[2]
	for _, part := range []string{"/health", "/metrics", "now - since >= 120"} {
		if !strings.Contains(script, part) {
			t.Fatalf("missing %s", part)
		}
	}
	if b.StartupProbe().HTTPGet == nil || b.ReadinessProbe().HTTPGet == nil {
		t.Fatal("runtime/startup probes changed")
	}
}

func TestStallProbeScript(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("stall probe reads Linux procfs")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	// The handler goroutine only reads these after the probe subprocess is
	// started, but the race detector cannot see ordering through another
	// process, so every access goes through mu.
	var mu sync.Mutex
	text := "vllm:num_requests_running 1\nvllm:num_requests_waiting 0\nvllm:prompt_tokens_total 10\nvllm:generation_tokens_total 20\n"
	health := 200
	update := func(f func()) {
		mu.Lock()
		defer mu.Unlock()
		f()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		status, body := health, text
		mu.Unlock()
		if r.URL.Path == "/health" {
			w.WriteHeader(status)
			return
		}
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()
	script := (&VLLMBackend{}).ReadinessProbeForSpec(&ModelSpec{Config: map[string]any{"stallWindow": "30s"}}).Exec.Command[2]
	script = strings.ReplaceAll(script, "http://127.0.0.1:8000", server.URL)
	script = strings.ReplaceAll(script, "/tmp/flexinfer-vllm-stall.json", filepath.Join(t.TempDir(), "state.json"))
	script = strings.ReplaceAll(script, "now = time.monotonic()", `now = float(os.environ["STALL_TEST_NOW"])`)
	run := func(now string, wantReady bool) {
		t.Helper()
		cmd := exec.Command(python, "-c", script)
		cmd.Env = append(os.Environ(), "STALL_TEST_NOW="+now)
		output, err := cmd.CombinedOutput()
		if (err == nil) != wantReady {
			t.Fatalf("time %s: ready=%v want %v: %s", now, err == nil, wantReady, output)
		}
	}
	run("100", true)
	run("129", true)
	run("130", false)
	update(func() { text = strings.Replace(text, "generation_tokens_total 20", "generation_tokens_total 21", 1) })
	run("131", true)
	update(func() { text = strings.Replace(text, "num_requests_running 1", "num_requests_running 0", 1) })
	run("161", true)
	run("191", true)
	// A long chunked prefill: the token counters stay flat past the window
	// while the KV gauge moves, so the lane stays ready; once the gauge
	// freezes too, readiness fails a window after its last change.
	update(func() {
		text = strings.Replace(text, "num_requests_running 0", "num_requests_running 1", 1) + "vllm:kv_cache_usage_perc 0.17\n"
	})
	run("200", true)
	update(func() { text = strings.Replace(text, "kv_cache_usage_perc 0.17", "kv_cache_usage_perc 0.35", 1) })
	run("225", true)
	update(func() { text = strings.Replace(text, "kv_cache_usage_perc 0.35", "kv_cache_usage_perc 0.47", 1) })
	run("250", true)
	run("279", true)
	run("280", false)
	update(func() { health = 503 })
	run("192", false)
	update(func() { health, text = 200, "" })
	run("193", false)
}

func TestStallWindowOverride(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  time.Duration
	}{
		{"2m", 2 * time.Minute}, {float64(90), 90 * time.Second}, {"invalid", 5 * time.Minute}, {-1, 5 * time.Minute},
	} {
		if got := StallWindowForSpec(&ModelSpec{Config: map[string]any{"stallWindow": tc.value}}); got != tc.want {
			t.Fatalf("%v: %s", tc.value, got)
		}
	}
	if StallWindowForSpec(nil) != 5*time.Minute {
		t.Fatal("default window")
	}
}
