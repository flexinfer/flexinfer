package main

import (
	"os"
	"testing"
	"time"
)

func TestProxyIdleTimeout_DefaultIsNonZero(t *testing.T) {
	// Verify that proxyIdleExitTimeout returns a non-zero default,
	// ensuring the idle timer will always be active in runProxy.
	saved := proxyConfigGlobal
	defer func() { proxyConfigGlobal = saved }()

	os.Unsetenv(loomProxyIdleExitSecondsEnv)
	proxyConfigGlobal.IdleExitSeconds = 0

	d := proxyIdleExitTimeout()
	if d <= 0 {
		t.Fatalf("proxyIdleExitTimeout() = %v, want > 0", d)
	}
	if d != time.Duration(defaultProxyIdleExitSeconds)*time.Second {
		t.Fatalf("proxyIdleExitTimeout() = %v, want %v", d, time.Duration(defaultProxyIdleExitSeconds)*time.Second)
	}
}

func TestProxyIdleTimeout_MinBoundEnforced(t *testing.T) {
	// Even with a very low value, the min bound (30s) is enforced.
	os.Setenv(loomProxyIdleExitSecondsEnv, "1")
	defer os.Unsetenv(loomProxyIdleExitSecondsEnv)

	saved := proxyConfigGlobal
	defer func() { proxyConfigGlobal = saved }()
	proxyConfigGlobal.IdleExitSeconds = 0

	d := proxyIdleExitTimeout()
	if d < 30*time.Second {
		t.Fatalf("proxyIdleExitTimeout() = %v, want >= 30s", d)
	}
}
