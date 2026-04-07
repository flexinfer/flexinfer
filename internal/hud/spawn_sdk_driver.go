package hud

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
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

	// spawnControlFileDir is the in-pod directory that holds per-spawn JSONL
	// control files for multi-turn driver mode (slice 8a/8b). It is created
	// lazily by injectControlFile so single-shot spawns never touch it.
	spawnControlFileDir = "/opt/loom/control"
)

// controlFilePathForSpawn returns the in-pod absolute path of the JSONL
// control file for a given spawn. The path is stable for the lifetime of
// the spawn so the orchestrator's REST handlers (slice 8c) can append to
// the same file the driver tails. Spawn IDs are sanitized to a safe shell
// subset so the path is always quote-free.
func controlFilePathForSpawn(spawnID string) string {
	safe := sanitizeSpawnID(spawnID)
	return fmt.Sprintf("%s/spawn-control-%s.jsonl", spawnControlFileDir, safe)
}

// sanitizeSpawnID strips characters that would require shell escaping or
// could traverse out of spawnControlFileDir. The orchestrator already
// generates spawn IDs as `spawn-<rand>` so the canonical case is a no-op,
// but the sanitizer keeps us safe if a future caller passes user input.
func sanitizeSpawnID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

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

// injectControlFile creates an empty JSONL control file inside the spawn pod
// for multi-turn driver mode. The driver's ControlFileReader tails this file
// for `{type:"message"|"interrupt"|"shutdown"}` commands. We create it
// before exec so the driver's fs.watch fires immediately on the first
// REST-driven append instead of waiting on the 200ms poll fallback.
func (o *SpawnOrchestrator) injectControlFile(ctx context.Context, containerID, spawnID string) error {
	path := controlFilePathForSpawn(spawnID)
	cmd := fmt.Sprintf("mkdir -p %s && : > %s", spawnControlFileDir, path)
	_, err := o.backend.Exec(ctx, backend.ExecOpts{
		ContainerID: containerID,
		Command:     cmd,
		TimeoutSec:  15,
	})
	if err != nil {
		return fmt.Errorf("create control file %s: %w", path, err)
	}
	return nil
}

// SpawnControlCommand mirrors the wire format the spawn-driver consumes
// (tools/spawn-driver/src/control-file.ts). The Go orchestrator constructs
// these and serializes them as one JSON object per line into the per-spawn
// control file. The Type field discriminates payload semantics:
//
//   - "message"   : push a follow-up user turn (Text required)
//   - "interrupt" : abort the in-flight generation (no payload)
//   - "shutdown"  : graceful exit after the current turn completes
type SpawnControlCommand struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// injectControlMessage appends a single JSONL command to the spawn pod's
// control file. The driver's tail loop picks up the new line within ~200ms
// (fs.watch + poll fallback) and dispatches it to the active SDK Query or
// Codex Thread.
//
// Slice 8c will call this from REST handlers; slice 8b ships the helper so
// it's reachable and unit-tested ahead of the wire-up.
func (o *SpawnOrchestrator) injectControlMessage(ctx context.Context, containerID, spawnID string, cmd SpawnControlCommand) error {
	if cmd.Type == "" {
		return fmt.Errorf("control command type is required")
	}
	line, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal control command: %w", err)
	}
	// Use base64 to keep arbitrary user text shell-safe; the driver decodes
	// the JSONL line, not the base64 wrapper.
	encoded := base64.StdEncoding.EncodeToString(append(line, '\n'))
	path := controlFilePathForSpawn(spawnID)
	shellCmd := fmt.Sprintf("mkdir -p %s && echo '%s' | base64 -d >> %s",
		spawnControlFileDir, encoded, path)
	_, err = o.backend.Exec(ctx, backend.ExecOpts{
		ContainerID: containerID,
		Command:     shellCmd,
		TimeoutSec:  15,
	})
	if err != nil {
		return fmt.Errorf("append control command to %s: %w", path, err)
	}
	return nil
}

// buildSDKDriverCommand constructs the shell command to invoke the
// loom-spawn-driver bundle. The driver emits JSONL events on stdout that the
// existing Claude/Codex parsers consume. Optional budget flags are forwarded
// so a follow-up slice can wire SDK-side enforcement. When controlFilePath is
// non-empty the driver enters multi-turn mode and tails the file for
// follow-up commands (slice 8a).
func buildSDKDriverCommand(agentType, task, agentID, spawnID, workingDir, controlFilePath string, maxTurns int, maxCostUSD float64) string {
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
	if controlFilePath != "" {
		fmt.Fprintf(&b, " --control-file %s", shellQuote(controlFilePath))
	}
	return b.String()
}

// shellQuote returns a single-quoted shell-safe representation of s. Embedded
// single quotes are escaped via the standard '\” splice.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
