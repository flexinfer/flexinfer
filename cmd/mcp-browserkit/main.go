// mcp-browserkit provides local-only browser automation utilities (screenshots) via MCP.
//
// This server intentionally shells out to Python and uses flexinfer-browser-kit (Playwright wrapper)
// to avoid re-implementing browser lifecycle, stealth, and session management in Go.
//
// Prereqs (local machine):
//
//	pip install flexinfer-browser-kit playwright
//	python3 -m playwright install chromium
//
// Note: Mark this server as local-only in the MCP registry so it is not deployed to the hub.
package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "dev"

//go:embed screenshot_helper.py
var screenshotHelperPy string

var (
	helperOnce sync.Once
	helperPath string
	helperErr  error
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-browserkit", "version", version)

	server := mcp.NewServer("mcp-browserkit", version)
	server.SetInstructions(strings.TrimSpace(`
BrowserKit screenshot server (local-only).

Tools:
- screenshot: Capture a PNG/JPEG screenshot of a URL (optionally scoped to a CSS selector).

Prerequisites (run once on the host):
- pip install flexinfer-browser-kit playwright
- python3 -m playwright install chromium
`))

	server.AddTool(mcp.Tool{
		Name:        "screenshot",
		Description: "Capture a screenshot of a URL (optionally for a specific CSS selector)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "URL to capture (http(s)://, file://, or localhost)",
				},
				"selector": map[string]any{
					"type":        "string",
					"description": "Optional CSS selector to capture (element screenshot). If omitted, captures the full page or viewport.",
				},
				"full_page": map[string]any{
					"type":        "boolean",
					"description": "Capture the full page (scrolling). Ignored if selector is provided. Default: true",
				},
				"format": map[string]any{
					"type":        "string",
					"description": "Image format: png or jpeg. Default: png",
				},
				"quality": map[string]any{
					"type":        "integer",
					"description": "JPEG quality 1-100 (only used when format=jpeg). Default: 85",
				},
				"viewport_width": map[string]any{
					"type":        "integer",
					"description": "Viewport width in pixels. Default: 1440",
				},
				"viewport_height": map[string]any{
					"type":        "integer",
					"description": "Viewport height in pixels. Default: 900",
				},
				"wait_until": map[string]any{
					"type":        "string",
					"description": "Navigation wait condition: load, domcontentloaded, networkidle. Default: load",
				},
				"wait_ms": map[string]any{
					"type":        "integer",
					"description": "Extra wait after navigation (ms). Default: 0",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Navigation/selector timeout (ms). Default: 30000",
				},
				"user_agent": map[string]any{
					"type":        "string",
					"description": "Optional User-Agent override.",
				},
				"session_id": map[string]any{
					"type":        "string",
					"description": "Optional persistent session id (cookies/localStorage).",
				},
				"storage_dir": map[string]any{
					"type":        "string",
					"description": "Directory for session storage. Default: ${TMPDIR}/browserkit-sessions",
				},
				"stealth": map[string]any{
					"type":        "boolean",
					"description": "Enable stealth patches/evasions. Default: true",
				},
				"block_resources": map[string]any{
					"type":        "array",
					"description": "Optional resource types to block (e.g. image,font,media). Default: [] (do not block).",
					"items": map[string]any{
						"type": "string",
					},
				},
				"omit_background": map[string]any{
					"type":        "boolean",
					"description": "Omit background for PNG screenshots. Default: false",
				},
			},
			Required: []string{"url"},
		},
	}, handleScreenshot)

	return server.Run(ctx)
}

