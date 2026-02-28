// Cost store — fetches cost/usage data from GET /api/cost
// and subscribes to SSE hud.cost events for real-time updates.
import { eventStore } from './events.svelte.ts';

export interface CostSnapshot {
  enabled: boolean;
  total_calls: number;
  total_errors: number;
  total_denied: number;
  total_cached: number;
  total_duration_ms: number;
  by_agent?: CostAgentSummary[];
  by_server?: CostServerSummary[];
}

export interface CostAgentSummary {
  agent_id: string;
  call_count: number;
  errors: number;
  denied: number;
  cached: number;
}

export interface CostServerSummary {
  server: string;
  call_count: number;
  errors: number;
}

class CostStore {
  data = $state<CostSnapshot | null>(null);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get enabled(): boolean {
    return this.data?.enabled ?? false;
  }

  get totalCalls(): number {
    return this.data?.total_calls ?? 0;
  }

  get totalErrors(): number {
    return this.data?.total_errors ?? 0;
  }

  get totalDenied(): number {
    return this.data?.total_denied ?? 0;
  }

  get totalCached(): number {
    return this.data?.total_cached ?? 0;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/cost');
      if (!res.ok) throw new Error(`Cost API: ${res.status}`);
      this.data = await res.json() as CostSnapshot;
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  applySnapshot(data: Record<string, unknown>): void {
    this.data = data as unknown as CostSnapshot;
    this.lastUpdated = new Date();
    this.error = null;
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);

    this.eventUnsubs.push(
      eventStore.on('hud.cost', (e) => this.applySnapshot(e.data)),
    );
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    for (const unsub of this.eventUnsubs) unsub();
    this.eventUnsubs = [];
  }
}

export const costStore = new CostStore();
