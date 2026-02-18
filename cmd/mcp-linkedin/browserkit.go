package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/pkg/mcperror"
)

const (
	linkedInBrowserKitModeAuto     = "auto"
	linkedInBrowserKitModeRequired = "required"
	linkedInBrowserKitModeOff      = "off"

	linkedInSessionStateUnknown   = "unknown"
	linkedInSessionStateHealthy   = "healthy"
	linkedInSessionStateChallenge = "challenge"
	linkedInSessionStateLoggedOut = "logged_out"
	linkedInSessionStateError     = "error"
	linkedInSessionStateOff       = "off"

	linkedInRecoveryModeInteractive = "interactive"
	linkedInRecoveryModeSilent      = "silent"

	maxBrowserKitMessageLen = 280
)

//go:embed browserkit_helper.py
var browserKitHelperPy string

var (
	browserKitHelperOnce sync.Once
	browserKitHelperPath string
	browserKitHelperErr  error
)

type linkedInBrowserKitConfig struct {
	mode             string
	python           string
	storageDir       string
	sessionID        string
	healthTTL        time.Duration
	recoveryCooldown time.Duration
}

type linkedInBrowserKitRequest struct {
	Action       string `json:"action"`
	Mode         string `json:"mode"`
	URL          string `json:"url"`
	Method       string `json:"method,omitempty"`
	Path         string `json:"path,omitempty"`
	Body         any    `json:"body,omitempty"`
	StorageDir   string `json:"storage_dir"`
	SessionID    string `json:"session_id"`
	Stealth      bool   `json:"stealth"`
	Headless     bool   `json:"headless"`
	TimeoutMS    int    `json:"timeout_ms"`
	SessionToken string `json:"session_token,omitempty"`
	JSessionID   string `json:"jsessionid,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
}

type linkedInBrowserKitResponse struct {
	OK           bool     `json:"ok"`
	State        string   `json:"state"`
	FinalURL     string   `json:"final_url,omitempty"`
	HasLIAt      bool     `json:"has_li_at"`
	HasJSession  bool     `json:"has_jsessionid"`
	LIAt         string   `json:"li_at,omitempty"`
	JSessionID   string   `json:"jsessionid,omitempty"`
	HTTPStatus   int      `json:"http_status,omitempty"`
	ResponseURL  string   `json:"response_url,omitempty"`
	ResponseJSON any      `json:"response_json,omitempty"`
	ResponseText string   `json:"response_text,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	ErrorMessage string   `json:"error,omitempty"`
}

type linkedInBrowserKitRunner func(ctx context.Context, req linkedInBrowserKitRequest) (*linkedInBrowserKitResponse, error)

type secretSetter interface {
	Set(key, value string) error
}

type authChallengeError struct {
	statusCode int
	body       string
}

func (e *authChallengeError) Error() string {
	return fmt.Sprintf("LinkedIn auth challenge detected (status=%d)", e.statusCode)
}

func parseLinkedInBrowserKitMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = linkedInBrowserKitModeAuto
	}
	switch mode {
	case linkedInBrowserKitModeAuto, linkedInBrowserKitModeRequired, linkedInBrowserKitModeOff:
		return mode, nil
	default:
		return "", mcperror.InvalidParam("LINKEDIN_BROWSERKIT_MODE", "must be one of: auto, required, off")
	}
}

func parseLinkedInRecoveryMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = linkedInRecoveryModeInteractive
	}
	switch mode {
	case linkedInRecoveryModeInteractive, linkedInRecoveryModeSilent:
		return mode, nil
	default:
		return "", mcperror.InvalidParam("mode", "must be one of: interactive, silent")
	}
}

func defaultLinkedInBrowserKitStorageDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join(os.TempDir(), "linkedin-browserkit")
	}
	return filepath.Join(home, ".config", "loom", "linkedin-browserkit")
}