func handleScreenshot(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	urlStr := v.Required("url")
	selector := v.String("selector", "")
	fullPage := v.Bool("full_page", true)
	format := strings.ToLower(strings.TrimSpace(v.String("format", "png")))
	quality := v.Int("quality", 85)
	viewportW := v.Int("viewport_width", 1440)
	viewportH := v.Int("viewport_height", 900)
	waitUntil := strings.ToLower(strings.TrimSpace(v.String("wait_until", "load")))
	waitMS := v.Int("wait_ms", 0)
	timeoutMS := v.Int("timeout_ms", 30000)
	userAgent := v.String("user_agent", "")
	sessionID := v.String("session_id", "")
	storageDir := v.String("storage_dir", "")
	stealth := v.Bool("stealth", true)
	omitBg := v.Bool("omit_background", false)

	var blockResources []string
	if raw, ok := args["block_resources"]; ok && raw != nil {
		switch vv := raw.(type) {
		case []any:
			for _, it := range vv {
				s, ok := it.(string)
				if !ok {
					continue
				}
				s = strings.TrimSpace(strings.ToLower(s))
				if s == "" {
					continue
				}
				blockResources = append(blockResources, s)
			}
		case []string:
			for _, s := range vv {
				s = strings.TrimSpace(strings.ToLower(s))
				if s == "" {
					continue
				}
				blockResources = append(blockResources, s)
			}
		}
	}

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	normURL, err := normalizeAndValidateURL(urlStr)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	urlStr = normURL

	if format != "png" && format != "jpeg" && format != "jpg" {
		return mcp.ErrorResult(mcperror.InvalidParam("format", "must be png or jpeg")), nil
	}
	if format == "jpg" {
		format = "jpeg"
	}
	if quality < 1 || quality > 100 {
		return mcp.ErrorResult(mcperror.InvalidParam("quality", "must be 1-100")), nil
	}
	if viewportW < 320 || viewportW > 8000 || viewportH < 240 || viewportH > 8000 {
		return mcp.ErrorResult(mcperror.InvalidParam("viewport_width/viewport_height", fmt.Sprintf("invalid viewport %dx%d", viewportW, viewportH))), nil
	}
	switch waitUntil {
	case "load", "domcontentloaded", "networkidle":
	default:
		return mcp.ErrorResult(mcperror.InvalidParam("wait_until", "must be load, domcontentloaded, or networkidle")), nil
	}
	if waitMS < 0 || waitMS > 120000 {
		return mcp.ErrorResult(mcperror.InvalidParam("wait_ms", "must be between 0 and 120000")), nil
	}
	if timeoutMS < 1000 || timeoutMS > 300000 {
		return mcp.ErrorResult(mcperror.InvalidParam("timeout_ms", "must be between 1000 and 300000")), nil
	}
	if strings.TrimSpace(storageDir) == "" {
		// Match docs/schema and keep sessions stable between calls.
		storageDir = filepath.Join(os.TempDir(), "browserkit-sessions")
	}

	req := map[string]any{
		"url":             urlStr,
		"selector":        selector,
		"full_page":       fullPage,
		"format":          format,
		"quality":         quality,
		"viewport_width":  viewportW,
		"viewport_height": viewportH,
		"wait_until":      waitUntil,
		"wait_ms":         waitMS,
		"timeout_ms":      timeoutMS,
		"user_agent":      userAgent,
		"session_id":      sessionID,
		"storage_dir":     storageDir,
		"stealth":         stealth,
		"block_resources": blockResources,
		"omit_background": omitBg,
	}

	out, err := runPythonHelper(ctx, req)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Return as image content (base64) so clients can render it inline.
	mimeType := "image/png"
	if out.Format == "jpeg" {
		mimeType = "image/jpeg"
	}
	imageDataURL, err := normalizeImageDataURL(mimeType, out.Base64)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("helper returned invalid image data: %w", err)), nil
	}

	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			{Type: "image", MimeType: mimeType, Data: imageDataURL},
			{Type: "text", Text: fmt.Sprintf("Captured %s (%s). Title: %s", out.FinalURL, out.Format, out.Title)},
		},
	}
	return result, nil
}

type helperResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Title    string `json:"title,omitempty"`
	FinalURL string `json:"final_url,omitempty"`
	Format   string `json:"format,omitempty"`
	Base64   string `json:"base64,omitempty"`
}

