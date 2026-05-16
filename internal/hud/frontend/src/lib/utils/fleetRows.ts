// Pure row builders for the Fleet panel. Extracted from FleetPanel.svelte
// during the Slice B1 panel decomp (.loom/117) so the join logic stays
// out of both the panel composition shell and the fleet store. Single seam
// for testing row shape.

import type { Session, SessionTreeNode } from '../stores/fleet.svelte.ts';
import type { SpawnState } from '../stores/spawn.svelte.ts';
import type { UnifiedAgent } from './agents.ts';
import { sanitizeText } from './format.ts';

export interface FleetRow {
  id: string;
  agent: UnifiedAgent;
  depth: number;
  ungrouped: boolean;
  session: Session | null;
  parentSession: Session | null;
  rootSession: Session | null;
  childSessions: Session[];
  lineage: Session[];
  liveChildCount: number;
  totalChildCount: number;
}

export interface FleetRowsInput {
  agents: UnifiedAgent[];
  sortKey: string;
  sortDir: 'asc' | 'desc';
  groupByRootSession: boolean;
  sessionById: Map<string, Session>;
  sessionTree: SessionTreeNode[];
  parentSession: (sessionId: string) => Session | null | undefined;
  rootSession: (sessionId: string) => Session | null | undefined;
  childSessions: (sessionId: string) => Session[];
  sessionLineage: (sessionId: string) => Session[];
  agentLookup: Map<string, UnifiedAgent>;
}

export interface FleetRowsResult {
  rows: FleetRow[];
  ungroupedStartIndex: number;
  rootGroupCount: number;
  ungroupedCount: number;
}

export function compareFleetAgents(
  left: UnifiedAgent,
  right: UnifiedAgent,
  sortKey: string,
  sortDir: 'asc' | 'desc',
): number {
  let cmp = 0;
  switch (sortKey) {
    case 'agent':
      cmp = sanitizeText(left.agent_id ?? '').localeCompare(sanitizeText(right.agent_id ?? ''));
      break;
    case 'status': {
      const order: Record<string, number> = { active: 0, idle: 1, offline: 2 };
      cmp = (order[left.status] ?? 9) - (order[right.status] ?? 9);
      break;
    }
    case 'evidence':
      cmp = Number(right.has_session) - Number(left.has_session);
      if (cmp === 0) cmp = Number(right.has_presence) - Number(left.has_presence);
      break;
    case 'namespace':
      cmp = sanitizeText(left.namespace ?? '').localeCompare(sanitizeText(right.namespace ?? ''));
      break;
    case 'heartbeat':
      cmp =
        new Date(left.last_heartbeat || left.session_started_at || 0).getTime() -
        new Date(right.last_heartbeat || right.session_started_at || 0).getTime();
      break;
    default:
      break;
  }
  return sortDir === 'desc' ? -cmp : cmp;
}

