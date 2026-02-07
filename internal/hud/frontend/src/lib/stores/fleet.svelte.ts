// Fleet store - daemon status and sessions overview

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

  startPolling(intervalMs = 5000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }
}

export const fleetStore = new FleetStore();