func runPythonHelper(ctx context.Context, req map[string]any) (*helperResponse, error) {
	helperOnce.Do(func() {
		helperPath, helperErr = materializeHelper()
	})
	if helperErr != nil {
		return nil, helperErr
	}

	py := browserkitPython()
	if _, err := exec.LookPath(py); err != nil {
		return nil, mcperror.NotConfigured("BROWSERKIT_PYTHON", fmt.Sprintf("python executable not found: %q", py))
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Use a timeout even if upstream didn't provide one; avoid hanging processes.
	timeout := 45 * time.Second
	if t, ok := getReqInt(req, "timeout_ms"); ok && t > 0 {
		// navigation timeout isn't total runtime; include some headroom for slow sites.
		timeout = time.Duration(t)*time.Millisecond + 15*time.Second
		if w, ok := getReqInt(req, "wait_ms"); ok && w > 0 {
			timeout += time.Duration(w) * time.Millisecond
		}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, py, helperPath, string(payload))
	cmd.Env = append(os.Environ(),
		// Keep Python noisy logs down; helper prints JSON only.
		"PYTHONUNBUFFERED=1",
	)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stdoutStr := stdout.String()
		stderrStr := stderr.String()
		msg := strings.TrimSpace(stderrStr)
		if resp, ok := decodeHelperResponse(stdoutStr); ok && !resp.OK {
			errText := strings.TrimSpace(resp.Error)
			if errText == "" {
				errText = "unknown error"
			}
			if errorsLikeMissingBrowserKit(errText) {
				return nil, mcperror.NotConfigured("mcp-browserkit python deps", fmt.Sprintf("install flexinfer-browser-kit + playwright, then install chromium (python=%q). Run: scripts/browserkit/install_deps.sh", py))
			}
			if errorsLikeMissingChromium(errText) {
				return nil, mcperror.NotConfigured("Playwright Chromium", fmt.Sprintf("install chromium for Playwright (python=%q). Run: %s -m playwright install chromium", py, py))
			}
			return nil, fmt.Errorf("screenshot failed: %s", errText)
		}
		var ee *exec.Error
		if errors.As(err, &ee) && errors.Is(ee.Err, exec.ErrNotFound) {
			return nil, mcperror.NotConfigured("BROWSERKIT_PYTHON", fmt.Sprintf("python executable not found: %q", py))
		}
		if errorsLikeMissingBrowserKit(msg) {
			return nil, mcperror.NotConfigured("flexinfer-browser-kit", fmt.Sprintf("%s. Install:\n  python3 -m pip install -U flexinfer-browser-kit playwright\n  python3 -m playwright install chromium", msg))
		}
		if errorsLikeMissingChromium(msg) {
			return nil, mcperror.NotConfigured("playwright chromium", fmt.Sprintf("%s. Fix:\n  python3 -m playwright install chromium", msg))
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("browserkit screenshot timed out (try increasing timeout_ms). stderr: %s", msg)
		}
		return nil, fmt.Errorf("browserkit helper failed: %v. stdout: %s stderr: %s", err, truncate(stdoutStr, 500), truncate(stderrStr, 500))
	}

	var resp helperResponse
	stdoutLine := lastNonEmptyLine(stdout.String())
	if err := json.Unmarshal([]byte(stdoutLine), &resp); err != nil {
		return nil, fmt.Errorf("decode helper response: %w (stdout=%q stderr=%q)", err, truncate(stdout.String(), 500), truncate(stderr.String(), 500))
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "unknown error"
		}
		return nil, fmt.Errorf("screenshot failed: %s", resp.Error)
	}
	if resp.Base64 == "" {
		return nil, fmt.Errorf("helper returned empty image data")
	}
	resp.Format = strings.ToLower(strings.TrimSpace(resp.Format))
	return &resp, nil
}

