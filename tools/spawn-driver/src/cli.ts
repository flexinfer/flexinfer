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
  dryRun: false,
};

export function parseArgs(argv: readonly string[]): DriverArgs {
  const args: DriverArgs = { ...DEFAULT_ARGS };
  for (let i = 2; i < argv.length; i++) {
    const flag = argv[i];
    if (typeof flag !== "string" || !flag.startsWith("--")) continue;
    const key = flag.slice(2);
    const next = argv[i + 1];
    const isBoolean = key === "dry-run";
    if (isBoolean) {
      args.dryRun = true;
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
      default:
        // Unknown flag — ignore for forward compatibility.
        break;
    }
  }
  return args;
}
