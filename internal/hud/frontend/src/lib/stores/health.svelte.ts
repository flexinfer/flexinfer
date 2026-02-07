// Health store - server health, latency sparklines

export interface HealthEndpoint {
  healthy: boolean;
  consecFails: number;
  avgLatencyMs: number;
  errorMessage: string;
}

export interface ServerHealth {
  local: HealthEndpoint;
  hub: HealthEndpoint;
  target: string;
}

export interface HealthResponse {
  servers: Record<string, ServerHealth>;
}

export interface ServerInfo {
  name: string;
  categories: string[];
  description: string;
  running: boolean;
}

export interface ServersResponse {
  servers: ServerInfo[];
}

export interface MergedServer {
  name: string;
  categories: string[];
  description: string;
  running: boolean;
  health: ServerHealth | null;
  latencyHistory: number[];
}

const SPARKLINE_BUFFER_SIZE = 60;

class HealthStore {
  servers = $state<MergedServer[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private latencyBuffers: Map<string, number[]> = new Map();

  get healthyCount(): number {
    return this.servers.filter(
      (s) => s.running && s.health?.local?.healthy
    ).length;
  }

  get degradedCount(): number {
    return this.servers.filter(
      (s) => s.running && s.health && !s.health.local.healthy
    ).length;
  }

  get downCount(): number {
    return this.servers.filter((s) => !s.running).length;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [healthRes, serversRes] = await Promise.all([
        globalThis.fetch('/api/health'),
        globalThis.fetch('/api/servers'),
      ]);
      if (!healthRes.ok) throw new Error(`Health API: ${healthRes.status}`);
      if (!serversRes.ok) throw new Error(`Servers API: ${serversRes.status}`);

      const healthData: HealthResponse = await healthRes.json();
      const serversData: ServersResponse = await serversRes.json();

      const serverList = serversData.servers || [];
      const merged: MergedServer[] = serverList.map((srv) => {
        const health = healthData.servers?.[srv.name] ?? null;
        const latency = health?.local?.avgLatencyMs ?? 0;

        // Update ring buffer
        let buffer = this.latencyBuffers.get(srv.name);
        if (!buffer) {
          buffer = [];
          this.latencyBuffers.set(srv.name, buffer);
        }
        buffer.push(latency);
        if (buffer.length > SPARKLINE_BUFFER_SIZE) {
          buffer.shift();
        }

        return {
          name: srv.name,
          categories: srv.categories || [],
          description: srv.description,
          running: srv.running,
          health,
          latencyHistory: [...buffer],
        };
      });

      this.servers = merged;
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

export const healthStore = new HealthStore();
