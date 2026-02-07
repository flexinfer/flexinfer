// Health store - server health, latency sparklines
// v2: SSE-first with 30s fallback poll. Applies hud.health snapshots directly.
import { eventStore } from './events.svelte.ts';

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

export type ServerStatus = 'healthy' | 'idle' | 'degraded' | 'down';

export interface TunnelInfo {
  name: string;
  state: string;
  remote_host: string;
  uptime?: string;
  reconnects: number;
}

export interface CacheStats {
  entries: number;
  size?: string;
  hit_rate: number;
}

export interface MergedServer {
  name: string;
  categories: string[];
  description: string;
  running: boolean;
  health: ServerHealth | null;
  latencyHistory: number[];
  // Derived view-model fields for direct template binding.
  status: ServerStatus;
  latency: number;
  target: string;
  error_message: string;
  tool_count: number;
}

const SPARKLINE_BUFFER_SIZE = 60;

class HealthStore {
  servers = $state<MergedServer[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private latencyBuffers: Map<string, number[]> = new Map();
  private eventUnsubs: Array<() => void> = [];

  get healthyCount(): number {
    return this.servers.filter((s) => s.status === 'healthy').length;
  }

  get idleCount(): number {
    return this.servers.filter((s) => s.status === 'idle').length;
  }

  get degradedCount(): number {
    return this.servers.filter((s) => s.status === 'degraded').length;
  }

  get downCount(): number {
    return this.servers.filter((s) => s.status === 'down').length;
  }

  /** Running + idle = all available servers. */
  get availableCount(): number {
    return this.healthyCount + this.idleCount;
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

        // Derive status from running + health state.
        const localHealthy = health?.local?.healthy ?? false;
        let status: ServerStatus;
        if (srv.running && localHealthy) {
          status = 'healthy';
        } else if (srv.running && !localHealthy) {
          status = 'degraded';
        } else if (!srv.running && localHealthy) {
          status = 'idle'; // On-demand server, available but not started.
        } else {
          status = 'down';
        }

        return {
          name: srv.name,
          categories: srv.categories || [],
          description: srv.description,
          running: srv.running,
          health,
          latencyHistory: [...buffer],
          status,
          latency,
          target: health?.target ?? '',
          error_message: health?.local?.errorMessage ?? '',
          // tool_count is only available via SSE hud.health snapshots from the monitor.
          // The REST /api/health endpoint doesn't include it, so default to 0 for fallback.
          tool_count: 0,
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

  async fetchTunnels(): Promise<TunnelInfo[]> {
    try {
      const res = await globalThis.fetch('/api/tunnels');
      if (!res.ok) throw new Error(`Tunnels API: ${res.status}`);
      const data = await res.json();
      return data.tunnels ?? [];
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return [];
    }
  }

  async fetchCacheStats(): Promise<CacheStats | null> {
    try {
      const res = await globalThis.fetch('/api/cache');
      if (!res.ok) throw new Error(`Cache API: ${res.status}`);
      return await res.json();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  /** Apply health snapshot directly from SSE hud.health event. */
  applySnapshot(data: Record<string, unknown>): void {
    const entries = data.servers as Array<Record<string, unknown>> | undefined;
    if (!entries) return;

    const merged: MergedServer[] = entries.map((entry) => {
      const name = entry.name as string;
      const latency = (entry.avg_latency_ms as number) ?? 0;

      // Update ring buffer
      let buffer = this.latencyBuffers.get(name);
      if (!buffer) {
        buffer = [];
        this.latencyBuffers.set(name, buffer);
      }
      buffer.push(latency);
      if (buffer.length > SPARKLINE_BUFFER_SIZE) {
        buffer.shift();
      }

      const running = entry.running as boolean;
      const healthy = entry.healthy as boolean;
      let status: ServerStatus;
      if (running && healthy) {
        status = 'healthy';
      } else if (running && !healthy) {
        status = 'degraded';
      } else if (!running && healthy) {
        status = 'idle';
      } else {
        status = 'down';
      }

      return {
        name,
        categories: (entry.categories as string[]) ?? [],
        description: (entry.description as string) ?? '',
        running,
        health: null,
        latencyHistory: entry.latency_history as number[] ?? [...buffer],
        status,
        latency,
        target: (entry.target as string) ?? '',
        error_message: (entry.error_message as string) ?? '',
        tool_count: (entry.tool_count as number) ?? 0,
      };
    });

    this.servers = merged;
    this.lastUpdated = new Date();
    this.error = null;
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    // 30s fallback poll (SSE is the primary data source).
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);

    // Subscribe to SSE events: apply data directly from hud.health snapshots.
    this.eventUnsubs.push(
      eventStore.on('hud.health', (e) => this.applySnapshot(e.data)),
      // Legacy daemon events still trigger a full refresh as fallback.
      eventStore.on('server.health', () => this.fetch()),
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

export const healthStore = new HealthStore();
