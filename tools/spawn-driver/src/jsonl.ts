// JSONL emit helpers shared by the Claude and Codex drivers. Every event is
// written as a single JSON object terminated by a newline so the Go HUD's
// line-callback machinery (internal/devbox/backend/stream_exec.go) can feed
// each line directly to the appropriate parser.

export function emit(event: unknown): void {
  try {
    process.stdout.write(JSON.stringify(event) + "\n");
  } catch (err) {
    // If serialization fails, surface a structured error so the parser still
    // sees a fatal-level event instead of a silent stall.
    process.stdout.write(
      JSON.stringify({
        type: "error",
        message: `spawn-driver: failed to serialize event: ${err instanceof Error ? err.message : String(err)}`,
      }) + "\n",
    );
  }
}

export function emitFatal(message: string): void {
  emit({ type: "error", message: `spawn-driver: ${message}` });
}
