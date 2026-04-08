// Types for SpawnTelemetry tab components. These mirror the Go shapes in
// internal/hud/bridge/spawn_telemetry.go and the sub-endpoint response
// envelopes in internal/hud/domain/spawn/handler_telemetry.go.
//
// The paginated sub-endpoints return:
//   { spawn_id, items: [...], total: number, limit: number, offset: number }
// (offset-based pagination, not cursor-based).

export interface ToolCallEntry {
  name: string;
  server_name?: string;
  duration_ms?: number;
  exit_code?: number;
  error?: string;
  timestamp: string;
}

export interface FileChangeEntry {
  path: string;
  kind: string; // "create" | "modify" | "delete"
  // The backend shape currently carries only path + kind. Optional counters
  // are typed in case a future slice extends FileChangeEntry with diff stats.
  lines_added?: number;
  lines_removed?: number;
}

export interface AgentErrorEntry {
  type: string; // "max_turns" | "max_budget" | "rate_limit" | "execution" | "tool_failure" | "permission_denied" | "fatal"
  message: string;
  time: string;
}

export interface PaginatedResponse<T> {
  spawn_id?: string;
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

export const PAGE_LIMIT = 50;
