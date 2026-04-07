package hud

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/crb2nu/loom/internal/devbox/backend"
)

// spawnDriverBundle is the bundled JavaScript implementation of the
// loom-spawn-driver, embedded at compile time. The source bundle lives at
// tools/spawn-driver/dist/spawn-driver.js and is synced into this package via
// `make sync-spawn-driver`. Slice 7a ships a hand-written stub bundle; slice 7c
// will replace it with an esbuild-generated bundle that wraps the real
// @anthropic-ai/claude-agent-sdk and @openai/codex-sdk SDKs.
//
//go:embed spawn_driver_bundle.js
var spawnDriverBundle []byte

const (
	// spawnDriverPodPath is where the driver bundle is written inside the
	// spawn pod. /opt/loom is created on demand by injectSDKDriver.
	spawnDriverPodPath = "/opt/loom/spawn-driver.js"

	// spawnDriverPodDir is the parent directory of spawnDriverPodPath.
	spawnDriverPodDir = "/opt/loom"
)

// injectSDKDriver writes the embedded spawn-driver bundle into a running spawn
// pod. It uses the same base64-encoded `cat | base64 -d` heredoc pattern as
// injectAgentConfig (stdout-only Exec) to avoid the K3s SPDY stdin hangs
// documented in spawn.go.
func (o *SpawnOrchestrator) injectSDKDriver(ctx context.Context, containerID string) error {
	if len(spawnDriverBundle) == 0 {
		return fmt.Errorf("spawn driver bundle is empty (build asset missing)")
	}

	encoded := base64.StdEncoding.EncodeToString(spawnDriverBundle)
	cmd := fmt.Sprintf(
		"mkdir -p %s && echo '%s' | base64 -d > %s && chmod +x %s",
		spawnDriverPodDir, encoded, spawnDriverPodPath, spawnDriverPodPath,
	)

	_, err := o.backend.Exec(ctx, backend.ExecOpts{
		ContainerID: containerID,
		Command:     cmd,
		TimeoutSec:  30,
	})
	if err != nil {
		return fmt.Errorf("inject spawn driver bundle: %w", err)
	}
	return nil
}

// buildSDKDriverCommand constructs the shell command to invoke the
// loom-spawn-driver bundle. The driver emits JSONL events on stdout that the
// existing Claude/Codex parsers consume. Optional budget flags are forwarded
// so a follow-up slice can wire SDK-side enforcement.
func buildSDKDriverCommand(agentType, task, agentID, spawnID, workingDir string, maxTurns int, maxCostUSD float64) string {
	var b strings.Builder
	b.WriteString("node ")
	b.WriteString(spawnDriverPodPath)
	fmt.Fprintf(&b, " --agent-type %s", shellQuote(agentType))
	fmt.Fprintf(&b, " --task %s", shellQuote(task))
	fmt.Fprintf(&b, " --agent-id %s", shellQuote(agentID))
	fmt.Fprintf(&b, " --spawn-id %s", shellQuote(spawnID))
	if workingDir != "" {
		fmt.Fprintf(&b, " --working-dir %s", shellQuote(workingDir))
	}
	if maxTurns > 0 {
		fmt.Fprintf(&b, " --max-turns %d", maxTurns)
	}
	if maxCostUSD > 0 {
		fmt.Fprintf(&b, " --max-cost-usd %.4f", maxCostUSD)
	}
	return b.String()
}

// shellQuote returns a single-quoted shell-safe representation of s. Embedded
// single quotes are escaped via the standard '\” splice.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
