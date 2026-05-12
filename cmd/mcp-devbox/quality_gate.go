package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/detect"
	"github.com/crb2nu/loom/pkg/poll"
	"github.com/crb2nu/loom/pkg/validate"
)

// qualityCheckResult holds the result of a single quality gate check.
//
// OutputTail is captured from the check's stdout. When stdout is empty
// (common for `make` errors and fallback commands that error to stderr),
// StderrTail surfaces the underlying failure so escalations are
// actionable instead of an unhelpful empty string.
type qualityCheckResult struct {
	Name       string `json:"name"`
	Passed     bool   `json:"passed"`
	ExitCode   int    `json:"exit_code,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	OutputTail string `json:"output_tail,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
}

// qualityGateResult holds the aggregate result of the quality gate.
type qualityGateResult struct {
	Language        string               `json:"language"`
	Passed          bool                 `json:"passed"`
	Checks          []qualityCheckResult `json:"checks"`
	TotalDurationMs int64                `json:"total_duration_ms"`
}

// languageCommands maps language → check name → command.
var languageCommands = map[string]map[string]string{
	"go": {
		"fmt":  "gofmt -l .",
		"lint": "go vet ./...",
		"test": "go test ./...",
		"diff": "git diff --exit-code",
	},
	"python": {
		"fmt":  "black --check .",
		"lint": "ruff check .",
		"test": "pytest",
		"diff": "git diff --exit-code",
	},
	"node": {
		"fmt":  `npx prettier --check "src/**"`,
		"lint": "npx eslint src/",
		"test": "npm test",
		"diff": "git diff --exit-code",
	},
	"rust": {
		"fmt":  "cargo fmt --check",
		"lint": "cargo clippy -- -D warnings",
		"test": "cargo test",
		"diff": "git diff --exit-code",
	},
}

// fallbackCommands are Makefile-based fallbacks when no language is detected.
var fallbackCommands = map[string]string{
	"fmt":  "make fmt",
	"lint": "make lint",
	"test": "make test",
	"diff": "git diff --exit-code",
}

// sandboxLanguageProbeCommand inspects the cwd for the canonical
// marker file of each supported language. Trailing newlines keep
// k8s exec capture happy (no-newline output can race with stream
// close in some kubelet versions) and stays harmless under strings.TrimSpace.
const sandboxLanguageProbeCommand = `if [ -f go.mod ]; then echo go; elif [ -f package.json ]; then echo node; elif [ -f pyproject.toml ] || [ -f requirements.txt ]; then echo python; elif [ -f Cargo.toml ]; then echo rust; else echo unknown; fi`

// sandboxLanguageProbePaths is the ordered list of cwds the probe tries
// when the first probe returns unknown. In git-clone mode the sources
// land under projectWorkDir, but tar-pipe sandboxes that pre-date a
// syncMode flip and home-rolled mounts can leave the marker at the
// workspace root instead. The extra candidates are inert when missing.
var sandboxLanguageProbePaths = []string{"", "/workspace"}

