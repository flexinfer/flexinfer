// CLI argument parsing for loom-spawn-driver. Mirrors the slice 7a stub flag
// surface so the Go HUD's buildSDKDriverCommand and the existing parser tests
// can be reused unchanged.

export interface DriverArgs {
  agentType: "claude-code" | "claude" | "codex";
  task: string;
  agentId: string;
  spawnId: string;
  workingDir: string;
  maxTurns: number;
  maxCostUsd: number;
  controlPort: number;
  /**
   * Path to a JSONL control file that the Go orchestrator appends commands
   * to during the spawn lifetime. Each line is a JSON object with a "type"
   * discriminator: "message" (push a follow-up user turn), "interrupt"
   * (abort the current generation), or "shutdown" (gracefully exit after
   * the current turn completes). When empty, the driver runs in single-shot
   * mode for full backwards compatibility with pre-slice-8a callers.
   */
  controlFile: string;
  /**
   * Explicit opt-in for multi-turn mode. Required to switch Claude into
   * streaming-input mode (AsyncIterable<SDKUserMessage> prompt) and to keep
   * the Codex driver's runStreamed loop alive beyond the first turn.
   * Implied-on when controlFile is non-empty; callable as a standalone flag
   * so the Go orchestrator can opt in without having to supply a file path.
   */
  multiTurn: boolean;
  dryRun: boolean;
}

const DEFAULT_ARGS: DriverArgs = {
  agentType: "claude-code",
  task: "",
  agentId: "",
  spawnId: "",
  workingDir: "",
  maxTurns: 0,
  maxCostUsd: 0,
  controlPort: 0,
  controlFile: "",
  multiTurn: false,
  dryRun: false,
};

export function parseArgs(argv: readonly string[]): DriverArgs {
  const args: DriverArgs = { ...DEFAULT_ARGS };
  for (let i = 2; i < argv.length; i++) {
    const flag = argv[i];
    if (typeof flag !== "string" || !flag.startsWith("--")) continue;
    const key = flag.slice(2);
    const next = argv[i + 1];
    if (key === "dry-run") {
      args.dryRun = true;
      continue;
    }
    if (key === "multi-turn") {
      args.multiTurn = true;
      continue;
    }
    if (next === undefined || next.startsWith("--")) continue;
    i++;
    switch (key) {
      case "agent-type":
        if (next === "claude-code" || next === "claude" || next === "codex") {
          args.agentType = next;
        }
        break;
      case "task":
        args.task = next;
        break;
      case "agent-id":
        args.agentId = next;
        break;
      case "spawn-id":
        args.spawnId = next;
        break;
      case "working-dir":
        args.workingDir = next;
        break;
      case "max-turns":
        args.maxTurns = Number.parseInt(next, 10) || 0;
        break;
      case "max-cost-usd":
        args.maxCostUsd = Number.parseFloat(next) || 0;
        break;
      case "control-port":
        args.controlPort = Number.parseInt(next, 10) || 0;
        break;
      case "control-file":
        args.controlFile = next;
        break;
      default:
        // Unknown flag — ignore for forward compatibility.
        break;
    }
  }
  // Providing a control file always implies multi-turn mode. This keeps the
  // Go orchestrator's buildSDKDriverCommand logic simple: it can pass a
  // control file path without having to also set --multi-turn.
  if (args.controlFile) {
    args.multiTurn = true;
  }
  return args;
}
