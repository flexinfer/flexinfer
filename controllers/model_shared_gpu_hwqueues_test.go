package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// gfx906QueueProfile mirrors deploy/gpuprofiles/gfx906.yaml: 16 GB, multi-model,
// GPU_MAX_HW_QUEUES=1, 24 HWS queues with 2 reserved for pyannote.
func gfx906QueueProfile() *aiv1alpha2.GPUProfileSpec {
	return &aiv1alpha2.GPUProfileSpec{
		Architecture:     "gfx906",
		Vendor:           "amd",
		VRAMMB:           16384,
		Features:         aiv1alpha2.GPUFeatures{MultiModel: true},
		Env:              []corev1.EnvVar{{Name: "HSA_ENABLE_SDMA", Value: "0"}, {Name: backend.EnvGPUMaxHWQueues, Value: "1"}},
		MaxHWQueues:      24,
		ReservedHWQueues: 2,
	}
}

func gfx906QueueBound(profile *aiv1alpha2.GPUProfileSpec) hwQueueBound {
	return hwQueueBound{
		limit:    profile.MaxHWQueues - profile.ReservedHWQueues,
		reserved: profile.ReservedHWQueues,
		estimate: func(m *aiv1alpha2.Model) int32 {
			return backend.ModelHWQueueEstimate(m, profile, backend.GPUVendorAMD, "gfx906")
		},
	}
}

func radeonviiLane(name, backendName string, priority int32, vramMB int64, warm bool, phase aiv1alpha2.ModelPhase, demand *time.Time) *aiv1alpha2.Model {
	m := withVRAMEst(makeSharedModel(name, priority, phase, demand, nil), vramMB)
	m.Spec.Backend = backendName
	m.Spec.GPU.Shared = "radeonvii-models"
	m.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"}
	if warm {
		markWarmPinned(m)
	}
	return m
}

// radeonviiGroup is the deployed radeonvii-models group as read on 2026-10-30:
// four warm llama.cpp lanes Ready, the cold lanes Idle. demanded names the cold
// lanes that just saw proxy traffic.
func radeonviiGroup(now time.Time, demanded ...string) []*aiv1alpha2.Model {
	want := map[string]bool{}
	for _, n := range demanded {
		want[n] = true
	}
	cold := func(name, backendName string, priority int32, vramMB int64) *aiv1alpha2.Model {
		var demand *time.Time
		if want[name] {
			demand = timePtr(now)
		}
		return radeonviiLane(name, backendName, priority, vramMB, false, aiv1alpha2.ModelPhaseIdle, demand)
	}
	return []*aiv1alpha2.Model{
		radeonviiLane("bge-large-radeonvii", "llamacpp", 120, 1500, true, aiv1alpha2.ModelPhaseReady, timePtr(now)),
		radeonviiLane("bge-reranker-radeonvii", "llamacpp", 115, 700, true, aiv1alpha2.ModelPhaseReady, timePtr(now)),
		radeonviiLane("gte-qwen2-1p5b-radeonvii", "llamacpp", 114, 2600, true, aiv1alpha2.ModelPhaseReady, timePtr(now)),
		radeonviiLane("orpheus-3b-radeonvii", "llamacpp", 113, 3300, true, aiv1alpha2.ModelPhaseReady, nil),
		cold("humanizer", "llamacpp", 130, 11000),
		cold("openjev-0p8b-crossencoder-radeonvii", "vllm", 112, 3000),
		cold("gemma4-e4b-radeonvii", "llamacpp", 110, 4000),
		cold("qwen3-embedding-4b-radeonvii", "llamacpp", 89, 5600),
		cold("qwen3-reranker-0p6b-radeonvii", "llamacpp", 88, 1700),
	}
}

// TestHWQueueBoundIsLiveNeutralOnRadeonVII replays the live and the recently
// run radeonvii-models combinations: the queue bound must elect exactly the
// set the VRAM-only election elects.
func TestHWQueueBoundIsLiveNeutralOnRadeonVII(t *testing.T) {
	now := time.Now()
	profile := gfx906QueueProfile()
	queues := gfx906QueueBound(profile)

	cases := []struct {
		name     string
		demanded []string
		want     []string
		queues   int32
	}{
		{"live warm plane", nil,
			[]string{"bge-large-radeonvii", "bge-reranker-radeonvii", "gte-qwen2-1p5b-radeonvii", "orpheus-3b-radeonvii"}, 12},
		{"warm plane plus both Qwen3 lanes (2026-10-24)", []string{"qwen3-embedding-4b-radeonvii", "qwen3-reranker-0p6b-radeonvii"},
			[]string{"bge-large-radeonvii", "bge-reranker-radeonvii", "gte-qwen2-1p5b-radeonvii", "orpheus-3b-radeonvii", "qwen3-embedding-4b-radeonvii", "qwen3-reranker-0p6b-radeonvii"}, 18},
		{"warm plane plus the openjev vLLM judge", []string{"openjev-0p8b-crossencoder-radeonvii"},
			[]string{"bge-large-radeonvii", "bge-reranker-radeonvii", "gte-qwen2-1p5b-radeonvii", "orpheus-3b-radeonvii", "openjev-0p8b-crossencoder-radeonvii"}, 18},
		{"humanizer demand parks orpheus on VRAM", []string{"humanizer"},
			[]string{"humanizer", "bge-large-radeonvii", "bge-reranker-radeonvii", "gte-qwen2-1p5b-radeonvii"}, 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := radeonviiGroup(now, tc.demanded...)
			vramOnly := chooseSharedGroupLeaders(group, now, true, profile.VRAMMB, false)
			bounded := chooseSharedGroupLeadersBounded(group, now, true, profile.VRAMMB, queues, nil, false)
			assert.Equal(t, names(vramOnly), names(bounded), "the queue bound must not change a live combination")
			want := map[string]bool{}
			for _, n := range tc.want {
				want[n] = true
			}
			assert.Equal(t, want, names(bounded))
			assert.Equal(t, tc.queues, queues.sum(bounded))
			assert.LessOrEqual(t, queues.sum(bounded), queues.limit)
		})
	}
}

