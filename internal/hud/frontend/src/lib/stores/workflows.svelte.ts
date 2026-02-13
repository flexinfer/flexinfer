// Workflows store - workflow orchestration
// v2: SSE-first with 30s fallback poll. Applies hud.workflows snapshots directly.
import { eventStore } from './events.svelte.ts';

export interface WorkflowSummary {
  id: string;
  definition_id: string;
  name?: string;
  status: string;
  current_step: string;
  started_at: string;
  completed_at?: string;
  steps?: Array<{
    id: string;
    name: string;
    type: string;
    status: string;
    depends_on?: string[];
  }>;
  events?: Array<{
    id?: string;
    type: string;
    event_type?: string;
    timestamp: string;
    step_id?: string;
    step_name?: string;
    message?: string;
    details?: string;
  }>;
}

export interface WorkflowStep {
  id: string;
  name: string;
  status: string;
  depends_on: string[];
}

export interface WorkflowDetail extends WorkflowSummary {
  step_states: Record<string, WorkflowStep>;
  steps?: Array<{
    id: string;
    name: string;
    type: string;
    status: string;
    depends_on?: string[];
  }>;
  name?: string;
  events?: Array<{
    id?: string;
    type: string;
    event_type?: string;
    timestamp: string;
    step_id?: string;
    step_name?: string;
    message?: string;
    details?: string;
  }>;
  completed_at?: string;
}

export interface WorkflowsResponse {
  workflows: WorkflowSummary[];
}

export interface WorkflowDefinition {
  id: string;
  name: string;
  description: string;
  namespace: string;
  step_count: number;
  created_by: string;
  created_at: string;
}

class WorkflowStore {
  workflows = $state<WorkflowSummary[]>([]);
  definitions = $state<WorkflowDefinition[]>([]);
  selectedWorkflow = $state<WorkflowDetail | null>(null);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get activeWorkflows(): WorkflowSummary[] {
    return this.workflows.filter(
      (w) => w.status === 'running' || w.status === 'pending' || w.status === 'waiting_approval'
    );
  }

  get completedWorkflows(): WorkflowSummary[] {
    return this.workflows.filter(
      (w) => w.status === 'completed' || w.status === 'failed' || w.status === 'cancelled'
    );
  }

  /** Deduplicate definitions by name, keeping the latest registration. */
  get uniqueDefinitions(): WorkflowDefinition[] {
    const byName = new Map<string, WorkflowDefinition>();
    for (const def of this.definitions) {
      const existing = byName.get(def.name);
      if (!existing || def.created_at > existing.created_at) {
        byName.set(def.name, def);
      }
    }
    return Array.from(byName.values()).sort((a, b) => a.name.localeCompare(b.name));
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [wfRes, defRes] = await Promise.all([
        globalThis.fetch('/api/workflows'),
        globalThis.fetch('/api/agent/workflow-definitions'),
      ]);
      if (!wfRes.ok) throw new Error(`Workflows API: ${wfRes.status}`);
      const data: WorkflowsResponse = await wfRes.json();
      this.workflows = data.workflows || [];
      if (defRes.ok) {
        const defData = await defRes.json();
        this.definitions = defData.definitions || [];
      }
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  /** Apply workflow list directly from SSE hud.workflows event. */
  applySnapshot(data: Record<string, unknown>): void {
    const workflows = data.workflows as Array<Record<string, unknown>> | undefined;
    if (!workflows) return;

    // Map MCP field names (workflow_id, created_at) to frontend field names (id, started_at).
    this.workflows = workflows.map((wf) => ({
      id: (wf.workflow_id as string) ?? (wf.id as string) ?? '',
      definition_id: (wf.definition_id as string) ?? '',
      name: wf.name as string | undefined,
      status: (wf.status as string) ?? '',
      current_step: (wf.current_step as string) ?? '',
      started_at: (wf.created_at as string) ?? (wf.started_at as string) ?? '',
      completed_at: wf.completed_at as string | undefined,
      progress: wf.progress as number | undefined,
    })) as WorkflowSummary[];
    this.lastUpdated = new Date();
    this.error = null;
  }

  async fetchDetail(workflowId: string): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch(`/api/workflows/${workflowId}`);
      if (!res.ok) throw new Error(`Workflow detail: ${res.status}`);
      this.selectedWorkflow = await res.json();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  async approveStep(workflowId: string, stepId: string): Promise<void> {
    try {
      const res = await globalThis.fetch(`/api/workflows/${workflowId}/approve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ step_id: stepId }),
      });
      if (!res.ok) throw new Error(`Approve step: ${res.status}`);
      await this.fetchDetail(workflowId);
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async rejectStep(workflowId: string, stepId: string): Promise<void> {
    try {
      const res = await globalThis.fetch(`/api/workflows/${workflowId}/reject`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ step_id: stepId }),
      });
      if (!res.ok) throw new Error(`Reject step: ${res.status}`);
      await this.fetchDetail(workflowId);
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    // 30s fallback poll (SSE is the primary data source).
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);

    // Subscribe to SSE events: apply data directly from hud.workflows snapshots.
    this.eventUnsubs.push(
      eventStore.on('hud.workflows', (e) => this.applySnapshot(e.data)),
      // Legacy daemon events still trigger a full refresh as fallback.
      eventStore.on('workflow.step', () => this.fetch()),
      // Granular workflow mutation events — trigger full refresh.
      eventStore.on('hud.workflow.approve', () => this.fetch()),
      eventStore.on('hud.workflow.reject', () => this.fetch()),
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

export const workflowStore = new WorkflowStore();
