package proxy

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Typed error envelope (D2-A of .loom/10-research-gfx1100-gfx906-next-
// differentiators-2026-10-31.md §6.2). Every non-2xx the proxy originates for a
// lane carries validation.FlexInferDetails: the lane's phase and loading
// substage, an ETA, the Retry-After it sent and an equivalent lane. Callers
// decide from error.code instead of matching message text; the messages keep
// their old wording so callers that still match text are not broken.

// headerActivation is set on every response to a request that entered the
// cold-start queue, including the eventual 200: the lane state the request
// found on arrival, "<phase>/<substage>", or "<phase>" with no substage.
const headerActivation = "X-FlexInfer-Activation"

// upstreamErrorType is the error type of a forward that reached no answer from
// a Ready lane (first-byte budget, transport failure).
const upstreamErrorType = "upstream_error"

// rerouteHintAnnotation names an equivalent lane on another card
// (flexinfer.ai/unavailable-reroute-to, G1). The envelope surfaces it as
// reroute_to whether or not this proxy reroutes to it itself.
const rerouteHintAnnotation = "flexinfer.ai/unavailable-reroute-to"

// Retry-After hints, in seconds, for states that do not resolve on a clock the
// proxy can read. A cold start's hint follows its ETA, clamped to
// [retryAfterColdStartMin, retryAfterColdStartMax].
const (
	retryAfterColdStartMin = 5
	retryAfterColdStartMax = 30
	retryAfterParked       = 60
	retryAfterPreempted    = 30
	retryAfterFailed       = 60
)

// laneError is one proxy-originated error about a lane. Zero retryAfter
// derives the hint from the code and the lane's ETA, negative sends none;
// empty rerouteTo falls back to the Model's rerouteHintAnnotation; a
// positive queuePosition is the caller's place in the lane's QoS queue.
type laneError struct {
	status        int
	errType       string
	code          string
	message       string
	param         string
	retryAfter    int
	rerouteTo     string
	queuePosition int
}

// writeLaneError writes e with the lane-state block built from m, which may be
// nil when the Model could not be read. A retry hint goes out as Retry-After
// on any status; a 503 always gets one (validation.WriteErrorWithDetails).
func (p *Proxy) writeLaneError(w http.ResponseWriter, m *aiv1alpha2.Model, e laneError) {
	if e.errType == "" {
		e.errType = validation.ErrorTypeServiceUnavailable
	}
	d := p.laneDetails(m, e, time.Now())
	if d.RetryAfterSeconds != nil && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", strconv.Itoa(*d.RetryAfterSeconds))
	}
	validation.WriteErrorWithDetails(w, e.status, e.message, e.errType, e.code, e.param, d)
}

// peekModel reads name's Model from the informer cache for an error body; nil
// when it cannot be read.
func (p *Proxy) peekModel(ctx context.Context, name string) *aiv1alpha2.Model {
	if p.client == nil || name == "" {
		return nil
	}
	m, err := p.getModel(ctx, name)
	if err != nil {
		return nil
	}
	return m
}

// laneDetails builds the flexinfer block for m at now.
func (p *Proxy) laneDetails(m *aiv1alpha2.Model, e laneError, now time.Time) *validation.FlexInferDetails {
	d := &validation.FlexInferDetails{}
	var eta *int
	if m != nil {
		if m.Status.Phase != "" {
			phase := string(m.Status.Phase)
			d.Phase = &phase
		}
		if m.Status.LoadingSubstage != "" {
			substage := string(m.Status.LoadingSubstage)
			d.LoadingSubstage = &substage
		}
		eta = p.loadETA(m, now)
	}
	if e.code == validation.CodeLaneUnresponsive && e.retryAfter > 0 {
		// A Ready lane with an open breaker serves again once its probe
		// passes, not after a load.
		reopen := e.retryAfter
		eta = &reopen
	}
	d.ETASeconds = eta
	reroute := e.rerouteTo
	if reroute == "" && m != nil {
		if hint := strings.TrimSpace(m.Annotations[rerouteHintAnnotation]); hint != m.Name {
			reroute = hint
		}
	}
	if reroute != "" {
		d.RerouteTo = &reroute
	}
	if pos := e.queuePosition; pos > 0 {
		d.QueuePosition = &pos
	}
	if retry := e.retryAfter; retry > 0 {
		d.RetryAfterSeconds = &retry
	} else if retry == 0 {
		if retry = retryAfterFor(e.code, eta); retry > 0 {
			d.RetryAfterSeconds = &retry
		}
	}
	return d
}