func verifyBrowserKitDeps(python string) error {
	if _, err := exec.LookPath(python); err != nil {
		return mcperror.NotConfigured("LINKEDIN_BROWSERKIT_PYTHON", fmt.Sprintf("python executable not found: %q", python))
	}

	checkCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, python, "-c", "import browser_kit, playwright")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return mcperror.NotConfigured("mcp-linkedin browserkit deps", fmt.Sprintf("install flexinfer-browser-kit + playwright for %q: %s", python, msg))
	}
	return nil
}

func (s *linkedInServer) ensureFreshSession(ctx context.Context) error {
	if s.browserKit.mode == linkedInBrowserKitModeOff {
		return nil
	}
	if strings.TrimSpace(s.sessionToken) == "" {
		if !s.canBootstrapSessionViaRecovery() {
			return nil
		}
		res, err := s.recoverSession(ctx, linkedInRecoveryModeSilent, false)
		if err != nil {
			return err
		}
		if res.State != linkedInSessionStateHealthy || strings.TrimSpace(s.sessionToken) == "" {
			return mcperror.Unauthorized("LinkedIn session bootstrap via credentials did not reach healthy state")
		}
		return nil
	}

	now := s.now()
	if !s.shouldRunHealthCheck(now) {
		return nil
	}

	res, err := s.runSessionHealth(ctx, true)
	if err != nil {
		s.logger.Warn("linkedin session health check failed", "error", err)
		if s.browserKit.mode == linkedInBrowserKitModeRequired {
			return err
		}
		return nil
	}
	if res.State == linkedInSessionStateHealthy {
		return nil
	}
	if s.browserKit.mode == linkedInBrowserKitModeRequired {
		return mcperror.Unauthorized("LinkedIn session is not healthy; run linkedin_session_recover")
	}
	return nil
}

func (s *linkedInServer) browserKitStorageStatePath() string {
	sessionID := strings.TrimSpace(s.browserKit.sessionID)
	if sessionID == "" {
		sessionID = "primary"
	}
	return filepath.Join(s.browserKit.storageDir, sessionID+".json")
}

func (s *linkedInServer) hasBrowserKitStorageState() bool {
	path := s.browserKitStorageStatePath()
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func (s *linkedInServer) shouldRunHealthCheck(now time.Time) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.lastHealthCheckAt.IsZero() {
		return true
	}
	return now.Sub(s.lastHealthCheckAt) >= s.browserKit.healthTTL
}

func (s *linkedInServer) cooldownRemaining(now time.Time) time.Duration {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.lastRecoveryAt.IsZero() {
		return 0
	}
	remaining := s.browserKit.recoveryCooldown - now.Sub(s.lastRecoveryAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (s *linkedInServer) beginRecovery(now time.Time, force bool) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if s.recoveryInProgress {
		return mcperror.OperationFailed("linkedin session recovery", errors.New("recovery already in progress"))
	}
	if !force && !s.lastRecoveryAt.IsZero() {
		remaining := s.browserKit.recoveryCooldown - now.Sub(s.lastRecoveryAt)
		if remaining > 0 {
			return mcperror.RateLimited(fmt.Sprintf("retry in %ds", int(remaining.Seconds())))
		}
	}
	s.recoveryInProgress = true
	s.lastRecoveryAt = now
	return nil
}

func (s *linkedInServer) endRecovery() {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.recoveryInProgress = false
}

func (s *linkedInServer) setSessionState(state string, checkedAt time.Time) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.lastSessionState = state
	s.lastHealthCheckAt = checkedAt
}

