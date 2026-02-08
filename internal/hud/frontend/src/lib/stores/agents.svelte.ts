// Agents store — agent list for task assignment and dispatch.
// SSE-first: applies agent data from hud.fleet snapshots; falls back to /api/agents poll.
import { eventStore } from './events.svelte.ts';

export interface Agent {
  agent_id: string;
  agent_type: string; // "claude-code", "codex", "gemini-cli", etc.
  status: string; // "active", "idle", "offline"
  session_id: string;
  current_task: string;
  description: string;
  branch: string;
}

class AgentStore {
  agents = $state<Agent[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get activeAgents(): Agent[] {
    return this.agents.filter((a) => a.status === 'active');
  }

  get idleAgents(): Agent[] {
    return this.agents.filter((a) => a.status === 'idle');
  }

  get liveAgents(): Agent[] {
    return this.agents.filter((a) => a.status === 'active' || a.status === 'idle');
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/agents');
      if (!res.ok) throw new Error(`Agents API: ${res.status}`);
      const data = await res.json();
      this.agents = data.agents || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  /** Apply agent data directly from an SSE fleet snapshot. */
  applySnapshot(data: Record<string, unknown>): void {
    if (data.agents) {
      this.agents = data.agents as Agent[];
      this.lastUpdated = new Date();
      this.error = null;
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);

    // SSE-first: extract agents from fleet snapshots.
    this.eventUnsubs.push(
      eventStore.on('hud.fleet', (e) => this.applySnapshot(e.data)),
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

export const agentStore = new AgentStore();
