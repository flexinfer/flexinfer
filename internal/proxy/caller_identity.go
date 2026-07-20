package proxy

import (
	"net/http"
)

// Caller attribution.
//
// The proxy fronts every model lane, but its logs historically carried no
// client identity: a misbehaving caller (e.g. one hammering a model with
// requests it cancels after a client-side timeout) showed up only as
// anonymous `upstream forward failed ... context canceled` pairs. These
// helpers capture the cheap, always-available identity fields once per
// request so both the request-start line and the forward-failure WARN can
// name the caller. No request bodies are read or logged.

// callerIdentity carries the client-attribution fields for one request:
// the propagated/generated request ID, the TCP peer address, any
// X-Forwarded-For chain (set when the client came through an ingress or
// another proxy), and the User-Agent.
type callerIdentity struct {
	requestID    string
	remoteAddr   string
	forwardedFor string
	userAgent    string
}

// callerIdentityFrom extracts the attribution fields from a request. The
// request ID is the one handleRequest stored in the context (inbound
// X-Request-ID or generated); it is empty for requests that bypass
// handleRequest (e.g. direct unit-test calls).
func callerIdentityFrom(r *http.Request) callerIdentity {
	id, _ := r.Context().Value(requestIDKey{}).(string)
	return callerIdentity{
		requestID:    id,
		remoteAddr:   r.RemoteAddr,
		forwardedFor: r.Header.Get("X-Forwarded-For"),
		userAgent:    r.Header.Get("User-Agent"),
	}
}

// logAttrs returns the identity fields as slog key/value pairs, ready to
// append to a log call's argument list.
func (c callerIdentity) logAttrs() []any {
	return []any{
		"request_id", c.requestID,
		"remote_addr", c.remoteAddr,
		"x_forwarded_for", c.forwardedFor,
		"user_agent", c.userAgent,
	}
}
