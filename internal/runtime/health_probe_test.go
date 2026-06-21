package runtime

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// TestHealthProbeFor_SelectsByProbeType is a regression guard for the runtime
// "Ready without an actual load" gap: a backend whose readiness probe is a TCP
// socket (ComfyUI, Steam) must be actively dialed before the model is marked
// Ready, not timer-marked. healthProbeFor must return a working closure for
// both HTTP and TCP probes, and nil only when the probe exposes neither (the
// single path allowed to mark Ready on the startup timer alone).
func TestHealthProbeFor_SelectsByProbeType(t *testing.T) {
	t.Run("nil probe -> no closure (timer fallback)", func(t *testing.T) {
		fn, desc := healthProbeFor(nil, 8000)
		assert.Nil(t, fn)
		assert.Empty(t, desc)
	})

	t.Run("probe without HTTP or TCP -> no closure", func(t *testing.T) {
		exec := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{Command: []string{"true"}},
		}}
		fn, desc := healthProbeFor(exec, 8000)
		assert.Nil(t, fn)
		assert.Empty(t, desc)
	})

	t.Run("HTTP probe dials runtime port + path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		port := int32(srv.Listener.Addr().(*net.TCPAddr).Port)

		probe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{Path: "/health", Port: intstr.FromInt32(8000)},
		}}
		fn, desc := healthProbeFor(probe, port)
		require.NotNil(t, fn)
		assert.Contains(t, desc, "http")
		assert.True(t, fn(), "live /health endpoint should report healthy")

		// Wrong path -> 404 -> unhealthy, proving the path is honored.
		badProbe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{Path: "/nope"},
		}}
		badFn, _ := healthProbeFor(badProbe, port)
		require.NotNil(t, badFn)
		assert.False(t, badFn())
	})

	t.Run("TCP probe dials runtime port", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = ln.Close() }()
		port := int32(ln.Addr().(*net.TCPAddr).Port)

		// Probe's declared port is intentionally different from the runtime
		// port to prove healthProbeFor dials the runtime-managed port.
		probe := &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(8188)},
		}}
		fn, desc := healthProbeFor(probe, port)
		require.NotNil(t, fn)
		assert.Contains(t, desc, "tcp")
		assert.True(t, fn(), "open listener should report healthy")
	})
}

// TestCheckTCPHealth verifies the dial helper distinguishes an open socket from
// a closed one — the actual proof-of-serving for TCP-probe backends.
func TestCheckTCPHealth(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	assert.True(t, checkTCPHealth(addr), "open listener should be reachable")

	require.NoError(t, ln.Close())
	assert.False(t, checkTCPHealth(addr), "closed listener should be unreachable")
}
