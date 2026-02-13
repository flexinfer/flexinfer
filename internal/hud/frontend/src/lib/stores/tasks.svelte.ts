// Tasks store - task management
// v2: SSE-first with 30s fallback poll. Applies task list from hud.fleet snapshots.
import { eventStore } from './events.svelte.ts';

export interface Task {
  id: string;
  session_id: string;
  agent_id: string;
  agent: string;
  namespace: string;
  title: string;
  context: string;
  description: string;
  priority: 'low' | 'medium' | 'high' | 'critical';
  status: 'pending' | 'in_progress' | 'completed' | 'blocked' | 'cancelled';
  tags: string[];
  blocked_by: string[];
  created_at: string;
  updated_at: string;
}

export interface TasksResponse {
  tasks: Task[];
}

export type TaskSortField = 'priority' | 'status' | 'updated_at' | 'created_at' | 'title';
export type TaskSortDir = 'asc' | 'desc';

const PRIORITY_ORDER: Record<string, number> = {
  critical: 0,
  high: 1,
  medium: 2,
  low: 3,
};

const STATUS_ORDER: Record<string, number> = {
  in_progress: 0,
  blocked: 1,
  pending: 2,
  completed: 3,
  cancelled: 4,
};

class TaskStore {
  tasks = $state<Task[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  filterStatus = $state<string>('all');
  filterPriority = $state<string>('all');
  sortField = $state<TaskSortField>('priority');
  sortDir = $state<TaskSortDir>('asc');

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get filteredTasks(): Task[] {
    let result = [...this.tasks];

    if (this.filterStatus !== 'all') {
      result = result.filter((t) => t.status === this.filterStatus);
    }
    if (this.filterPriority !== 'all') {
      result = result.filter((t) => t.priority === this.filterPriority);
    }

    result.sort((a, b) => {
      let cmp = 0;
      switch (this.sortField) {
        case 'priority':
          cmp = (PRIORITY_ORDER[a.priority] ?? 9) - (PRIORITY_ORDER[b.priority] ?? 9);
          break;
        case 'status':
          cmp = (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9);
          break;
        case 'updated_at':
          cmp = new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime();
          break;
        case 'created_at':
          cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
          break;
        case 'title':
          cmp = a.title.localeCompare(b.title);
          break;
      }
      return this.sortDir === 'desc' ? -cmp : cmp;
    });

    return result;
  }

  get pendingCount(): number {
    return this.tasks.filter((t) => t.status === 'pending').length;
  }

  get inProgressCount(): number {
    return this.tasks.filter((t) => t.status === 'in_progress').length;
  }

  get blockedCount(): number {
    return this.tasks.filter((t) => t.status === 'blocked').length;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/tasks');
      if (!res.ok) throw new Error(`Tasks API: ${res.status}`);
      const data: TasksResponse = await res.json();
      this.tasks = data.tasks || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  /** Apply task list directly from SSE hud.fleet snapshot, avoiding an HTTP round-trip. */
  applySnapshot(data: Record<string, unknown>): void {
    const tasks = data.tasks as Task[] | undefined;
    if (!tasks) return;
    this.tasks = tasks;
    this.lastUpdated = new Date();
    this.error = null;
  }

  async updateStatus(taskId: string, status: string): Promise<void> {
    try {
      const res = await globalThis.fetch(`/api/tasks/${taskId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status }),
      });
      if (!res.ok) throw new Error(`Update task: ${res.status}`);
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async setPriority(taskId: string, priority: string): Promise<void> {
    try {
      const res = await globalThis.fetch(`/api/tasks/${taskId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ priority }),
      });
      if (!res.ok) throw new Error(`Set priority: ${res.status}`);
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async createTask(params: {
    title: string;
    priority: string;
    sessionId?: string;
    tags?: string[];
    context?: string;
    filePath?: string;
    lineNumber?: number;
    blockedBy?: string[];
  }): Promise<boolean> {
    try {
      const body: Record<string, unknown> = {
        title: params.title,
        priority: params.priority,
      };
      if (params.sessionId) body.session_id = params.sessionId;
      if (params.tags?.length) body.tags = params.tags;
      if (params.context) body.context = params.context;
      if (params.filePath) body.file_path = params.filePath;
      if (params.lineNumber) body.line_number = params.lineNumber;
      if (params.blockedBy?.length) body.blocked_by = params.blockedBy;
      const res = await globalThis.fetch('/api/tasks', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`Create task: ${res.status}`);
      await this.fetch();
      return true;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async resolve(taskId: string, resolution: string): Promise<void> {
    try {
      const res = await globalThis.fetch(`/api/tasks/${taskId}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status: 'completed', resolution }),
      });
      if (!res.ok) throw new Error(`Resolve task: ${res.status}`);
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

    // Subscribe to SSE events: apply task list directly from hud.fleet snapshots.
    // The FleetMonitor fetches all tasks on its 15s cadence and broadcasts them.
    this.eventUnsubs.push(
      eventStore.on('hud.fleet', (e) => this.applySnapshot(e.data)),
      // Granular task creation event — trigger full refresh.
      eventStore.on('hud.task.create', () => this.fetch()),
      // Granular agent.task.update — apply single-task status change immediately.
      eventStore.on('agent.task.update', (e) => {
        const data = e.data as Record<string, unknown>;
        const taskId = data.task_id as string;
        const status = data.status as string;
        if (taskId && status) {
          this.tasks = this.tasks.map((t) =>
            t.id === taskId ? { ...t, status: status as Task['status'], updated_at: new Date().toISOString() } : t,
          );
          this.lastUpdated = new Date();
        }
      }),
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

export const taskStore = new TaskStore();
