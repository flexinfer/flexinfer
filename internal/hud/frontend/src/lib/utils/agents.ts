export type UnifiedAgentStatus = 'active' | 'idle' | 'offline';

interface SessionLike {
  id: string;
  agent_id: string;
  namespace?: string;
  status?: string;
  description?: string;
  started_at?: string;
  entry_count?: number;
  total_tokens?: number;
  tokens_used?: number;
  task_count?: number;
  memory_items?: number;
}

interface PresenceLike {
  agent_id: string;
  session_id?: string;
  status?: string;
  agent_type?: string;
  description?: string;
  current_task?: string;
  active_files?: string[];
  branch?: string;
  pr_url?: string;
  worktree_id?: string;
  last_heartbeat?: string;
  registered_at?: string;
}

interface TaskLike {
  agent_id?: string;
  status?: string;
}

interface FileClaimLike {
  agent_id?: string;
}

interface SpawnLike {
  spawn_id: string;
  agent_id: string;
  status?: string;
  request?: {
    project?: string;
    branch?: string;
    task_description?: string;
    agent_type?: string;
  };
}

export interface UnifiedAgent {
  agent_id: string;
  agent_type: string;
  status: UnifiedAgentStatus;
  source: 'presence' | 'presence+session' | 'session' | 'spawn';
  description: string;
  current_task: string;
  branch: string;
  last_heartbeat: string;
  registered_at: string;
  active_files: string[];
  active_file_count: number;
  pr_url?: string;
  worktree_id?: string;
  session_id?: string;
  namespace?: string;
  session_status?: string;
  session_started_at?: string;
  entry_count: number;
  total_tokens: number;
  task_count: number;
  blocked_tasks: number;
  claim_count: number;
  spawn_id?: string;
  spawn_status?: string;
  project?: string;
  has_presence: boolean;
  has_session: boolean;
  has_spawn: boolean;
}

export interface UnifiedAgentSummary {
  total_agents: number;
  active_agents: number;
  idle_agents: number;
  offline_agents: number;
  live_agents: number;
  with_sessions: number;
  with_presence: number;
  with_spawns: number;
}

export function inferAgentType(agentId: string | null | undefined, declaredType?: string | null): string {
  const raw = (declaredType ?? '').trim();
  if (raw && raw.toLowerCase() !== 'unknown') return raw;
  const id = (agentId ?? '').trim().toLowerCase();
  if (!id) return 'unknown';
  if (id.startsWith('claude')) return 'claude';
  if (id.startsWith('codex')) return 'codex';
  if (id.startsWith('gemini')) return 'gemini';
  if (id.startsWith('copilot')) return 'copilot';
  if (id.startsWith('kilocode')) return 'kilocode';
  return id.split('-')[0] || 'unknown';
}

export function normalizeUnifiedStatus(raw: string | null | undefined): UnifiedAgentStatus {
  const status = (raw ?? '').trim().toLowerCase();
  if (status === 'active') return 'active';
  if (status === 'idle') return 'idle';
  return 'offline';
}

export function isLiveSession(raw: string | null | undefined): boolean {
  return (raw ?? '').trim().toLowerCase() === 'active';
}

function agentSortTime(agent: UnifiedAgent): number {
  const ts = agent.last_heartbeat || agent.session_started_at || agent.registered_at;
  const parsed = ts ? new Date(ts).getTime() : 0;
  return Number.isFinite(parsed) ? parsed : 0;
}

