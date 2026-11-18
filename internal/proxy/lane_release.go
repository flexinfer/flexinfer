package proxy

import (
	"context"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Lane release: the job runner's signal that a shared-GPU lane it woke is no
// longer needed. A job's forward stamps demand like any request, so without a
// release the lane keeps its card for the whole serverless.idleTimeout after
// the last job, and the lane the election parked to make room waits that
// long to come back. The release is status.releasedAt on the Model; the
// controller treats a release that no demand answers as "idle now"
// (controllers/model_release.go).
//
// The lane's last-access writer owns the write, so it lands after the job's
// own last demand stamp and is dropped when a request is live on the lane.
// Every later demand stamp clears the field (patchLastActiveTime), and a
// standing release makes the next stamp unskippable (touchLastAccess), so a
// request on any replica cancels a release within one heartbeat.

// defaultReleaseInteractiveWindow stands in for a lane's idle timeout when
// its spec leaves the backend default in place.
const defaultReleaseInteractiveWindow = 5 * time.Minute

type jobForwardKey struct{}

// jobForwardContext marks ctx as carrying a job's forward through the proxy's
// own handler, so the lane does not count the request as interactive use.
func jobForwardContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, jobForwardKey{}, true)
}

func isJobForward(ctx context.Context) bool {
	marked, _ := ctx.Value(jobForwardKey{}).(bool)
	return marked
}

// Results of a release decision, for flexinfer_jobs_lane_releases_total.
const (
	laneReleaseRequested       = "released"
	laneReleaseKeptInteractive = "kept_interactive"
	laneReleaseNotApplicable   = "not_applicable"
)

// releaseJobLane hands lane's shared GPU back after the job runner has no
// further use for it. It keeps the normal idle window when a client used the
// lane within one idle timeout: that caller's next request is what the window
// is for.
func (p *Proxy) releaseJobLane(ctx context.Context, lane string) string {
	m := p.jobLaneModel(ctx, lane)
	if m == nil || !m.Spec.IsShared() || !m.Spec.IsServerless() {
		return laneReleaseNotApplicable
	}
	window := defaultReleaseInteractiveWindow
	if m.Spec.Serverless != nil && m.Spec.Serverless.IdleTimeout != nil && m.Spec.Serverless.IdleTimeout.Duration > 0 {
		window = m.Spec.Serverless.IdleTimeout.Duration
	}
	heartbeat := p.lastAccessHeartbeatFor(m.Name)
	heartbeat.mu.Lock()
	interactive := !heartbeat.interactiveAt.IsZero() && time.Since(heartbeat.interactiveAt) < window
	if !interactive {
		heartbeat.release = true
	}
	heartbeat.mu.Unlock()
	if interactive {
		slog.Info("job lane keeps its idle window: a client used it recently", "model", m.Name, "window", window.String())
		return laneReleaseKeptInteractive
	}
	select {
	case heartbeat.wake <- struct{}{}:
	default: // a wake-up is already pending
	}
	return laneReleaseRequested
}

// writeLaneRelease stamps status.releasedAt with a status merge patch that
// carries only that field (see patchLastActiveTime). It reports whether the
// release was written.
func (p *Proxy) writeLaneRelease(ctx context.Context, modelName string) bool {
	if p.client == nil {
		return false
	}
	m := &aiv1alpha2.Model{}
	if err := p.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: p.namespace}, m); err != nil {
		slog.Debug("lane release: reading the model failed", "model", modelName, "error", err)
		return false
	}
	base := m.DeepCopy()
	now := metav1.Now()
	m.Status.ReleasedAt = &now
	if err := p.client.Status().Patch(ctx, m, client.MergeFrom(base)); err != nil {
		slog.Warn("lane release: writing status.releasedAt failed", "model", modelName, "error", err)
		return false
	}
	slog.Info("released the shared GPU lane after its last job", "model", modelName)
	return true
}
