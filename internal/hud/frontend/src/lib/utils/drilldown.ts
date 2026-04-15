interface RouterLike {
  navigate: (view: string, subView?: string, detail?: string | null) => void;
}

interface SessionLike {
  id: string;
}

interface AgentTarget {
  agent_id?: string | null;
  session_id?: string | null;
}

type SessionResolver = (agentId: string) => SessionLike | undefined;

export function resolveSessionDetail(target: AgentTarget, resolveSession?: SessionResolver): string | null {
  const explicitSession = (target.session_id ?? '').trim();
  if (explicitSession) return explicitSession;
  const agentID = (target.agent_id ?? '').trim();
  if (!agentID) return null;
  return resolveSession?.(agentID)?.id ?? null;
}

export function navigateToAgentTraces(router: RouterLike, agentId?: string | null): void {
  const agentID = (agentId ?? '').trim();
  if (agentID) {
    router.navigate('activity', 'traces', agentID);
    return;
  }
  router.navigate('activity', 'traces');
}

export function navigateToAgentSessionOrTraces(
  router: RouterLike,
  target: AgentTarget,
  resolveSession?: SessionResolver,
): 'session' | 'traces' | 'fleet' {
  const sessionID = resolveSessionDetail(target, resolveSession);
  if (sessionID) {
    router.navigate('agents', 'fleet', sessionID);
    return 'session';
  }
  const agentID = (target.agent_id ?? '').trim();
  if (agentID) {
    navigateToAgentTraces(router, agentID);
    return 'traces';
  }
  router.navigate('agents', 'fleet');
  return 'fleet';
}
