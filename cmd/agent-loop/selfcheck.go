package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/flexinfer/flexinfer/internal/agentloop"
)

// runSelfCheck exercises the whole client offline: a canned chat server that
// mimics the proxy (flexinfer headers + a scripted tool-call→final dialogue),
// a real temp-dir file the read_file tool actually reads, and assertions on
// the append-only prefix invariant, header parsing, real tool execution, the
// final answer, and the path-jail. No cluster required — this is the dev/CI
// gate, mirroring the --self-check mode of the F4 kill-test scripts.
func runSelfCheck() error {
	dir, err := os.MkdirTemp("", "agent-loop-selfcheck-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	const fileBody = "F4 append-only tool loop: prefix cache makes tool history a sunk cost.\n"
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte(fileBody), 0o644); err != nil {
		return err
	}

	srv, seen := cannedChatServer()
	defer srv.Close()

	tools, err := fsTools(dir)
	if err != nil {
		return fmt.Errorf("fsTools: %w", err)
	}
	reg, err := agentloop.NewRegistry(tools...)
	if err != nil {
		return fmt.Errorf("registry: %w", err)
	}
	client, err := agentloop.NewChatClient(agentloop.ChatClientConfig{
		Endpoint: srv.URL, Model: "selfcheck-model", CacheKey: "selfcheck-session",
	})
	if err != nil {
		return err
	}
	eng := &agentloop.Engine{
		Client:       client,
		Registry:     reg,
		Budget:       agentloop.Budget{MaxModelLen: 20480, SystemTokens: 100, OutputReserve: 48},
		MaxRounds:    8,
		OutputTokens: 48,
		ToolTimeout:  5 * time.Second,
	}

	conv := agentloop.NewConversation("You are a self-check agent.")
	res, err := eng.Run(context.Background(), conv, "Read hello.txt and report its content.")
	if err != nil {
		return fmt.Errorf("engine run: %w", err)
	}

	if err := assertResult(res, fileBody); err != nil {
		return err
	}
	if err := assertAppendOnly(seen.slices()); err != nil {
		return err
	}
	if err := assertMetrics(res); err != nil {
		return err
	}
	if err := assertPathJail(reg); err != nil {
		return err
	}
	if u := eng.Budget.Usable(); u != 20480-100-48 {
		return fmt.Errorf("budget usable=%d want %d", u, 20480-100-48)
	}
	return nil
}

// assertResult checks the loop reached a tool-call-free final answer after
// really executing the read_file tool (the file body must appear in the
// recorded tool result).
func assertResult(res *agentloop.Result, fileBody string) error {
	if res.Stopped != agentloop.StopFinal {
		return fmt.Errorf("stopped=%q want %q", res.Stopped, agentloop.StopFinal)
	}
	if len(res.Rounds) != 2 {
		return fmt.Errorf("rounds=%d want 2 (one tool round, one final)", len(res.Rounds))
	}
	if len(res.Rounds[0].ToolCalls) != 1 {
		return fmt.Errorf("round 0 tool calls=%d want 1", len(res.Rounds[0].ToolCalls))
	}
	tc := res.Rounds[0].ToolCalls[0]
	if tc.Err != "" {
		return fmt.Errorf("tool error: %s", tc.Err)
	}
	if tc.Result != fileBody {
		return fmt.Errorf("tool result=%q want the real file body %q", tc.Result, fileBody)
	}
	if res.Answer == "" {
		return fmt.Errorf("empty final answer")
	}
	return nil
}

// assertAppendOnly verifies each request the engine sent was a strict
// prefix-extension of the previous one — the cache-paying invariant.
func assertAppendOnly(reqs [][]agentloop.Message) error {
	if len(reqs) < 2 {
		return fmt.Errorf("only %d requests captured; need ≥2 to check append-only", len(reqs))
	}
	for r := 1; r < len(reqs); r++ {
		prev, cur := reqs[r-1], reqs[r]
		if len(cur) < len(prev) {
			return fmt.Errorf("request %d shrank (%d < %d) — not append-only", r, len(cur), len(prev))
		}
		for i := range prev {
			if !messagesEqual(prev[i], cur[i]) {
				return fmt.Errorf("request %d diverged at message %d — prefix busted", r, i)
			}
		}
	}
	return nil
}

func assertMetrics(res *agentloop.Result) error {
	m := res.Rounds[0].Metrics
	if m.UpstreamMs <= 0 {
		return fmt.Errorf("upstream_ms not parsed from header (got %d)", m.UpstreamMs)
	}
	if m.PromptTokens <= 0 {
		return fmt.Errorf("prompt_tokens not parsed (got %d)", m.PromptTokens)
	}
	if m.PrefixHitRatio == nil {
		return fmt.Errorf("prefix_hit_ratio nil — cached-tokens header path not exercised")
	}
	return nil
}

// assertPathJail confirms the read-only tool refuses a path that escapes the
// working directory (returns an error rather than reading outside root).
func assertPathJail(reg *agentloop.Registry) error {
	tool, ok := reg.Get("read_file")
	if !ok {
		return fmt.Errorf("read_file tool missing")
	}
	if _, err := tool.Invoke(context.Background(), `{"path":"../../../../etc/passwd"}`); err == nil {
		return fmt.Errorf("path jail breached: ../etc/passwd was allowed")
	}
	return nil
}

func messagesEqual(a, b agentloop.Message) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// captured records the message slices the canned server received, so the
// self-check can assert the append-only invariant from the wire side.
type captured struct {
	mu   sync.Mutex
	reqs [][]agentloop.Message
}

func (c *captured) add(msgs []agentloop.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]agentloop.Message, len(msgs))
	copy(cp, msgs)
	c.reqs = append(c.reqs, cp)
}

func (c *captured) slices() [][]agentloop.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reqs
}

// cannedChatServer returns an httptest server that scripts a two-round
// dialogue: round 0 asks for read_file("hello.txt"); round 1 (once it has
// seen a tool result) returns a final answer. It always sets the flexinfer
// instrumentation headers so the metric-parse path is exercised.
func cannedChatServer() (*httptest.Server, *captured) {
	cap := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []agentloop.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		cap.add(body.Messages)

		hasToolResult := false
		for _, m := range body.Messages {
			if m.Role == agentloop.RoleTool {
				hasToolResult = true
			}
		}

		w.Header().Set(agentloop.HeaderUpstreamMs, "1400")
		w.Header().Set(agentloop.HeaderPromptTokens, strconv.Itoa(500+100*len(body.Messages)))
		w.Header().Set(agentloop.HeaderCachedTokens, "480")
		w.Header().Set("Content-Type", "application/json")

		var reply string
		if hasToolResult {
			w.Header().Set(agentloop.HeaderFinishReason, "stop")
			reply = `{"choices":[{"message":{"role":"assistant","content":"hello.txt describes the F4 append-only tool loop."},"finish_reason":"stop"}],"usage":{"prompt_tokens":620}}`
		} else {
			w.Header().Set(agentloop.HeaderFinishReason, "tool_calls")
			reply = `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"hello.txt\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":500}}`
		}
		_, _ = w.Write([]byte(reply))
	}))
	return srv, cap
}