func (s *linkedInServer) runSessionHealth(ctx context.Context, force bool) (*linkedInBrowserKitResponse, error) {
	if s.browserKit.mode == linkedInBrowserKitModeOff {
		s.setSessionState(linkedInSessionStateOff, s.now())
		return &linkedInBrowserKitResponse{OK: true, State: linkedInSessionStateOff}, nil
	}

	if !force {
		now := s.now()
		if !s.shouldRunHealthCheck(now) {
			return s.currentSessionHealthSnapshot(now), nil
		}
	}

	res, err := s.browserKitRunner(ctx, linkedInBrowserKitRequest{
		Action:       "health",
		Mode:         linkedInRecoveryModeSilent,
		URL:          "https://www.linkedin.com/feed/",
		StorageDir:   s.browserKit.storageDir,
		SessionID:    s.browserKit.sessionID,
		Stealth:      true,
		Headless:     true,
		TimeoutMS:    45000,
		SessionToken: strings.TrimSpace(s.sessionToken),
		JSessionID:   strings.TrimSpace(s.jsessionID),
	})
	checkedAt := s.now()
	if err != nil {
		s.setSessionState(linkedInSessionStateError, checkedAt)
		return nil, err
	}
	if res.State == "" {
		res.State = linkedInSessionStateUnknown
	}
	s.setSessionState(res.State, checkedAt)
	if res.State == linkedInSessionStateHealthy {
		s.applyRecoveredSessionTokens(res, true)
	}
	return res, nil
}

func (s *linkedInServer) currentSessionHealthSnapshot(now time.Time) *linkedInBrowserKitResponse {
	s.stateMu.Lock()
	state := s.lastSessionState
	checkedAt := s.lastHealthCheckAt
	s.stateMu.Unlock()
	if state == "" {
		state = linkedInSessionStateUnknown
	}
	return &linkedInBrowserKitResponse{
		OK:          true,
		State:       state,
		HasLIAt:     strings.TrimSpace(s.sessionToken) != "",
		HasJSession: strings.TrimSpace(s.jsessionID) != "",
		Warnings: []string{fmt.Sprintf(
			"using cached health check from %s",
			checkedAt.Format(time.RFC3339),
		)},
	}
}

func (s *linkedInServer) recoverSession(ctx context.Context, mode string, force bool) (*linkedInBrowserKitResponse, error) {
	if s.browserKit.mode == linkedInBrowserKitModeOff {
		return nil, mcperror.Forbidden("session recovery disabled when LINKEDIN_BROWSERKIT_MODE=off")
	}
	if mode == "" {
		mode = linkedInRecoveryModeInteractive
	}
	now := s.now()
	if err := s.beginRecovery(now, force); err != nil {
		return nil, err
	}
	defer s.endRecovery()

	headless := mode == linkedInRecoveryModeSilent
	timeoutMS := 60000
	if mode == linkedInRecoveryModeInteractive {
		timeoutMS = 240000
	}

	res, err := s.browserKitRunner(ctx, linkedInBrowserKitRequest{
		Action:       "recover",
		Mode:         mode,
		URL:          "https://www.linkedin.com/feed/",
		StorageDir:   s.browserKit.storageDir,
		SessionID:    s.browserKit.sessionID,
		Stealth:      true,
		Headless:     headless,
		TimeoutMS:    timeoutMS,
		SessionToken: strings.TrimSpace(s.sessionToken),
		JSessionID:   strings.TrimSpace(s.jsessionID),
		Username:     strings.TrimSpace(s.loginUsername),
		Password:     strings.TrimSpace(s.loginPassword),
	})
	checkedAt := s.now()
	if err != nil {
		s.setSessionState(linkedInSessionStateError, checkedAt)
		return nil, err
	}
	if res.State == "" {
		res.State = linkedInSessionStateUnknown
	}
	s.setSessionState(res.State, checkedAt)
	if res.State == linkedInSessionStateHealthy {
		s.applyRecoveredSessionTokens(res, true)
	}
	return res, nil
}

