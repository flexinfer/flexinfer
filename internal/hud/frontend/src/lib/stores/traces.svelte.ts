export interface TraceEntry {
  timestamp: string;
  agent_id?: string;
  agent_type?: string;
  server: string;
  tool: string;
  status: string;
  error?: string;
  target?: string;
  cached?: boolean;
  pipeline_stage?: string;
  duration_ms: number;
  route_ms?: number;
  build_ms?: number;
  execute_ms?: number;
  send_ms?: number;
  recv_ms?: number;
}

export interface TraceSummary {
  count?: number;
  errors?: number;
  denied?: number;
  cached?: number;
  p50_ms?: number;
  p95_ms?: number;
  slowest_ms?: number;
}

class TraceStore {
  entries = $state<TraceEntry[]>([]);
  summary = $state<TraceSummary>({});
  enabled = $state(false);
  path = $state('');
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  async fetch(limit = 200): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch(`/api/traces?limit=${limit}`);
      if (!res.ok) throw new Error(`Trace API: ${res.status}`);
      const data = await res.json();
      this.entries = data.traces ?? [];
      this.summary = data.summary ?? {};
      this.enabled = !!data.enabled;
      this.path = data.path ?? '';
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  startPolling(intervalMs = 15000): void {
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

export const traceStore = new TraceStore();
