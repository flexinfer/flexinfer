package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func connectionActivityTestProxy(t *testing.T, idleTimeout time.Duration) (*Proxy, *aiv1alpha2.Model) {
	t.Helper()
	RegisterMetrics()
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "activity-model", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
			Serverless: &aiv1alpha2.ServerlessSpec{
				IdleTimeout: &metav1.Duration{Duration: idleTimeout},
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase:          aiv1alpha2.ModelPhaseReady,
			LastActiveTime: &metav1.Time{Time: time.Now().Add(-2 * time.Second).Truncate(time.Second)},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(Scheme).
		WithObjects(model).WithStatusSubresource(model).Build()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := &Proxy{
		client:    k8sClient,
		namespace: model.Namespace,
		resolver:  NewModelResolver(k8sClient, model.Namespace),
		ctx:       ctx,
		cancel:    cancel,
	}
	return p, model
}

func TestLastAccessIntervals(t *testing.T) {
	for _, tc := range []struct {
		name      string
		idle      time.Duration
		heartbeat time.Duration
		throttle  time.Duration
	}{
		{"short idle", 30 * time.Second, 5 * time.Second, 5 * time.Second},
		{"one minute idle", time.Minute, 10 * time.Second, 10 * time.Second},
		{"minimum cadence boundary", 6 * time.Second, time.Second, time.Second},
		{"one second idle", time.Second, time.Second, time.Second},
		{"subsecond idle", time.Nanosecond, time.Second, time.Second},
		{"long idle", 5 * time.Minute, 30 * time.Second, time.Minute},
		{"unset idle", 0, 30 * time.Second, time.Minute},
		{"invalid idle", -time.Second, 30 * time.Second, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			heartbeat, throttle := lastAccessIntervals(tc.idle)
			assert.Equal(t, tc.heartbeat, heartbeat)
			assert.Equal(t, tc.throttle, throttle)
		})
	}
}

func TestUpdateLastAccessIdleCadence(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		apiName := "Model"
		if legacy {
			apiName = "ModelDeployment"
		}
		t.Run(apiName, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				idle       time.Duration
				age        time.Duration
				force      bool
				wantUpdate bool
			}{
				{"short idle skips recent heartbeat", 30 * time.Second, 2 * time.Second, false, false},
				{"short idle refreshes before old minute throttle", 30 * time.Second, 10 * time.Second, false, true},
				{"long idle retains minute throttle", 5 * time.Minute, 45 * time.Second, false, false},
				{"forced request boundary bypasses throttle", 30 * time.Second, 2 * time.Second, true, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					old := metav1.NewTime(time.Now().Add(-tc.age).Truncate(time.Second))
					m := &aiv1alpha2.Model{
						ObjectMeta: metav1.ObjectMeta{Name: "activity-model", Namespace: "default"},
						Spec: aiv1alpha2.ModelSpec{Serverless: &aiv1alpha2.ServerlessSpec{
							IdleTimeout: &metav1.Duration{Duration: tc.idle},
						}},
						Status: aiv1alpha2.ModelStatus{LastActiveTime: &old},
					}
					idleSeconds := int32(tc.idle / time.Second)
					md := &aiv1alpha1.ModelDeployment{
						ObjectMeta: m.ObjectMeta,
						Spec:       aiv1alpha1.ModelDeploymentSpec{IdleTimeoutSeconds: &idleSeconds},
						Status:     aiv1alpha1.ModelDeploymentStatus{LastAccessTime: &old},
					}
					var object client.Object = m
					if legacy {
						object = md
					}
					k8sClient := fake.NewClientBuilder().WithScheme(Scheme).
						WithObjects(object).WithStatusSubresource(object).Build()
					p := &Proxy{client: k8sClient, namespace: "default"}
					p.touchLastAccess(context.Background(), object.GetName(), tc.force)
					require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(object), object))
					updated := m.Status.LastActiveTime
					if legacy {
						updated = md.Status.LastAccessTime
					}
					require.NotNil(t, updated)
					if tc.wantUpdate {
						assert.True(t, updated.After(old.Time), "activity timestamp must refresh")
					} else {
						assert.True(t, updated.Equal(&old), "throttled heartbeat must preserve the timestamp")
					}
				})
			}
		})
	}
}

