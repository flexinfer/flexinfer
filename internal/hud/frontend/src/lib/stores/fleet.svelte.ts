// Fleet store - daemon status and sessions overview
// v2: SSE-first with 30s fallback poll. Applies hud.fleet snapshots directly.
import { eventStore } from './events.svelte.ts';
import { arraysEqualById } from '../utils/diff.ts';
import { spawnStore, type SpawnState } from './spawn.svelte.ts';
import {
  buildUnifiedAgents,
  summarizeUnifiedAgents,
  type UnifiedAgent,
  type UnifiedAgentSummary,
} from '../utils/agents.ts';

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
  // Session hierarchy (subagent grouping). parent_session_id points at
  // the directly enclosing session; root_session_id points at the top of
  // the spawn chain (matches id for root sessions).
  parent_session_id?: string;
  root_session_id?: string;
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

export interface SessionTreeNode {
  session: Session;
  depth: number;
  children: SessionTreeNode[];
}

type PollingOwner = string | symbol;
const DEFAULT_POLLING_OWNER = 'default';

function extractProject(namespace: string | undefined): string {
  if (!namespace) return '(ungrouped)';
  const seg = namespace.split('/')[0];
  return seg || '(ungrouped)';
}

function isPinnedMobileSession(session: { agentType?: string; description?: string }): boolean {
  const agentType = (session.agentType ?? '').trim().toLowerCase();
  if (agentType === 'mobile') return true;
  const description = (session.description ?? '').trim().toLowerCase();
  return description.startsWith('mobile session');
}

function sortSessionsByStartedAt(sessions: Session[]): Session[] {
  return [...sessions].sort((left, right) => {
    const leftStarted = new Date(left.started_at ?? 0).getTime();
    const rightStarted = new Date(right.started_at ?? 0).getTime();
    if (leftStarted !== rightStarted) return leftStarted - rightStarted;
    return left.id.localeCompare(right.id);
  });
}

function fallbackParentSessionId(session: Session): string | undefined {
  if (session.parent_session_id) return session.parent_session_id;
  if (session.root_session_id && session.root_session_id !== session.id) {
    return session.root_session_id;
  }
  return undefined;
}

