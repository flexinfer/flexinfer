// Presence store - agent presence registry, file claims, and worktree assignments
import { eventStore } from './events.svelte.ts';

export interface AgentPresence {
  agent_id: string;
  session_id: string;
  status: string;
  agent_type: string;
  description: string;
  current_task: string;
  active_files: string[];
  branch: string;
  pr_url?: string;
  worktree_id: string;
  last_heartbeat: string;
  registered_at: string;
}

export interface FileClaim {
  id: string;
  agent_id: string;
  session_id: string;
  file_path: string;
  claim_type: string;
  reason: string;
  created_at: string;
  expires_at: string | null;
}

export interface WorktreeAssignment {
  assignment_id: string;
  agent_id: string;
  session_id: string;
  worktree_path: string;
  branch: string;
  base_branch: string;
  purpose: string;
  status: string;
  created_at: string;
  released_at: string | null;
  git_status: string;
}

export interface PresenceResponse {
  agents: AgentPresence[];
  active_agents: number;
  idle_agents: number;
  offline_agents: number;
  total: number;
}

export interface ClaimsResponse {
  claims: FileClaim[];
  count: number;
}

export interface WorktreesResponse {
  worktrees: WorktreeAssignment[];
  active_worktrees: number;
}

class PresenceStore {
  agents = $state<AgentPresence[]>([]);
  claims = $state<FileClaim[]>([]);
  worktrees = $state<WorktreeAssignment[]>([]);
  activeCount = $state(0);
  idleCount = $state(0);
  offlineCount = $state(0);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get liveAgents(): AgentPresence[] {
    return this.agents.filter((a) => a.status === 'active' || a.status === 'idle');
  }

  get claimedFiles(): string[] {
    return [...new Set(this.claims.map((c) => c.file_path))];
  }

  get agentTypes(): string[] {
    return [...new Set(this.agents.map((a) => a.agent_type).filter(Boolean))];
  }

  get fileConflicts(): Array<{ path: string; agents: string[] }> {
    const fileCounts: Record<string, string[]> = {};
    for (const c of this.claims) {
      if (!fileCounts[c.file_path]) fileCounts[c.file_path] = [];
      fileCounts[c.file_path].push(c.agent_id);
    }
    return Object.entries(fileCounts)
      .filter(([, agents]) => agents.length > 1)
      .map(([path, agents]) => ({ path, agents: [...new Set(agents)] }));
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [presenceRes, claimsRes, worktreesRes] = await Promise.all([
        globalThis.fetch('/api/presence'),
        globalThis.fetch('/api/claims'),
        globalThis.fetch('/api/worktrees'),
      ]);
      if (!presenceRes.ok) throw new Error(`Presence API: ${presenceRes.status}`);
      if (!claimsRes.ok) throw new Error(`Claims API: ${claimsRes.status}`);
      if (!worktreesRes.ok) throw new Error(`Worktrees API: ${worktreesRes.status}`);

      const presenceData: PresenceResponse = await presenceRes.json();
      this.agents = presenceData.agents || [];
      this.activeCount = presenceData.active_agents ?? 0;
      this.idleCount = presenceData.idle_agents ?? 0;
      this.offlineCount = presenceData.offline_agents ?? 0;

      const claimsData: ClaimsResponse = await claimsRes.json();
      this.claims = claimsData.claims || [];

      const worktreesData: WorktreesResponse = await worktreesRes.json();
      this.worktrees = worktreesData.worktrees || [];

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
      eventStore.on('process.start', () => this.fetch()),
      eventStore.on('process.stop', () => this.fetch()),
      // Granular agent events — update presence in real-time.
      eventStore.on('agent.heartbeat', (e) => {
        const data = e.data as Record<string, unknown>;
        const agentId = data.agent_id as string;
        const status = (data.status as string) || 'active';
        const ts = (data.timestamp as string) || new Date().toISOString();
        const currentTask = (data.current_task as string) || '';
        const branch = (data.branch as string) || '';
        const activeFiles = (data.active_files as string[]) || [];
        this.agents = this.agents.map((a) =>
          a.agent_id === agentId
            ? {
                ...a,
                status,
                current_task: currentTask || a.current_task,
                branch: branch || a.branch,
                active_files: activeFiles.length > 0 ? activeFiles : a.active_files,
                last_heartbeat: ts,
              }
            : a,
        );
        this.lastUpdated = new Date();
      }),
      // Session start/end — trigger full refresh for complete data.
      eventStore.on('agent.session.start', () => this.fetch()),
      eventStore.on('agent.session.end', () => this.fetch()),
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

export const presenceStore = new PresenceStore();