func TestTrackAndServeRefreshesActivityAtCompletion(t *testing.T) {
	for _, tc := range []struct {
		name       string
		background bool
		cancel     bool
	}{
		{name: "foreground"},
		{name: "canceled foreground", cancel: true},
		{name: "background preserves demand", background: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, model := connectionActivityTestProxy(t, 30*time.Second)
			initial := model.Status.LastActiveTime.DeepCopy()
			requestCtx, cancelRequest := context.WithCancel(context.Background())
			defer cancelRequest()
			observed := make(chan *metav1.Time, 1)
			completionTime := make(chan time.Time, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				updated := &aiv1alpha2.Model{}
				if err := p.client.Get(context.Background(), client.ObjectKeyFromObject(model), updated); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				observed <- updated.Status.LastActiveTime.DeepCopy()
				if !tc.background {
					// Keep this recent enough that a normal heartbeat is throttled.
					// Completion must nevertheless open a fresh 30s idle window.
					updated.Status.LastActiveTime = &metav1.Time{Time: time.Now().Add(-2 * time.Second)}
					if err := p.client.Status().Update(context.Background(), updated); err != nil {
						t.Error(err)
					}
				}
				completionTime <- time.Now().Truncate(time.Second)
				if tc.cancel {
					cancelRequest()
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			p.directLoadTargets.Store(model.Name, server.URL)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"activity-model"}`)).WithContext(requestCtx)
			rec := httptest.NewRecorder()
			if tc.background {
				p.trackAndServeBackground(rec, req, model.Name, time.Now())
			} else {
				p.trackAndServe(rec, req, model.Name, time.Now())
			}

			updated := &aiv1alpha2.Model{}
			require.NoError(t, p.client.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
			atStart := <-observed
			atCompletion := <-completionTime
			if tc.background {
				assert.True(t, atStart.Equal(initial))
				assert.True(t, updated.Status.LastActiveTime.Equal(initial), "background completion must not create demand")
			} else {
				assert.True(t, atStart.After(initial.Time), "start must refresh even within the write throttle")
				assert.False(t, updated.Status.LastActiveTime.Time.Before(atCompletion), "completion must refresh even for canceled requests")
			}
			assert.Zero(t, p.GetActiveConnections(model.Name))
		})
	}
}

func TestTrackAndServeKeepsConcurrentLongRequestsActive(t *testing.T) {
	p, model := connectionActivityTestProxy(t, 30*time.Second)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	// Ensure the blocked upstream is released before closing its HTTP server.
	defer unblock()
	p.directLoadTargets.Store(model.Name, server.URL)
	var requests sync.WaitGroup
	defer func() {
		unblock()
		requests.Wait()
	}()
	for range 2 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"activity-model"}`))
			p.trackAndServe(httptest.NewRecorder(), req, model.Name, time.Now())
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("request did not reach the upstream")
		}
	}
	assert.Equal(t, int64(2), p.GetActiveConnections(model.Name))
	atStart := &aiv1alpha2.Model{}
	require.NoError(t, p.client.Get(context.Background(), client.ObjectKeyFromObject(model), atStart))
	require.NotNil(t, atStart.Status.LastActiveTime)
	require.Eventually(t, func() bool {
		updated := &aiv1alpha2.Model{}
		return p.client.Get(context.Background(), client.ObjectKeyFromObject(model), updated) == nil &&
			updated.Status.LastActiveTime != nil && updated.Status.LastActiveTime.After(atStart.Status.LastActiveTime.Time)
	}, 7*time.Second, 20*time.Millisecond, "in-flight requests must refresh before the short idle timeout")
	unblock()
	requests.Wait()
	assert.Zero(t, p.GetActiveConnections(model.Name))
}

// Pod-level connection tracking tests

func TestPodConnectionTracking(t *testing.T) {
	p := setupTestProxy(t)

	// Initially zero
	assert.Equal(t, int64(0), p.getPodConnections("10.42.0.5:8000"))

	// Increment
	p.incrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(1), p.getPodConnections("10.42.0.5:8000"))

	p.incrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(2), p.getPodConnections("10.42.0.5:8000"))

	// Decrement
	p.decrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(1), p.getPodConnections("10.42.0.5:8000"))

	p.decrementPodConnections("10.42.0.5:8000")
	assert.Equal(t, int64(0), p.getPodConnections("10.42.0.5:8000"))
}

func TestPodConnectionTracking_MultiplePods(t *testing.T) {
	p := setupTestProxy(t)

	podA := "10.42.0.10:8000"
	podB := "10.42.0.11:8000"

	p.incrementPodConnections(podA)
	p.incrementPodConnections(podA)
	p.incrementPodConnections(podB)

	assert.Equal(t, int64(2), p.getPodConnections(podA))
	assert.Equal(t, int64(1), p.getPodConnections(podB))

	// Decrementing one pod does not affect the other
	p.decrementPodConnections(podA)
	assert.Equal(t, int64(1), p.getPodConnections(podA))
	assert.Equal(t, int64(1), p.getPodConnections(podB))
}

func TestPodConnections_DecrementNonExistent(t *testing.T) {
	p := setupTestProxy(t)

	// Decrement a pod that was never tracked -- must not panic
	p.decrementPodConnections("10.42.99.99:8000")

	// Count should still be zero (no entry created)
	assert.Equal(t, int64(0), p.getPodConnections("10.42.99.99:8000"))
}

func TestGetPodConnections_UnknownPod(t *testing.T) {
	p := setupTestProxy(t)

	// A pod address that was never touched should return 0
	assert.Equal(t, int64(0), p.getPodConnections("192.168.1.1:11434"))
}
