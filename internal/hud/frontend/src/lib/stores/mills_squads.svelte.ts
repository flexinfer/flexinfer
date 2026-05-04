// Mills Squads store — per-squad rows + outcome stats from the
// loom-mills-operator, proxied through /api/mills/squads* by the HUD's
// domain/mills package. Polls the list endpoint at 15s by default so the
// cadence agrees with the rest of the Mills panels.
//
// Empty/disabled state: 503 from the proxy means the HUD has no operator
// URL set; surface that as a calm empty state rather than a fetch error.

import type { Squad, SquadMemory } from './mills_squads_types.ts';

// SquadOutcomeStats mirrors the operator's squadsOutcomeStat shape.
// Field names are snake_case to match the JSON the operator returns; the
// store keeps that on purpose so a panel can render a `field` directly
// without an extra mapping layer.
export interface SquadOutcomeStats {
  window: number;
  total: number;
  merged_clean: number;
  merged_regressed: number;
  failed: number;
  self_vetoed: number;
  success_rate: number;
  total_cost_usd: number;
  in_flight: number;
}

// SquadsListEntry is one row from GET /api/mills/squads.
export interface SquadsListEntry {
  squad: Squad;
  outcome_stats: SquadOutcomeStats;
}

// SquadDetail is the response from GET /api/mills/squads/{name}. The store
// keeps the latest detail per squad so a panel can render the recent
// memory inline without an extra request when the user expands a card.
export interface SquadDetail {
  squad: Squad;
  recent_memory: SquadMemory[];
  recent_outcomes: unknown[];
  outcome_stats: SquadOutcomeStats;
}

class MillsSquadsStore {
  state = $state<SquadsListEntry[]>([]);
  details = $state<Record<string, SquadDetail>>({});

  loading = $state(false);
  error = $state<string | null>(null);
  disabled = $state(false);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  async refresh(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const list = await this.getJSON<SquadsListEntry[]>('/api/mills/squads');
      this.state = list ?? [];
      this.lastUpdated = new Date();
      this.disabled = false;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (msg.includes('503') || msg.toLowerCase().includes('not configured')) {
        this.disabled = true;
        this.error = null;
        this.state = [];
      } else {
        this.disabled = false;
        this.error = msg;
      }
    } finally {
      this.loading = false;
    }
  }

  async fetchDetail(name: string): Promise<SquadDetail | null> {
    if (!name) return null;
    try {
      const detail = await this.getJSON<SquadDetail>(
        `/api/mills/squads/${encodeURIComponent(name)}`,
      );
      if (detail) {
        this.details = { ...this.details, [name]: detail };
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

export const millsSquadsStore = new MillsSquadsStore();
