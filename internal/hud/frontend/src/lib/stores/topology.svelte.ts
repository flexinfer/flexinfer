// Topology store - agent relationship graph
import { eventStore } from './events.svelte.ts';

export interface TopologyNode {
  agent_id: string;
  status: string;
  agent_type: string;
  current_task: string;
  branch: string;
  pr_url?: string;
  namespace: string;
}

export interface TopologyEdge {
  source: string;
  target: string;
  edge_type: 'handoff' | 'shared_file' | 'shared_branch';
  weight: number;
  label?: string;
  status?: string;
}

export interface TopologyCluster {
  project: string;
  agent_ids: string[];
}

export interface TopologyGraph {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  clusters: TopologyCluster[];
}

class TopologyStore {
  nodes = $state<TopologyNode[]>([]);
  edges = $state<TopologyEdge[]>([]);
  clusters = $state<TopologyCluster[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  selectedNode = $state<string | null>(null);

  private eventUnsubs: Array<() => void> = [];
  private pollTimer: ReturnType<typeof setInterval> | null = null;

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/topology');
      if (!res.ok) throw new Error(`Topology API: ${res.status}`);
      const data: TopologyGraph = await res.json();
      this.nodes = data.nodes ?? [];
      this.edges = data.edges ?? [];
      this.clusters = data.clusters ?? [];
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  /** Update node statuses in-place from fleet snapshot (no re-fetch). */
  applyFleetUpdate(data: Record<string, unknown>): void {
    const agents = data.agents as Array<{ agent_id: string; status: string; current_task: string }> | undefined;
    if (!agents) return;
    const lookup = new Map(agents.map((a) => [a.agent_id, a]));
    this.nodes = this.nodes.map((n) => {
      const a = lookup.get(n.agent_id);
      if (a) {
        return { ...n, status: a.status, current_task: a.current_task };
      }
      return n;
    });
  }

  selectNode(agentId: string | null): void {
    this.selectedNode = agentId;
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);

    // Fleet snapshot updates node statuses in-place.
    this.eventUnsubs.push(
      eventStore.on('hud.fleet', (e) => this.applyFleetUpdate(e.data)),
      // Structure-changing events trigger full re-fetch.
      eventStore.on('agent.session.start', () => this.fetch()),
      eventStore.on('agent.session.end', () => this.fetch()),
      eventStore.on('hud.handoff.created', () => this.fetch()),
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

export const topologyStore = new TopologyStore();
