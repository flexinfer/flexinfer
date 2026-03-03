package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/internal/daemon"
)

func newRBACCmd() *cobra.Command {
	rbacCmd := &cobra.Command{
		Use:   "rbac",
		Short: "RBAC policy tools",
	}

	var source string
	var configPath string
	var agentID string
	var agentType string
	var server string
	var tool string
	var mode string
	var outputJSON bool

	simulateCmd := &cobra.Command{
		Use:   "simulate",
		Short: "Simulate RBAC access decisions without restarting the daemon",
		Long: `Evaluate an RBAC decision for (agent_id, agent_type, server, tool)
using policy loaded from user config or repo policy files.

Modes:
  - dry-run (default): evaluates policy without consuming rate-limit counters
  - enforce: evaluates policy with normal limiter side effects`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(server) == "" || strings.TrimSpace(tool) == "" {
				return errors.New("--server and --tool are required")
			}

			cfg, err := loadRBACConfigForSimulation(source, configPath)
			if err != nil {
				return err
			}

			decision, err := simulateRBACDecision(cfg, agentID, agentType, server, tool, mode)
			if err != nil {
				return err
			}

			if outputJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(decision)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "mode: %s\n", mode)
			fmt.Fprintf(cmd.OutOrStdout(), "allowed: %t\n", decision.Allowed)
			fmt.Fprintf(cmd.OutOrStdout(), "agent_id: %s\n", decision.AgentID)
			fmt.Fprintf(cmd.OutOrStdout(), "role: %s\n", decision.Role)
			fmt.Fprintf(cmd.OutOrStdout(), "target: %s__%s\n", decision.Server, decision.Tool)
			fmt.Fprintf(cmd.OutOrStdout(), "reason: %s\n", decision.Reason)
			return nil
		},
	}

	simulateCmd.Flags().StringVar(&source, "source", "repo", "Policy source: repo|user")
	simulateCmd.Flags().StringVar(&configPath, "config", "", "Explicit RBAC config path (overrides --source)")
	simulateCmd.Flags().StringVar(&agentID, "agent-id", "", "Agent identifier")
	simulateCmd.Flags().StringVar(&agentType, "agent-type", "", "Agent type (for binding matches)")
	simulateCmd.Flags().StringVar(&server, "server", "", "Server name (required)")
	simulateCmd.Flags().StringVar(&tool, "tool", "", "Tool name (required)")
	simulateCmd.Flags().StringVar(&mode, "mode", string(daemon.RBACEvaluationModeDryRun), "Evaluation mode: dry-run|enforce")
	simulateCmd.Flags().BoolVar(&outputJSON, "json", false, "Print JSON output")

	rbacCmd.AddCommand(simulateCmd)
	return rbacCmd
}

func simulateRBACDecision(cfg daemon.RBACConfig, agentID, agentType, server, tool, mode string) (daemon.AccessDecision, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	var evalMode daemon.RBACEvaluationMode
	switch daemon.RBACEvaluationMode(mode) {
	case daemon.RBACEvaluationModeDryRun:
		evalMode = daemon.RBACEvaluationModeDryRun
	case daemon.RBACEvaluationModeEnforce:
		evalMode = daemon.RBACEvaluationModeEnforce
	default:
		return daemon.AccessDecision{}, fmt.Errorf("invalid mode %q (expected dry-run or enforce)", mode)
	}

	// Disabled RBAC behaves as allow-all with an explicit reason.
	if !cfg.Enabled {
		return daemon.AccessDecision{
			Allowed: true,
			AgentID: agentID,
			Server:  server,
			Tool:    tool,
			Reason:  "rbac disabled",
		}, nil
	}

	enforcer := daemon.NewRBACEnforcer(cfg, nil)
	if enforcer == nil {
		return daemon.AccessDecision{
			Allowed: true,
			AgentID: agentID,
			Server:  server,
			Tool:    tool,
			Reason:  "rbac disabled",
		}, nil
	}
	return enforcer.CheckWithMode(agentID, agentType, server, tool, evalMode), nil
}

func loadRBACConfigForSimulation(source, configPath string) (daemon.RBACConfig, error) {
	if strings.TrimSpace(configPath) != "" {
		return parseRBACConfigFile(configPath)
	}

	switch strings.ToLower(strings.TrimSpace(source)) {
	case "repo":
		path, err := findRepoRBACPolicyPath()
		if err != nil {
			return daemon.RBACConfig{}, err
		}
		return parseRBACConfigFile(path)
	case "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return daemon.RBACConfig{}, fmt.Errorf("resolve home directory: %w", err)
		}
		return parseRBACConfigFile(filepath.Join(home, ".config", "loom", "config.yaml"))
	default:
		return daemon.RBACConfig{}, fmt.Errorf("invalid source %q (expected repo or user)", source)
	}
}

func findRepoRBACPolicyPath() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for range 20 {
		candidate := filepath.Join(dir, ".loom", "rbac-policy.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", errors.New("repo RBAC policy not found (.loom/rbac-policy.yaml)")
}

func parseRBACConfigFile(path string) (daemon.RBACConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return daemon.RBACConfig{}, fmt.Errorf("read RBAC config %q: %w", path, err)
	}

	var fileCfg daemon.FileConfig
	if err := yaml.Unmarshal(data, &fileCfg); err == nil && rbacConfigConfigured(fileCfg.RBAC) {
		return fileCfg.RBAC, nil
	}

	var rbacCfg daemon.RBACConfig
	if err := yaml.Unmarshal(data, &rbacCfg); err != nil {
		return daemon.RBACConfig{}, fmt.Errorf("parse RBAC config %q: %w", path, err)
	}
	if !rbacConfigConfigured(rbacCfg) {
		return daemon.RBACConfig{}, fmt.Errorf("RBAC config not found in %q", path)
	}
	return rbacCfg, nil
}

func rbacConfigConfigured(cfg daemon.RBACConfig) bool {
	return cfg.Enabled ||
		cfg.DefaultPolicy != "" ||
		len(cfg.GlobalDeny) > 0 ||
		len(cfg.RateLimits) > 0 ||
		len(cfg.Roles) > 0 ||
		len(cfg.Bindings) > 0
}
