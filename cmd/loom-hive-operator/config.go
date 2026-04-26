package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config bundles every operator-tunable knob. Resolved via flag → env →
// default in that order; see (*Config).BindFlags + (*Config).ApplyEnv.
type Config struct {
	// DBPath is the SQLite file backing the canonical store. The parent
	// directory must exist; the operator creates the file on first run.
	DBPath string

	// PolicyPath points at the YAML policy mounted into the pod (the k3s
	// ConfigMap or a developer's local file).
	PolicyPath string

	// HTTPAddr is the bind address for the REST + MCP Streamable HTTP
	// listener. Pods expose this via a ClusterIP Service.
	HTTPAddr string

	// MetricsAddr is the bind address for /metrics + /healthz + /readyz.
	// Kept on a separate listener so health probes don't interleave with
	// real traffic and so a misbehaving handler can't break liveness.
	MetricsAddr string

	// EnableReconciler defaults to whatever the policy says. Set
	// LOOM_HIVE_ENABLED=true to override the YAML; "false" forces off.
	// Unset (the default) defers to the policy.
	EnableReconciler *bool

	// RepoRoot is the absolute path to the loom-core checkout the
	// council writes artifacts into and the brief assembler reads
	// .loom/00-index.md from. In production this is the operator pod's
	// mounted clone (a read-write PVC). For local dev it's the
	// developer's worktree.
	RepoRoot string

	// Debug enables verbose slog output.
	Debug bool

	// FlexInferProxyURL is the OpenAI-compatible HTTP proxy that
	// LLM-judged gates and the WeaverWorker call. Empty disables the
	// real LLM clients (gates fall back to skip; the research stage
	// returns empty notes via NoOpDispatcher).
	FlexInferProxyURL string
	// FlexInferToken is an optional bearer auth token forwarded to the
	// proxy.
	FlexInferToken string
	// FlexInferJudgeModel is the model id rubric judges target. Empty
	// uses the client's "qwen3-8b-instruct" default.
	FlexInferJudgeModel string
	// FlexInferWeaverModel is the model id WeaverWorker targets. Empty
	// falls through to JudgeModel.
	FlexInferWeaverModel string

	// GitLabAPIURL is the GitLab REST API base, e.g.
	// "https://gitlab.flexinfer.ai/api/v4". Empty disables the GitLab
	// client (mr/ci_watch/merge/cleanup stages stub out, escalation
	// issues are skipped with a warn log).
	GitLabAPIURL string
	// GitLabToken is the project or personal access token sent as the
	// PRIVATE-TOKEN header.
	GitLabToken string
	// GitLabProject is the URL-encoded slug or numeric id of the
	// project the operator manages MRs against.
	GitLabProject string
}

// DefaultConfig returns the values used when neither flag nor env supplies one.
func DefaultConfig() Config {
	return Config{
		DBPath:      "/var/lib/loom-hive/state.db",
		PolicyPath:  "/etc/loom-hive/policy.yaml",
		HTTPAddr:    ":8090",
		MetricsAddr: ":9090",
		RepoRoot:    "/workspace/loom-core",
	}
}

// ApplyEnv overlays env-derived values on top of c. Flags should be parsed
// after this call so they win over env. LOOM_HIVE_* is the canonical prefix.
func (c *Config) ApplyEnv() {
	if v := strings.TrimSpace(os.Getenv("LOOM_HIVE_DB_PATH")); v != "" {
		c.DBPath = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_HIVE_POLICY_PATH")); v != "" {
		c.PolicyPath = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_HIVE_HTTP_ADDR")); v != "" {
		c.HTTPAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_HIVE_METRICS_ADDR")); v != "" {
		c.MetricsAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_HIVE_REPO_ROOT")); v != "" {
		c.RepoRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_HIVE_ENABLED")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			b := true
			c.EnableReconciler = &b
		case "0", "false", "no", "off":
			b := false
			c.EnableReconciler = &b
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_HIVE_DEBUG")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			c.Debug = true
		}
	}
	if v := strings.TrimSpace(os.Getenv("FLEXINFER_PROXY_URL")); v != "" {
		c.FlexInferProxyURL = v
	}
	if v := strings.TrimSpace(os.Getenv("FLEXINFER_TOKEN")); v != "" {
		c.FlexInferToken = v
	}
	if v := strings.TrimSpace(os.Getenv("FLEXINFER_JUDGE_MODEL")); v != "" {
		c.FlexInferJudgeModel = v
	}
	if v := strings.TrimSpace(os.Getenv("FLEXINFER_WEAVER_MODEL")); v != "" {
		c.FlexInferWeaverModel = v
	}
	if v := strings.TrimSpace(os.Getenv("GITLAB_API_URL")); v != "" {
		c.GitLabAPIURL = v
	}
	if v := strings.TrimSpace(os.Getenv("GITLAB_TOKEN")); v != "" {
		c.GitLabToken = v
	}
	if v := strings.TrimSpace(os.Getenv("GITLAB_PROJECT")); v != "" {
		c.GitLabProject = v
	}
}

// Validate ensures the resolved config is internally consistent. Called once
// after flag parsing.
func (c *Config) Validate() error {
	if c.DBPath == "" {
		return errors.New("config: --db-path is required")
	}
	if c.PolicyPath == "" {
		return errors.New("config: --policy-path is required")
	}
	if c.HTTPAddr == "" && c.MetricsAddr == "" {
		return errors.New("config: at least one of --listen / --metrics-addr must be set")
	}
	dbDir := filepath.Dir(c.DBPath)
	if dbDir == "" || dbDir == "." {
		return fmt.Errorf("config: db-path %q must include a directory", c.DBPath)
	}
	return nil
}
