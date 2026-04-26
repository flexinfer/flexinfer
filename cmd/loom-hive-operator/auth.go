package main

import (
	"net/http"
	"os"
	"strings"
	"sync/atomic"
)

// adminTokenEnv is the env var the operator reads on startup to populate
// the admin token. Empty / unset means mutating endpoints reject all
// callers — fail-closed is the right default for production.
const adminTokenEnv = "LOOM_HIVE_ADMIN_TOKEN"

// adminToken stores the operator's expected bearer token. Atomic so
// future hot-reload paths (e.g. K8s Secret rotation) can swap it in
// without restart.
var adminToken atomic.Value // string

// setAdminToken installs the expected token. Call once at startup.
func setAdminToken(t string) {
	adminToken.Store(strings.TrimSpace(t))
}

// loadAdminTokenFromEnv reads adminTokenEnv into the atomic store. Safe
// to call from main(); separated for testability.
func loadAdminTokenFromEnv() {
	setAdminToken(os.Getenv(adminTokenEnv))
}

// requireAdmin is the middleware mutating endpoints wrap themselves in.
// Behaviour:
//   - No token configured (env unset): always 401. This makes
//     "production with no admin token" fail-closed instead of fail-open.
//   - Token configured: require Authorization: Bearer <token>; reject
//     with 401 + WWW-Authenticate on miss.
//
// The gate is implemented as a wrapper, not a global router middleware,
// so read-only handlers stay zero-overhead and so 401s only fire for
// endpoints that actually mutate state.
func requireAdmin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected, _ := adminToken.Load().(string)
		if expected == "" {
			http.Error(w,
				"admin token not configured (set "+adminTokenEnv+" on the operator)",
				http.StatusUnauthorized)
			return
		}
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(got, "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="loom-hive"`)
			http.Error(w, "missing Bearer token", http.StatusUnauthorized)
			return
		}
		if subtleEqual(strings.TrimPrefix(got, "Bearer "), expected) {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="loom-hive", error="invalid_token"`)
		http.Error(w, "invalid Bearer token", http.StatusUnauthorized)
	}
}

// subtleEqual is a constant-time string comparison so token checks are
// resistant to timing-side-channel probes. A malicious caller can still
// learn token length, but we trust the deployment to keep length out of
// the threat model.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
