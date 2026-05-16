// Pure helpers + types for the Mills Overview "System Health" banner.
//
// Split out of mills.svelte.ts so the logic stays runnable under plain
// Node (no $state rune dependency) — fixture/test scripts and SSR helpers
// can import these without dragging in the rune-based store. The
// rune-based store re-exports SystemHealth / computeSystemHealth so panel
// code keeps `import ... from './mills.svelte.ts'` ergonomics.

// Minimal shapes — duplicated by name from mills.svelte.ts on purpose.
// We intentionally avoid `import type` from the rune file so the bundler
// can tree-shake this helper without dragging in the store module graph.
export interface PipelineRunLike {
  ID: string;
  State: string;
  StartedAt?: string;
  EndedAt?: string;
}

export interface MillsStatusLike {
  active_pipeline_runs?: number;
  queue_depth?: number;
}

export interface CouncilRunLike {
  ID: string;
}

export interface BacklogItemLike {
  ID: string;
}

// SystemHealthState is the high-level behavioral health the Overview
// banner surfaces *above* capability health. The capability summary can
// be all green while the pipeline is in a failure loop (lots of
// escalations, zero merges), which is exactly the scenario the banner
// exists to flag.
export type SystemHealthState = 'broken' | 'in_flight' | 'idle' | 'healthy';

export interface SystemHealth {
  state: SystemHealthState;
  escalations_24h: number;
  merges_24h: number;
  active_runs: number;
  queued: number;
  last_successful_merge_at: string | null;
  council_runs_total: number;
}

// Pipeline states the operator emits for an actively-running run. We
// treat anything that isn't a terminal state (done/escalated/paused) as
// active so the banner can say "X pipelines in flight" without leaning
// on the status.active_pipeline_runs counter (which may lag the run list
// during transitions).
const PIPELINE_ACTIVE_STATES: ReadonlySet<string> = new Set([
  'queued',
  'planning',
  'slicing',
  'implementing',
  'testing',
  'reviewing',
  'mr',
  'ci',
  'merging',
]);

// Pipeline states the operator emits for a *successful* merge. The
// canonical terminal-success state is `done`, but some surfaces (and the
// PipelinesPanel CSS) also recognize `merged`; accept both so the banner
// stays honest if either lands in the future.
const PIPELINE_MERGED_STATES: ReadonlySet<string> = new Set(['done', 'merged']);

const PIPELINE_ESCALATED_STATES: ReadonlySet<string> = new Set(['escalated']);

const TWENTY_FOUR_HOURS_MS = 24 * 60 * 60 * 1000;

// computeSystemHealth derives the banner state from the pipeline-run
// list, status counters, and council run history. Pure — no DOM, no
// runes, no fetch — so it's safe to call from fixtures and unit tests
// without a Svelte runtime.
export function computeSystemHealth(input: {
  pipelineRuns: PipelineRunLike[];
  status: MillsStatusLike | null;
  councilRuns: CouncilRunLike[];
  backlog: BacklogItemLike[];
  now?: number;
}): SystemHealth {
  const now = input.now ?? Date.now();
  const cutoff = now - TWENTY_FOUR_HOURS_MS;

  let escalations24h = 0;
  let merges24h = 0;
  let activeRuns = 0;
  let queued = 0;
  let lastSuccessfulMergeAt: string | null = null;
  let lastSuccessfulMergeTs = 0;

  for (const run of input.pipelineRuns) {
    const state = (run.State ?? '').toLowerCase();
    const endedTs = run.EndedAt ? Date.parse(run.EndedAt) : NaN;
    const startedTs = run.StartedAt ? Date.parse(run.StartedAt) : NaN;
    // Use EndedAt for terminal states; fall back to StartedAt so a run
    // with a missing EndedAt (older operator) still contributes.
    const terminalTs = Number.isFinite(endedTs) ? endedTs : startedTs;

    if (PIPELINE_ACTIVE_STATES.has(state)) {
      activeRuns += 1;
      if (state === 'queued') queued += 1;
    } else if (PIPELINE_MERGED_STATES.has(state)) {
      if (Number.isFinite(terminalTs) && terminalTs >= cutoff) merges24h += 1;
      if (Number.isFinite(terminalTs) && terminalTs > lastSuccessfulMergeTs) {
        lastSuccessfulMergeTs = terminalTs;
        lastSuccessfulMergeAt = run.EndedAt ?? run.StartedAt ?? null;
      }
    } else if (PIPELINE_ESCALATED_STATES.has(state)) {
      if (Number.isFinite(terminalTs) && terminalTs >= cutoff) escalations24h += 1;
    }
  }

  // Prefer the live operator counters when available; they reflect
  // truth-of-record (active runs in flight, queue depth) rather than the
  // truncated `pipeline_runs` page we polled. Fall back to derived
  // counts otherwise.
  if (typeof input.status?.active_pipeline_runs === 'number') {
    activeRuns = input.status.active_pipeline_runs;
  }
  if (typeof input.status?.queue_depth === 'number') {
    queued = input.status.queue_depth;
  }

  const councilRunsTotal = input.councilRuns.length;
  const backlogEmpty = input.backlog.length === 0;

  // State priority (highest first):
  //   broken     — escalations happened AND nothing merged in 24h
  //   in_flight  — any pipeline currently running
  //   idle       — council has never run + no backlog + no merges (vacuum)
  //   healthy    — escalations==0 AND at least one merge in 24h
  // Mixed signal (escalations + some merges, or merges with nothing
  // active) falls through to `healthy` so the banner stays quiet; the
  // underlying KPI cards already surface the partial-failure signal.
  let state: SystemHealthState;
  if (escalations24h > 0 && merges24h === 0) {
    state = 'broken';
  } else if (activeRuns > 0) {
    state = 'in_flight';
  } else if (councilRunsTotal === 0 && backlogEmpty && merges24h === 0) {
    state = 'idle';
  } else if (escalations24h === 0 && merges24h > 0) {
    state = 'healthy';
  } else {
    state = 'healthy';
  }

  return {
    state,
    escalations_24h: escalations24h,
    merges_24h: merges24h,
    active_runs: activeRuns,
    queued,
    last_successful_merge_at: lastSuccessfulMergeAt,
    council_runs_total: councilRunsTotal,
  };
}