func materializeHelper() (string, error) {
	dir := filepath.Join(os.TempDir(), "mcp-browserkit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}

	sum := sha256.Sum256([]byte(screenshotHelperPy))
	name := fmt.Sprintf("screenshot-%s.py", hex.EncodeToString(sum[:])[:12])
	path := filepath.Join(dir, name)

	// Write once (idempotent).
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(screenshotHelperPy), 0o700); err != nil {
		return "", fmt.Errorf("write helper: %w", err)
	}
	return path, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func errorsLikeMissingBrowserKit(msg string) bool {
	l := strings.ToLower(msg)
	return strings.Contains(l, "no module named") && (strings.Contains(l, "browser_kit") || strings.Contains(l, "playwright"))
}

func errorsLikeMissingChromium(msg string) bool {
	l := strings.ToLower(msg)
	if !strings.Contains(l, "chromium") {
		return false
	}
	return strings.Contains(l, "executable doesn't exist") ||
		strings.Contains(l, "executable does not exist") ||
		strings.Contains(l, "playwright install") ||
		strings.Contains(l, "browsertype.launch")
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

func decodeHelperResponse(stdout string) (*helperResponse, bool) {
	line := lastNonEmptyLine(stdout)
	if strings.TrimSpace(line) == "" {
		return nil, false
	}
	var resp helperResponse
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, false
	}
	return &resp, true
}

func normalizeAndValidateURL(urlStr string) (string, error) {
	raw := strings.TrimSpace(urlStr)
	if raw == "" {
		return "", mcperror.RequiredParam("url")
	}
	if !strings.Contains(raw, "://") && (strings.HasPrefix(raw, "localhost") || strings.HasPrefix(raw, "127.0.0.1") || strings.HasPrefix(raw, "[::1]") || strings.HasPrefix(raw, "::1")) {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", mcperror.InvalidParam("url", fmt.Sprintf("failed to parse: %v", err))
	}
	if u.Scheme == "" {
		return "", mcperror.InvalidParam("url", "missing scheme (use http://, https://, or file://)")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "file":
		return raw, nil
	default:
		return "", mcperror.InvalidParam("url", fmt.Sprintf("unsupported scheme: %q (use http, https, or file)", u.Scheme))
	}
}

func getReqInt(req map[string]any, key string) (int, bool) {
	v, ok := req[key]
	if !ok || v == nil {
		return 0, false
	}
	i, ok := v.(int)
	if ok {
		return i, true
	}
	return 0, false
}

func browserkitPython() string {
	if py := strings.TrimSpace(os.Getenv("BROWSERKIT_PYTHON")); py != "" {
		return py
	}

	home, _ := os.UserHomeDir()
	venvDir := strings.TrimSpace(os.Getenv("BROWSERKIT_VENV_DIR"))
	if venvDir == "" && home != "" {
		venvDir = filepath.Join(home, ".config", "loom", "browserkit-venv")
	}
	if venvDir != "" {
		if p := filepath.Join(venvDir, "bin", "python3"); fileExists(p) {
			return p
		}
		if p := filepath.Join(venvDir, "bin", "python"); fileExists(p) {
			return p
		}
	}

	return "python3"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func normalizeImageDataURL(mimeType, data string) (string, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return "", errors.New("empty image payload")
	}

	lower := strings.ToLower(data)
	if strings.HasPrefix(lower, "data:image/") {
		comma := strings.Index(data, ",")
		if comma <= 0 || comma == len(data)-1 {
			return "", errors.New("invalid data URL image payload")
		}
		meta := data[:comma]
		if !strings.Contains(strings.ToLower(meta), ";base64") {
			return "", errors.New("image data URL must use base64 encoding")
		}
		b64, err := normalizeBase64(data[comma+1:])
		if err != nil {
			return "", err
		}
		return meta + "," + b64, nil
	}

	b64, err := normalizeBase64(data)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data:%s;base64,%s", mimeType, b64), nil
}

func normalizeBase64(data string) (string, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return "", errors.New("empty base64 image payload")
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(data)
		if err != nil {
			return "", fmt.Errorf("invalid base64 image payload: %w", err)
		}
	}
	return base64.StdEncoding.EncodeToString(decoded), nil
}