func TestHWQueueBoundCapsTheActiveSet(t *testing.T) {
	now := time.Now()
	profile := gfx906QueueProfile()
	queues := gfx906QueueBound(profile)

	// Nine small warm llama.cpp lanes fit 16 GB easily (900 MB) but not 22
	// queues: 7 x 3 = 21 fits, an eighth lane would make 24.
	var group []*aiv1alpha2.Model
	for i := range 9 {
		group = append(group, radeonviiLane(fmt.Sprintf("lane-%d", i), "llamacpp", int32(200-i), 100, true, aiv1alpha2.ModelPhaseReady, timePtr(now)))
	}
	assert.Len(t, chooseSharedGroupLeaders(group, now, true, profile.VRAMMB, false), 9, "VRAM alone admits all nine")
	bounded := chooseSharedGroupLeadersBounded(group, now, true, profile.VRAMMB, queues, nil, false)
	require.Len(t, bounded, 7)
	assert.Equal(t, int32(21), queues.sum(bounded))
	for i := range 7 {
		assert.True(t, names(bounded)[fmt.Sprintf("lane-%d", i)], "admission follows priority")
	}

	// A vLLM member (6 queues) does not fit beside six llama.cpp lanes (18),
	// but a lower-priority llama.cpp lane (3) still does.
	group = group[:6]
	group = append(group,
		radeonviiLane("judge", "vllm", 150, 1000, true, aiv1alpha2.ModelPhaseReady, timePtr(now)),
		radeonviiLane("small", "llamacpp", 50, 100, true, aiv1alpha2.ModelPhaseReady, timePtr(now)),
	)
	bounded = chooseSharedGroupLeadersBounded(group, now, true, profile.VRAMMB, queues, nil, false)
	assert.False(t, names(bounded)["judge"], "18 + 6 = 24 exceeds the 22-queue budget")
	assert.True(t, names(bounded)["small"], "18 + 3 = 21 fits")
	assert.Len(t, bounded, 7)
	assert.Equal(t, int32(21), queues.sum(bounded))

	// The primary is always included, as with the VRAM bound.
	huge := int32(40)
	solo := radeonviiLane("solo", "llamacpp", 100, 100, true, aiv1alpha2.ModelPhaseReady, timePtr(now))
	solo.Spec.GPU.HWQueueEstimate = &huge
	assert.Len(t, chooseSharedGroupLeadersBounded([]*aiv1alpha2.Model{solo}, now, true, profile.VRAMMB, queues, nil, false), 1)

	// Single-slot election ignores the bound.
	assert.Len(t, chooseSharedGroupLeadersBounded(group, now, false, profile.VRAMMB, queues, nil, false), 1)
}

func TestStaticallyParkedInGroupBoundedQueues(t *testing.T) {
	now := time.Now()
	profile := gfx906QueueProfile()
	queues := gfx906QueueBound(profile)

	leader := radeonviiLane("leader", "llamacpp", 300, 100, true, aiv1alpha2.ModelPhaseReady, timePtr(now))
	member := radeonviiLane("member", "llamacpp", 100, 100, false, aiv1alpha2.ModelPhaseIdle, nil)
	active := []*aiv1alpha2.Model{leader}
	for i := range 6 {
		active = append(active, radeonviiLane(fmt.Sprintf("warm-%d", i), "llamacpp", 250, 100, true, aiv1alpha2.ModelPhaseReady, timePtr(now)))
	}
	// 7 x 3 = 21 queues: the member's 3 would make 24 > 22.
	assert.True(t, staticallyParkedInGroupBounded(member, leader, active, true, profile.VRAMMB, queues, nil))
	assert.False(t, staticallyParkedInGroup(member, leader, active, true, profile.VRAMMB), "VRAM alone still fits")
	// With room in the queue budget the member stays promotable by demand.
	assert.False(t, staticallyParkedInGroupBounded(member, leader, active[:3], true, profile.VRAMMB, queues, nil))
	// An unbounded VRAM budget no longer means "never parked" when queues bind.
	assert.True(t, staticallyParkedInGroupBounded(member, leader, active, true, 0, queues, nil))
	assert.False(t, staticallyParkedInGroupBounded(member, leader, active, true, 0, hwQueueBound{}, nil))
}