export function buildUnifiedAgents(input: {
  sessions: SessionLike[];
  agents: PresenceLike[];
  tasks?: TaskLike[];
  fileClaims?: FileClaimLike[];
  spawns?: SpawnLike[];
}): UnifiedAgent[] {
  const sessions = input.sessions ?? [];
  const agents = input.agents ?? [];
  const tasks = input.tasks ?? [];
  const fileClaims = input.fileClaims ?? [];
  const spawns = input.spawns ?? [];

  const byAgent = new Map<string, UnifiedAgent>();
  const liveSessionsByID = new Map<string, SessionLike>();
  const liveSessionsByAgent = new Map<string, SessionLike>();

  for (const session of sessions) {
    if (!isLiveSession(session.status) || !session.agent_id) continue;
    liveSessionsByID.set(session.id, session);
    const existing = liveSessionsByAgent.get(session.agent_id);
    const sessionTime = new Date(session.started_at ?? 0).getTime();
    const existingTime = new Date(existing?.started_at ?? 0).getTime();
    if (!existing || sessionTime >= existingTime) {
      liveSessionsByAgent.set(session.agent_id, session);
    }
  }

  const taskCounts = new Map<string, { total: number; blocked: number }>();
  for (const task of tasks) {
    const agentID = task.agent_id?.trim();
    if (!agentID) continue;
    const current = taskCounts.get(agentID) ?? { total: 0, blocked: 0 };
    current.total += 1;
    if ((task.status ?? '').trim().toLowerCase() === 'blocked') {
      current.blocked += 1;
    }
    taskCounts.set(agentID, current);
  }

  const claimCounts = new Map<string, number>();
  for (const claim of fileClaims) {
    const agentID = claim.agent_id?.trim();
    if (!agentID) continue;
    claimCounts.set(agentID, (claimCounts.get(agentID) ?? 0) + 1);
  }

  for (const agent of agents) {
    if (!agent.agent_id) continue;
    const session =
      (agent.session_id ? liveSessionsByID.get(agent.session_id) : undefined) ??
      liveSessionsByAgent.get(agent.agent_id);

    byAgent.set(agent.agent_id, {
      agent_id: agent.agent_id,
      agent_type: inferAgentType(agent.agent_id, agent.agent_type),
      status: normalizeUnifiedStatus(agent.status),
      source: session ? 'presence+session' : 'presence',
      description: agent.description ?? session?.description ?? '',
      current_task: agent.current_task ?? '',
      branch: agent.branch ?? '',
      last_heartbeat: agent.last_heartbeat ?? '',
      registered_at: agent.registered_at ?? '',
      active_files: agent.active_files ?? [],
      active_file_count: agent.active_files?.length ?? 0,
      pr_url: agent.pr_url,
      worktree_id: agent.worktree_id,
      session_id: session?.id ?? agent.session_id,
      namespace: session?.namespace,
      session_status: session?.status,
      session_started_at: session?.started_at,
      entry_count: session?.entry_count ?? 0,
      total_tokens: session?.total_tokens ?? session?.tokens_used ?? 0,
      task_count: taskCounts.get(agent.agent_id)?.total ?? session?.task_count ?? 0,
      blocked_tasks: taskCounts.get(agent.agent_id)?.blocked ?? 0,
      claim_count: claimCounts.get(agent.agent_id) ?? 0,
      has_presence: true,
      has_session: !!session,
      has_spawn: false,
    });
  }

  for (const session of liveSessionsByAgent.values()) {
    const existing = byAgent.get(session.agent_id);
    if (existing) {
      existing.session_id = session.id;
      existing.namespace = session.namespace;
      existing.session_status = session.status;
      existing.session_started_at = session.started_at;
      existing.entry_count = session.entry_count ?? existing.entry_count;
      existing.total_tokens = session.total_tokens ?? session.tokens_used ?? existing.total_tokens;
      existing.task_count = Math.max(existing.task_count, session.task_count ?? 0);
      if (!existing.description) existing.description = session.description ?? '';
      existing.has_session = true;
      if (existing.source === 'presence') existing.source = 'presence+session';
      continue;
    }

    byAgent.set(session.agent_id, {
      agent_id: session.agent_id,
      agent_type: inferAgentType(session.agent_id),
      status: 'active',
      source: 'session',
      description: session.description ?? '',
      current_task: '',
      branch: '',
      last_heartbeat: '',
      registered_at: '',
      active_files: [],
      active_file_count: 0,
      session_id: session.id,
      namespace: session.namespace,
      session_status: session.status,
      session_started_at: session.started_at,
      entry_count: session.entry_count ?? 0,
      total_tokens: session.total_tokens ?? session.tokens_used ?? 0,
      task_count: taskCounts.get(session.agent_id)?.total ?? session.task_count ?? 0,
      blocked_tasks: taskCounts.get(session.agent_id)?.blocked ?? 0,
      claim_count: claimCounts.get(session.agent_id) ?? 0,
      has_presence: false,
      has_session: true,
      has_spawn: false,
    });
  }

  for (const spawn of spawns) {
    if (!spawn.agent_id) continue;
    const existing = byAgent.get(spawn.agent_id);
    if (existing) {
      existing.spawn_id = spawn.spawn_id;
      existing.spawn_status = spawn.status ?? existing.spawn_status;
      existing.project = existing.project || spawn.request?.project || '';
      existing.branch = existing.branch || spawn.request?.branch || '';
      if (!existing.description) existing.description = spawn.request?.task_description ?? '';
      if (existing.agent_type === 'unknown') {
        existing.agent_type = inferAgentType(spawn.agent_id, spawn.request?.agent_type);
      }
      existing.has_spawn = true;
      continue;
    }

    byAgent.set(spawn.agent_id, {
      agent_id: spawn.agent_id,
      agent_type: inferAgentType(spawn.agent_id, spawn.request?.agent_type),
      status: 'active',
      source: 'spawn',
      description: spawn.request?.task_description ?? '',
      current_task: '',
      branch: spawn.request?.branch ?? '',
      last_heartbeat: '',
      registered_at: '',
      active_files: [],
      active_file_count: 0,
      entry_count: 0,
      total_tokens: 0,
      task_count: taskCounts.get(spawn.agent_id)?.total ?? 0,
      blocked_tasks: taskCounts.get(spawn.agent_id)?.blocked ?? 0,
      claim_count: claimCounts.get(spawn.agent_id) ?? 0,
      spawn_id: spawn.spawn_id,
      spawn_status: spawn.status ?? '',
      project: spawn.request?.project ?? '',
      has_presence: false,
      has_session: false,
      has_spawn: true,
    });
  }

  const unified = [...byAgent.values()];
  unified.sort((left, right) => {
    const statusOrder: Record<UnifiedAgentStatus, number> = { active: 0, idle: 1, offline: 2 };
    const statusDelta = statusOrder[left.status] - statusOrder[right.status];
    if (statusDelta !== 0) return statusDelta;
    const timeDelta = agentSortTime(right) - agentSortTime(left);
    if (timeDelta !== 0) return timeDelta;
    return left.agent_id.localeCompare(right.agent_id);
  });
  return unified;
}

export function summarizeUnifiedAgents(agents: UnifiedAgent[]): UnifiedAgentSummary {
  const summary: UnifiedAgentSummary = {
    total_agents: agents.length,
    active_agents: 0,
    idle_agents: 0,
    offline_agents: 0,
    live_agents: 0,
    with_sessions: 0,
    with_presence: 0,
    with_spawns: 0,
  };

  for (const agent of agents) {
    if (agent.status === 'active') summary.active_agents += 1;
    else if (agent.status === 'idle') summary.idle_agents += 1;
    else summary.offline_agents += 1;

    if (agent.status === 'active' || agent.status === 'idle') {
      summary.live_agents += 1;
    }
    if (agent.has_session) summary.with_sessions += 1;
    if (agent.has_presence) summary.with_presence += 1;
    if (agent.has_spawn) summary.with_spawns += 1;
  }

  return summary;
}