func (s *linkedInServer) maybeRecoverAfterChallenge(ctx context.Context, path string) error {
	if !isMessagingPath(path) {
		return mcperror.Unauthorized("LinkedIn auth challenge; retry not eligible for this tool")
	}
	if s.mode == linkedinModeOfficial {
		return mcperror.Unauthorized("LinkedIn auth challenge detected; session-cookie recovery unavailable")
	}
	if strings.TrimSpace(s.sessionToken) == "" && !s.canBootstrapSessionViaRecovery() {
		return mcperror.Unauthorized("LinkedIn auth challenge detected; no session cookie or recovery credentials configured")
	}
	if s.browserKit.mode == linkedInBrowserKitModeOff {
		return mcperror.Unauthorized("LinkedIn auth challenge detected; enable BrowserKit recovery or refresh cookies")
	}
	if remaining := s.cooldownRemaining(s.now()); remaining > 0 {
		return mcperror.RateLimited(fmt.Sprintf("LinkedIn recovery cooldown active; retry in %ds", int(remaining.Seconds())))
	}

	res, err := s.recoverSession(ctx, linkedInRecoveryModeSilent, false)
	if err != nil {
		return err
	}
	if res.State != linkedInSessionStateHealthy {
		return mcperror.Unauthorized("LinkedIn session recovery did not reach healthy state")
	}
	return nil
}

func (s *linkedInServer) shouldUseBrowserKitTransport(path string) bool {
	if s.browserKit.mode == linkedInBrowserKitModeOff {
		return false
	}
	if s.mode == linkedinModeOfficial {
		return false
	}
	return isMessagingPath(path)
}

