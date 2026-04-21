// cmd_agent_auth.go implements `loom agent auth` subcommands for agent
// authentication diagnostics and token export.
//
// Subcommands:
//   - loom agent auth status          — Show auth status for all agents
//   - loom agent auth export-claude   — Extract Claude Code OAuth JSON from macOS Keychain
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newAgentAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Agent authentication diagnostics and token export",
		Long: `Inspect and export agent authentication tokens for headless K8s agent spawns.

Shows token status for all supported agents (Claude Code, Codex, Gemini) and
can extract Claude Code OAuth credentials from macOS Keychain for sync to K8s.`,
	}

	cmd.AddCommand(
		newAgentAuthStatusCmd(),
		newAgentAuthExportClaudeCmd(),
	)

	return cmd
}

// claudeOAuthCredentials represents the parsed Claude Code Keychain credential.
type claudeOAuthCredentials struct {
	ClaudeAiOauth *claudeOAuthToken `json:"claudeAiOauth,omitempty"`
}

type claudeOAuthToken struct {
	AccessToken      string `json:"accessToken,omitempty"`
	RefreshToken     string `json:"refreshToken,omitempty"`
	ExpiresAt        int64  `json:"expiresAt,omitempty"` // Unix milliseconds
	SubscriptionType string `json:"subscriptionType,omitempty"`
	Scopes           any    `json:"scopes,omitempty"`
}

func newAgentAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show auth status for all agents",
		Long: `Check authentication readiness for Claude Code, Codex, and Gemini.

Reports:
  - Local token files (Codex, Gemini) and macOS Keychain (Claude Code)
  - Token presence and expiry (where parseable)
  - K8s secret status (if kubeconfig is available)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentAuthStatus()
		},
	}
}

func newAgentAuthExportClaudeCmd() *cobra.Command {
	var outputFile string
	cmd := &cobra.Command{
		Use:   "export-claude",
		Short: "Extract Claude Code OAuth JSON from macOS Keychain",
		Long: `Extract Claude Code subscription OAuth credentials from macOS Keychain.

Outputs the full credential JSON (containing accessToken, refreshToken,
expiresAt, subscriptionType) to stdout or a file. This is used by the
token sync pipeline to include Claude subscription auth in K8s secrets.

Only works on macOS (uses 'security' CLI to read Keychain).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExportClaude(outputFile)
		},
	}
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Write to file instead of stdout")
	return cmd
}

// readClaudeKeychainCredential extracts Claude Code OAuth JSON from macOS Keychain.
func readClaudeKeychainCredential() ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("macOS Keychain is only available on darwin (current: %s)", runtime.GOOS)
	}

	user := os.Getenv("USER")
	if user == "" {
		return nil, fmt.Errorf("USER environment variable not set")
	}

	out, err := exec.CommandContext(context.Background(), "security", "find-generic-password", //nolint:gosec // trusted args
		"-s", "Claude Code-credentials",
		"-a", user,
		"-w",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed (is Claude Code authenticated?): %w", err)
	}

	// Validate it's valid JSON.
	var parsed json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("keychain data is not valid JSON: %w", err)
	}

	return out, nil
}

// parseClaudeOAuth parses the raw Keychain JSON into structured credentials.
func parseClaudeOAuth(raw []byte) (*claudeOAuthCredentials, error) {
	var creds claudeOAuthCredentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		return nil, fmt.Errorf("parse claude credentials: %w", err)
	}
	return &creds, nil
}

func runExportClaude(outputFile string) error {
	raw, err := readClaudeKeychainCredential()
	if err != nil {
		return err
	}

	// Pretty-print for readability.
	var prettyBuf json.RawMessage
	if err := json.Unmarshal(raw, &prettyBuf); err != nil {
		return err
	}
	pretty, err := json.MarshalIndent(prettyBuf, "", "  ")
	if err != nil {
		return err
	}

	if outputFile != "" {
		if err := os.MkdirAll(filepath.Dir(outputFile), 0700); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
		if err := os.WriteFile(outputFile, append(pretty, '\n'), 0600); err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Wrote Claude OAuth credentials to %s\n", outputFile)
		return nil
	}

	fmt.Println(string(pretty))
	return nil
}

