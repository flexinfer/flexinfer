// loom-spawn-driver entry point. Parses CLI args, dispatches to the
// appropriate SDK driver, and exits with the driver's status code.
//
// Note: the executable shebang is injected by esbuild's banner config so the
// emitted CJS bundle remains executable; we deliberately omit it from the
// TypeScript source to avoid double-shebang errors after bundling.

import { parseArgs } from "./cli.js";
import { runClaudeDriver } from "./claude-driver.js";
import { runCodexDriver } from "./codex-driver.js";
import { emitFatal } from "./jsonl.js";

async function main(): Promise<void> {
  const args = parseArgs(process.argv);

  let exitCode = 0;
  switch (args.agentType) {
    case "claude-code":
    case "claude":
      exitCode = await runClaudeDriver(args);
      break;
    case "codex":
      exitCode = await runCodexDriver(args);
      break;
    default:
      emitFatal(`unsupported agent type: ${args.agentType}`);
      exitCode = 1;
  }
  process.exit(exitCode);
}

main().catch((err: unknown) => {
  emitFatal(`unhandled top-level error: ${err instanceof Error ? err.message : String(err)}`);
  process.exit(1);
});
