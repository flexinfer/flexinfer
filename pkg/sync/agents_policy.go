package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

const (
	agentsSafetyBegin = "<!-- BEGIN LOOM:AGENT-SAFETY -->"
	agentsSafetyEnd   = "<!-- END LOOM:AGENT-SAFETY -->"
)

type agentSafetySettings struct {
	DirtyWorktreeMode    string
	DirtyWorktreeMessage string
}

func defaultAgentSafetySettings() agentSafetySettings {
	return agentSafetySettings{
		DirtyWorktreeMode:    "continue_scoped_commits",
		DirtyWorktreeMessage: "Dirty worktree detected. Treat pre-existing changes as baseline context, continue work, and stage/commit only files for the active task. Escalate only if new unexpected changes appear in files you are editing.",
	}
}

func loadAgentSafetySettings(reg *registry.Registry) agentSafetySettings {
	settings := defaultAgentSafetySettings()
	if reg == nil || reg.PlatformPermissions == nil {
		return settings
	}
	pp := reg.PlatformPermissions["agents"]
	if pp == nil || pp.Settings == nil {
		return settings
	}

	if v, ok := pp.Settings["dirty_worktree_mode"].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			settings.DirtyWorktreeMode = trimmed
		}
	}
	if v, ok := pp.Settings["dirty_worktree_nudge_message"].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			settings.DirtyWorktreeMessage = trimmed
		}
	}
	return settings
}

func renderAgentsSafetyBlock(settings agentSafetySettings) string {
	var sb strings.Builder
	sb.WriteString(agentsSafetyBegin + "\n")
	sb.WriteString("## Loom Agent Safety Policy (Generated)\n\n")
	sb.WriteString("- Pre-existing uncommitted/untracked files are baseline context, not an automatic blocker.\n")
	sb.WriteString("- Continue on the current branch/worktree by default.\n")
	sb.WriteString("- Stage and commit only files intentionally changed for the active task.\n")
	sb.WriteString("- Escalate only when new unexpected changes appear in files you are editing, or when a branch/worktree switch is explicitly requested.\n")
	sb.WriteString(fmt.Sprintf("- Dirty-worktree mode: `%s`.\n\n", settings.DirtyWorktreeMode))
	sb.WriteString("Canonical nudge for CLI hooks:\n")
	sb.WriteString(fmt.Sprintf("> %s\n", settings.DirtyWorktreeMessage))
	sb.WriteString("\n" + agentsSafetyEnd + "\n")
	return sb.String()
}

func upsertManagedBlock(content, begin, end, block string) string {
	start := strings.Index(content, begin)
	stop := strings.Index(content, end)
	if start >= 0 && stop > start {
		stop += len(end)
		return strings.TrimRight(content[:start], "\n") + "\n\n" + block + strings.TrimLeft(content[stop:], "\n")
	}

	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

func syncAgentsSafetyPolicy(repoRoot string, reg *registry.Registry) error {
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	block := renderAgentsSafetyBlock(loadAgentSafetySettings(reg))
	updated := upsertManagedBlock(string(data), agentsSafetyBegin, agentsSafetyEnd, block)
	if updated == string(data) {
		return nil
	}
	return os.WriteFile(agentsPath, []byte(updated), 0o644)
}
