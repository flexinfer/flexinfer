// Sandbox store — fetches devbox sandbox data from GET /api/sandbox
// and subscribes to SSE events for real-time exec/build activity.
// Follows the health.svelte.ts SSE-first pattern with fallback polling.
import { eventStore } from './events.svelte.ts';

export interface SandboxSummary {
  available: boolean;
  total_sandboxes: number;
  running: number;
  paused: number;
  total_execs: number;
  total_builds: number;
  uptime_seconds: number;
  projects: string[];
}

export interface SandboxEvent {
  type: string;       // "exec", "build", "start", "stop"
  project: string;
  detail: string;
  timestamp: Date;
}

const MAX_EVENTS = 20;

class SandboxStore {
  summary = $state<SandboxSummary | null>(null);
  available = $state(false);
  loading = $state(false);
  error = $state<string | null>(null);
  recentEvents = $state<SandboxEvent[]>([]);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get runningCount(): number {
    return this.summary?.running ?? 0;
  }

  get pausedCount(): number {
    return this.summary?.paused ?? 0;
  }

  get totalExecs(): number {
    return this.summary?.total_execs ?? 0;
  }

  get totalBuilds(): number {
    return this.summary?.total_builds ?? 0;
  }

  get totalSandboxes(): number {
    return this.summary?.total_sandboxes ?? 0;
  }

  get projects(): string[] {
    return this.summary?.projects ?? [];
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/sandbox');
      if (!res.ok) throw new Error(`Sandbox API: ${res.status}`);
      const data: SandboxSummary = await res.json();
      this.summary = data;
      this.available = data.available ?? false;
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      this.available = false;
    } finally {
      this.loading = false;
    }
  }

  /** Apply full sandbox snapshot from SSE hud.sandbox event. */
  applySnapshot(data: Record<string, unknown>): void {
    this.summary = data as unknown as SandboxSummary;
    this.available = (data.available as boolean) ?? false;
    this.lastUpdated = new Date();
    this.error = null;
  }

  /** Push a sandbox activity event from SSE hud.sandbox.event. */
  pushEvent(data: Record<string, unknown>): void {
    const evt: SandboxEvent = {
      type: (data.type as string) ?? 'unknown',
      project: (data.project as string) ?? '',
      detail: (data.detail as string) ?? '',
      timestamp: new Date((data.timestamp as string) ?? Date.now()),
    };
    this.recentEvents = [evt, ...this.recentEvents].slice(0, MAX_EVENTS);
  }

  startPolling(intervalMs = 15000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);

    // Subscribe to SSE events.
    this.eventUnsubs.push(
      eventStore.on('hud.sandbox', (e) => this.applySnapshot(e.data)),
      eventStore.on('hud.sandbox.event', (e) => this.pushEvent(e.data)),
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

export const sandboxStore = new SandboxStore();
