// Fixture / smoke test for the Mills Overview "System Health" banner.
//
// The HUD frontend doesn't ship a test runner (no vitest, no jest), so this
// file is a runnable self-check: import it from a small script or paste the
// helper into a REPL to drive each banner state. Pure data, no DOM, no
// rune access — safe to run under plain `node --experimental-strip-types`.
//
// Run with esbuild/tsx if you need to:
//   pnpm dlx tsx internal/hud/frontend/src/lib/stores/mills.systemHealth.fixture.ts
//
// What the four cases here demonstrate:
//   1. broken    — escalations happened, zero merges in last 24h
//   2. in_flight — at least one active pipeline run
//   3. idle      — council has never run, no backlog, no merges
//   4. healthy   — merges in 24h, no escalations, nothing active

import type {
  BacklogItemLike,
  CouncilRunLike,
  MillsStatusLike,
  PipelineRunLike,
  SystemHealth,
} from './mills.systemHealth.ts';
import { computeSystemHealth } from './mills.systemHealth.ts';

// Aliases keep the fixture cases readable while still typechecking against
// the pure-helper shapes.
type BacklogItem = BacklogItemLike & { Title?: string; State?: string; Priority?: string };
type CouncilRun = CouncilRunLike & { Trigger?: string; Outcome?: string };
type MillsStatus = MillsStatusLike;
type PipelineRun = PipelineRunLike & { BacklogID?: string; Template?: string; Attempts?: number };

const NOW = Date.parse('2026-05-16T12:00:00Z');
const HOUR_AGO = new Date(NOW - 60 * 60 * 1000).toISOString();
const TWO_DAYS_AGO = new Date(NOW - 48 * 60 * 60 * 1000).toISOString();

function run(name: string, h: SystemHealth, expect: Partial<SystemHealth>): boolean {
  let ok = true;
  for (const [k, v] of Object.entries(expect)) {
    if ((h as unknown as Record<string, unknown>)[k] !== v) {
      console.error(`FAIL ${name}: expected ${k}=${String(v)}, got ${String((h as unknown as Record<string, unknown>)[k])}`);
      ok = false;
    }
  }
  if (ok) console.log(`PASS ${name}: state=${h.state} escalations=${h.escalations_24h} merges=${h.merges_24h} active=${h.active_runs}`);
  return ok;
}

const baseStatus: MillsStatus = {
  ok: true,
  active_pipeline_runs: 0,
  queue_depth: 0,
};

// 1. broken: 3 escalations in 24h, zero merges.
const broken = computeSystemHealth({
  pipelineRuns: [
    { ID: 'r1', BacklogID: 'b1', Template: 't', State: 'escalated', Attempts: 1, EndedAt: HOUR_AGO },
    { ID: 'r2', BacklogID: 'b2', Template: 't', State: 'escalated', Attempts: 1, EndedAt: HOUR_AGO },
    { ID: 'r3', BacklogID: 'b3', Template: 't', State: 'escalated', Attempts: 1, EndedAt: HOUR_AGO },
  ] as PipelineRun[],
  status: baseStatus,
  councilRuns: [{ ID: 'c1', Trigger: 'cron', Outcome: 'ok' } as CouncilRun],
  backlog: [{ ID: 'b1', Title: 't', State: 'escalated', Priority: 'P1' } as BacklogItem],
  now: NOW,
});
let allOk = true;
allOk = run('broken', broken, { state: 'broken', escalations_24h: 3, merges_24h: 0 }) && allOk;

// 2. in_flight: two active runs, none escalated, no merges yet.
const inFlight = computeSystemHealth({
  pipelineRuns: [
    { ID: 'r1', BacklogID: 'b1', Template: 't', State: 'implementing', Attempts: 1, StartedAt: HOUR_AGO },
    { ID: 'r2', BacklogID: 'b2', Template: 't', State: 'ci', Attempts: 1, StartedAt: HOUR_AGO },
  ] as PipelineRun[],
  status: { ...baseStatus, active_pipeline_runs: 2 },
  councilRuns: [{ ID: 'c1', Trigger: 'cron', Outcome: 'ok' } as CouncilRun],
  backlog: [{ ID: 'b1', Title: 't', State: 'ready', Priority: 'P1' } as BacklogItem],
  now: NOW,
});
allOk = run('in_flight', inFlight, { state: 'in_flight', active_runs: 2, escalations_24h: 0 }) && allOk;

// 3. idle: council has never run, backlog empty, no merges, nothing active.
const idle = computeSystemHealth({
  pipelineRuns: [],
  status: baseStatus,
  councilRuns: [],
  backlog: [],
  now: NOW,
});
allOk = run('idle', idle, { state: 'idle', council_runs_total: 0, merges_24h: 0 }) && allOk;

// 4. healthy: a merge inside the 24h window, no escalations, nothing active.
const healthy = computeSystemHealth({
  pipelineRuns: [
    { ID: 'r1', BacklogID: 'b1', Template: 't', State: 'done', Attempts: 1, EndedAt: HOUR_AGO },
  ] as PipelineRun[],
  status: baseStatus,
  councilRuns: [{ ID: 'c1', Trigger: 'cron', Outcome: 'ok' } as CouncilRun],
  backlog: [],
  now: NOW,
});
allOk = run('healthy', healthy, { state: 'healthy', merges_24h: 1, escalations_24h: 0 }) && allOk;

// 5. negative case: an old merge (>24h) should NOT count, falls to idle.
const oldMerge = computeSystemHealth({
  pipelineRuns: [
    { ID: 'r1', BacklogID: 'b1', Template: 't', State: 'done', Attempts: 1, EndedAt: TWO_DAYS_AGO },
  ] as PipelineRun[],
  status: baseStatus,
  councilRuns: [],
  backlog: [],
  now: NOW,
});
allOk = run('old-merge-no-count', oldMerge, { merges_24h: 0 }) && allOk;
// last_successful_merge_at still exposed for "last merged X days ago" copy
if (oldMerge.last_successful_merge_at !== TWO_DAYS_AGO) {
  console.error('FAIL old-merge-no-count: last_successful_merge_at not propagated');
  allOk = false;
}

if (!allOk) {
  console.error('mills.systemHealth fixture: FAILURES detected');
  // Avoid process.exit so this remains safely importable for SSR-ish flows.
  throw new Error('system-health fixture failed');
}
console.log('mills.systemHealth fixture: all cases pass');
