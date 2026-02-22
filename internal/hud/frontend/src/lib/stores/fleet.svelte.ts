// Fleet store - daemon status and sessions overview
// v2: SSE-first with 30s fallback poll. Applies hud.fleet snapshots directly.
import { eventStore } from './events.svelte.ts';
import { arraysEqualById } from '../utils/diff.ts';

export interface Process {
  pid: number;
  name: string;
  status: string;
}

export interface StatusResponse {
  running: boolean;
  servers: number;
  activeConns: number;
  idleConns: number;
  processes: Process[];
}

export interface Session {
  id: string;
  agent_id: string;
  agent: string;
  namespace: string;
  started_at: string;
  ended_at: string | null;
  status: string;
  description: string;
  entry_count: number;
  total_tokens: number;
  tokens_used: number;
  task_count: number;
  memory_items: number;
  active: boolean;
}

export interface SessionsResponse {
  sessions: Session[];
}

export interface PresenceInfo {
  agent_id: string;
  session_id?: string;
  status: string;
  agent_type: string;
  description: string;
  current_task: string;
  active_files?: string[];
  branch: string;
  pr_url?: string;
  worktree_id?: string;
  last_heartbeat: string;
  registered_at: string;
}

export interface TaskInfo {
  id: string;
  session_id: string;
  agent_id: string;
  namespace: string;
  title: string;
  context?: string;
  priority: string;
  status: string;
  tags?: string[];
  blocked_by?: string[];
  created_at: string;
  updated_at: string;
}

export interface FileClaimInfo {
  id: string;
  agent_id: string;
  session_id: string;
  file_path: string;
  claim_type: string;
  reason: string;
  created_at: string;
  expires_at?: string;
}

export interface EnrichedSession extends Session {
  agentStatus?: string;
  agentType?: string;
  currentTask?: string;
  branch?: string;
  tasks: TaskInfo[];
}

export interface NamespaceGroup {
  project: string;
  sessions: EnrichedSession[];
  orphanTasks: TaskInfo[];
  hasActiveWork: boolean;
  totalTokens: number;
  sessionCount: number;
  taskCount: number;
}

function extractProject(namespace: string | undefined): string {
  if (!namespace) return '(ungrouped)';
  const seg = namespace.split('/')[0];
  return seg || '(ungrouped)';
}