func TestHandleSharedGPUPublishesHWQueueGauge(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))

	now := time.Now()
	group := radeonviiGroup(now)
	objs := make([]client.Object, 0, len(group))
	for _, m := range group {
		objs = append(objs, m)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(&aiv1alpha2.Model{}).Build()
	profiles := &GPUProfileReconciler{}
	profiles.profiles.Store("gfx906", gfx906QueueProfile())
	r := &ModelReconciler{Client: cl, Scheme: s, Recorder: &FakeEventRecorder{}, GPUProfiles: profiles}

	got := &aiv1alpha2.Model{}
	require.NoError(t, cl.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "bge-large-radeonvii"}, got))
	_, err := r.handleSharedGPU(t.Context(), got)
	require.NoError(t, err)
	assert.Equal(t, "Active", got.Status.SharedGroup.State)

	gauge := func(kind string) float64 {
		return testutil.ToFloat64(metrics.SharedGroupHWQueues.WithLabelValues("radeonvii-models", "default", "gfx906", kind))
	}
	assert.Equal(t, 12.0, gauge("estimated"))
	assert.Equal(t, 22.0, gauge("budget"))
	assert.Equal(t, 2.0, gauge("reserved"))

	// Without maxHWQueues the bound is disabled.
	plain := gfx906QueueProfile()
	plain.MaxHWQueues, plain.ReservedHWQueues = 0, 0
	profiles.profiles.Store("gfx906", plain)
	bound, arch := r.sharedGroupHWQueueBound(got)
	assert.False(t, bound.enabled())
	assert.Equal(t, "gfx906", arch)
}

func TestValidateHWQueueFit(t *testing.T) {
	profiles := &GPUProfileReconciler{}
	profiles.profiles.Store("gfx906", gfx906QueueProfile())
	r := &ModelReconciler{GPUProfiles: profiles}

	lane := radeonviiLane("lane", "llamacpp", 100, 1000, false, aiv1alpha2.ModelPhaseIdle, nil)
	require.NoError(t, r.validateHWQueueFit(lane, backend.GPUVendorAMD, "gfx906"))
	judge := radeonviiLane("judge", "vllm", 100, 1000, false, aiv1alpha2.ModelPhaseIdle, nil)
	require.NoError(t, r.validateHWQueueFit(judge, backend.GPUVendorAMD, "gfx906"))

	tooMany := int32(23)
	lane.Spec.GPU.HWQueueEstimate = &tooMany
	err := r.validateHWQueueFit(lane, backend.GPUVendorAMD, "gfx906")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "budget of 22")

	// No queue budget on the profile, or an unknown arch: no-op.
	require.NoError(t, r.validateHWQueueFit(lane, backend.GPUVendorAMD, "gfx1100"))
	plain := gfx906QueueProfile()
	plain.MaxHWQueues = 0
	profiles.profiles.Store("gfx906", plain)
	require.NoError(t, r.validateHWQueueFit(lane, backend.GPUVendorAMD, "gfx906"))
}

func TestRuntimeClientMapsQueueRefusal(t *testing.T) {
	for _, tc := range []struct {
		body string
		want error
	}{
		{fmt.Sprintf(`{"error":"%s: cannot load \"q\": insufficient hardware compute queues: ..."}`, pkgrt.ErrInsufficientHWQueues), pkgrt.ErrInsufficientHWQueues},
		{fmt.Sprintf(`{"error":"%s: cannot load \"v\": insufficient VRAM"}`, pkgrt.ErrInsufficientVRAM), pkgrt.ErrInsufficientVRAM},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, tc.body, http.StatusServiceUnavailable)
		}))
		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		var port int
		_, err = fmt.Sscanf(u.Port(), "%d", &port)
		require.NoError(t, err)
		ep := &RuntimeEndpoint{PodIP: u.Hostname(), Port: int32(port)}
		err = (&RuntimeReconciler{}).LoadModel(context.Background(), ep, "m", []byte(`{}`))
		server.Close()
		require.True(t, errors.Is(err, tc.want), "got %v", err)
		assert.True(t, isRuntimeCapacityError(err))
	}
	assert.Equal(t, "Waiting for previous models to release GPU hardware compute queues",
		runtimeCapacityMessage(fmt.Errorf("x: %w", pkgrt.ErrInsufficientHWQueues)))
	assert.Equal(t, "Waiting for previous models to release GPU memory",
		runtimeCapacityMessage(fmt.Errorf("x: %w", pkgrt.ErrInsufficientVRAM)))
	assert.False(t, isRuntimeCapacityError(errors.New("boom")))
}
