import { eventStore } from './events.svelte.ts';
import {
  fetchOrchestrationStatus,
  type CapacityInfo,
  type DispatchRecommendation,
  type OrchestrationSnapshot,
} from '../clients/orchestration.ts';

class OrchestrationStore {
  capacities = $state<CapacityInfo[]>([]);
  recommendations = $state<DispatchRecommendation[]>([]);
  pendingTasks = $state(0);
  activeAgents = $state(0);
  systemLoad = $state(0);
  updatedAt = $state('');
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get hasRecommendations(): boolean {
    return this.recommendations.length > 0;
  }

  get systemLoadPct(): string {
    return `${Math.round(this.systemLoad * 100)}%`;
  }

  applySnapshot(data: OrchestrationSnapshot): void {
    this.capacities = data.capacities ?? [];
    this.recommendations = data.recommendations ?? [];
    this.pendingTasks = data.pending_tasks ?? 0;
    this.activeAgents = data.active_agents ?? 0;
    this.systemLoad = data.system_load ?? 0;
    this.updatedAt = data.updated_at ?? '';
    this.lastUpdated = new Date();
    this.error = null;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const data = await fetchOrchestrationStatus();
      this.applySnapshot(data);
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);
    this.eventUnsubs.push(
      eventStore.on('hud.fleet', () => this.fetch()),
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

export const orchestrationStore = new OrchestrationStore();
