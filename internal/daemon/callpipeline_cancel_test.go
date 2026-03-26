package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kitregistry "gitlab.flexinfer.ai/libs/fi-mcp-kit/pkg/registry"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/internal/router"
)

func TestCallPipeline_CancelledCallReleasesPoolCapacity(t *testing.T) {
	d := newCallPipelineTestDaemon()
	d.procMgr = process.NewManager(nil, "codex")
	d.router = router.New(router.Config{
		HubEnabled:       false,
		FailureThreshold: 10,
		Registry: &kitregistry.Registry{
			Servers: []*kitregistry.Server{
				{Name: "cancel_srv", Categories: []string{"local-only"}},
			},
		},
	})

	var dialCount atomic.Int32
	d.pool = pool.New(pool.Config{
		MaxIdle:     1,
		MaxOpen:     1,
		IdleTimeout: time.Minute,
		DialFunc: func(_ context.Context, _ string) (mcp.Transport, error) {
			call := dialCount.Add(1)
			if call == 1 {
				return &fakeTransport{
					sendFn: func(ctx context.Context, _ *mcp.Message) error {
						<-ctx.Done()
						return ctx.Err()
					},
				}, nil
			}
			return &fakeTransport{
				recvMsg: &mcp.Message{
					JSONRPC: mcp.JSONRPCVersion,
					ID:      "test-id",
					Result:  json.RawMessage(`{"ok":true}`),
				},
			}, nil
		},
	})
	defer func() { _ = d.pool.Close() }()

	msg := newCallMessage(t, map[string]any{
		"server": "cancel_srv",
		"tool":   "check",
	})

	ctx, cancel := context.WithCancel(context.Background())
	firstDone := make(chan *mcp.Message, 1)
	go func() {
		resp, err := d.handleCall(ctx, msg)
		if err != nil {
			t.Errorf("cancelled call returned error: %v", err)
			firstDone <- nil
			return
		}
		firstDone <- resp
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	resp1 := <-firstDone
	if resp1 == nil || resp1.Error == nil {
		t.Fatal("cancelled call: expected error response")
	}

	statsAfterCancel := d.pool.Stats()
	if statsAfterCancel.ActiveConns != 0 {
		t.Fatalf("active conns after cancelled call = %d, want 0", statsAfterCancel.ActiveConns)
	}

	resp2, err := d.handleCall(context.Background(), msg)
	if err != nil {
		t.Fatalf("follow-up call returned error: %v", err)
	}
	if resp2.Error != nil {
		t.Fatalf("follow-up call returned error response: %+v", resp2.Error)
	}
	if !strings.Contains(string(resp2.Result), `"ok":true`) {
		t.Fatalf("follow-up call result = %s, want success payload", string(resp2.Result))
	}
	if got := dialCount.Load(); got != 2 {
		t.Fatalf("dial count = %d, want 2", got)
	}
}
