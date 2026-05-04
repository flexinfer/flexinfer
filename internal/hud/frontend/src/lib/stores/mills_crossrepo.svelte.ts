// Mills Cross-Repo store — atomic-merge run inventory + abort surface
// from the loom-mills-operator, proxied through /api/mills/cross-repo/* by
// the HUD's domain/mills package. Polls the list endpoint at 15s by default
// so the cadence agrees with the rest of the Mills panels.
//
// Empty/disabled state: 503 from the proxy means the HUD has no operator
// URL set; surface that as a calm empty state rather than a fetch error
// (mirrors mills_squads.svelte.ts).

import {
  inFlightStates,
  terminalStates,
  type CrossRepoAbortResponse,
  type CrossRepoListResponse,
  type CrossRepoRun,
  type CrossRepoState,
} from './mills_crossrepo_types.ts';

const ATOMICITY_WINDOW = 30;

class MillsCrossRepoStore {
  runs = $state<CrossRepoRun[]>([]);
  details = $state<Record<string, CrossRepoRun>>({});

  loading = $state(false);
  error = $state<string | null>(null);
  disabled = $state(false);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  // atomicityRate is a derived value computed off the most recent
  // ATOMICITY_WINDOW runs; the spec calls this out as the headline
  // health metric for the cross-repo card.
  atomicityRate = $derived.by(() => {
    const window = this.recentRuns();
    let merged = 0;
    let denom = 0;
    for (const run of window) {
      if (terminalStates.has(run.state)) {
        denom++;
        if (run.state === 'merged') merged++;
      }
    }
    if (denom === 0) return null;
    return merged / denom;
  });

  inFlightCount = $derived.by(
    () => this.runs.filter((r) => inFlightStates.has(r.state)).length,
  );

  mergedTodayCount = $derived.by(() => this.countToday('merged'));
  revertedTodayCount = $derived.by(() => this.countToday('reverted'));

  async refresh(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const resp = await this.getJSON<CrossRepoListResponse>('/api/mills/cross-repo/runs');
      this.runs = resp?.runs ?? [];
      this.lastUpdated = new Date();
      this.disabled = false;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (msg.includes('503') || msg.toLowerCase().includes('not configured')) {
        this.disabled = true;
        this.error = null;
        this.runs = [];
      } else {
        this.disabled = false;
        this.error = msg;
      }
    } finally {
      this.loading = false;
    }
  }

  async fetchDetail(id: string): Promise<CrossRepoRun | null> {
    if (!id) return null;
    try {
      const detail = await this.getJSON<CrossRepoRun>(
        `/api/mills/cross-repo/runs/${encodeURIComponent(id)}`,
      );
      if (detail) {
        this.details = { ...this.details, [id]: detail };
      }
      return detail;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (!msg.includes('404')) {
        this.error = msg;
      }
      return null;
    }
  }

  // abort POSTs to /abort with the operator-supplied admin token. The
  // returned promise resolves with the operator's transition payload so
  // callers can display the previous→new state line; on failure the error
  // bubbles up to the panel for inline rendering.
  async abort(id: string, adminToken: string): Promise<CrossRepoAbortResponse> {
    if (!id) throw new Error('run id is required');
    if (!adminToken) throw new Error('admin token is required to abort a cross-repo run');
    const res = await globalThis.fetch(
      `/api/mills/cross-repo/runs/${encodeURIComponent(id)}/abort`,
      {
        method: 'POST',
        headers: { Authorization: `Bearer ${adminToken}` },
      },
    );
    if (res.status === 401 || res.status === 403) {
      throw new Error(`abort rejected: ${res.status} (admin token missing or invalid)`);
    }
    if (res.status === 404) throw new Error(`run ${id} not found`);
    if (res.status === 409) throw new Error(`run ${id} is already in a terminal state`);
    if (!res.ok) throw new Error(`abort: ${res.status}`);
    const text = await res.text();
    return JSON.parse(text) as CrossRepoAbortResponse;
  }

  startPolling(intervalMs = 15000): void {
    this.stopPolling();
    void this.refresh();
    this.pollTimer = setInterval(() => void this.refresh(), intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }

  // recentRuns returns the latest ATOMICITY_WINDOW runs by created_at desc.
  // We sort defensively because the operator may emit any order; computing
  // off a stable subset keeps the rate metric from oscillating wildly when
  // a long-running planning state lands at the head of the list.
  private recentRuns(): CrossRepoRun[] {
    if (this.runs.length === 0) return [];
    const sorted = [...this.runs].sort((a, b) => {
      const ta = Date.parse(a.created_at) || 0;
      const tb = Date.parse(b.created_at) || 0;
      return tb - ta;
    });
    return sorted.slice(0, ATOMICITY_WINDOW);
  }

  private countToday(state: CrossRepoState): number {
    const startOfDay = new Date();
    startOfDay.setHours(0, 0, 0, 0);
    const cutoff = startOfDay.getTime();
    let n = 0;
    for (const run of this.runs) {
      if (run.state !== state) continue;
      const t = Date.parse(run.updated_at) || 0;
      if (t >= cutoff) n++;
    }
    return n;
  }

  private async getJSON<T>(path: string): Promise<T | null> {
    const res = await globalThis.fetch(path);
    if (res.status === 503) {
      throw new Error(`mills proxy: 503 (operator not configured)`);
    }
    if (res.status === 404) {
      return null;
    }
    if (!res.ok) {
      throw new Error(`${path}: ${res.status}`);
    }
    const text = await res.text();
    if (!text) return null;
    return JSON.parse(text) as T;
  }
}

export const millsCrossRepoStore = new MillsCrossRepoStore();
