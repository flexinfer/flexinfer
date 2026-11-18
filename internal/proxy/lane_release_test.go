package proxy

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

func sharedCardLane(name string) *aiv1alpha2.Model {
	m := readyLane(name, "diffusers", map[string]string{constants.AnnotationServePaths: "/v1/images/generations"})
	m.Spec.GPU = &aiv1alpha2.GPUSpec{Shared: "xtx-flex"}
	return m
}

func (f *jobFixture) lane(t *testing.T, name string) *aiv1alpha2.Model {
	t.Helper()
	m := &aiv1alpha2.Model{}
	require.NoError(t, f.client.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: name}, m))
	return m
}

// settleReleases runs the pass that follows a job's end and waits for the
// lane writers.
func (f *jobFixture) settleReleases(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.p.jobs.runningCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	f.p.jobs.pass(context.Background())
	f.p.flushLastAccess(5 * time.Second)
}

func (f *jobFixture) queueJob(t *testing.T, name, model string, queuedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	job := &aiv1alpha2.InferenceJob{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: aiv1alpha2.InferenceJobSpec{Model: model, Path: "/v1/images/generations",
			Request: &apiextensionsv1.JSON{Raw: []byte(`{"model":"` + model + `","prompt":"x"}`)}},
	}
	require.NoError(t, f.client.Create(ctx, job))
	at := metav1.NewTime(queuedAt)
	job.Status = aiv1alpha2.InferenceJobStatus{Phase: aiv1alpha2.InferenceJobQueued, QueuedAt: &at}
	require.NoError(t, f.client.Status().Update(ctx, job))
}

func TestLastJobOnASharedCardReleasesItsLane(t *testing.T) {
	f := newJobFixture(t, sharedCardLane("qwen-image"))

	rec, obj := f.do(t, http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"qwen-image","prompt":"a lighthouse","background":true}`), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	done := f.waitJob(t, obj.ID)
	require.Equal(t, aiv1alpha2.InferenceJobCompleted, done.Status.Phase)
	f.settleReleases(t)

	m := f.lane(t, "qwen-image")
	require.NotNil(t, m.Status.ReleasedAt, "the lane is handed back once no job needs it")
	require.NotNil(t, m.Status.LastActiveTime, "the job's forward stamped demand")
	assert.False(t, m.Status.ReleasedAt.Before(m.Status.LastActiveTime),
		"the release is written after the job's last demand stamp")
}

func TestJobLaneKeepsItsIdleWindowAfterAClientUsedIt(t *testing.T) {
	f := newJobFixture(t, sharedCardLane("qwen-image"))

	rec, _ := f.do(t, http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"qwen-image","prompt":"interactive"}`), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec, obj := f.do(t, http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"qwen-image","prompt":"batch","background":true}`), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	f.waitJob(t, obj.ID)
	f.settleReleases(t)

	assert.Nil(t, f.lane(t, "qwen-image").Status.ReleasedAt,
		"a client rendered on this lane inside its idle window, so the window stays")
}

func TestOnlySharedCardJobLanesAreReleased(t *testing.T) {
	f := newJobFixture(t, readyLane("qwen-image", "diffusers",
		map[string]string{constants.AnnotationServePaths: "/v1/images/generations"}))

	rec, obj := f.do(t, http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"qwen-image","prompt":"x","background":true}`), nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	f.waitJob(t, obj.ID)
	f.settleReleases(t)

	assert.Nil(t, f.lane(t, "qwen-image").Status.ReleasedAt, "a lane with its own card keeps its idle window")
}

// The lane a finished job used is released only when neither a running job
// nor the key's next queued job uses it.
func TestReleaseLanesFollowsTheCardQueue(t *testing.T) {
	const key = "gpu/xtx-flex"
	tests := []struct {
		name        string
		wanted      map[string]map[string]bool
		wantRelease bool
	}{
		{name: "nothing else on the card", wanted: map[string]map[string]bool{}, wantRelease: true},
		{name: "the next job uses the same lane", wanted: map[string]map[string]bool{key: {"qwen-image": true}}},
		{name: "the next job uses another lane", wanted: map[string]map[string]bool{key: {"wan-t2v": true}}, wantRelease: true},
		{name: "another card's queue wants a lane of that name", wanted: map[string]map[string]bool{"gpu/other": {"qwen-image": true}}, wantRelease: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newJobFixture(t, sharedCardLane("qwen-image"), sharedCardLane("wan-t2v"))
			f.p.jobs.releasable["qwen-image"] = key

			f.p.jobs.releaseLanes(context.Background(), tt.wanted)
			f.p.flushLastAccess(5 * time.Second)

			_, stillRecorded := f.p.jobs.releasable["qwen-image"]
			assert.Equal(t, !tt.wantRelease, stillRecorded, "a lane that is still wanted stays recorded for a later pass")
			assert.Equal(t, tt.wantRelease, f.lane(t, "qwen-image").Status.ReleasedAt != nil)
		})
	}
}

// pass feeds releaseLanes the running jobs and each key's queue head: a
// finished image lane is released as soon as the card's next job is a video.
func TestPassReleasesAFinishedLaneWhenTheNextJobNeedsAnotherLane(t *testing.T) {
	f := newJobFixture(t, sharedCardLane("qwen-image"), sharedCardLane("wan-t2v"))
	ctx := context.Background()
	base := time.Now().Add(-time.Minute)
	f.queueJob(t, "job-video", "wan-t2v", base)
	f.queueJob(t, "job-image", "qwen-image", base.Add(time.Second))
	f.p.jobs.releasable["qwen-image"] = "gpu/xtx-flex"
	f.status.Store(http.StatusServiceUnavailable)
	jobRetryMinWait = time.Hour
	t.Cleanup(func() { jobRetryMinWait = 5 * time.Second })

	f.p.jobs.pass(ctx)
	f.p.flushLastAccess(5 * time.Second)
	t.Cleanup(func() {
		f.p.jobs.mu.Lock()
		for _, cancel := range f.p.jobs.running {
			cancel()
		}
		f.p.jobs.mu.Unlock()
	})

	assert.NotNil(t, f.lane(t, "qwen-image").Status.ReleasedAt,
		"the queue head is the video job, so the image lane gives the card up now")
	assert.Nil(t, f.lane(t, "wan-t2v").Status.ReleasedAt)
}

func TestReleaseIsDroppedWhileARequestIsLive(t *testing.T) {
	f := newJobFixture(t, sharedCardLane("qwen-image"))
	ctx := context.Background()

	stop := f.p.startLastAccessHeartbeat(jobForwardContext(ctx), "qwen-image")
	assert.Equal(t, laneReleaseRequested, f.p.releaseJobLane(ctx, "qwen-image"))
	f.p.flushLastAccess(5 * time.Second)
	assert.Nil(t, f.lane(t, "qwen-image").Status.ReleasedAt, "a live request is the demand a release would deny")

	stop()
	f.p.flushLastAccess(5 * time.Second)
	assert.Nil(t, f.lane(t, "qwen-image").Status.ReleasedAt, "a dropped release is not written later")
}
