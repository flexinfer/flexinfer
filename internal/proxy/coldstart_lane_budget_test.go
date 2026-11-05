package proxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// A cold-started request drains from the queue through trackAndServe, which
// never passes rejectLane. The lane's first-byte budget must therefore be
// loaded before the request is queued, or a lane whose first request after a
// proxy restart is a cold start runs under the 120 s default. KT-FLEX
// 2026-11-03: a cold Wan 2.1 clip (lane budget 600 s) was cut at 120 s while
// the card was still rendering.
func TestColdStartLoadsTheLaneBudgetBeforeQueueing(t *testing.T) {
	budget := map[string]string{"flexinfer.ai/lane-first-byte-budget": "600s"}
	video := queuedBehind(rerouteModel("video", 500, "", budget), "audio")
	f := newRerouteFixture(t, video, rerouteModel("audio", 500, aiv1alpha2.ModelPhaseReady, nil))
	f.p.laneConfig.LaneBreakerThreshold = 3
	f.p.laneConfig.LaneFirstByteBudget = 2 * time.Minute

	f.serve(t, chatReq("video"))

	require.Positive(t, f.act.touches, "the request entered the lane's cold start")
	lane := f.p.lane("video")
	lane.mu.Lock()
	defer lane.mu.Unlock()
	assert.Equal(t, 600*time.Second, lane.budget, "the queued request is served under the lane's annotated budget")
}

func TestColdStartWithoutABudgetKeepsTheDefault(t *testing.T) {
	video := queuedBehind(rerouteModel("video", 500, "", nil), "audio")
	f := newRerouteFixture(t, video, rerouteModel("audio", 500, aiv1alpha2.ModelPhaseReady, nil))
	f.p.laneConfig.LaneBreakerThreshold = 3
	f.p.laneConfig.LaneFirstByteBudget = 2 * time.Minute

	f.serve(t, chatReq("video"))

	require.Positive(t, f.act.touches)
	lane := f.p.lane("video")
	lane.mu.Lock()
	defer lane.mu.Unlock()
	assert.Equal(t, 2*time.Minute, lane.budget, "no annotation leaves the proxy default")
}
