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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
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

	if format != "png" && format != "jpeg" && format != "jpg" {
		return mcp.ErrorResult(fmt.Errorf("unsupported format: %q (use png or jpeg)", format)), nil
	}
	if format == "jpg" {
		format = "jpeg"
	}
	if quality < 1 || quality > 100 {
		return mcp.ErrorResult(fmt.Errorf("quality must be 1-100, got %d", quality)), nil
	}
	if viewportW < 320 || viewportW > 8000 || viewportH < 240 || viewportH > 8000 {
		return mcp.ErrorResult(fmt.Errorf("invalid viewport %dx%d", viewportW, viewportH)), nil
	}
	switch waitUntil {
	case "load", "domcontentloaded", "networkidle":
	default:
		return mcp.ErrorResult(fmt.Errorf("invalid wait_until: %q", waitUntil)), nil
	}
	if waitMS < 0 || waitMS > 120000 {
		return mcp.ErrorResult(fmt.Errorf("wait_ms out of range: %d", waitMS)), nil
	}
	if timeoutMS < 1000 || timeoutMS > 300000 {
		return mcp.ErrorResult(fmt.Errorf("timeout_ms out of range: %d", timeoutMS)), nil
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

	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			{Type: "image", MimeType: mimeType, Data: out.Base64},
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

	py := strings.TrimSpace(os.Getenv("BROWSERKIT_PYTHON"))
	if py == "" {
		py = "python3"
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Use a timeout even if upstream didn't provide one; avoid hanging processes.
	timeout := 45 * time.Second
	if t, ok := req["timeout_ms"].(int); ok && t > 0 {
		// navigation timeout isn't total runtime; include some headroom for slow sites.
		timeout = time.Duration(t)*time.Millisecond + 15*time.Second
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
		// Provide actionable errors for missing deps.
		msg := strings.TrimSpace(stderr.String())
		if errorsLikeMissingBrowserKit(msg) {
			return nil, fmt.Errorf("%s\n\nInstall:\n  pip install flexinfer-browser-kit playwright\n  python3 -m playwright install chromium", msg)
		}
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("browserkit screenshot timed out (try increasing timeout_ms). stderr: %s", msg)
		}
		return nil, fmt.Errorf("browserkit helper failed: %v. stderr: %s", err, msg)
	}

	var resp helperResponse
	if err := json.Unmarshal([]byte(stdout.String()), &resp); err != nil {
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
