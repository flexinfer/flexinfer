// Spawn store — manages headless agent spawns via /api/agent/spawn endpoints
// and subscribes to SSE events for real-time spawn lifecycle updates.
import { eventStore } from './events.svelte.ts';

export interface SpawnRequest {
  agent_type: string;
  project: string;
  branch?: string;
  base_branch?: string;
  task_description: string;
  namespace?: string;
  memory_mb?: number;
  cpus?: number;
  timeout_minutes?: number;
}

export interface SpawnState {
  spawn_id: string;
  agent_id: string;
  pod_name: string;
  status: 'creating' | 'running' | 'completed' | 'failed' | 'stopped';
  request: SpawnRequest;
  started_at: string;
  ended_at?: string;
  error?: string;
}

class SpawnStore {
  spawns = $state<SpawnState[]>([]);
  loading = $state(false);
  spawning = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get activeSpawns(): SpawnState[] {
    return this.spawns.filter(s => s.status === 'creating' || s.status === 'running');
  }

  get completedSpawns(): SpawnState[] {
    return this.spawns.filter(s => s.status !== 'creating' && s.status !== 'running');
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await fetch('/api/agent/spawns');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      this.spawns = data.spawns ?? [];
      this.lastUpdated = new Date();
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
    } finally {
      this.loading = false;
    }
  }

  async spawn(req: SpawnRequest): Promise<{ spawn_id: string; agent_id: string } | null> {
    this.spawning = true;
    this.error = null;
    try {
      const res = await fetch('/api/agent/spawn', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(req),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      const data = await res.json();
      await this.fetch();
      return { spawn_id: data.spawn_id, agent_id: data.agent_id };
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
      return null;
    } finally {
      this.spawning = false;
    }
  }

  async stop(spawnId: string): Promise<boolean> {
    this.error = null;
    try {
      const res = await fetch(`/api/agent/spawn/${spawnId}/stop`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await this.fetch();
      return true;
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  startPolling(intervalMs = 10000): void {
    this.fetch();
    this.subscribeSSE();
    if (this.pollTimer) return;
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    this.eventUnsubs.forEach(u => u());
    this.eventUnsubs = [];
  }

  private subscribeSSE(): void {
    const spawnEvents = [
      'agent.spawn.running',
      'agent.spawn.completed',
      'agent.spawn.failed',
      'agent.spawn.stopped',
    ];
    for (const eventType of spawnEvents) {
      this.eventUnsubs.push(
        eventStore.on(eventType, () => {
          this.fetch();
        })
      );
    }
  }
}

export const spawnStore = new SpawnStore();
