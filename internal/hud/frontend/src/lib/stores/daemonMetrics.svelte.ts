// Daemon metrics store — polls /api/daemon-metrics for per-server latency
// percentiles, error rates, and request counts from the daemon's prometheus
// registry.

export interface ServerMetric {
  name: string;
  request_count: number;
  error_count: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  in_flight: number;
}

export interface DaemonMetricsResponse {
  servers: ServerMetric[];
}

class DaemonMetricsStore {
  servers = $state<ServerMetric[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  get byName(): Map<string, ServerMetric> {
    const map = new Map<string, ServerMetric>();
    for (const s of this.servers) {
      map.set(s.name, s);
    }
    return map;
  }

  get totalRequests(): number {
    return this.servers.reduce((sum, s) => sum + s.request_count, 0);
  }

  get totalErrors(): number {
    return this.servers.reduce((sum, s) => sum + s.error_count, 0);
  }

  get overallErrorRate(): number {
    const total = this.totalRequests;
    return total > 0 ? this.totalErrors / total : 0;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/daemon-metrics');
      if (!res.ok) throw new Error(`Daemon metrics: ${res.status}`);

      const data: DaemonMetricsResponse = await res.json();
      this.servers = data.servers ?? [];
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

export const daemonMetricsStore = new DaemonMetricsStore();