class FleetStore {
  status = $state<StatusResponse>({
    running: false,
    servers: 0,
    activeConns: 0,
    idleConns: 0,
    processes: [],
  });
  sessions = $state<Session[]>([]);
  agents = $state<PresenceInfo[]>([]);
  tasks = $state<TaskInfo[]>([]);
  fileClaims = $state<FileClaimInfo[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get activeSessions(): Session[] {
    return this.sessions.filter((s) => s.status === 'active');
  }

  get totalTokens(): number {
    return this.sessions.reduce((sum, s) => sum + (s.total_tokens || 0), 0);
  }

  get agentCount(): number {
    const agents = new Set(this.sessions.map((s) => s.agent_id));
    return agents.size;
  }

  /** Group active sessions by namespace project, enriched with agent presence and linked tasks. */
  get namespaceGroups(): NamespaceGroup[] {
    // Build agent lookup by session_id for O(1) enrichment
    const agentBySession = new Map<string, PresenceInfo>();
    for (const a of this.agents) {
      if (a.session_id) agentBySession.set(a.session_id, a);
    }

    // Build task lookup by session_id
    const tasksBySession = new Map<string, TaskInfo[]>();
    const orphansByProject = new Map<string, TaskInfo[]>();
    for (const t of this.tasks) {
      if (t.session_id) {
        const arr = tasksBySession.get(t.session_id) ?? [];
        arr.push(t);
        tasksBySession.set(t.session_id, arr);
      } else {
        const proj = extractProject(t.namespace);
        const arr = orphansByProject.get(proj) ?? [];
        arr.push(t);
        orphansByProject.set(proj, arr);
      }
    }

    // Group active sessions by project
    const groupMap = new Map<string, EnrichedSession[]>();
    for (const s of this.activeSessions) {
      const proj = extractProject(s.namespace);
      const agent = agentBySession.get(s.id);
      const enriched: EnrichedSession = {
        ...s,
        agentStatus: agent?.status,
        agentType: agent?.agent_type,
        currentTask: agent?.current_task,
        branch: agent?.branch,
        tasks: tasksBySession.get(s.id) ?? [],
      };
      const arr = groupMap.get(proj) ?? [];
      arr.push(enriched);
      groupMap.set(proj, arr);
    }

    // Build NamespaceGroup array
    const groups: NamespaceGroup[] = [];
    for (const [project, sessions] of groupMap) {
      // Sort sessions: active agents first, then by start time
      sessions.sort((a, b) => {
        const aActive = a.agentStatus === 'active' ? 0 : 1;
        const bActive = b.agentStatus === 'active' ? 0 : 1;
        if (aActive !== bActive) return aActive - bActive;
        return (b.started_at ?? '').localeCompare(a.started_at ?? '');
      });

      const orphans = orphansByProject.get(project) ?? [];
      const totalTasks = sessions.reduce((s, sess) => s + sess.tasks.length, 0) + orphans.length;
      const hasActive = sessions.some(
        (s) => s.agentStatus === 'active' || s.tasks.some((t) => t.status === 'in_progress'),
      );

      groups.push({
        project,
        sessions,
        orphanTasks: orphans,
        hasActiveWork: hasActive,
        totalTokens: sessions.reduce((s, sess) => s + (sess.total_tokens || 0), 0),
        sessionCount: sessions.length,
        taskCount: totalTasks,
      });
    }

    // Also include orphan-only projects (tasks with no matching session)
    for (const [project, orphans] of orphansByProject) {
      if (!groupMap.has(project)) {
        groups.push({
          project,
          sessions: [],
          orphanTasks: orphans,
          hasActiveWork: orphans.some((t) => t.status === 'in_progress'),
          totalTokens: 0,
          sessionCount: 0,
          taskCount: orphans.length,
        });
      }
    }

    // Sort: active namespaces first, then alphabetical
    groups.sort((a, b) => {
      if (a.hasActiveWork !== b.hasActiveWork) return a.hasActiveWork ? -1 : 1;
      return a.project.localeCompare(b.project);
    });

    return groups;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [statusRes, sessionsRes] = await Promise.all([
        globalThis.fetch('/api/status'),
        globalThis.fetch('/api/sessions'),
      ]);
      if (!statusRes.ok) throw new Error(`Status API: ${statusRes.status}`);
      if (!sessionsRes.ok) throw new Error(`Sessions API: ${sessionsRes.status}`);
      this.status = await statusRes.json();
      const sessData: SessionsResponse = await sessionsRes.json();
      this.sessions = sessData.sessions || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  /** Apply a fleet snapshot directly from SSE, avoiding an HTTP round-trip. */
  applySnapshot(data: Record<string, unknown>): void {
    // The hud.fleet event carries the full FleetSnapshot from the monitor.
    if (data.daemon_running !== undefined) {
      this.status = {
        running: data.daemon_running as boolean,
        servers: (data.server_count as number) ?? 0,
        activeConns: (data.active_conns as number) ?? 0,
        idleConns: 0,
        processes: (data.processes as string[]) ?? [],
      };
    }
    if (data.sessions) {
      const next = data.sessions as Session[];
      const hashSession = (s: Session) => `${s.id}|${s.status}|${s.ended_at ?? ''}`;
      if (!arraysEqualById(this.sessions, next, hashSession)) {
        this.sessions = next;
      }
    }
    if (data.agents) {
      this.agents = data.agents as PresenceInfo[];
    }
    if (data.tasks) {
      const next = data.tasks as TaskInfo[];
      const hashTask = (t: TaskInfo) => `${t.id}|${t.status}|${t.priority}|${t.updated_at}`;
      if (!arraysEqualById(this.tasks, next, hashTask)) {
        this.tasks = next;
      }
    }
    if (data.file_claims) {
      this.fileClaims = data.file_claims as FileClaimInfo[];
    }
    this.lastUpdated = new Date();
    this.error = null;
  }

  async fetchSessionEntries(sessionId: string, limit = 50): Promise<Record<string, unknown>[] | null> {
    try {
      const params = new URLSearchParams({ limit: String(limit) });
      const res = await globalThis.fetch(`/api/sessions/${sessionId}/entries?${params.toString()}`);
      if (!res.ok) throw new Error(`Session entries: ${res.status}`);
      const data = await res.json();
      return data.entries ?? [];
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    // 30s fallback poll (SSE is the primary data source).
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);

    // Subscribe to SSE events: apply data directly from hud.fleet snapshots.
    this.eventUnsubs.push(
      eventStore.on('hud.fleet', (e) => this.applySnapshot(e.data)),
      // Legacy daemon events still trigger a full refresh as fallback.
      eventStore.on('config.reload', () => this.fetch()),
      eventStore.on('process.start', () => this.fetch()),
      eventStore.on('process.stop', () => this.fetch()),
      // Granular agent events — fetch full session data so new sessions appear immediately.
      eventStore.on('agent.session.start', () => this.fetch()),
      eventStore.on('agent.session.bootstrap', () => this.fetch()),
      // Reaped sessions should be treated the same as ended.
      eventStore.on('agent.session.reaped', (e) => {
        const sessionId = (e.data as Record<string, unknown>).session_id as string;
        if (sessionId) {
          this.sessions = this.sessions.map((s) =>
            s.id === sessionId ? { ...s, status: 'ended', ended_at: new Date().toISOString(), active: false } : s,
          );
        }
        this.lastUpdated = new Date();
      }),
      eventStore.on('agent.session.end', (e) => {
        const sessionId = (e.data as Record<string, unknown>).session_id as string;
        if (sessionId) {
          this.sessions = this.sessions.map((s) =>
            s.id === sessionId ? { ...s, status: 'ended', ended_at: new Date().toISOString(), active: false } : s,
          );
        }
        this.lastUpdated = new Date();
      }),
      eventStore.on('agent.heartbeat', (e) => {
        const data = e.data as Record<string, unknown>;
        const agentId = data.agent_id as string;
        const status = (data.status as string) || 'active';
        const ts = (data.timestamp as string) || new Date().toISOString();
        this.agents = this.agents.map((a) =>
          a.agent_id === agentId ? { ...a, status, last_heartbeat: ts } : a,
        );
        this.lastUpdated = new Date();
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

export const fleetStore = new FleetStore();
