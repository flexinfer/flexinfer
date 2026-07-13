package controllers

import (
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestScanQuantizationTelemetrySelectsNewestValidProgress(t *testing.T) {
	log := strings.Join([]string{
		`TQDM redraw\r{"event":"progress","ts":"2026-07-13T19:45:10Z","phase":"quantizing","percent":42.0,"detail":"layer 17 subset 1/2 via name"}`,
		`{"event":"progress","ts":"2026-07-13T19:44:00Z","phase":"quantizing","percent":41.0,"detail":"older replay"}`,
		`{"event":"progress","ts":"2026-07-13T19:46:00Z","phase":"loading","percent":90.0,"detail":"wrong phase"}`,
	}, "\n")

	got := scanQuantizationTelemetry(strings.NewReader(strings.ReplaceAll(log, `\r`, "\r")))
	if got.Progress == nil {
		t.Fatal("structured progress was not parsed")
	}
	if got.Progress.Percent != 42 || got.Progress.Detail != "layer 17 subset 1/2 via name" {
		t.Fatalf("progress = %#v", got.Progress)
	}
	if want := metav1.NewTime(time.Date(2026, 7, 13, 19, 45, 10, 0, time.UTC)); !got.Progress.At.Equal(&want) {
		t.Fatalf("progress timestamp = %s, want %s", got.Progress.At.Time, want)
	}
}

func TestScanQuantizationTelemetryRejectsMalformedProgress(t *testing.T) {
	log := `
{"event":"progress","ts":"bad","phase":"quantizing","percent":44,"detail":"bad timestamp"}
{"event":"progress","ts":"2026-07-13T19:45:10Z","phase":"quantizing","percent":101,"detail":"out of range"}
{"event":"progress","ts":"2026-07-13T19:45:10Z","phase":"quantizing","percent":"forty","detail":"bad type"}
not json
`
	got := scanQuantizationTelemetry(strings.NewReader(log))
	if got.Progress != nil {
		t.Fatalf("malformed progress was accepted: %#v", got.Progress)
	}
}

func TestQuantizationProgressStatusPreservesTelemetryAcrossReadFailure(t *testing.T) {
	at := metav1.NewTime(time.Date(2026, 7, 13, 19, 45, 10, 0, time.UTC))
	pct := int32(42)
	existing := &aiv1alpha1.QuantizationStatus{
		Progress: &pct, ProgressDetail: "layer 17 subset 1/2", ProgressSource: quantizationProgressSourceTelemetry, LastProgressAt: &at,
	}
	gotPct, detail, source, gotAt := quantizationProgressStatus(6*time.Hour, int64((24 * time.Hour).Seconds()), nil, existing)
	if gotPct != 42 || detail != existing.ProgressDetail || source != quantizationProgressSourceTelemetry || gotAt == nil || !gotAt.Equal(&at) {
		t.Fatalf("preserved status = %d %q %q %#v", gotPct, detail, source, gotAt)
	}
}

func TestScanLatestQuantizationLayer_PicksLatestCompletion(t *testing.T) {
	// Realistic quantize-pod log interleaves TQDM redraws with the JSON
	// progress events emitted by build/scripts/quantize_gptq.py. We must
	// still extract the highest `completed layer N` value despite the
	// noise.
	log := `
{"event": "progress", "ts": "2026-04-21T00:00:00Z", "phase": "quantizing", "percent": 5.0, "detail": "model loaded"}
> Quantizing layer 0 of 59 ['p' to ||] [0 of 59] | 0:00:00 / 0:00:00
{"event": "progress", "ts": "2026-04-21T00:01:00Z", "phase": "quantizing", "percent": 10.0, "detail": "calibration data ready"}
{"event": "progress", "ts": "2026-04-21T00:05:00Z", "phase": "quantizing", "percent": 11.3, "detail": "completed layer 1 | gpu_alloc=1000MB"}
> Quantizing layer 1 of 59 ['p' to ||] [1 of 59] | 0:05:00 / 5:00:00
{"event": "progress", "ts": "2026-04-21T00:10:00Z", "phase": "quantizing", "percent": 12.7, "detail": "completed layer 2 | gpu_alloc=1200MB"}
{"event": "progress", "ts": "2026-04-21T00:15:00Z", "phase": "quantizing", "percent": 14.0, "detail": "completed layer 3 | gpu_alloc=1500MB"}
`
	got := scanLatestQuantizationLayer(strings.NewReader(log))
	if got != 3 {
		t.Errorf("scanLatestQuantizationLayer: got %d, want 3", got)
	}
}

func TestScanLatestQuantizationLayer_NoCompletion_ReturnsNegativeOne(t *testing.T) {
	log := `
{"event": "progress", "ts": "2026-04-21T00:00:00Z", "phase": "quantizing", "percent": 5.0, "detail": "model loaded"}
{"event": "progress", "ts": "2026-04-21T00:01:00Z", "phase": "quantizing", "percent": 10.0, "detail": "calibration data ready"}
> Quantizing layer 0 of 59 ['p' to ||] [0 of 59] | 0:01:00 / 10:00:00
{"event": "progress", "ts": "2026-04-21T00:02:00Z", "phase": "quantizing", "percent": 10.0, "detail": "layer 1 subset 0/2 via name"}
`
	got := scanLatestQuantizationLayer(strings.NewReader(log))
	if got != -1 {
		t.Errorf("scanLatestQuantizationLayer with no completions: got %d, want -1", got)
	}
}

func TestScanLatestQuantizationLayer_OutOfOrder_StillPicksMax(t *testing.T) {
	// Defend against log-stream quirks where an older event appears after a
	// newer one (e.g., TQDM replay on restart). We always want the MAX layer
	// index, not the last one in the stream.
	log := `
{"event": "progress", "detail": "completed layer 5"}
{"event": "progress", "detail": "completed layer 42"}
{"event": "progress", "detail": "completed layer 3"}
`
	got := scanLatestQuantizationLayer(strings.NewReader(log))
	if got != 42 {
		t.Errorf("scanLatestQuantizationLayer with out-of-order events: got %d, want 42", got)
	}
}

func TestScanLatestQuantizationLayer_IgnoresNonCompletionText(t *testing.T) {
	// `layer 5 subset 2/6` is a subset progress event, not a completion.
	// Only `completed layer N` should match.
	log := `
{"event": "progress", "detail": "layer 5 subset 2/6 via name"}
{"event": "progress", "detail": "layer 6 subset 0/6 via tp-pre-pad"}
`
	got := scanLatestQuantizationLayer(strings.NewReader(log))
	if got != -1 {
		t.Errorf("scanLatestQuantizationLayer with only subset events: got %d, want -1", got)
	}
}

func TestScanLatestQuantizationLayer_ToleratesNoisyTail(t *testing.T) {
	// Reproduces the layer_index=1 bug: GPTQModel emits a `Forward rows N/64`
	// line per forward pass (dozens per second), which dominates the tail
	// window. The scanner must still find the most recent `completed layer N`
	// event even when hundreds of noise lines separate it from older events.
	var b strings.Builder
	b.WriteString(`{"event": "progress", "detail": "completed layer 1 | gpu_alloc=900MB"}` + "\n")
	for layer := 2; layer <= 50; layer++ {
		for row := 0; row < 64; row++ {
			b.WriteString("Forward: Layer=`model.layers.")
			b.WriteString(strconv.Itoa(layer))
			b.WriteString("`, subset=1/2, batches=64 Forward rows ")
			b.WriteString(strconv.Itoa(row))
			b.WriteString("/64 | 0:00:05 / 0:05:00\n")
		}
		b.WriteString(`{"event": "progress", "detail": "completed layer `)
		b.WriteString(strconv.Itoa(layer))
		b.WriteString(` | gpu_alloc=1000MB"}` + "\n")
	}
	got := scanLatestQuantizationLayer(strings.NewReader(b.String()))
	if got != 50 {
		t.Errorf("scanLatestQuantizationLayer on noisy tail: got %d, want 50", got)
	}
}

func TestScanLatestQuantizationLayer_HandlesLongLine(t *testing.T) {
	// Scanner buffer is 1 MiB; a log line with lots of TQDM redraws before
	// the JSON event should still parse. This guards against breaking the
	// buffer tuning by accident.
	prefix := strings.Repeat(`> Quantizing layer 5 of 59 ['p' to ||] `, 200)
	log := prefix + `{"event": "progress", "detail": "completed layer 17 | gpu_alloc=999MB"}`
	got := scanLatestQuantizationLayer(strings.NewReader(log))
	if got != 17 {
		t.Errorf("scanLatestQuantizationLayer with long line: got %d, want 17", got)
	}
}
