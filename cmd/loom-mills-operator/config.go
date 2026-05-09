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

	// SquadsPath is the directory containing squad manifest YAMLs (one per
	// squad). The loader scans this path on boot and watches it via
	// fsnotify for hot-reload. Empty / missing dir is non-fatal: the
	// operator boots without squads and the squad endpoints return empty
	// results.
	SquadsPath string

	// HTTPAddr is the bind address for the REST + MCP Streamable HTTP
	// listener. Pods expose this via a ClusterIP Service.
	HTTPAddr string

	// MetricsAddr is the bind address for /metrics + /healthz + /readyz.
	// Kept on a separate listener so health probes don't interleave with
	// real traffic and so a misbehaving handler can't break liveness.
	MetricsAddr string

	// EnableReconciler defaults to whatever the policy says. Set
	// LOOM_MILLS_ENABLED=true to override the YAML; "false" forces off.
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

	// HUDBaseURL is the loom HUD's HTTP base, e.g.
	// "http://hud.loom-system.svc.cluster.local:8090". Empty disables
	// the HUD spawn client (plan_slice/implement/pr_self_review fall
	// back to NoOpDispatcher).
	HUDBaseURL string
	// HUDToken is the mobile bearer token configured via
	// HUD_MOBILE_OPERATOR_TOKEN on the HUD process.
	HUDToken string

	// WeaverURL is the HTTP base for the routed weaver dispatch (POST
	// /api/weaver/query). When set together with MILLS_RESEARCH_VIA_
	// WEAVER=shadow|on, the WeaverWorker delegates research to the
	// loom Router instead of the legacy single-prompt FlexInfer chat.
	// Defaults to HUDBaseURL when unset (the same loomd process owns
	// both surfaces). Empty + non-default mode logs a warning and
	// keeps the legacy chat path.
	WeaverURL string
	// WeaverToken is an optional bearer for /api/weaver/query. Today
	// the endpoint sits behind the HUD's withCORS middleware (no
	// token required); the field is plumbed for future hardening.
	WeaverToken string
}

// DefaultConfig returns the values used when neither flag nor env supplies one.
func DefaultConfig() Config {
	return Config{
		DBPath:      "/var/lib/loom-mills/state.db",
		PolicyPath:  "/etc/loom-mills/policy.yaml",
		SquadsPath:  "/etc/loom-mills/squads",
		HTTPAddr:    ":8090",
		MetricsAddr: ":9090",
		RepoRoot:    "/workspace/loom-core",
	}
}

// ApplyEnv overlays env-derived values on top of c. Flags should be parsed
// after this call so they win over env. LOOM_MILLS_* is the canonical prefix.
func (c *Config) ApplyEnv() {
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_DB_PATH")); v != "" {
		c.DBPath = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_POLICY_PATH")); v != "" {
		c.PolicyPath = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_SQUADS_PATH")); v != "" {
		c.SquadsPath = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_HTTP_ADDR")); v != "" {
		c.HTTPAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_METRICS_ADDR")); v != "" {
		c.MetricsAddr = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_REPO_ROOT")); v != "" {
		c.RepoRoot = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_ENABLED")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			b := true
			c.EnableReconciler = &b
		case "0", "false", "no", "off":
			b := false
			c.EnableReconciler = &b
		}
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_MILLS_DEBUG")); v != "" {
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
	if v := strings.TrimSpace(os.Getenv("LOOM_HUD_URL")); v != "" {
		c.HUDBaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_HUD_TOKEN")); v != "" {
		c.HUDToken = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_WEAVER_URL")); v != "" {
		c.WeaverURL = v
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_WEAVER_TOKEN")); v != "" {
		c.WeaverToken = v
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
