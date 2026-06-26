package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/pkg/goodhart"
)

// TestLiveQualityProbe exercises the production QualityFunc (NewWorkloadQualityFunc)
// against a real chat-completions endpoint — the Slice-4 live-validation harness
// for the Goodhart guard. It is skipped unless FLEXINFER_LIVE_CHAT_URL and
// FLEXINFER_LIVE_MODEL are set, so it never runs in CI. Example:
//
//	FLEXINFER_LIVE_CHAT_URL=http://localhost:18000/v1/chat/completions \
//	FLEXINFER_LIVE_MODEL=gemma4-26b-a4b-gptq \
//	go test ./agents/autotune/ -run TestLiveQualityProbe -v
//
// To also print the veto verdict, set FLEXINFER_LIVE_BASELINE to a JSON map of a
// previously-captured baseline, e.g. '{"lookup":67.0,"novel":72.6}'.
func TestLiveQualityProbe(t *testing.T) {
	chatURL := os.Getenv("FLEXINFER_LIVE_CHAT_URL")
	model := os.Getenv("FLEXINFER_LIVE_MODEL")
	if chatURL == "" || model == "" {
		t.Skip("set FLEXINFER_LIVE_CHAT_URL and FLEXINFER_LIVE_MODEL to run the live quality probe")
	}

	qf := NewWorkloadQualityFunc(&http.Client{Timeout: 4 * time.Minute}, chatURL, model, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	q, err := qf(ctx)
	if err != nil {
		t.Fatalf("quality probe: %v", err)
	}
	t.Logf("LIVE per-class decode tok/s: %v", q)
	for cls, tps := range q {
		if tps <= 0 {
			t.Errorf("class %q returned non-positive tok/s %.2f", cls, tps)
		}
	}

	if raw := os.Getenv("FLEXINFER_LIVE_BASELINE"); raw != "" {
		var baseline map[string]float64
		if err := json.Unmarshal([]byte(raw), &baseline); err != nil {
			t.Fatalf("parse FLEXINFER_LIVE_BASELINE: %v", err)
		}
		f := goodhart.WorkloadRegression(baseline, q, DefaultQualityTolerancePct)
		t.Logf("LIVE WorkloadRegression vs baseline %v: tripped=%v value=%.1f%% — %s",
			baseline, f.Tripped, f.Value, f.Reason)
	}
}