function buildSessionIndex(sessions: Session[]): {
  sessionById: Map<string, Session>;
  childSessionsById: Map<string, Session[]>;
} {
  const orderedSessions = sortSessionsByStartedAt(sessions);
  const sessionById = new Map<string, Session>();
  const childSessionsById = new Map<string, Session[]>();

  for (const session of orderedSessions) {
    if (session?.id) sessionById.set(session.id, session);
  }

  for (const session of orderedSessions) {
    const parentId = fallbackParentSessionId(session);
    if (!parentId || parentId === session.id) continue;
    const siblings = childSessionsById.get(parentId) ?? [];
    siblings.push(session);
    childSessionsById.set(parentId, siblings);
  }

  return { sessionById, childSessionsById };
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
  private pollingOwners = new Map<PollingOwner, number>();

  get activeSessions(): Session[] {
    return this.sessions.filter((s) => s.status === 'active');
  }

  get unifiedAgents(): UnifiedAgent[] {
    return buildUnifiedAgents({
      sessions: this.sessions,
      agents: this.agents,
      tasks: this.tasks,
      fileClaims: this.fileClaims,
      spawns: spawnStore.spawns,
    });
  }

  get unifiedSummary(): UnifiedAgentSummary {
    return summarizeUnifiedAgents(this.unifiedAgents);
  }

  get liveAgents(): UnifiedAgent[] {
    return this.unifiedAgents.filter((agent) => agent.status === 'active' || agent.status === 'idle');
  }

  get totalTokens(): number {
    return this.sessions.reduce((sum, s) => sum + (s.total_tokens || 0), 0);
  }

  get agentCount(): number {
    return this.unifiedSummary.live_agents;
  }

  get sessionById(): Map<string, Session> {
    return buildSessionIndex(this.sessions).sessionById;
  }

  get sessionTree(): SessionTreeNode[] {
    const { sessionById, childSessionsById } = buildSessionIndex(this.sessions);
    const nodeById = new Map<string, SessionTreeNode>();
    for (const session of sortSessionsByStartedAt(this.sessions)) {
      nodeById.set(session.id, {
        session,
        depth: 0,
        children: [],
      });
    }

    const rootNodes: SessionTreeNode[] = [];
    for (const session of sortSessionsByStartedAt(this.sessions)) {
      const node = nodeById.get(session.id);
      if (!node) continue;
      const parentId = fallbackParentSessionId(session);
      const parentNode = parentId ? nodeById.get(parentId) : undefined;
      if (parentNode && parentNode.session.id !== node.session.id) {
        parentNode.children.push(node);
        continue;
      }
      rootNodes.push(node);
    }

    const assignDepth = (node: SessionTreeNode, depth: number, seen = new Set<string>()) => {
      if (seen.has(node.session.id)) return;
      seen.add(node.session.id);
      node.depth = depth;
      const children = childSessionsById.get(node.session.id) ?? [];
      node.children = children
        .map((session) => nodeById.get(session.id))
        .filter((child): child is SessionTreeNode => !!child);
      for (const child of node.children) assignDepth(child, depth + 1, new Set(seen));
    };

    for (const rootNode of rootNodes) assignDepth(rootNode, 0);
    return rootNodes;
  }

  /** Find a session by agent_id (for cross-referencing with spawns). */
  sessionForAgent(agentId: string): Session | undefined {
    return this.sessions.find(s => s.agent_id === agentId);
  }

  parentSession(sessionId: string): Session | undefined {
    const { sessionById } = buildSessionIndex(this.sessions);
    const session = sessionById.get(sessionId);
    if (!session) return undefined;
    const parentId = fallbackParentSessionId(session);
    if (!parentId || parentId === session.id) return undefined;
    return sessionById.get(parentId);
  }

  childSessions(sessionId: string): Session[] {
    const { childSessionsById } = buildSessionIndex(this.sessions);
    return childSessionsById.get(sessionId) ?? [];
  }

  rootSession(sessionId: string): Session | undefined {
    const { sessionById } = buildSessionIndex(this.sessions);
    let cursor = sessionById.get(sessionId);
    if (!cursor) return undefined;
    const seen = new Set<string>();
    while (cursor && !seen.has(cursor.id)) {
      seen.add(cursor.id);
      if (cursor.root_session_id && cursor.root_session_id !== cursor.id) {
        const root = sessionById.get(cursor.root_session_id);
        if (root) return root;
      }
      if (!cursor.parent_session_id || cursor.parent_session_id === cursor.id) {
        return cursor;
      }
      const parent = sessionById.get(cursor.parent_session_id);
      if (!parent) return cursor;
      cursor = parent;
    }
    return cursor;
  }

  sessionLineage(sessionId: string): Session[] {
    const { sessionById } = buildSessionIndex(this.sessions);
    const lineage: Session[] = [];
    let cursor = sessionById.get(sessionId);
    const seen = new Set<string>();
    while (cursor && !seen.has(cursor.id)) {
      seen.add(cursor.id);
      lineage.unshift(cursor);
      const parentId = fallbackParentSessionId(cursor);
      if (!parentId || parentId === cursor.id) break;
      const parent = sessionById.get(parentId);
      if (!parent) break;
      cursor = parent;
    }
    return lineage;
  }

  /**
   * spawnForSession looks up the spawn linked to a given session.
   * Finds the session by sessionId, extracts its agent_id, then queries
   * spawnStore.spawns for a spawn with the same agent_id.
   */
  spawnForSession(sessionId: string): SpawnState | undefined {
    const session = this.sessions.find(s => s.id === sessionId);
    if (!session) return undefined;
    return spawnStore.spawnForAgent(session.agent_id);
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
        (s) => s.agentStatus === 'active' || isPinnedMobileSession(s) || s.tasks.some((t) => t.status === 'in_progress'),
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
      const res = await globalThis.fetch('/api/fleet');
      if (!res.ok) throw new Error(`Fleet API: ${res.status}`);
      const snapshot = await res.json();
      this.applySnapshot(snapshot as Record<string, unknown>);
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

  private ensureEventSubscriptions(): void {
    if (this.eventUnsubs.length > 0) return;
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
      // Live entry count updates from context additions.
      eventStore.on('agent.context.added', (e) => {
        const data = e.data as Record<string, unknown>;
        const sessionId = data.session_id as string;
        const count = (data.entry_count as number) || 0;
        if (sessionId && count > 0) {
          this.sessions = this.sessions.map((s) =>
            s.id === sessionId ? { ...s, entry_count: s.entry_count + count } : s,
          );
          this.lastUpdated = new Date();
        }
      }),
      eventStore.on('agent.task.update', () => this.fetch()),
    );
  }

  private refreshPollTimer(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }

    if (this.pollingOwners.size === 0) return;

    const intervalMs = Math.min(...this.pollingOwners.values());
    this.pollTimer = setInterval(() => {
      if (!eventStore.connected) this.fetch();
    }, intervalMs);
  }

  startPolling(intervalMs = 30000, owner: PollingOwner = DEFAULT_POLLING_OWNER): void {
    const wasIdle = this.pollingOwners.size === 0;
    this.pollingOwners.set(owner, intervalMs);
    this.ensureEventSubscriptions();
    this.refreshPollTimer();
    if (wasIdle) this.fetch();
  }

  stopPolling(owner: PollingOwner = DEFAULT_POLLING_OWNER): void {
    this.pollingOwners.delete(owner);
    if (this.pollingOwners.size > 0) {
      this.refreshPollTimer();
      return;
    }

    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    for (const unsub of this.eventUnsubs) unsub();
    this.eventUnsubs = [];
  }
}

export const fleetStore = new FleetStore();