export function buildFleetRows(input: FleetRowsInput): FleetRowsResult {
  const {
    agents,
    sortKey,
    sortDir,
    groupByRootSession,
    sessionById,
    sessionTree,
    parentSession,
    rootSession,
    childSessions,
    sessionLineage,
    agentLookup,
  } = input;

  function buildRow(agent: UnifiedAgent, depth = 0, ungrouped = false): FleetRow {
    const session = agent.session_id ? (sessionById.get(agent.session_id) ?? null) : null;
    const parent = session ? (parentSession(session.id) ?? null) : null;
    const root = session ? (rootSession(session.id) ?? null) : null;
    const children = session ? childSessions(session.id) : [];
    const lineage = session ? sessionLineage(session.id) : [];
    const liveChildCount = children.filter((child) => agentLookup.has(child.agent_id)).length;
    return {
      id: agent.agent_id,
      agent,
      depth,
      ungrouped,
      session,
      parentSession: parent,
      rootSession: root,
      childSessions: children,
      lineage,
      liveChildCount,
      totalChildCount: children.length,
    };
  }

  function leadAgentForNode(
    node: SessionTreeNode,
    agentBySessionId: Map<string, UnifiedAgent>,
  ): UnifiedAgent | null {
    const direct = agentBySessionId.get(node.session.id);
    if (direct) return direct;
    for (const child of node.children ?? []) {
      const nested = leadAgentForNode(child, agentBySessionId);
      if (nested) return nested;
    }
    return null;
  }

  function flattenSessionNode(
    node: SessionTreeNode,
    agentBySessionId: Map<string, UnifiedAgent>,
    depth = 0,
  ): FleetRow[] {
    const rows: FleetRow[] = [];
    const directAgent = agentBySessionId.get(node.session.id);
    if (directAgent) rows.push(buildRow(directAgent, depth));
    const sortedChildren = [...(node.children ?? [])].sort((left, right) => {
      const leftLead = leadAgentForNode(left, agentBySessionId);
      const rightLead = leadAgentForNode(right, agentBySessionId);
      if (leftLead && rightLead) return compareFleetAgents(leftLead, rightLead, sortKey, sortDir);
      if (leftLead) return -1;
      if (rightLead) return 1;
      return new Date(left.session.started_at ?? 0).getTime() - new Date(right.session.started_at ?? 0).getTime();
    });
    for (const child of sortedChildren) {
      rows.push(...flattenSessionNode(child, agentBySessionId, depth + 1));
    }
    return rows;
  }

  const flatRows = [...agents]
    .sort((a, b) => compareFleetAgents(a, b, sortKey, sortDir))
    .map((agent) => buildRow(agent, 0));

  if (!groupByRootSession) {
    return {
      rows: flatRows,
      ungroupedStartIndex: -1,
      rootGroupCount: 0,
      ungroupedCount: 0,
    };
  }

  const agentBySessionId = new Map<string, UnifiedAgent>();
  for (const agent of agents) {
    if (agent.session_id && sessionById.has(agent.session_id)) {
      agentBySessionId.set(agent.session_id, agent);
    }
  }

  const groupedRows: FleetRow[] = [];
  const seenAgents = new Set<string>();
  const sortedRoots = [...sessionTree].sort((left, right) => {
    const leftLead = leadAgentForNode(left, agentBySessionId);
    const rightLead = leadAgentForNode(right, agentBySessionId);
    if (leftLead && rightLead) return compareFleetAgents(leftLead, rightLead, sortKey, sortDir);
    if (leftLead) return -1;
    if (rightLead) return 1;
    return new Date(left.session.started_at ?? 0).getTime() - new Date(right.session.started_at ?? 0).getTime();
  });

  for (const root of sortedRoots) {
    const rows = flattenSessionNode(root, agentBySessionId);
    if (rows.length === 0) continue;
    groupedRows.push(...rows);
    for (const row of rows) seenAgents.add(row.agent.agent_id);
  }

  // Anything not slotted into a session tree (orphans, idle session-less
  // presences, spawn-only entries) gets appended below the grouped section
  // with `ungrouped: true` so the renderer can show a divider before the
  // first one.
  for (const row of flatRows) {
    if (!seenAgents.has(row.agent.agent_id)) {
      groupedRows.push({ ...row, ungrouped: true });
    }
  }

  let ungroupedStartIndex = -1;
  for (let i = 0; i < groupedRows.length; i++) {
    if (groupedRows[i].ungrouped) {
      ungroupedStartIndex = i;
      break;
    }
  }

  const groupKeys = new Set<string>();
  for (const row of groupedRows) {
    if (row.ungrouped) continue;
    const groupKey = row.rootSession?.id || row.session?.id || row.id;
    groupKeys.add(groupKey);
  }

  const ungroupedCount = groupedRows.filter((r) => r.ungrouped).length;

  return {
    rows: groupedRows,
    ungroupedStartIndex,
    rootGroupCount: groupKeys.size,
    ungroupedCount,
  };
}

export function buildSpawnByAgentId(spawns: SpawnState[]): Map<string, SpawnState> {
  const map = new Map<string, SpawnState>();
  for (const s of spawns) {
    map.set(s.agent_id, s);
  }
  return map;
}

export function buildExpiringClaims(
  fileClaims: Array<{ agent_id: string; file_path: string; expires_at?: string | null }>,
  windowMs = 5 * 60 * 1000,
): Map<string, string[]> {
  const map = new Map<string, string[]>();
  const now = Date.now();
  const cutoff = now + windowMs;
  for (const claim of fileClaims) {
    if (!claim.expires_at) continue;
    const exp = new Date(claim.expires_at).getTime();
    if (exp > now && exp <= cutoff) {
      const arr = map.get(claim.agent_id) ?? [];
      arr.push(claim.file_path);
      map.set(claim.agent_id, arr);
    }
  }
  return map;
}
