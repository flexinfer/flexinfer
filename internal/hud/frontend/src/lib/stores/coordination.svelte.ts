import { eventStore } from './events.svelte.ts';

export interface CoordinationSummary {
  active_namespaces: number;
  namespaces_at_risk: number;
  agents_needing_attention: number;
  shared_branches: number;
  conflict_files: number;
  cross_agent_blockers: number;
  orphan_tasks: number;
  idle_claim_holders: number;
  merge_ready_branches: number;
}

export interface CoordinationNamespace {
  namespace: string;
  session_count: number;
  agent_count: number;
  task_count: number;
  blocked_tasks: number;
  orphan_tasks: number;
  conflict_files: number;
  shared_branches: number;
  cross_agent_blockers: number;
  needs_attention: boolean;
  attention_score: number;
  attention_reasons?: string[];
  agents?: string[];
  branches?: string[];
}

export interface CoordinationAgent {
  agent_id: string;
  session_id?: string;
  namespace?: string;
  status: string;
  branch?: string;
  worktree_status?: string;
  task_count: number;
  blocked_tasks: number;
  claim_count: number;
  conflict_files: number;
  blocking_others: number;
  blocked_by_others: number;
  idle_holding_claims: boolean;
  merge_ready: boolean;
  merge_blockers?: string[];
  needs_attention: boolean;
  attention_reasons?: string[];
}

export interface CoordinationBlocker {
  task_id: string;
  task_title: string;
  task_status: string;
  task_agent_id?: string;
  task_namespace?: string;
  blocked_by_task_id: string;
  blocked_by_task_title?: string;
  blocked_by_status?: string;
  blocked_by_agent_id?: string;
  blocked_by_namespace?: string;
  cross_agent: boolean;
  resolved: boolean;
}

export interface CoordinationRelation {
  kind: string;
  source: string;
  source_label: string;
  target: string;
  target_label: string;
  namespace?: string;
  detail?: string;
  severity?: string;
  cross_agent: boolean;
}

export interface CoordinationSnapshot {
  summary: CoordinationSummary;
  namespaces: CoordinationNamespace[];
  agents: CoordinationAgent[];
  blockers: CoordinationBlocker[];
  relations: CoordinationRelation[];
}

const EMPTY_SUMMARY: CoordinationSummary = {
  active_namespaces: 0,
  namespaces_at_risk: 0,
  agents_needing_attention: 0,
  shared_branches: 0,
  conflict_files: 0,
  cross_agent_blockers: 0,
  orphan_tasks: 0,
  idle_claim_holders: 0,
  merge_ready_branches: 0,
};

class CoordinationStore {
  summary = $state<CoordinationSummary>(EMPTY_SUMMARY);
  namespaces = $state<CoordinationNamespace[]>([]);
  agents = $state<CoordinationAgent[]>([]);
  blockers = $state<CoordinationBlocker[]>([]);
  relations = $state<CoordinationRelation[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get topAttentionAgents(): CoordinationAgent[] {
    return this.agents.filter((agent) => agent.needs_attention).slice(0, 6);
  }

  get riskyNamespaces(): CoordinationNamespace[] {
    return this.namespaces.filter((namespace) => namespace.needs_attention).slice(0, 6);
  }

  get activeBlockers(): CoordinationBlocker[] {
    return this.blockers.filter((blocker) => !blocker.resolved).slice(0, 8);
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/fleet');
      if (!res.ok) throw new Error(`Fleet API: ${res.status}`);
      const data = await res.json();
      this.applySnapshot(data as Record<string, unknown>);
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  applySnapshot(data: Record<string, unknown>): void {
    const coordination = data.coordination as Partial<CoordinationSnapshot> | undefined;
    if (!coordination) return;
    this.summary = { ...EMPTY_SUMMARY, ...(coordination.summary ?? {}) };
    this.namespaces = coordination.namespaces ?? [];
    this.agents = coordination.agents ?? [];
    this.blockers = coordination.blockers ?? [];
    this.relations = coordination.relations ?? [];
    this.lastUpdated = new Date();
    this.error = null;
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);
    this.eventUnsubs.push(
      eventStore.on('hud.fleet', (event) => this.applySnapshot(event.data as Record<string, unknown>)),
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

export const coordinationStore = new CoordinationStore();
