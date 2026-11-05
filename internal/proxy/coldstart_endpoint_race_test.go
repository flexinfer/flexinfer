package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
)

// The controller marks a Model Ready from the same pod readiness that puts the
// pod into the Service's EndpointSlice, and kube-proxy programs the ClusterIP
// some time after that. Until it does, a dial to the ClusterIP from the
// proxy's node is REJECTed. KT-FLEX 2026-11-03 15:23:49Z: the first request
// drained from a cold Wan 2.1 queue was refused on both attempts, 50 ms apart,
// and got a 502; a request 150 ms later was served.

// clusterIPDialer stands in for a model Service's ClusterIP: a dial is refused
// until lag after the first one, when kube-proxy has programmed the endpoint,
// and reaches the live backend from then on.
type clusterIPDialer struct {
	refused, live string
	lag           time.Duration

	mu       sync.Mutex
	first    time.Time
	refusals int
}

func (d *clusterIPDialer) dial(ctx context.Context, network, _ string) (net.Conn, error) {
	d.mu.Lock()
	if d.first.IsZero() {
		d.first = time.Now()
	}
	addr := d.live
	if time.Since(d.first) < d.lag {
		addr = d.refused
		d.refusals++
	}
	d.mu.Unlock()
	var dialer net.Dialer
	return dialer.DialContext(ctx, network, addr)
}

func (d *clusterIPDialer) refusedDials() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refusals
}

// readyOnScaleUp is the controller in miniature: scaling a lane up marks its
// Model Ready. It patches rather than updates, because the proxy's demand
// heartbeat writes the same Model's status concurrently.
type readyOnScaleUp struct{ c client.Client }

func (a readyOnScaleUp) TriggerScaleUp(ctx context.Context, name string) error {
	m := &aiv1alpha2.Model{}
	if err := a.c.Get(ctx, client.ObjectKey{Namespace: "default", Name: name}, m); err != nil {
		return err
	}
	base := m.DeepCopy()
	m.Status.Phase = aiv1alpha2.ModelPhaseReady
	return a.c.Status().Patch(ctx, m, client.MergeFrom(base))
}

func (readyOnScaleUp) TouchLastActiveTime(context.Context, string) {}

func (readyOnScaleUp) GetColdStartTimeout(context.Context, string) time.Duration {
	return 5 * time.Second
}

func (readyOnScaleUp) IsNodeTerminating(context.Context, string) bool { return false }

// newEndpointRaceProxy serves Model "wan", in phase, through a ClusterIP whose
// endpoint kube-proxy programs lag after the first dial.
func newEndpointRaceProxy(t *testing.T, phase aiv1alpha2.ModelPhase, lag time.Duration) (*Proxy, *clusterIPDialer) {
	t.Helper()
	model := rerouteModel("wan", 500, phase, nil)
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(model).WithStatusSubresource(model).Build()

	p := newGateTestProxyWithClient(t, readyOnScaleUp{c: c}, c)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p.ctx, p.cancel = ctx, cancel
	p.laneConfig.LaneBreakerThreshold = 3
	p.laneConfig.LaneFirstByteBudget = 2 * time.Minute

	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(live.Close)
	gone := httptest.NewServer(http.NotFoundHandler())
	refused := gone.Listener.Addr().String()
	gone.Close()

	d := &clusterIPDialer{refused: refused, live: live.Listener.Addr().String(), lag: lag}
	svc := k8surl.ServiceURL("wan", p.namespace, p.getBackendPort(ctx, "wan"), true)
	rp, ok := p.loadOrCreateProxy(svc)
	require.True(t, ok)
	rp.Transport = &http.Transport{DialContext: d.dial}
	return p, d
}

func TestColdStartDrainWaitsOutTheUnprogrammedClusterIP(t *testing.T) {
	p, d := newEndpointRaceProxy(t, "", 400*time.Millisecond)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("wan"))

	assert.Equal(t, http.StatusOK, rec.Code, "the first drained request is served once the endpoint is programmed: %s", rec.Body.String())
	assert.Greater(t, d.refusedDials(), defaultMaxForwardAttempts, "the ClusterIP refused more dials than a Ready lane retries")
}

func TestReadyLaneKeepsTheShortDialRetry(t *testing.T) {
	p, d := newEndpointRaceProxy(t, aiv1alpha2.ModelPhaseReady, time.Minute)

	start := time.Now()
	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("wan"))

	assert.Equal(t, http.StatusBadGateway, rec.Code)
	assert.Equal(t, defaultMaxForwardAttempts, d.refusedDials(), "a request to a Ready lane retries a refused dial once")
	assert.Less(t, time.Since(start), time.Second)
}

func TestColdStartDrainGivesUpAfterTheDialWindow(t *testing.T) {
	p, d := newEndpointRaceProxy(t, "", time.Minute)
	p.coldStartDialWindow = 300 * time.Millisecond

	rec := httptest.NewRecorder()
	p.handleRequest(rec, chatReq("wan"))

	assert.Equal(t, http.StatusBadGateway, rec.Code, "a ClusterIP that is never programmed still ends in a 502")
	assert.Greater(t, d.refusedDials(), defaultMaxForwardAttempts)
}

func TestColdStartRetryBackoff(t *testing.T) {
	want := []time.Duration{
		50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond,
		400 * time.Millisecond, 800 * time.Millisecond, time.Second, time.Second,
	}
	for attempt, w := range want {
		assert.Equal(t, w, coldStartRetryBackoff(attempt), "after refused dial %d", attempt+1)
	}
}
