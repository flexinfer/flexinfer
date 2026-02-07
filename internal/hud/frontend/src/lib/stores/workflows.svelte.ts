// Workflows store - workflow orchestration
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

class WorkflowStore {
  workflows = $state<WorkflowSummary[]>([]);
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

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/workflows');
      if (!res.ok) throw new Error(`Workflows API: ${res.status}`);
      const data: WorkflowsResponse = await res.json();
      this.workflows = data.workflows || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
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

  startPolling(intervalMs = 5000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);

    // Subscribe to SSE events for immediate refresh.
    this.eventUnsubs.push(
      eventStore.on('workflow.step', () => this.fetch()),
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
