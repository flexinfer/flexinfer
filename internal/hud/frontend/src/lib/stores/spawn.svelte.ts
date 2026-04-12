// Spawn store — manages headless agent spawns via /api/agent/spawn endpoints
// and subscribes to SSE events for real-time spawn lifecycle updates.
import { eventStore } from './events.svelte.ts';
import { adminFetch, labsAuthStore } from './labsAuth.svelte.ts';
import { fleetStore, type Session } from './fleet.svelte.ts';

export interface SpawnActivityEvent {
  type: string;
  timestamp: string;
  data: Record<string, unknown>;
}

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
  status: 'creating' | 'building' | 'running' | 'completed' | 'failed' | 'stopped';
  request: SpawnRequest;
  started_at: string;
  ended_at?: string;
  error?: string;
  telemetry?: SpawnTelemetry | null;
}

export interface SpawnConfigAgentType {
  id: string;
  name: string;
  available: boolean;
}

export interface SpawnConfigProject {
  name: string;
  path: string;
}

export interface SpawnConfigDefaults {
  agent_type: string;
  base_branch: string;
  memory_mb: number;
  cpus: number;
  timeout_minutes: number;
  max_cost_usd?: number;
  max_turns?: number;
}

export interface SpawnConfig {
  configured: boolean;
  agent_types: SpawnConfigAgentType[];
  projects: SpawnConfigProject[];
  defaults: SpawnConfigDefaults;
  notes?: {
    auth_required?: boolean;
    multi_turn_supported?: boolean;
    follow_up_supported?: boolean;
    interrupt_supported?: boolean;
    telemetry_requires_auth?: boolean;
    project_count?: number;
    active_spawn_count?: number;
    reason?: string;
    hint?: string;
  };
}