func (m *manager) handleQualityGate(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	agentID := v.String("agent_id", "")
	failFast := v.Bool("fail_fast", true)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Parse checks parameter
	checks := []string{"fmt", "lint", "test"}
	if checksRaw, ok := args["checks"]; ok {
		switch checksArr := checksRaw.(type) {
		case []any:
			checks = make([]string, 0, len(checksArr))
			for _, c := range checksArr {
				if s, ok := c.(string); ok {
					checks = append(checks, s)
				}
			}
		case []string:
			checks = append([]string(nil), checksArr...)
		}
	}

	projectDir, projectName, err := m.resolveProject(project)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Ensure sandbox is running
	key := storeKey(projectName, agentID)
	mu := m.projectLock(key)
	mu.Lock()
	containerID, err := m.ensureRunning(ctx, projectDir, projectName, agentID)
	mu.Unlock()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("ensure sandbox: %w", err)), nil
	}

	// Re-sync workspace
	if err := m.syncIfNeeded(ctx, containerID, projectDir); err != nil {
		m.logger.Warn("pre-quality-gate sync failed", "project", projectName, "error", err)
	}

	_ = m.store.TouchLastUsed(key)
	m.incActiveExecs(key)
	defer m.decActiveExecs(key)

	// Detect language
	fp, err := detect.Fingerprint(projectDir)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("fingerprint: %w", err)), nil
	}

	lang := "unknown"
	if len(fp.Languages) > 0 {
		lang = fp.Languages[0].Language
	}
	if lang == "unknown" {
		if detected := m.detectSandboxLanguage(ctx, containerID, projectDir); detected != "" {
			lang = detected
		}
	}

	// Look up commands
	cmds := languageCommands[lang]
	if cmds == nil {
		cmds = fallbackCommands
	}

	gateStart := time.Now()
	allPassed := true
	results := make([]qualityCheckResult, 0, len(checks))

	for _, check := range checks {
		cmd, ok := cmds[check]
		if !ok {
			cmd = fallbackCommands[check]
		}
		if cmd == "" {
			continue
		}

		checkStart := time.Now()

		// Run with retry=1 for infrastructure resilience
		var result *backend.ExecResult
		execFn := func(ctx context.Context) error {
			var execErr error
			result, execErr = m.backend.Exec(ctx, backend.ExecOpts{
				ContainerID: containerID,
				Command:     cmd,
				WorkDir:     m.projectWorkDir(projectDir),
				TimeoutSec:  300, // 5 min per check
				MaxLines:    50,
			})
			return execErr
		}
		err := poll.RetryWithBackoff(ctx, 2, time.Second, 4*time.Second, execFn)

		checkDuration := time.Since(checkStart).Milliseconds()
		cr := qualityCheckResult{
			Name:       check,
			DurationMs: checkDuration,
		}

		if err != nil {
			cr.Passed = false
			cr.OutputTail = err.Error()
			allPassed = false
		} else {
			cr.ExitCode = result.ExitCode
			cr.Passed = result.ExitCode == 0
			if !cr.Passed {
				// Surface stderr when stdout is empty so `make` errors like
				// "*** No rule to make target 'fmt'" stop showing up as a
				// blank Output in escalations. Truncate independently so
				// neither stream crowds the other out of the artifact.
				cr.OutputTail = truncateOutput(result.StdoutTail, 500)
				cr.StderrTail = truncateOutput(result.StderrTail, 500)
				if cr.OutputTail == "" {
					cr.OutputTail = cr.StderrTail
				}
				allPassed = false
			}
		}

		results = append(results, cr)

		if !cr.Passed && failFast {
			break
		}
	}

	gateResult := qualityGateResult{
		Language:        lang,
		Passed:          allPassed,
		Checks:          results,
		TotalDurationMs: time.Since(gateStart).Milliseconds(),
	}

	m.logger.Info("quality gate", "project", projectName, "language", lang,
		"passed", allPassed, "duration_ms", gateResult.TotalDurationMs)

	if m.events != nil {
		m.events.Emit(ctx, "quality_gate", projectName,
			fmt.Sprintf("passed=%v language=%s duration=%dms", allPassed, lang, gateResult.TotalDurationMs))
	}

	return mcp.JSONResult(gateResult)
}

// detectSandboxLanguage probes the running sandbox for a language marker.
//
// It tries projectWorkDir first (where git-clone deposits source) and
// falls back to /workspace so tar-pipe-era sandboxes and any layout
// drift still resolve. Every attempt is logged with stdout/stderr/exit
// so empty results don't disappear into the void.
func (m *manager) detectSandboxLanguage(ctx context.Context, containerID, projectDir string) string {
	project := filepath.Base(projectDir)
	defaultWorkDir := m.projectWorkDir(projectDir)
	for _, wd := range sandboxLanguageProbePaths {
		if wd == "" {
			wd = defaultWorkDir
		}
		result, err := m.backend.Exec(ctx, backend.ExecOpts{
			ContainerID: containerID,
			Command:     sandboxLanguageProbeCommand,
			WorkDir:     wd,
			TimeoutSec:  10,
			MaxLines:    1,
		})
		if m.logger != nil {
			exit := -1
			var stdout, stderr string
			if result != nil {
				exit = result.ExitCode
				stdout = strings.TrimSpace(result.StdoutTail)
				stderr = strings.TrimSpace(result.StderrTail)
			}
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			m.logger.Info("sandbox language probe",
				"project", project,
				"workdir", wd,
				"exit", exit,
				"stdout", stdout,
				"stderr", stderr,
				"error", errStr,
			)
		}
		if err != nil || result == nil || result.ExitCode != 0 {
			continue
		}
		switch strings.TrimSpace(result.StdoutTail) {
		case "go", "python", "node", "rust":
			return strings.TrimSpace(result.StdoutTail)
		}
	}
	return ""
}

// truncateOutput returns the last N bytes of output.
func truncateOutput(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Find a newline boundary near the cut point
	cut := s[len(s)-maxBytes:]
	if idx := strings.Index(cut, "\n"); idx > 0 {
		return "..." + cut[idx:]
	}
	return "..." + cut
}
