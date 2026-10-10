package backend

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	text := "vllm:num_requests_running 1\nvllm:num_requests_waiting 0\nvllm:prompt_tokens_total 10\nvllm:generation_tokens_total 20\n"
	health := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(health)
			return
		}
		_, _ = fmt.Fprint(w, text)
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
	text = strings.Replace(text, "generation_tokens_total 20", "generation_tokens_total 21", 1)
	run("131", true)
	text = strings.Replace(text, "num_requests_running 1", "num_requests_running 0", 1)
	run("161", true)
	run("191", true)
	health = 503
	run("192", false)
	health = 200
	text = ""
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
