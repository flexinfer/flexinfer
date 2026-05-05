package daemon

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
)

// publishEnvelope mirrors pkg/eventpub.envelope so out-of-process publishers
// can post structured events to the daemon EventBus without sharing types.
type publishEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// HandleEventsPublish accepts JSON envelopes from out-of-process publishers
// (mcp-agent-context et al.) and republishes them on the daemon EventBus.
// This is the inbound complement to ServeSSE: it lets sibling processes
// inject events into the bus without importing internal/daemon directly.
//
// Auth: when an admin token is configured, callers must supply it as a
// Bearer header. With no admin token configured, only localhost callers are
// accepted (matching the security posture of the rest of the daemon HTTP
// surface, which is meant for in-host tool integration).
func (d *Daemon) HandleEventsPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !d.eventsPublishAuthorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	var env publishEnvelope
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&env); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(env.Type) == "" {
		http.Error(w, "type required", http.StatusBadRequest)
		return
	}

	// Decode Data into a generic any so EventBus subscribers receive a
	// shape symmetric to in-process Publish callers (they pass typed
	// structs which json-encode the same way as the raw message we got).
	var data any
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			http.Error(w, "invalid data: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	d.eventBus.Publish(EventType(env.Type), data)
	w.WriteHeader(http.StatusNoContent)
}

// eventsPublishAuthorized enforces auth for /events/publish. When an admin
// token is configured (file config's embedded_hud.admin_token, or
// LOOM_HUD_ADMIN_TOKEN / HUD_ADMIN_TOKEN env vars — same precedence the
// HUD itself uses), a matching Bearer header is required. When no token is
// configured, only loopback callers are accepted (no implicit anyone-on-
// the-network access — that would be surprising for an event-injection
// endpoint).
func (d *Daemon) eventsPublishAuthorized(r *http.Request) bool {
	token := firstNonEmptyStr(
		d.fileCfg.EmbeddedHUD.AdminToken,
		os.Getenv("LOOM_HUD_ADMIN_TOKEN"),
		os.Getenv("HUD_ADMIN_TOKEN"),
	)
	if token != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			return false
		}
		return strings.TrimSpace(strings.TrimPrefix(auth, prefix)) == token
	}
	// No admin token configured — restrict to loopback only.
	return remoteIsLoopback(r)
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func remoteIsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Unix socket or similar non-IP transport — treat as local-trust.
		return true
	}
	return ip.IsLoopback()
}
