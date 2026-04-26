package hive

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// operatorProxy is a thin reverse proxy that forwards /api/hive/* from
// the HUD to the in-cluster loom-hive-operator. It:
//
//   - rewrites Host so the upstream sees its own service name
//   - injects Authorization: Bearer <admin-token> when the request is a
//     mutation (POST/PUT/PATCH/DELETE) AND the caller didn't already
//     supply one (HUD admin token is the source of truth here; the
//     operator token never reaches the browser)
//   - drops hop-by-hop headers + the HUD's own bearer (so we never leak
//     it to the operator)
//
// The upstream URL is fixed for the lifetime of the HUD process. Config
// hot-reload would require recreating the proxy; not needed for v1.
type operatorProxy struct {
	upstream *url.URL
	token    string
	logger   *slog.Logger
	rp       *httputil.ReverseProxy
}

func newOperatorProxy(upstream *url.URL, token string, logger *slog.Logger) *operatorProxy {
	p := &operatorProxy{upstream: upstream, token: token, logger: logger}
	rp := &httputil.ReverseProxy{
		Director:       p.director,
		ErrorHandler:   p.errorHandler,
		FlushInterval:  -1,
		ModifyResponse: nil,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       60 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	p.rp = rp
	return p
}

func (p *operatorProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.rp.ServeHTTP(w, r)
}

func (p *operatorProxy) director(req *http.Request) {
	req.URL.Scheme = p.upstream.Scheme
	req.URL.Host = p.upstream.Host
	req.Host = p.upstream.Host
	// Path: keep /api/hive/* exactly as-is; the operator listens on the
	// same path prefix so the rewrite is a no-op.
	if p.upstream.Path != "" && p.upstream.Path != "/" {
		req.URL.Path = strings.TrimRight(p.upstream.Path, "/") + req.URL.Path
	}
	// Strip the HUD's own admin-token header — the operator has its own
	// admin gate and shouldn't trust anything the browser sent. Then
	// inject the operator's admin token for mutations.
	req.Header.Del("X-Loom-Admin-Token")
	if p.token != "" && isMutation(req.Method) {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	// Identify ourselves so operator audit logs show the call origin.
	if ua := req.Header.Get("User-Agent"); ua == "" {
		req.Header.Set("User-Agent", "loom-hud/proxy")
	} else if !strings.Contains(ua, "loom-hud") {
		req.Header.Set("User-Agent", ua+" (via loom-hud/proxy)")
	}
}

func (p *operatorProxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	if p.logger != nil {
		p.logger.Warn("hive proxy upstream error",
			"path", r.URL.Path, "method", r.Method, "error", err)
	}
	http.Error(w, "loom-hive operator unreachable: "+err.Error(), http.StatusBadGateway)
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
