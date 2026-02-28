// RBAC store — fetches RBAC configuration and recent denied calls from GET /api/rbac.
import { eventStore } from './events.svelte.ts';

export interface RBACRole {
  name: string;
  allow: string[];
  deny: string[];
}

export interface RBACBinding {
  agent_id?: string;
  agent_type?: string;
  role: string;
}

export interface RBACDenied {
  agent_id: string;
  server: string;
  tool: string;
  role?: string;
  reason: string;
  timestamp: string;
}

export interface RBACConfig {
  enabled: boolean;
  default_policy?: string;
  roles?: RBACRole[];
  bindings?: RBACBinding[];
  global_deny?: string[];
  rate_limits?: { agent_id?: string; tool?: string; requests_per_minute: number }[];
  recent_denied?: RBACDenied[];
}

class RBACStore {
  config = $state<RBACConfig | null>(null);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get enabled(): boolean {
    return this.config?.enabled ?? false;
  }

  get roles(): RBACRole[] {
    return this.config?.roles ?? [];
  }

  get bindings(): RBACBinding[] {
    return this.config?.bindings ?? [];
  }

  get recentDenied(): RBACDenied[] {
    return this.config?.recent_denied ?? [];
  }

  get deniedCount(): number {
    return this.recentDenied.length;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/rbac');
      if (!res.ok) throw new Error(`RBAC API: ${res.status}`);
      this.config = await res.json() as RBACConfig;
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);

    this.eventUnsubs.push(
      eventStore.on('access.denied', () => this.fetch()),
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

export const rbacStore = new RBACStore();
