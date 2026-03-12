// doctor.go implements hook health diagnostics for loom doctor.
// It compares on-disk platform configs against expected generated output
// to detect stale hooks, permissions drift, and schema errors.
package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/validator"
)

// PlatformHealth holds diagnostic results for a single platform.
type PlatformHealth struct {
	Platform     string              `json:"platform"`
	Hooks        string              `json:"hooks"`                 // "ok", "stale", "missing", "n/a"
	Perms        string              `json:"perms"`                 // "ok", "drift", "missing", "n/a"
	Schema       string              `json:"schema"`                // "ok", "errors", "n/a"
	Status       string              `json:"status"`                // "healthy", "stale", "not_configured"
	ConfigPath   string              `json:"config_path,omitempty"` // path checked
	Details      []string            `json:"details,omitempty"`
	Capabilities *VendorCapabilities `json:"capabilities,omitempty"` // vendor feature matrix
	Warnings     []VendorWarning     `json:"warnings,omitempty"`     // feature mismatch warnings
}

// DoctorReport holds the full diagnostic report.
type DoctorReport struct {
	OK        bool              `json:"ok"`
	Platforms []*PlatformHealth `json:"platforms"`
}

// DoctorPlatforms is the ordered list of platforms the doctor checks.
var DoctorPlatforms = []string{
	"claude", "gemini", "codex", "opencode", "kilocode", "vscode", "antigravity", "zed",
}

// DoctorCheckAll runs doctor checks for all supported platforms.
// workspaceRoot is the project root (cwd or detected workspace).
// homeDir is the user's home directory.
func DoctorCheckAll(reg *registry.Registry, workspaceRoot, homeDir string) *DoctorReport {
	report := &DoctorReport{OK: true}

	for _, platform := range DoctorPlatforms {
		configDir := resolveConfigDir(platform, workspaceRoot, homeDir)
		health := DoctorCheck(reg, platform, configDir)
		report.Platforms = append(report.Platforms, health)
		if health.Status != "healthy" && health.Status != "not_configured" {
			report.OK = false
		}
	}

	return report
}

// DoctorCheck runs the doctor check for a single platform.
// configDir is the absolute path to the platform's config directory
// (e.g., /workspace/.claude or ~/.claude).
func DoctorCheck(reg *registry.Registry, platform, configDir string) *PlatformHealth {
	health := &PlatformHealth{
		Platform:   platform,
		Hooks:      "n/a",
		Perms:      "n/a",
		Schema:     "n/a",
		Status:     "healthy",
		ConfigPath: configDir,
	}

	if configDir == "" || !dirExists(configDir) {
		health.Status = "not_configured"
		health.Details = append(health.Details, "config directory not found")
		return health
	}

	switch platform {
	case "claude":
		checkClaudeHealth(health, reg, configDir)
	case "gemini":
		checkGeminiHealth(health, reg, configDir)
	case "codex":
		checkCodexHealth(health, configDir)
	case "opencode":
		checkOpenCodeHealth(health, configDir)
	default:
		checkBasicHealth(health, platform, configDir)
	}

	// Attach vendor capability matrix and check for feature mismatches.
	health.Capabilities = GetVendorCapabilities(platform)
	health.Warnings = CheckVendorFeatures(platform)

	// Derive overall status from individual checks.
	health.Status = deriveStatus(health)
	return health
}

// checkClaudeHealth checks Claude Code settings.json for hooks + permissions freshness.
func checkClaudeHealth(health *PlatformHealth, reg *registry.Registry, configDir string) {
	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		health.Hooks = "missing"
		health.Perms = "missing"
		health.Details = append(health.Details, "settings.json not found")
		return
	}

	var onDisk map[string]any
	if err := json.Unmarshal(data, &onDisk); err != nil {
		health.Hooks = "missing"
		health.Perms = "missing"
		health.Schema = "errors"
		health.Details = append(health.Details, "settings.json is not valid JSON")
		return
	}

	// Compare hooks block.
	claudeProfile, _ := GetPlatformProfile("claude")
	expectedHooks := claudeHooks(reg, claudeProfile, "")
	onDiskHooks := onDisk["hooks"]
	if onDiskHooks == nil {
		health.Hooks = "missing"
		health.Details = append(health.Details, "no hooks block in settings.json")
	} else if jsonFingerprint(onDiskHooks) != jsonFingerprint(expectedHooks) {
		health.Hooks = "stale"
		health.Details = append(health.Details, "hooks block differs from expected (regenerate with: loom sync claude --regen)")
	} else {
		health.Hooks = "ok"
	}

	// Compare permissions block.
	expectedPerms := claudePermissions(reg)
	onDiskPerms := onDisk["permissions"]
	if onDiskPerms == nil {
		health.Perms = "missing"
		health.Details = append(health.Details, "no permissions block in settings.json")
	} else if jsonFingerprint(onDiskPerms) != jsonFingerprint(expectedPerms) {
		health.Perms = "drift"
		health.Details = append(health.Details, "permissions block differs from expected")
	} else {
		health.Perms = "ok"
	}

	// Schema validation.
	result := validator.ValidateClaudeSettings(settingsPath, data)
	if result == nil {
		health.Schema = "n/a"
	} else if result.HasErrors() {
		health.Schema = "errors"
		for _, e := range result.Errors {
			health.Details = append(health.Details, fmt.Sprintf("schema: %s - %s", e.Field, e.Message))
		}
	} else {
		health.Schema = "ok"
	}
}

