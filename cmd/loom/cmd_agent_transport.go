package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/internal/hud/bridge"
)

// loadHUDConfig reads HUD and CF Access settings from ~/.config/loom/config.yaml.
func loadHUDConfig() (hudURL, cfID, cfSecret string) {
	if hudConfigOnce.loaded {
		return hudConfigOnce.url, hudConfigOnce.cfID, hudConfigOnce.cfSecret
	}
	hudConfigOnce.loaded = true

	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".config", "loom", "config.yaml"))
	if err != nil {
		return "", "", ""
	}
	var cfg struct {
		Hub struct {
			URL                  string `yaml:"url"`
			CFAccessClientID     string `yaml:"cf_access_client_id"`
			CFAccessClientSecret string `yaml:"cf_access_client_secret"`
		} `yaml:"hub"`
		HUD struct {
			URL                  string `yaml:"url"`
			Host                 string `yaml:"host"`
			CFAccessClientID     string `yaml:"cf_access_client_id"`
			CFAccessClientSecret string `yaml:"cf_access_client_secret"`
		} `yaml:"hud"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", "", ""
	}

	hudConfigOnce.url = cfg.HUD.URL
	if hudConfigOnce.url == "" {
		hudConfigOnce.url = deriveHUDURLFromHub(cfg.Hub.URL)
	}
	hudConfigOnce.host = cfg.HUD.Host
	// HUD-specific CF Access creds take precedence, fallback to hub creds.
	hudConfigOnce.cfID = cfg.HUD.CFAccessClientID
	if hudConfigOnce.cfID == "" {
		hudConfigOnce.cfID = cfg.Hub.CFAccessClientID
	}
	hudConfigOnce.cfSecret = cfg.HUD.CFAccessClientSecret
	if hudConfigOnce.cfSecret == "" {
		hudConfigOnce.cfSecret = cfg.Hub.CFAccessClientSecret
	}

	return hudConfigOnce.url, hudConfigOnce.cfID, hudConfigOnce.cfSecret
}

func deriveHUDURLFromHub(hubURL string) string {
	hubURL = strings.TrimSpace(hubURL)
	if hubURL == "" {
		return ""
	}

	u, err := url.Parse(hubURL)
	if err != nil || strings.TrimSpace(u.Host) == "" {
		return ""
	}

	scheme := "https"
	switch strings.ToLower(u.Scheme) {
	case "ws", "http":
		scheme = "http"
	case "wss", "https", "":
		scheme = "https"
	}

	host := u.Host
	if strings.HasPrefix(host, "mcp.") {
		host = "hud." + strings.TrimPrefix(host, "mcp.")
	}

	return strings.TrimRight((&url.URL{Scheme: scheme, Host: host}).String(), "/")
}

// hudHostOverride returns the Host header override for internal ingress access.
func hudHostOverride() string {
	loadHUDConfig() // ensure loaded
	if h := os.Getenv("LOOM_HUD_HOST"); h != "" {
		return h
	}
	return hudConfigOnce.host
}

// hudBaseURL builds the base URL for the HUD API.
// Priority: LOOM_HUD_URL env > config.yaml hud.url > http://127.0.0.1:{port}.
func hudBaseURL(port string) string {
	if u := os.Getenv("LOOM_HUD_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	if cfgURL, _, _ := loadHUDConfig(); cfgURL != "" {
		return strings.TrimRight(cfgURL, "/")
	}
	return "http://127.0.0.1:" + port
}

// hudCFAccessHeaders returns CF Access headers if configured.
// Priority: LOOM_HUD_CF_ACCESS_ID/SECRET env > config.yaml.
func hudCFAccessHeaders() (cfID, cfSecret string) {
	cfID = os.Getenv("LOOM_HUD_CF_ACCESS_ID")
	cfSecret = os.Getenv("LOOM_HUD_CF_ACCESS_SECRET")
	if cfID != "" && cfSecret != "" {
		return cfID, cfSecret
	}
	_, configID, configSecret := loadHUDConfig()
	return configID, configSecret
}

const defaultHUDTimeout = 10 * time.Second
const sessionStartHUDPingTimeout = 250 * time.Millisecond
const sessionStartHUDPostTimeout = 4 * time.Second

// hudHTTPClient is a shared HTTP client for HUD requests.
// Uses TLS skip-verify for internal ingress access (IP-based URLs).
var hudHTTPClient = &http.Client{
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // internal LAN access to K8s ingress
	},
}

// hudRequest sends a request to the HUD API and returns the raw response body.
func hudRequest(port, method, path string, body any, headers map[string]string, timeout time.Duration) (json.RawMessage, error) {
	if timeout <= 0 {
		timeout = defaultHUDTimeout
	}

	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	baseURL := hudBaseURL(port)
	doRequest := func(currentBaseURL string) (json.RawMessage, int, error) {
		var bodyReader io.Reader
		if payload != nil {
			bodyReader = bytes.NewReader(payload)
		}

		req, err := http.NewRequestWithContext(ctx, method, currentBaseURL+path, bodyReader)
		if err != nil {
			return nil, 0, fmt.Errorf("create request: %w", err)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		if host := hudHostOverride(); host != "" {
			req.Host = host
		}

		for k, v := range headers {
			if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
				req.Header.Set(k, v)
			}
		}

		if cfID, cfSecret := hudCFAccessHeaders(); cfID != "" && cfSecret != "" {
			req.Header.Set("CF-Access-Client-Id", cfID)
			req.Header.Set("CF-Access-Client-Secret", cfSecret)
		}

		client := http.DefaultClient
		if strings.HasPrefix(currentBaseURL, "https://") {
			client = hudHTTPClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, resp.StatusCode, fmt.Errorf("read response: %w", err)
		}
		return data, resp.StatusCode, nil
	}

	data, statusCode, err := doRequest(baseURL)
	if err != nil {
		return nil, err
	}

	if shouldRetryHUDOverHTTPS(baseURL, statusCode, data) {
		data, statusCode, err = doRequest("https://" + strings.TrimPrefix(baseURL, "http://"))
		if err != nil {
			return nil, err
		}
	}

	if statusCode >= 400 {
		return nil, fmt.Errorf("HUD returned %d: %s", statusCode, string(data))
	}
	if looksLikeUnexpectedHUDHTML(data) {
		return nil, fmt.Errorf("HUD returned unexpected HTML response, likely an auth challenge")
	}
	return data, nil
}

func shouldRetryHUDOverHTTPS(baseURL string, statusCode int, data []byte) bool {
	return statusCode == http.StatusBadRequest &&
		(strings.HasPrefix(baseURL, "http://127.0.0.1:") || strings.HasPrefix(baseURL, "http://localhost:")) &&
		bytes.Contains(data, []byte("Client sent an HTTP request to an HTTPS server"))
}

func looksLikeUnexpectedHUDHTML(data []byte) bool {
	body := bytes.TrimSpace(data)
	if len(body) == 0 || body[0] != '<' {
		return false
	}

	lower := bytes.ToLower(body)
	return bytes.HasPrefix(lower, []byte("<!doctype html")) ||
		bytes.HasPrefix(lower, []byte("<html")) ||
		bytes.Contains(lower, []byte("<html")) ||
		bytes.Contains(lower, []byte("cloudflare access"))
}

// hudPost sends a POST request with a JSON body to the HUD API.
func hudPost(port, path string, body any) (json.RawMessage, error) {
	return hudRequest(port, http.MethodPost, path, body, nil, defaultHUDTimeout)
}

// hudPostWithHeaders sends a POST request with optional extra headers.
func hudPostWithHeaders(port, path string, body any, headers map[string]string) (json.RawMessage, error) {
	return hudRequest(port, http.MethodPost, path, body, headers, defaultHUDTimeout)
}

// hudGet sends a GET request to the HUD API.
func hudGet(port, path string) (json.RawMessage, error) {
	return hudRequest(port, http.MethodGet, path, nil, nil, defaultHUDTimeout)
}

// hudPostFast sends a POST with a short timeout (for latency-sensitive ops like heartbeats).
func hudPostFast(port, path string, body any, timeout time.Duration) (json.RawMessage, error) {
	return hudRequest(port, http.MethodPost, path, body, nil, timeout)
}

// hudGetFast sends a GET with a short timeout (for preflight health checks).
func hudGetFast(port, path string, timeout time.Duration) (json.RawMessage, error) {
	return hudRequest(port, http.MethodGet, path, nil, nil, timeout)
}

// hudPostWithRetry sends a POST with retry and exponential backoff.
// Retries up to maxAttempts times with the given backoff schedule.
func hudPostWithRetry(port, path string, body any, timeout time.Duration, backoffs []time.Duration) (json.RawMessage, error) {
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		result, err := hudPostFast(port, path, body, timeout)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt < len(backoffs) {
			time.Sleep(backoffs[attempt])
		}
	}
	return nil, lastErr
}

// resolvePort returns the HUD port from flag, env var, port file, or default.
// Priority: --port flag > LOOM_HUD_PORT env > port file > 3333.
func resolvePort(cmd *cobra.Command) string {
	port, _ := cmd.Flags().GetString("port")
	if port != "" {
		return port
	}
	if p := os.Getenv("LOOM_HUD_PORT"); p != "" {
		return p
	}
	// Read port file written by the running HUD.
	if data, err := os.ReadFile(hud.PortFilePath()); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			return p
		}
	}
	return defaultHUDPort
}

// resolveSocketPath returns the daemon socket path from inherited --socket,
// LOOM_SOCKET, or the default ~/.config/loom/loom.sock.
func resolveSocketPath(cmd *cobra.Command) string {
	if cmd != nil {
		if socketPath, err := cmd.Flags().GetString("socket"); err == nil && strings.TrimSpace(socketPath) != "" {
			return socketPath
		}
		if socketPath, err := cmd.InheritedFlags().GetString("socket"); err == nil && strings.TrimSpace(socketPath) != "" {
			return socketPath
		}
	}
	if socketPath := strings.TrimSpace(os.Getenv("LOOM_SOCKET")); socketPath != "" {
		return socketPath
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "loom", "loom.sock")
}

func withAgentBridge(cmd *cobra.Command, fn func(*bridge.AgentBridge) (json.RawMessage, error)) (json.RawMessage, error) {
	socketPath := resolveSocketPath(cmd)
	client := bridge.NewDaemonClient(socketPath, nil)
	if err := client.Connect(); err != nil {
		return nil, err
	}
	defer client.Close()
	return fn(bridge.NewAgentBridge(client))
}

func withAgentFallback(op string, hudCall, daemonCall func() (json.RawMessage, error)) (json.RawMessage, error) {
	result, err := hudCall()
	if err == nil {
		return result, nil
	}

	fallbackResult, fallbackErr := daemonCall()
	if fallbackErr == nil {
		return fallbackResult, nil
	}

	return nil, fmt.Errorf("%s failed via HUD (%v) and daemon fallback (%w)", op, err, fallbackErr)
}
