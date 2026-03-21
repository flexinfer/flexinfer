package mobile

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (d *MobileDomain) handleMobilePushRegister(w http.ResponseWriter, r *http.Request) {
	if !d.deps.MobileConfig().PushEnabled {
		d.writeMobileError(w, http.StatusNotFound, "not_found", "push notifications are not enabled")
		return
	}
	if !d.requireMobileScope(w, r, ScopePush) {
		return
	}

	var body struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	token := strings.TrimSpace(body.Token)
	platform := strings.TrimSpace(body.Platform)

	if token == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}
	if platform != "apns" && platform != "fcm" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "platform must be 'apns' or 'fcm'")
		return
	}

	deviceID := ExtractDeviceID(r)
	store := d.deps.DeviceTokens()
	regID := store.Register(token, deviceID, platform)

	d.logMobileAudit(r, "push_register", map[string]string{
		"platform": platform,
	}, "success", nil)

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"registered":      true,
		"registration_id": regID,
	})
}

func (d *MobileDomain) handleMobilePushUnregister(w http.ResponseWriter, r *http.Request) {
	if !d.deps.MobileConfig().PushEnabled {
		d.writeMobileError(w, http.StatusNotFound, "not_found", "push notifications are not enabled")
		return
	}
	if !d.requireMobileScope(w, r, ScopePush) {
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	token := strings.TrimSpace(body.Token)
	if token == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "token is required")
		return
	}

	store := d.deps.DeviceTokens()
	removed := store.Invalidate(token)

	d.logMobileAudit(r, "push_unregister", nil, "success", nil)

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"removed": removed,
	})
}

func (d *MobileDomain) handleMobileSandbox(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	snap := d.deps.Monitors().Sandbox.Snapshot()
	if snap == nil {
		d.writeMobileJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	snap["available"] = true
	d.writeMobileJSON(w, http.StatusOK, snap)
}

func (d *MobileDomain) handleMobileSandboxStart(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeSessionCreate) {
		return
	}

	var body struct {
		Project string `json:"project"`
		AgentID string `json:"agent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if body.Project == "" {
		d.writeMobileError(w, http.StatusBadRequest, "missing_project", "project is required")
		return
	}

	parsed, err := d.deps.DoSandboxStart(body.Project, body.AgentID)
	if err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "devbox_build_failed", "failed to start sandbox: "+err.Error())
		return
	}
	if parsed == nil {
		d.writeMobileJSON(w, http.StatusOK, map[string]any{"started": true, "project": body.Project})
		return
	}
	parsed["started"] = true
	d.writeMobileJSON(w, http.StatusOK, parsed)
}

func (d *MobileDomain) handleMobileSandboxStop(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeSessionCreate) {
		return
	}

	var body struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}
	if body.Project == "" {
		d.writeMobileError(w, http.StatusBadRequest, "missing_project", "project is required")
		return
	}

	if err := d.deps.DoSandboxStop(body.Project); err != nil {
		d.writeMobileError(w, http.StatusBadGateway, "devbox_stop_failed", "failed to stop sandbox: "+err.Error())
		return
	}
	d.writeMobileJSON(w, http.StatusOK, map[string]any{"stopped": true, "project": body.Project})
}

func (d *MobileDomain) handleMobileAdminRevoke(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if r.Body != nil {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			d.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &body); err != nil {
				d.writeMobileError(w, http.StatusBadRequest, "bad_request", "invalid request body")
				return
			}
		}
	}

	if strings.TrimSpace(body.Token) == "" {
		d.writeMobileError(w, http.StatusBadRequest, "bad_request", "token field is required")
		return
	}

	if rl := d.deps.RevocationList(); rl != nil {
		rl.Revoke(body.Token)
	}

	d.logMobileAudit(r, "token_revoke", nil, "success", nil)
	d.writeMobileJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