// checkGeminiHealth checks Gemini CLI config.toml and settings.json for loom
// proxy wiring, hooks freshness, and policy drift.
func checkGeminiHealth(health *PlatformHealth, reg *registry.Registry, configDir string) {
	configPath := filepath.Join(configDir, "config.toml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		health.Perms = "missing"
		health.Details = append(health.Details, "config.toml not found")
	} else if !hasGeminiLoomProxyConfig(configData) {
		health.Perms = "drift"
		health.Details = append(health.Details, "config.toml exists but has no loom MCP proxy configuration")
	} else {
		health.Perms = "ok"
	}

	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		health.Hooks = "missing"
		health.Perms = "missing"
		health.Details = append(health.Details, "settings.json not found")
		return
	}

	var onDisk map[string]any
	if err := json.Unmarshal(data, &onDisk); err != nil {
		health.Hooks = "missing"
		health.Schema = "errors"
		health.Details = append(health.Details, "settings.json is not valid JSON")
		return
	}

	// Compare hooks block.
	geminiProfile, _ := GetPlatformProfile("gemini")
	expectedConfig := geminiHooksConfigFromRegistry(reg, geminiProfile, "")
	expectedHooks := expectedConfig["hooks"]
	onDiskHooks := onDisk["hooks"]
	if onDiskHooks == nil {
		health.Hooks = "missing"
		health.Details = append(health.Details, "no hooks block in settings.json")
	} else if jsonFingerprint(onDiskHooks) != jsonFingerprint(expectedHooks) {
		health.Hooks = "stale"
		health.Details = append(health.Details, "hooks block differs from expected (regenerate with: loom sync gemini --regen)")
	} else {
		health.Hooks = "ok"
	}

	expectedGeneral, expectedGeneralOK := expectedConfig["general"]
	onDiskGeneral, onDiskGeneralOK := onDisk["general"]
	expectedTools, expectedToolsOK := expectedConfig["tools"]
	onDiskTools, onDiskToolsOK := onDisk["tools"]

	switch {
	case expectedGeneralOK && !onDiskGeneralOK:
		health.Perms = "missing"
		health.Details = append(health.Details, "general block missing from settings.json")
	case !expectedGeneralOK && onDiskGeneralOK:
		if health.Perms != "missing" {
			health.Perms = "drift"
		}
		health.Details = append(health.Details, "general block differs from expected")
	case expectedGeneralOK && onDiskGeneralOK && jsonFingerprint(onDiskGeneral) != jsonFingerprint(expectedGeneral):
		if health.Perms != "missing" {
			health.Perms = "drift"
		}
		health.Details = append(health.Details, "general block differs from expected")
	}

	switch {
	case expectedToolsOK && !onDiskToolsOK:
		health.Perms = "missing"
		health.Details = append(health.Details, "tools block missing from settings.json")
	case !expectedToolsOK && onDiskToolsOK:
		if health.Perms != "missing" {
			health.Perms = "drift"
		}
		health.Details = append(health.Details, "tools block differs from expected")
	case expectedToolsOK && onDiskToolsOK && jsonFingerprint(onDiskTools) != jsonFingerprint(expectedTools):
		if health.Perms != "missing" {
			health.Perms = "drift"
		}
		health.Details = append(health.Details, "tools block differs from expected")
	}

	// Schema validation.
	result := validator.ValidateGeminiSettings(settingsPath, data)
	if result == nil {
		health.Schema = "n/a"
	} else if result.HasErrors() {
		health.Schema = "errors"
		for _, e := range result.Errors {
			health.Details = append(health.Details, fmt.Sprintf("schema: %s - %s", e.Field, e.Message))
		}
	} else {
		health.Schema = "ok"
	}
}

func hasGeminiLoomProxyConfig(data []byte) bool {
	content := string(data)
	return strings.Contains(content, "[mcp_servers.loom]") &&
		strings.Contains(content, `args = ["proxy"]`)
}

// checkCodexHealth checks Codex config.toml for the notify hook line.
func checkCodexHealth(health *PlatformHealth, configDir string) {
	configPath := filepath.Join(configDir, "config.toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		health.Hooks = "missing"
		health.Details = append(health.Details, "config.toml not found")
		return
	}

	content := string(data)
	// Codex hooks are a single "notify" line containing loom agent heartbeat.
	if strings.Contains(content, "notify") && strings.Contains(content, "loom") && strings.Contains(content, "heartbeat") {
		health.Hooks = "ok"
	} else if strings.Contains(content, "[mcp_servers") {
		health.Hooks = "missing"
		health.Details = append(health.Details, "config.toml exists but has no loom notify hook")
	} else {
		health.Hooks = "missing"
		health.Details = append(health.Details, "config.toml exists but appears incomplete")
	}

	// Schema validation.
	result := validator.ValidateCodexConfig(configPath, data)
	if result == nil {
		health.Schema = "n/a"
	} else if result.HasErrors() {
		health.Schema = "errors"
		for _, e := range result.Errors {
			health.Details = append(health.Details, fmt.Sprintf("schema: %s - %s", e.Field, e.Message))
		}
	} else {
		health.Schema = "ok"
	}
}

