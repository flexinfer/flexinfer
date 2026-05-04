// Cross-repo run types — mirrors the slice 4.4 REST shape from
// GET /api/mills/cross-repo/runs and ../{id}. Field names are snake_case
// to match the JSON the operator returns; keeping that on purpose so
// panels can render `entry.field` directly without a mapping layer.

export type CrossRepoState =
  | 'planning'
  | 'open'
  | 'gates_green'
  | 'merging'
  | 'merged'
  | 'reverted'
  | 'failed';

export interface CrossRepoRepoEntry {
  project_id: number;
  repo_name?: string;
  branch: string;
  mr_iid?: number;
  ci_status?: string;
  gate_status?: string;
}

export interface CrossRepoRun {
  id: string;
  backlog_item_id: string;
  state: CrossRepoState;
  atomicity_strategy: string;
  repos: CrossRepoRepoEntry[];
  created_at: string;
  updated_at: string;
}

export interface CrossRepoListResponse {
  runs: CrossRepoRun[];
  total: number;
  limit: number;
  filter?: string;
}

export interface CrossRepoAbortResponse {
  id: string;
  state: CrossRepoState;
  previous_state: CrossRepoState;
  aborted_at: string;
}

// Set of states that count as "in flight" for the HUD KPI block. Terminal
// states (merged/reverted/failed) are excluded so the operator can tell at
// a glance how many runs are still moving.
export const inFlightStates: ReadonlySet<CrossRepoState> = new Set<CrossRepoState>([
  'open',
  'gates_green',
  'merging',
]);

export const terminalStates: ReadonlySet<CrossRepoState> = new Set<CrossRepoState>([
  'merged',
  'reverted',
  'failed',
]);
