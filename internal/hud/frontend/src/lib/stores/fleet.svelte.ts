// Fleet store - daemon status and sessions overview
// v2: SSE-first with 30s fallback poll. Applies hud.fleet snapshots directly.
import { eventStore } from './events.svelte.ts';

export interface Process {
  pid: number;
  name: string;
  status: string;
}

export interface StatusResponse {
  running: boolean;
  servers: number;
  activeConns: number;
  idleConns: number;
  processes: Process[];
}

export interface Session {
  id: string;
  agent_id: string;
  agent: string;
  namespace: string;
  started_at: string;
  ended_at: string | null;
  status: string;
  description: string;
  entry_count: number;
  total_tokens: number;
  tokens_used: number;
  task_count: number;
  memory_items: number;
  active: boolean;
}

export interface SessionsResponse {
  sessions: Session[];
}

class FleetStore {
  status = $state<StatusResponse>({
    running: false,
    servers: 0,
    activeConns: 0,
    idleConns: 0,
    processes: [],
  });
  sessions = $state<Session[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get activeSessions(): Session[] {
    return this.sessions.filter((s) => s.status === 'active');
  }

  get totalTokens(): number {
    return this.sessions.reduce((sum, s) => sum + (s.total_tokens || 0), 0);
  }

  get agentCount(): number {
    const agents = new Set(this.sessions.map((s) => s.agent_id));
    return agents.size;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [statusRes, sessionsRes] = await Promise.all([
        globalThis.fetch('/api/status'),
        globalThis.fetch('/api/sessions'),
      ]);
      if (!statusRes.ok) throw new Error(`Status API: ${statusRes.status}`);
      if (!sessionsRes.ok) throw new Error(`Sessions API: ${sessionsRes.status}`);
      this.status = await statusRes.json();
      const sessData: SessionsResponse = await sessionsRes.json();
      this.sessions = sessData.sessions || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  /** Apply a fleet snapshot directly from SSE, avoiding an HTTP round-trip. */
  applySnapshot(data: Record<string, unknown>): void {
    // The hud.fleet event carries the full FleetSnapshot from the monitor.
    if (data.daemon_running !== undefined) {
      this.status = {
        running: data.daemon_running as boolean,
        servers: (data.server_count as number) ?? 0,
        activeConns: (data.active_conns as number) ?? 0,
        idleConns: 0,
        processes: (data.processes as string[]) ?? [],
      };
    }
    if (data.sessions) {
      this.sessions = data.sessions as Session[];
    }
    this.lastUpdated = new Date();
    this.error = null;
  }

  async fetchSessionEntries(sessionId: string, limit = 50): Promise<Record<string, unknown>[] | null> {
    try {
      const params = new URLSearchParams({ limit: String(limit) });
      const res = await globalThis.fetch(`/api/sessions/${sessionId}/entries?${params.toString()}`);
      if (!res.ok) throw new Error(`Session entries: ${res.status}`);
      const data = await res.json();
      return data.entries ?? [];
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    // 30s fallback poll (SSE is the primary data source).
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);

    // Subscribe to SSE events: apply data directly from hud.fleet snapshots.
    this.eventUnsubs.push(
      eventStore.on('hud.fleet', (e) => this.applySnapshot(e.data)),
      // Legacy daemon events still trigger a full refresh as fallback.
      eventStore.on('config.reload', () => this.fetch()),
      eventStore.on('process.start', () => this.fetch()),
      eventStore.on('process.stop', () => this.fetch()),
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

export const fleetStore = new FleetStore();