// checkOpenCodeHealth checks OpenCode config and hooks plugin.
func checkOpenCodeHealth(health *PlatformHealth, configDir string) {
	// Check opencode.json exists.
	configPath := filepath.Join(configDir, "opencode.json")
	if _, err := os.Stat(configPath); err != nil {
		health.Status = "not_configured"
		health.Details = append(health.Details, "opencode.json not found")
		return
	}

	// Check hooks plugin.
	pluginPath := filepath.Join(configDir, "plugins", "loom-hooks.ts")
	if _, err := os.Stat(pluginPath); err != nil {
		health.Hooks = "missing"
		health.Details = append(health.Details, "plugins/loom-hooks.ts not found (regenerate with: loom sync opencode --regen)")
	} else {
		// Plugin exists. Check if it contains expected loom agent commands.
		data, err := os.ReadFile(pluginPath)
		if err != nil {
			health.Hooks = "missing"
			health.Details = append(health.Details, "cannot read plugins/loom-hooks.ts")
		} else {
			content := string(data)
			if strings.Contains(content, "session-start") && strings.Contains(content, "heartbeat") {
				health.Hooks = "ok"
			} else {
				health.Hooks = "stale"
				health.Details = append(health.Details, "plugins/loom-hooks.ts exists but appears incomplete")
			}
		}
	}
}

// checkBasicHealth checks platforms that only have MCP config (no hooks).
func checkBasicHealth(health *PlatformHealth, platform, configDir string) {
	// These platforms have no native hook support.
	var configFile string
	switch platform {
	case "kilocode":
		configFile = "config.toml"
	default:
		configFile = "mcp.json"
	}

	configPath := filepath.Join(configDir, configFile)
	if _, err := os.Stat(configPath); err != nil {
		health.Status = "not_configured"
		health.Details = append(health.Details, configFile+" not found")
	}
}

// resolveConfigDir returns the config directory for a platform.
// Prefers workspace-local config (e.g. .claude/) over home dir.
func resolveConfigDir(platform, workspaceRoot, homeDir string) string {
	// Map platform to directory names.
	dirName, homeDirName := platformDirNames(platform)

	// Check workspace-local first.
	if workspaceRoot != "" {
		candidate := filepath.Join(workspaceRoot, dirName)
		if dirExists(candidate) {
			return candidate
		}
	}

	// Fall back to home directory.
	if homeDir != "" {
		candidate := filepath.Join(homeDir, homeDirName)
		if dirExists(candidate) {
			return candidate
		}
	}

	// Return the workspace path even if it doesn't exist (for reporting).
	if workspaceRoot != "" {
		return filepath.Join(workspaceRoot, dirName)
	}
	return filepath.Join(homeDir, homeDirName)
}

// platformDirNames returns the workspace-relative and home-relative directory names.
func platformDirNames(platform string) (workspace, home string) {
	switch platform {
	case "claude":
		return ".claude", ".claude"
	case "gemini":
		return ".gemini", ".gemini"
	case "codex":
		return ".codex", ".codex"
	case "kilocode":
		return ".kilocode", ".kilocode"
	case "antigravity":
		return ".antigravity", ".antigravity"
	case "opencode":
		return ".opencode", filepath.Join(".config", "opencode")
	case "vscode":
		return ".vscode-mcp", filepath.Join("Library", "Application Support", "Code", "User")
	case "zed":
		return ".zed", filepath.Join("Library", "Application Support", "Zed")
	default:
		return "." + platform, "." + platform
	}
}

// deriveStatus computes the overall platform status from individual check results.
func deriveStatus(h *PlatformHealth) string {
	if h.Hooks == "stale" || h.Perms == "drift" || h.Perms == "missing" {
		return "stale"
	}
	if h.Schema == "errors" {
		return "errors"
	}
	if h.Hooks == "missing" && h.Hooks != "n/a" {
		// Missing hooks on a platform that should have them.
		switch h.Platform {
		case "claude", "gemini", "codex", "opencode":
			return "stale"
		}
	}
	return "healthy"
}

// jsonFingerprint returns a SHA256 fingerprint of the JSON-normalized value.
// Normalization re-marshals through json.Marshal to get deterministic key ordering.
func jsonFingerprint(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	// Re-parse and re-marshal to normalize (json.Marshal sorts map keys).
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return hex.EncodeToString(sha256Sum(data))
	}
	out, err := json.Marshal(normalized)
	if err != nil {
		return hex.EncodeToString(sha256Sum(data))
	}
	return hex.EncodeToString(sha256Sum(out))
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