class SpawnStore {
  spawns = $state<SpawnState[]>([]);
  loading = $state(false);
  spawning = $state(false);
  error = $state<string | null>(null);
  config = $state<SpawnConfig | null>(null);
  configLoading = $state(false);
  configError = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);
  // telemetryBySpawnId holds live telemetry snapshots for spawns that do not
  // embed telemetry in the list response (typically active spawns). Completed
  // spawns generally carry telemetry directly on SpawnState.telemetry.
  telemetryBySpawnId = $state(new Map<string, SpawnTelemetry>());
  // activityBySpawnId accumulates real-time SSE activity events per spawn
  // (messages, tool calls, thinking, file changes, results). Capped at 500 per spawn.
  activityBySpawnId = $state(new Map<string, SpawnActivityEvent[]>());

  private static readonly MAX_ACTIVITY_EVENTS = 500;
  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get activeSpawns(): SpawnState[] {
    return this.spawns.filter(s => s.status === 'creating' || s.status === 'building' || s.status === 'running');
  }

  get completedSpawns(): SpawnState[] {
    return this.spawns.filter(s => s.status !== 'creating' && s.status !== 'building' && s.status !== 'running');
  }

  /** Find a spawn by its agent_id (for cross-referencing with fleet sessions). */
  spawnForAgent(agentId: string): SpawnState | undefined {
    return this.spawns.find(s => s.agent_id === agentId);
  }

  /**
   * sessionForSpawn looks up the fleet session linked to a given spawn.
   * Finds the spawn by spawnId, extracts its agent_id, then queries
   * fleetStore.sessions for a session with the same agent_id.
   */
  sessionForSpawn(spawnId: string): Session | undefined {
    const spawn = this.spawns.find(s => s.spawn_id === spawnId);
    if (!spawn) return undefined;
    return fleetStore.sessionForAgent(spawn.agent_id);
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

  clearError(): void {
    this.error = null;
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

  async fetchConfig(): Promise<void> {
    this.configLoading = true;
    this.configError = null;
    try {
      const res = await fetch('/api/agent/spawn/config');
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      this.config = await res.json();
    } catch (err) {
      this.configError = err instanceof Error ? err.message : String(err);
    } finally {
      this.configLoading = false;
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
    if (!labsAuthStore.hasToken) {
      if (this.telemetryBySpawnId.size > 0) {
        this.telemetryBySpawnId = new Map<string, SpawnTelemetry>();
      }
      return;
    }

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
        const res = await adminFetch(`/api/agent/spawn/${encodeURIComponent(s.spawn_id)}/telemetry`, {
          requireToken: true,
          action: 'Loading spawn telemetry',
        });
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
      const res = await adminFetch('/api/agent/spawn', {
        method: 'POST',
        requireToken: true,
        action: 'Spawning an agent',
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
      const res = await adminFetch(`/api/agent/spawn/${spawnId}/stop`, {
        method: 'POST',
        requireToken: true,
        action: 'Stopping a spawn',
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      await this.fetch();
      return true;
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  async delete(spawnId: string): Promise<boolean> {
    this.error = null;
    try {
      const res = await adminFetch(`/api/agent/spawn/${encodeURIComponent(spawnId)}`, {
        method: 'DELETE',
        requireToken: true,
        action: 'Deleting a spawn',
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      await this.fetch();
      return true;
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  async sendMessage(spawnId: string, message: string): Promise<boolean> {
    this.error = null;
    try {
      const res = await adminFetch(`/api/agent/spawn/${encodeURIComponent(spawnId)}/message`, {
        method: 'POST',
        requireToken: true,
        action: 'Sending a spawn follow-up',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: message }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      return true;
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  async interrupt(spawnId: string): Promise<boolean> {
    this.error = null;
    try {
      const res = await adminFetch(`/api/agent/spawn/${encodeURIComponent(spawnId)}/interrupt`, {
        method: 'POST',
        requireToken: true,
        action: 'Interrupting a spawn',
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      return true;
    } catch (err) {
      this.error = err instanceof Error ? err.message : String(err);
      return false;
    }
  }

  startPolling(intervalMs = 10000): void {
    if (!this.config && !this.configLoading) {
      this.fetchConfig().catch(() => { /* best-effort */ });
    }
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
    if (this.eventUnsubs.length > 0) return;
    const spawnEvents = [
      'agent.spawn.building',
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
    this.eventUnsubs.push(
      eventStore.on('agent.spawn.telemetry.delta', (event) => {
        const data = event.data ?? {};
        const spawnId = typeof data.spawn_id === 'string' ? data.spawn_id : '';
        if (!spawnId) return;
        const tokenUsage = (data.token_usage as Record<string, unknown> | undefined) ?? {};
        const next = new Map(this.telemetryBySpawnId);
        next.set(spawnId, {
          turn_count: Number(data.turn_count ?? 0),
          total_cost_usd: Number(data.total_cost_usd ?? 0),
          cost_estimated: Boolean(data.cost_estimated),
          token_usage: {
            input_tokens: Number(tokenUsage.input_tokens ?? 0),
            output_tokens: Number(tokenUsage.output_tokens ?? 0),
            cache_creation_tokens: Number(tokenUsage.cache_creation_tokens ?? 0),
            cache_read_tokens: Number(tokenUsage.cache_read_tokens ?? 0),
          },
          stop_reason: typeof data.stop_reason === 'string' ? data.stop_reason : undefined,
          last_message: typeof data.last_message === 'string' ? data.last_message : undefined,
        });
        this.telemetryBySpawnId = next;
      })
    );

    // Subscribe to spawn activity events for live feed.
    const activityEvents = [
      'agent.spawn.message', 'agent.spawn.thinking', 'agent.spawn.reasoning',
      'agent.spawn.todo', 'agent.spawn.tool_start', 'agent.spawn.tool_complete',
      'agent.spawn.file_change', 'agent.spawn.result', 'agent.spawn.rate_limit',
    ];
    for (const eventType of activityEvents) {
      this.eventUnsubs.push(
        eventStore.on(eventType, (event) => {
          const data = event.data ?? {};
          const spawnId = typeof data.spawn_id === 'string' ? data.spawn_id
            : typeof data.agent_id === 'string' ? data.agent_id : '';
          if (!spawnId) return;
          // Resolve spawn by agent_id if spawn_id not directly in payload.
          const resolvedId = this.spawns.find(s => s.spawn_id === spawnId)?.spawn_id
            ?? this.spawns.find(s => s.agent_id === spawnId)?.spawn_id
            ?? spawnId;
          const next = new Map(this.activityBySpawnId);
          const list = [...(next.get(resolvedId) ?? [])];
          list.push({
            type: eventType.replace('agent.spawn.', ''),
            timestamp: event.timestamp || new Date().toISOString(),
            data,
          });
          // Cap at max events.
          if (list.length > SpawnStore.MAX_ACTIVITY_EVENTS) {
            list.splice(0, list.length - SpawnStore.MAX_ACTIVITY_EVENTS);
          }
          next.set(resolvedId, list);
          this.activityBySpawnId = next;
        })
      );
    }
  }
}

export const spawnStore = new SpawnStore();