func runAgentAuthStatus() error {
	home, _ := os.UserHomeDir()

	fmt.Println("Agent authentication status:")
	fmt.Println()

	// Claude Code (macOS Keychain).
	fmt.Println("  Claude Code:")
	if runtime.GOOS == "darwin" {
		raw, err := readClaudeKeychainCredential()
		if err != nil {
			fmt.Printf("    Token: not found (%v)\n", err)
		} else {
			creds, parseErr := parseClaudeOAuth(raw)
			if parseErr != nil {
				fmt.Println("    Token: present (parse error)")
			} else if creds.ClaudeAiOauth != nil {
				tok := creds.ClaudeAiOauth
				expiry := "unknown"
				expired := false
				if tok.ExpiresAt > 0 {
					t := time.UnixMilli(tok.ExpiresAt)
					if time.Now().After(t) {
						expiry = fmt.Sprintf("EXPIRED (%s)", t.Format(time.RFC3339))
						expired = true
					} else {
						expiry = fmt.Sprintf("valid until %s (%s)", t.Format(time.RFC3339), time.Until(t).Round(time.Minute))
					}
				}
				sub := tok.SubscriptionType
				if sub == "" {
					sub = "unknown"
				}
				hasAccess := tok.AccessToken != ""
				hasRefresh := tok.RefreshToken != ""

				mark := "+"
				if expired || !hasAccess {
					mark = "!"
				}
				fmt.Printf("    Token: present [%s]\n", mark)
				fmt.Printf("    Subscription: %s\n", sub)
				fmt.Printf("    Access token: %v, Refresh token: %v\n", hasAccess, hasRefresh)
				fmt.Printf("    Expiry: %s\n", expiry)
			} else {
				fmt.Println("    Token: present (no claudeAiOauth key)")
			}
		}
		fmt.Println("    Source: macOS Keychain (\"Claude Code-credentials\")")
	} else {
		fmt.Println("    Token: n/a (macOS Keychain not available on this platform)")
	}

	fmt.Println()

	// Codex (file-based).
	fmt.Println("  Codex:")
	codexPath := filepath.Join(home, ".codex", "auth.json")
	printFileTokenStatus(codexPath, "codex auth.json")

	fmt.Println()

	// Gemini (file-based).
	fmt.Println("  Gemini:")
	geminiOAuthPath := filepath.Join(home, ".gemini", "oauth_creds.json")
	geminiAccountsPath := filepath.Join(home, ".gemini", "google_accounts.json")
	printFileTokenStatus(geminiOAuthPath, "gemini oauth_creds.json")
	printFileTokenStatus(geminiAccountsPath, "gemini google_accounts.json")

	fmt.Println()

	// K8s secret status (best-effort).
	fmt.Println("  K8s Secrets (cluster-owned, preferred):")
	printK8sSecretStatus("cluster-agent-api-keys", "devbox")
	printK8sSecretStatus("cluster-agent-auth", "devbox")
	printClusterAuthOAuthDetail("devbox")

	fmt.Println()
	fmt.Println("  K8s Secrets (legacy, Mac-sourced — DEPRECATED):")
	printK8sSecretStatus("agent-api-keys", "devbox")
	printK8sSecretStatus("agent-auth-tokens", "devbox")

	return nil
}

