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
  multi_turn?: boolean;
  max_cost_usd?: number;
  max_turns?: number;
}

// SpawnTokenUsage mirrors internal/hud/bridge/spawn_telemetry.go SpawnTokenUsage.
export interface SpawnTokenUsage {
  input_tokens: number;
  output_tokens: number;
  cache_creation_tokens: number;
  cache_read_tokens: number;
}

// SpawnModelUsage mirrors internal/hud/bridge/spawn_telemetry.go ModelUse.
export interface SpawnModelUsage {
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
}

// SpawnTelemetry mirrors internal/hud/bridge/spawn_telemetry.go SpawnTelemetry.
export interface SpawnTelemetry {
  external_session_id?: string;
  turn_count: number;
  total_cost_usd: number;
  cost_estimated?: boolean;
  token_usage: SpawnTokenUsage;
  model_usage?: Record<string, SpawnModelUsage>;
  stop_reason?: string;
  last_message?: string;
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
  telemetry?: SpawnTelemetry | null;
}

class SpawnStore {
  spawns = $state<SpawnState[]>([]);
  loading = $state(false);
  spawning = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);
  // telemetryBySpawnId holds live telemetry snapshots for spawns that do not
  // embed telemetry in the list response (typically active spawns). Completed
  // spawns generally carry telemetry directly on SpawnState.telemetry.
  telemetryBySpawnId = $state(new Map<string, SpawnTelemetry>());

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get activeSpawns(): SpawnState[] {
    return this.spawns.filter(s => s.status === 'creating' || s.status === 'running');
  }

  get completedSpawns(): SpawnState[] {
    return this.spawns.filter(s => s.status !== 'creating' && s.status !== 'running');
  }

  /**
   * telemetryFor returns the best-known telemetry for a spawn:
   *   1. Live snapshot from telemetryBySpawnId (populated by fetchActiveTelemetry).
   *   2. Embedded telemetry on SpawnState (backend sends this for completed spawns).
   * Returns undefined when neither source has data yet.
   */
  telemetryFor(spawnId: string): SpawnTelemetry | undefined {
    const live = this.telemetryBySpawnId.get(spawnId);
    if (live) return live;
    const s = this.spawns.find(sp => sp.spawn_id === spawnId);
    return s?.telemetry ?? undefined;
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
      // Fire-and-forget active-spawn telemetry refresh. Any errors are
      // swallowed — the map just stays stale and rows fall back to caps-only.
      this.fetchActiveTelemetry().catch(() => { /* best-effort */ });
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
    } finally {
      this.loading = false;
    }
  }

  /**
   * fetchActiveTelemetry refreshes telemetry snapshots for every currently
   * active spawn in parallel. The paginated list response from /api/agent/spawns
   * does not embed live telemetry for active spawns, so we pull each via
   * /api/agent/spawn/{id}/telemetry. Completed spawns are skipped (they carry
   * telemetry on the list payload).
   */
  async fetchActiveTelemetry(): Promise<void> {
    const active = this.activeSpawns;
    if (active.length === 0) {
      // Prune stale entries for spawns that have since completed.
      if (this.telemetryBySpawnId.size > 0) {
        const next = new Map<string, SpawnTelemetry>();
        for (const [id, tel] of this.telemetryBySpawnId) {
          if (this.spawns.some(s => s.spawn_id === id)) {
            next.set(id, tel);
          }
        }
        this.telemetryBySpawnId = next;
      }
      return;
    }

    const results = await Promise.allSettled(
      active.map(async (s) => {
        const res = await fetch(`/api/agent/spawn/${encodeURIComponent(s.spawn_id)}/telemetry`);
        if (!res.ok) return { spawnId: s.spawn_id, telemetry: null as SpawnTelemetry | null };
        const data = await res.json();
        return { spawnId: s.spawn_id, telemetry: (data?.telemetry ?? null) as SpawnTelemetry | null };
      })
    );

    const next = new Map(this.telemetryBySpawnId);
    for (const r of results) {
      if (r.status !== 'fulfilled') continue;
      const { spawnId, telemetry } = r.value;
      if (telemetry) {
        next.set(spawnId, telemetry);
      } else {
        next.delete(spawnId);
      }
    }
    // Drop entries for spawns no longer in the list (terminal + evicted).
    for (const id of Array.from(next.keys())) {
      if (!this.spawns.some(s => s.spawn_id === id)) {
        next.delete(id);
      }
    }
    this.telemetryBySpawnId = next;
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
