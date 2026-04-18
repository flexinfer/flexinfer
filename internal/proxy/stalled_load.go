package proxy

import (
	"errors"
	"fmt"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// defaultStalledLoadThreshold is how long a Model may sit in
// Phase=Loading / Substage=LoadingWeights without the LoadingProgressAt
// timestamp advancing before the proxy treats it as stalled and fails fresh
// requests fast with 503 + Retry-After.
//
// Chosen to be several times larger than a healthy per-shard rate (~2 s/it on
// local NVMe, ~10-20 s/it on acceptable network storage) so transient Longhorn
// or disk hiccups do not trip fail-fast, while the obviously-stuck case
// (shard-31-at-141s/it kind of stall) does.
const defaultStalledLoadThreshold = 120 * time.Second

// defaultStalledLoadRetryAfter is the Retry-After hint sent back with a 503 when
// a load is considered stalled. Picked so clients back off instead of spinning.
const defaultStalledLoadRetryAfter = 30

// StalledLoadError is returned when a Model's LoadingWeights substage has had
// no LoadingProgressAt advancement for at least the configured threshold.
// Carries the substage and progress age so the HTTP path can emit a useful
// 503 body and Retry-After.
type StalledLoadError struct {
	Model       string
	Substage    aiv1alpha2.LoadingSubstage
	ProgressAge time.Duration
	Message     string
}

func (e *StalledLoadError) Error() string {
	if e == nil {
		return "stalled load"
	}
	msg := e.Message
	if msg == "" {
		msg = "no progress"
	}
	return fmt.Sprintf(
		"model %q load stalled at substage=%s (no progress for %s): %s",
		e.Model, e.Substage, e.ProgressAge.Round(time.Second), msg,
	)
}

// isStalledLoadError reports whether err (or anything wrapped in err) is a
// *StalledLoadError.
func isStalledLoadError(err error) (*StalledLoadError, bool) {
	var s *StalledLoadError
	if errors.As(err, &s) {
		return s, true
	}
	return nil, false
}

// detectStalledLoad checks whether a Model is currently in a wedged cold start.
// Returns a *StalledLoadError when the conditions are met, nil otherwise.
//
// The heuristic is intentionally narrow: only LoadingWeights stalls are flagged
// today. ImagePulling can legitimately take 5-10 min on a cold node cache, and
// Compiling / HealthCheckPending are short enough that a per-pass timeout
// (cold-start deadline) catches them already. The thing that needed an explicit
// stall signal is shard-load wedging on the read path.
func detectStalledLoad(model *aiv1alpha2.Model, threshold time.Duration) *StalledLoadError {
	if model == nil {
		return nil
	}
	if model.Status.Phase != aiv1alpha2.ModelPhaseLoading {
		return nil
	}
	if model.Status.LoadingSubstage != aiv1alpha2.LoadingSubstageLoadingWeights {
		return nil
	}
	if model.Status.LoadingProgressAt == nil {
		return nil
	}
	age := time.Since(model.Status.LoadingProgressAt.Time)
	if age < threshold {
		return nil
	}
	return &StalledLoadError{
		Model:       model.Name,
		Substage:    model.Status.LoadingSubstage,
		ProgressAge: age,
		Message:     model.Status.Message,
	}
}