func (s *linkedInServer) requestViaBrowserKit(ctx context.Context, method, path string, body any) ([]byte, error) {
	res, err := s.browserKitRunner(ctx, linkedInBrowserKitRequest{
		Action:       "voyager_request",
		Mode:         linkedInRecoveryModeSilent,
		URL:          "https://www.linkedin.com/feed/",
		Method:       method,
		Path:         path,
		Body:         body,
		StorageDir:   s.browserKit.storageDir,
		SessionID:    s.browserKit.sessionID,
		Stealth:      true,
		Headless:     true,
		TimeoutMS:    45000,
		SessionToken: strings.TrimSpace(s.sessionToken),
		JSessionID:   strings.TrimSpace(s.jsessionID),
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, mcperror.OperationFailed("linkedin browserkit voyager request", errors.New("empty browserkit response"))
	}

	s.applyRecoveredSessionTokens(res, true)

	if res.HTTPStatus >= 200 && res.HTTPStatus < 300 {
		if res.ResponseJSON != nil {
			raw, marshalErr := json.Marshal(res.ResponseJSON)
			if marshalErr == nil {
				return raw, nil
			}
		}
		if strings.TrimSpace(res.ResponseText) == "" {
			return []byte("{}"), nil
		}
		return []byte(res.ResponseText), nil
	}

	if res.HTTPStatus == http.StatusUnauthorized || res.HTTPStatus == http.StatusForbidden ||
		res.State == linkedInSessionStateChallenge || res.State == linkedInSessionStateLoggedOut {
		detail := sanitizeBrowserKitMessage(res.ResponseText)
		if detail == "" {
			detail = sanitizeBrowserKitMessage(strings.Join(res.Warnings, "; "))
		}
		if detail == "" {
			detail = fmt.Sprintf("state=%s status=%d", res.State, res.HTTPStatus)
		}
		return nil, &authChallengeError{statusCode: res.HTTPStatus, body: detail}
	}

	detail := sanitizeBrowserKitMessage(res.ResponseText)
	if detail == "" {
		detail = sanitizeBrowserKitMessage(strings.Join(res.Warnings, "; "))
	}
	if detail == "" {
		detail = fmt.Sprintf("state=%s status=%d", res.State, res.HTTPStatus)
	}
	return nil, mcperror.OperationFailed("linkedin browserkit voyager request", errors.New(detail))
}

func (s *linkedInServer) applyRecoveredSessionTokens(res *linkedInBrowserKitResponse, persist bool) {
	if res == nil {
		return
	}
	liAt := strings.TrimSpace(res.LIAt)
	jsid := strings.TrimSpace(res.JSessionID)
	if liAt != "" {
		s.sessionToken = liAt
		res.HasLIAt = true
	}
	if jsid != "" {
		s.jsessionID = strings.Trim(jsid, `"`)
		res.HasJSession = true
	}
	if !persist || s.secretStore == nil {
		return
	}
	if liAt != "" {
		if err := s.secretStore.Set("LINKEDIN_SESSION_COOKIE", liAt); err != nil {
			res.Warnings = append(res.Warnings, "failed to persist LINKEDIN_SESSION_COOKIE to secret store")
			s.logger.Warn("failed to store linkedin session cookie", "error", err)
		}
	}
	if jsid != "" {
		if err := s.secretStore.Set("LINKEDIN_JSESSIONID", strings.Trim(jsid, `"`)); err != nil {
			res.Warnings = append(res.Warnings, "failed to persist LINKEDIN_JSESSIONID to secret store")
			s.logger.Warn("failed to store linkedin jsessionid", "error", err)
		}
	}
}

func (s *linkedInServer) runBrowserKitHelper(ctx context.Context, req linkedInBrowserKitRequest) (*linkedInBrowserKitResponse, error) {
	browserKitHelperOnce.Do(func() {
		browserKitHelperPath, browserKitHelperErr = materializeLinkedInBrowserKitHelper()
	})
	if browserKitHelperErr != nil {
		return nil, browserKitHelperErr
	}

	if _, err := exec.LookPath(s.browserKit.python); err != nil {
		return nil, mcperror.NotConfigured("LINKEDIN_BROWSERKIT_PYTHON", fmt.Sprintf("python executable not found: %q", s.browserKit.python))
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, mcperror.ParseError("browserkit request payload", err)
	}

	timeout := 120 * time.Second
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS)*time.Millisecond + 20*time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, s.browserKit.python, browserKitHelperPath, string(payload))
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	stderrMsg := sanitizeBrowserKitMessage(strings.TrimSpace(stderr.String()))

	line := lastNonEmptyLine(stdout.String())
	var out linkedInBrowserKitResponse
	parsed := false
	if strings.TrimSpace(line) != "" {
		if err := json.Unmarshal([]byte(line), &out); err == nil {
			parsed = true
			out.JSessionID = strings.Trim(out.JSessionID, `"`)
			if out.State == "" {
				out.State = linkedInSessionStateUnknown
			}
			out.ErrorMessage = sanitizeBrowserKitMessage(out.ErrorMessage)
			out.Warnings = sanitizeBrowserKitWarnings(out.Warnings)
		}
	}

	if runErr != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			return nil, mcperror.Timeout("linkedin browserkit helper")
		}
		if parsed {
			if out.OK {
				if stderrMsg != "" {
					out.Warnings = sanitizeBrowserKitWarnings(append(out.Warnings, stderrMsg))
				}
				return &out, nil
			}
			msg := sanitizeBrowserKitMessage(out.ErrorMessage)
			if msg == "" {
				msg = stderrMsg
			}
			if msg == "" {
				msg = sanitizeBrowserKitMessage(runErr.Error())
			}
			return nil, mcperror.OperationFailed("linkedin browserkit helper", errors.New(msg))
		}
		if stderrMsg == "" {
			stderrMsg = sanitizeBrowserKitMessage(runErr.Error())
		}
		return nil, mcperror.OperationFailed("linkedin browserkit helper", errors.New(stderrMsg))
	}

	if !parsed {
		return nil, mcperror.ParseError("linkedin browserkit helper", errors.New("empty or invalid helper stdout"))
	}
	if !out.OK {
		if strings.TrimSpace(out.ErrorMessage) == "" {
			out.ErrorMessage = "unknown browserkit error"
		}
		return nil, mcperror.OperationFailed("linkedin browserkit helper", errors.New(sanitizeBrowserKitMessage(out.ErrorMessage)))
	}
	if stderrMsg != "" {
		out.Warnings = sanitizeBrowserKitWarnings(append(out.Warnings, stderrMsg))
	}
	return &out, nil
}

