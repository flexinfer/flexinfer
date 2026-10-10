package runtime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
)

func TestRuntimeEngineStallDespiteHealthyAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_, _ = fmt.Fprint(w, "vllm:num_requests_running 1\nvllm:num_requests_waiting 0\nvllm:prompt_tokens_total 10\nvllm:generation_tokens_total 20\n")
		}
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	n, _ := strconv.Atoi(port)
	loaded := &LoadedModel{Name: "stalled", Backend: backend.NameVLLM, State: ModelStateReady, Port: int32(n)}
	m := NewManager(ManagerConfig{})
	m.models[loaded.Name] = loaded
	detector := backend.StallDetector{Window: time.Minute}
	now := time.Now()
	if m.checkEngineStall(context.Background(), loaded, &detector, now) {
		t.Fatal("first sample stalled")
	}
	if !m.checkEngineStall(context.Background(), loaded, &detector, now.Add(time.Minute)) {
		t.Fatal("flat counters did not stall")
	}
	if loaded.State != ModelStateFailed || !strings.Contains(loaded.Error, "prompt_tokens=10") {
		t.Fatalf("%+v", loaded)
	}
	api := httptest.NewRecorder()
	NewServer(m).Handler().ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/v1/models/stalled/health", nil))
	if api.Code != http.StatusServiceUnavailable {
		t.Fatalf("runtime health=%d", api.Code)
	}
	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("API not healthy")
	}
}
