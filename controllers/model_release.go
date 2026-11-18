package controllers

import (
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// A release is the opposite of a demand stamp. The proxy's job runner writes
// status.releasedAt on a shared-GPU lane when the last background job that
// needed it has ended and nothing else is asking for it. Without it a lane
// that a job woke keeps its card for the whole serverless.idleTimeout after
// the job: the idle window exists so an interactive caller's next request
// finds the lane warm, and a finished job has no next request.
//
// A release counts only while no demand signal answers it: a lastActiveTime
// written after it, or an activation request still in its window, makes it
// inert. A stale releasedAt left on the object is therefore harmless.

// releaseGrace is how long a release must stand unanswered before the
// controller acts on it. A proxy replica with a request live on the lane
// sees the standing release at its next demand heartbeat (at most 30 s
// apart) and clears it whatever its write throttle says, so the grace is
// longer than one heartbeat.
const releaseGrace = 45 * time.Second

// releaseStanding reports whether the model carries a release that no demand
// signal has answered. The proxy clears releasedAt with every demand stamp,
// so a release that is still present was written after the last one; the
// lastActiveTime comparison only guards against a writer that does not clear
// it (an older proxy). A lastActiveTime in the same second does not answer
// the release: that is the job's own last stamp, and both serialize at
// second precision.
func releaseStanding(model *aiv1alpha2.Model, now time.Time) bool {
	if model == nil || model.Status.ReleasedAt == nil {
		return false
	}
	if activationRequestInWindowAt(model, now) {
		return false
	}
	if last := model.Status.LastActiveTime; last != nil && last.After(model.Status.ReleasedAt.Time) {
		return false
	}
	return true
}

// modelReleased reports whether a standing release has outlived its grace, so
// the model counts as having no demand.
func modelReleased(model *aiv1alpha2.Model, now time.Time) bool {
	return releaseStanding(model, now) && now.Sub(model.Status.ReleasedAt.Time) >= releaseGrace
}

// releaseGraceRemaining returns how long until a standing release takes
// effect, or 0 when there is none or it already has.
func releaseGraceRemaining(model *aiv1alpha2.Model, now time.Time) time.Duration {
	if !releaseStanding(model, now) {
		return 0
	}
	if left := releaseGrace - now.Sub(model.Status.ReleasedAt.Time); left > 0 {
		return left
	}
	return 0
}