func materializeLinkedInBrowserKitHelper() (string, error) {
	dir := filepath.Join(os.TempDir(), "mcp-linkedin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	sum := sha256.Sum256([]byte(browserKitHelperPy))
	name := fmt.Sprintf("browserkit-%s.py", hex.EncodeToString(sum[:])[:12])
	path := filepath.Join(dir, name)

	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(browserKitHelperPy), 0o700); err != nil {
		return "", fmt.Errorf("write helper: %w", err)
	}
	return path, nil
}

func isLinkedInAuthChallenge(statusCode int, payload []byte) bool {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return false
	}
	body := strings.ToLower(string(payload))
	markers := []string{
		"checkpoint",
		"challenge",
		"login",
		"authwall",
		"security verification",
		"session expired",
	}
	for _, marker := range markers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func isLinkedInSessionInvalidation(resp *http.Response, payload []byte) bool {
	if resp == nil {
		return false
	}

	clearSiteData := strings.ToLower(strings.TrimSpace(resp.Header.Get("Clear-Site-Data")))
	if strings.Contains(clearSiteData, "storage") {
		return true
	}

	setCookies := strings.ToLower(strings.Join(resp.Header.Values("Set-Cookie"), "\n"))
	if strings.Contains(setCookies, "li_at=delete me") || strings.Contains(setCookies, `li_at="delete me"`) {
		return true
	}

	location := strings.ToLower(strings.TrimSpace(resp.Header.Get("Location")))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 &&
		(strings.Contains(location, "/login") || strings.Contains(location, "authwall") || strings.Contains(location, "/checkpoint")) {
		return true
	}

	body := strings.ToLower(string(payload))
	if strings.Contains(body, "clear-site-data") || strings.Contains(body, "li_at=delete me") {
		return true
	}
	return false
}

func isAuthChallengeErr(err error) bool {
	var challengeErr *authChallengeError
	return errors.As(err, &challengeErr)
}

func isMessagingPath(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	return strings.HasPrefix(p, "/messaging/") ||
		strings.HasPrefix(p, "/voyagermessaginggraphql/") ||
		strings.HasPrefix(p, "/voyagermessagingdash")
}

func redactedDescriptor(raw string) map[string]any {
	value := strings.TrimSpace(raw)
	if value == "" {
		return map[string]any{"present": false}
	}
	sum := sha256.Sum256([]byte(value))
	fp := hex.EncodeToString(sum[:])[:10]
	suffix := value
	if len(suffix) > 4 {
		suffix = suffix[len(suffix)-4:]
	}
	return map[string]any{
		"present":       true,
		"length":        len(value),
		"fingerprint":   fp,
		"suffix":        suffix,
		"redacted_hint": "fingerprint+suffix only",
	}
}

func lastNonEmptyLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func sanitizeBrowserKitMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "max redirect count exceeded") ||
		strings.Contains(lower, "err_too_many_redirects") ||
		strings.Contains(lower, "too many redirects") {
		return "voyager request redirect loop (max redirect count exceeded)"
	}
	if idx := strings.Index(lower, "call log:"); idx >= 0 {
		msg = strings.TrimSpace(msg[:idx])
	}
	msg = strings.ReplaceAll(msg, "\r\n", "\n")
	lines := strings.Split(msg, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			msg = line
			break
		}
	}
	if len(msg) > maxBrowserKitMessageLen {
		return msg[:maxBrowserKitMessageLen-3] + "..."
	}
	return msg
}

func sanitizeBrowserKitWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return warnings
	}
	out := make([]string, 0, len(warnings))
	seen := make(map[string]struct{}, len(warnings))
	for _, warning := range warnings {
		cleaned := sanitizeBrowserKitMessage(warning)
		if cleaned == "" {
			continue
		}
		if isIgnorableBrowserKitWarning(cleaned) {
			continue
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}
	return out
}

func isIgnorableBrowserKitWarning(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if lower == "" {
		return true
	}
	return strings.Contains(lower, "playwright-stealth not installed; skipping stealth patches")
}