// retryAfterFor is the default Retry-After for code, 0 when the code has none
// (a 4xx the caller must change before retrying).
func retryAfterFor(code string, eta *int) int {
	switch code {
	case validation.CodeModelColdStarting, validation.CodeQueueFull:
		retry := retryAfterColdStartMax
		if eta != nil {
			retry = min(max(*eta, retryAfterColdStartMin), retryAfterColdStartMax)
		}
		return retry
	case validation.CodeModelParked:
		return retryAfterParked
	case validation.CodeModelPreempted:
		return retryAfterPreempted
	case validation.CodeModelFailed:
		return retryAfterFailed
	case validation.CodeStalledLoad:
		return defaultStalledLoadRetryAfter
	}
	return 0
}

// modelStateCode names why m is not serving, for errors raised while waiting
// on it (queue full, cold-start timeout, failed activation): Failed, parked
// behind a primary, preempted on its shared GPU, else cold starting.
func modelStateCode(m *aiv1alpha2.Model) string {
	switch {
	case m == nil:
		return validation.CodeModelColdStarting
	case m.Status.Phase == aiv1alpha2.ModelPhaseFailed:
		return validation.CodeModelFailed
	case parkedBehindPrimary(m):
		return validation.CodeModelParked
	case m.Status.Phase == aiv1alpha2.ModelPhasePreempted,
		m.Status.SharedGroup != nil && (m.Status.SharedGroup.State == "Preempted" || m.Status.SharedGroup.State == "Queued"):
		return validation.CodeModelPreempted
	}
	return validation.CodeModelColdStarting
}

// activationValue renders m's state for headerActivation.
func activationValue(m *aiv1alpha2.Model) string {
	if m == nil || m.Status.Phase == "" {
		return "Unknown"
	}
	if m.Status.LoadingSubstage == "" {
		return string(m.Status.Phase)
	}
	return string(m.Status.Phase) + "/" + string(m.Status.LoadingSubstage)
}

// substageFloorSeconds is the least an ETA may say while a load is in the
// given substage: a lane running past its p50 still has the rest of that
// substage ahead of it, so the ETA never collapses to zero before Ready.
var substageFloorSeconds = map[aiv1alpha2.LoadingSubstage]int{
	aiv1alpha2.LoadingSubstageImagePulling:       60,
	aiv1alpha2.LoadingSubstageInitializing:       30,
	aiv1alpha2.LoadingSubstageLoadingWeights:     30,
	aiv1alpha2.LoadingSubstageCompiling:          15,
	aiv1alpha2.LoadingSubstageHealthCheckPending: 5,
}

const defaultSubstageFloorSeconds = 15

// loadETA estimates the seconds until m is Ready from the lane's cold-start
// p50 for its serving path (loadP50) minus how long its current load has run.
// It returns nil when the proxy has no load history for the lane, or when
// readiness waits on something other than time (Failed, parked, preempted).
func (p *Proxy) loadETA(m *aiv1alpha2.Model, now time.Time) *int {
	switch modelStateCode(m) {
	case validation.CodeModelFailed, validation.CodeModelParked, validation.CodeModelPreempted:
		return nil
	}
	if m.Status.Phase == aiv1alpha2.ModelPhaseReady {
		zero := 0
		return &zero
	}
	p50, ok := p.loadP50(m)
	if !ok {
		return nil
	}
	eta := p50
	if m.Status.Phase == aiv1alpha2.ModelPhaseLoading {
		if started := loadStartedAt(m); !started.IsZero() && now.After(started) {
			eta -= now.Sub(started).Seconds()
		}
		floor, ok := substageFloorSeconds[m.Status.LoadingSubstage]
		if !ok {
			floor = defaultSubstageFloorSeconds
		}
		eta = max(eta, float64(floor))
	}
	secs := int(math.Ceil(eta))
	return &secs
}

// loadStartedAt is when m's current load began: the ModelReady=False
// transition the controller's cold-start histogram also measures from, else
// the last loading progress stamp.
func loadStartedAt(m *aiv1alpha2.Model) time.Time {
	for _, c := range m.Status.Conditions {
		if c.Type == aiv1alpha2.ConditionModelReady && c.Status == metav1.ConditionFalse && !c.LastTransitionTime.IsZero() {
			return c.LastTransitionTime.Time
		}
	}
	if m.Status.LoadingProgressAt != nil {
		return m.Status.LoadingProgressAt.Time
	}
	return time.Time{}
}

// preferredWait parses an RFC 7240 "Prefer: wait=N" header: how many seconds
// the caller will wait for a cold lane before it would rather have the
// envelope and retry on its own schedule.
func preferredWait(r *http.Request) (time.Duration, bool) {
	for _, v := range r.Header.Values("Prefer") {
		for _, pref := range strings.Split(v, ",") {
			name, value, ok := strings.Cut(strings.TrimSpace(strings.SplitN(pref, ";", 2)[0]), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "wait") {
				continue
			}
			secs, err := strconv.Atoi(strings.Trim(strings.TrimSpace(value), `"`))
			if err != nil || secs < 0 {
				continue
			}
			return time.Duration(secs) * time.Second, true
		}
	}
	return 0, false
}