// printClusterAuthOAuthDetail inspects cluster-agent-auth for the specific keys
// spawn pods mount (claude-oauth-token, codex-auth-json) and reports actionable
// readiness. See internal/hud/spawn.go:1298-1381 for where the keys are consumed.
func printClusterAuthOAuthDetail(namespace string) {
	kubeconfig := findKubeconfig()
	if kubeconfig == "" {
		return
	}
	// Check claude-oauth-token (Slice 2b.2a — vendor-sanctioned 1yr headless token).
	token := readSecretKey(kubeconfig, namespace, "cluster-agent-auth", "claude-oauth-token")
	switch {
	case token == "":
		fmt.Println("      CLAUDE_CODE_OAUTH_TOKEN: absent — run `claude setup-token`, set under claude-oauth-token")
	case token == "PLACEHOLDER":
		fmt.Println("      CLAUDE_CODE_OAUTH_TOKEN: placeholder — run `claude setup-token`")
	case strings.HasPrefix(token, "sk-ant-oat01-"):
		fmt.Printf("      CLAUDE_CODE_OAUTH_TOKEN: present (%dB, sk-ant-oat01-…)\n", len(token))
	default:
		prefix := token
		if len(prefix) > 12 {
			prefix = prefix[:12] + "…"
		}
		fmt.Printf("      CLAUDE_CODE_OAUTH_TOKEN: unexpected format (prefix=%q)\n", prefix)
	}
}

// findKubeconfig returns a usable kubeconfig path, or "" if none is available.
func findKubeconfig() string {
	candidates := []string{
		os.Getenv("KUBECONFIG"),
		filepath.Join(os.Getenv("HOME"), "workspace", "platform", "gitops", ".kube", "k3s.yaml"),
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// readSecretKey returns the decoded string value of a specific key in a Secret,
// or "" if the secret or key is missing / unreadable.
func readSecretKey(kubeconfig, namespace, secretName, key string) string {
	out, err := exec.CommandContext(context.Background(), "kubectl", "--kubeconfig", kubeconfig, //nolint:gosec // trusted args
		"get", "secret", secretName, "-n", namespace,
		"-o", fmt.Sprintf("jsonpath={.data.%s}", key),
	).Output()
	if err != nil || len(out) == 0 {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		return ""
	}
	return string(decoded)
}

func printFileTokenStatus(path, label string) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Printf("    %s: not found\n", label)
		return
	}
	age := time.Since(info.ModTime())
	fmt.Printf("    %s: present (%dB, %s old)\n", label, info.Size(), formatDuration(age))
}

func printK8sSecretStatus(secretName, namespace string) {
	// Best-effort: try kubectl if available and kubeconfig exists.
	kubeconfigPaths := []string{
		os.Getenv("KUBECONFIG"),
		filepath.Join(os.Getenv("HOME"), "workspace", "platform", "gitops", ".kube", "k3s.yaml"),
	}

	var kubeconfig string
	for _, p := range kubeconfigPaths {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				kubeconfig = p
				break
			}
		}
	}

	if kubeconfig == "" {
		fmt.Printf("    %s: skipped (no kubeconfig found)\n", secretName)
		return
	}

	out, err := exec.CommandContext(context.Background(), "kubectl", "--kubeconfig", kubeconfig, //nolint:gosec // trusted args
		"get", "secret", secretName, "-n", namespace,
		"-o", "jsonpath={.metadata.creationTimestamp}",
	).Output()
	if err != nil {
		fmt.Printf("    %s: not found in cluster\n", secretName)
		return
	}

	// Parse keys.
	keysOut, keysErr := exec.CommandContext(context.Background(), "kubectl", "--kubeconfig", kubeconfig, //nolint:gosec // trusted args
		"get", "secret", secretName, "-n", namespace,
		"-o", "jsonpath={.data}",
	).Output()

	keys := "?"
	if keysErr == nil {
		var dataMap map[string]any
		if json.Unmarshal(keysOut, &dataMap) == nil {
			keyNames := make([]string, 0, len(dataMap))
			for k := range dataMap {
				keyNames = append(keyNames, k)
			}
			keys = fmt.Sprintf("%d keys: %v", len(keyNames), keyNames)
		}
	}

	fmt.Printf("    %s: present (created %s, %s)\n", secretName, string(out), keys)
}
