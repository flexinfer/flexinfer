// Pure helpers for the Spawn panel. Extracted from SpawnPanel.svelte
// during the Slice B2 panel decomp so display formatting + filtering live
// in one testable module instead of inside the .svelte template.

import type { SpawnState, SpawnTelemetry } from '../stores/spawn.svelte';

export function filterSpawns(
  spawns: SpawnState[],
  statusFilter: string,
  searchQuery: string,
): SpawnState[] {
  let result = spawns;
  if (statusFilter === 'active') {
    result = result.filter((s) => s.status === 'creating' || s.status === 'building' || s.status === 'running');
  } else if (statusFilter === 'completed') {
    result = result.filter((s) => s.status === 'completed');
  } else if (statusFilter === 'failed') {
    result = result.filter((s) => s.status === 'failed' || s.status === 'stopped');
  }
  const q = searchQuery.trim().toLowerCase();
  if (q) {
    result = result.filter((s) =>
      s.request.project.toLowerCase().includes(q) ||
      s.request.task_description.toLowerCase().includes(q) ||
      s.agent_id.toLowerCase().includes(q) ||
      s.request.agent_type.toLowerCase().includes(q)
    );
  }
  return result;
}

export function taskSummary(text: string | undefined | null): { firstLine: string; hasMore: boolean } {
  const t = (text ?? '').trim();
  if (!t) return { firstLine: '(no task)', hasMore: false };
  const first = t.split(/\r?\n/)[0]?.trim() ?? '';
  return { firstLine: first || t, hasMore: t.length > first.length };
}

// classifySpawnError lifts a one-line headline + a coarse "kind" out of
// the noisy multi-line errors that spawn pods produce (buildah dumps,
// quota strings, HUD spawn failures, etc.). The full text is still
// rendered inside the <details> body — this is purely to give the card a
// meaningful header instead of a wall of red text.
export function classifySpawnError(raw: string): { kind: string; headline: string } {
  const trimmed = (raw ?? '').trim();
  if (!trimmed) return { kind: 'unknown', headline: '(empty error)' };
  const lc = trimmed.toLowerCase();
  let kind = 'error';
  if (lc.includes('exceeded quota') || lc.includes('forbidden: exceeded')) kind = 'quota';
  else if (lc.includes('image build failed') || lc.includes('buildah') || lc.includes('build pod failed')) kind = 'build';
  else if (lc.includes('connection refused') || lc.includes('dial tcp')) kind = 'network';
  else if (lc.includes('max concurrent') || lc.includes('max retries')) kind = 'throttle';
  else if (lc.includes('not found') || lc.includes('errimagepull')) kind = 'missing';
  else if (lc.includes('budget') || lc.includes('cost cap')) kind = 'budget';
  else if (lc.includes('timeout') || lc.includes('timed out')) kind = 'timeout';
  else if (lc.includes('syntax error') || lc.includes('parse error')) kind = 'syntax';
  let headline = trimmed.split(/\r?\n/).find((line) => line.trim().length > 0)?.trim() ?? trimmed;
  if (headline.length > 160) headline = headline.slice(0, 157) + '…';
  return { kind, headline };
}

export function statusColor(status: string): string {
  switch (status) {
    case 'running': return 'var(--color-success, #22c55e)';
    case 'building': return 'var(--color-info, #60a5fa)';
    case 'creating': return 'var(--color-info, #3b82f6)';
    case 'completed': return 'var(--color-muted, #6b7280)';
    case 'failed': return 'var(--color-error, #ef4444)';
    case 'stopped': return 'var(--color-warn, #f59e0b)';
    default: return 'var(--color-muted, #6b7280)';
  }
}

export function formatDuration(startedAt: string, endedAt?: string | null): string {
  const start = new Date(startedAt).getTime();
  const end = endedAt ? new Date(endedAt).getTime() : Date.now();
  const seconds = Math.floor((end - start) / 1000);
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

export function hasBudget(s: SpawnState): boolean {
  return Boolean(s.request.max_cost_usd || s.request.max_turns);
}

export function formatCostShort(usd: number): string {
  return `$${usd.toFixed(4)}`;
}

export function formatTurns(n: number): string {
  return Number.isFinite(n) ? String(Math.floor(n)) : '0';
}

// rowTelemetry returns the best-known telemetry for a list row:
//   1. Live snapshot from telemetryBySpawnId (active spawns)
//   2. Embedded telemetry on SpawnState (completed/failed/stopped)
export function rowTelemetry(
  s: SpawnState,
  liveTelemetryBySpawnId: Map<string, SpawnTelemetry>,
): SpawnTelemetry | undefined {
  const live = liveTelemetryBySpawnId.get(s.spawn_id);
  if (live) return live;
  return s.telemetry ?? undefined;
}

export function spawnStatusDot(status: string): 'healthy' | 'degraded' | 'down' | 'idle' {
  if (status === 'running') return 'healthy';
  if (status === 'creating' || status === 'building') return 'degraded';
  if (status === 'failed') return 'down';
  return 'idle';
}
