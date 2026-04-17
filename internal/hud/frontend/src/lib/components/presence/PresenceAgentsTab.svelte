<script>
  import { timelineStore } from '../../stores/timeline.svelte.ts';
  import { fleetStore } from '../../stores/fleet.svelte.ts';
  import { router } from '../../stores/router.svelte.ts';
  import { formatTime, relativeTime, agentColor } from '../../utils/format.ts';
  import StatusDot from '../../widgets/StatusDot.svelte';
  import AgentCard from '../../widgets/AgentCard.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let {
    agents = [],
    claims = [],
    worktrees = [],
    activeCount = 0,
    idleCount = 0,
    offlineCount = 0,
    claimedFilesCount = 0,
    showOfflineAgents = false,
    hiddenOfflineCount = 0,
    agentView = 'cards',
    onOpenDispatch = () => {},
    onOpenNudge = () => {},
  } = $props();

  // Start timeline polling for heartbeat data in card view.
  $effect(() => {
    if (agentView === 'cards') {
      timelineStore.startPolling(30000);
      return () => timelineStore.stopPolling();
    }
  });

  // Relative time tick (forces re-render).
  let tick = $state(0);
  $effect(() => {
    const t = setInterval(() => {
      tick++;
    }, 5000);
    return () => clearInterval(t);
  });

  function reactiveRelativeTime(ts) {
    void tick;
    return relativeTime(ts);
  }

  let branchCollisions = $derived.by(() => {
    const branchAgents = {};
    agents
      .filter((a) => a.status === 'active' && a.branch)
      .forEach((a) => {
        if (!branchAgents[a.branch]) branchAgents[a.branch] = [];
        branchAgents[a.branch].push(a.agent_id);
      });
    return Object.entries(branchAgents)
      .filter(([, branchAgentIds]) => branchAgentIds.length > 1)
      .map(([branch, branchAgentIds]) => ({ branch, agents: branchAgentIds }));
  });

  let agentOverlaps = $derived.by(() => {
    const fileCounts = {};
    claims.forEach((c) => {
      if (!fileCounts[c.file_path]) fileCounts[c.file_path] = [];
      fileCounts[c.file_path].push(c.agent_id);
    });
    const result = new Map();
    for (const claimAgents of Object.values(fileCounts)) {
      if (claimAgents.length < 2) continue;
      for (const a of claimAgents) {
        const others = claimAgents.filter((x) => x !== a);
        const existing = result.get(a) ?? [];
        for (const o of others) {
          if (!existing.includes(o)) existing.push(o);
        }
        result.set(a, existing);
      }
    }
    return result;
  });

  // Compute heartbeat frequency data per agent from timeline entries (12 buckets of 5min = 60min).
  let heartbeatDataMap = $derived.by(() => {
    const now = Date.now();
    const bucketSize = 5 * 60_000;
    const bucketCount = 12;
    const result = new Map();
    const entries = timelineStore.entries ?? [];

    for (const agent of agents) {
      const buckets = new Array(bucketCount).fill(0);
      for (const e of entries) {
        if (e.agent_id !== agent.agent_id) continue;
        if (e.event_type !== 'agent.heartbeat') continue;
        const ts = new Date(e.timestamp).getTime();
        const age = now - ts;
        const idx = bucketCount - 1 - Math.floor(age / bucketSize);
        if (idx >= 0 && idx < bucketCount) buckets[idx]++;
      }
      result.set(agent.agent_id, buckets);
    }
    return result;
  });

  let sortedAgents = $derived.by(() => {
    const statusOrder = { active: 0, idle: 1, offline: 2 };
    return [...agents].sort((left, right) => {
      const statusDelta = (statusOrder[left.status] ?? 9) - (statusOrder[right.status] ?? 9);
      if (statusDelta !== 0) return statusDelta;
      const leftHeartbeat = new Date(left.last_heartbeat || 0).getTime();
      const rightHeartbeat = new Date(right.last_heartbeat || 0).getTime();
      if (leftHeartbeat !== rightHeartbeat) return rightHeartbeat - leftHeartbeat;
      return (left.agent_id || '').localeCompare(right.agent_id || '');
    });
  });

  // Build a lookup map from session id → Session so we can resolve subagent
  // hierarchy without an O(n²) scan per agent.
  let sessionById = $derived.by(() => {
    const map = new Map();
    for (const s of fleetStore.sessions ?? []) {
      if (s?.id) map.set(s.id, s);
    }
    return map;
  });

  // Resolve the root group key for an agent. Traverses the session hierarchy
  // via root_session_id (preferred) or parent_session_id (walked up the chain)
  // and falls back to the agent's own session_id or agent_id so every row
  // always has a stable group key.
  function codexInfrastructureGroupKey(agent) {
    const type = `${agent?.agent_type ?? ''} ${agent?.agent_id ?? ''}`.toLowerCase();
    if (!type.includes('codex')) return '';
    const description = (agent?.description ?? '').toLowerCase();
    const isInfrastructureSession =
      description.includes('keepalive wrapper session') ||
      description.includes('heartbeat bootstrap session');
    if (!isInfrastructureSession) return '';
    const namespace = agent?.namespace ?? '';
    return namespace ? `codex-infra:${namespace}` : '';
  }

  function groupKeyFor(agent) {
    const infraKey = codexInfrastructureGroupKey(agent);
    if (infraKey) return infraKey;

    const sid = agent?.session_id;
    if (!sid) return `agent:${agent?.agent_id ?? 'unknown'}`;
    const session = sessionById.get(sid);
    if (!session) return `session:${sid}`;
    if (session.root_session_id && session.root_session_id !== session.id) {
      return `session:${session.root_session_id}`;
    }
    // Walk parent chain defensively in case root_session_id isn't populated.
    let cursor = session;
    const seen = new Set();
    while (cursor?.parent_session_id && !seen.has(cursor.id)) {
      seen.add(cursor.id);
      const parent = sessionById.get(cursor.parent_session_id);
      if (!parent) return `session:${cursor.parent_session_id}`;
      cursor = parent;
    }
    return `session:${cursor?.id ?? sid}`;
  }

  // Group agents by root session so subagents cluster under their spawning
  // agent. Each group has a root agent (if still present in the presence
  // registry) plus an ordered list of children.
  let agentGroups = $derived.by(() => {
    const groups = new Map();
    for (const agent of sortedAgents) {
      const key = groupKeyFor(agent);
      if (!groups.has(key)) {
        groups.set(key, { key, root: null, children: [] });
      }
      const group = groups.get(key);
      const isRoot =
        key === `agent:${agent.agent_id}` ||
        (agent.session_id && key === `session:${agent.session_id}`);
      if (isRoot && !group.root) {
        group.root = agent;
      } else {
        group.children.push(agent);
      }
    }
    // If a group has children but no root in the presence list, promote the
    // first child so the cluster still has a header.
    const result = [];
    for (const group of groups.values()) {
      if (!group.root && group.children.length > 0) {
        group.root = group.children.shift();
      }
      if (group.root) result.push(group);
    }
    // Preserve sortedAgents order by sorting groups by their root's position.
    const rootIndex = new Map(sortedAgents.map((a, i) => [a.agent_id, i]));
    result.sort((a, b) => {
      const ai = rootIndex.get(a.root?.agent_id) ?? Infinity;
      const bi = rootIndex.get(b.root?.agent_id) ?? Infinity;
      return ai - bi;
    });
    return result;
  });

  // Flatten groups back to a row-ordered list with a depth marker so the
  // table view can indent subagents under their root. The synthetic `id`
  // field is what DataTable keys rows by.
  let flatGroupedAgents = $derived.by(() => {
    const rows = [];
    for (const group of agentGroups) {
      if (group.root) {
        rows.push({ id: group.root.agent_id, agent: group.root, depth: 0, groupKey: group.key });
      }
      for (const child of group.children) {
        rows.push({ id: child.agent_id, agent: child, depth: 1, groupKey: group.key });
      }
    }
    return rows;
  });

  function presenceStatus(status) {
    const map = {
      active: 'healthy',
      idle: 'degraded',
      offline: 'down',
    };
    return map[status] ?? 'down';
  }

  const agentColumns = [
    { key: 'agent_id', label: 'Agent' },
    { key: 'status', label: 'Status', width: '100px' },
    { key: 'agent_type', label: 'Type', width: '90px' },
    { key: 'current_task', label: 'Current Task' },
    { key: 'branch', label: 'Branch / PR', width: '120px' },
    { key: 'last_heartbeat', label: 'Heartbeat', width: '90px' },
    { key: 'actions', label: 'Actions', width: '200px' },
  ];
</script>

{#if agentView === 'cards'}
  {#if hiddenOfflineCount > 0}
    <div class="status-banner">
      <span class="status-banner-label">Showing live agents</span>
      <span class="status-banner-copy">{hiddenOfflineCount} offline {hiddenOfflineCount === 1 ? 'entry is' : 'entries are'} hidden to keep this view focused.</span>
    </div>
  {:else if showOfflineAgents && offlineCount > 0}
    <div class="status-banner status-banner-muted">
      <span class="status-banner-label">Showing all agents</span>
      <span class="status-banner-copy">Offline entries are included for lifecycle tracing.</span>
    </div>
  {/if}
  {#if branchCollisions.length > 0}
    <div class="conflict-banner">
      <span class="conflict-icon">⚠</span>
      <span>Branch collision: multiple agents on same branch</span>
      {#each branchCollisions as col}
        <div class="conflict-detail">
          <span class="text-mono text-xs">{col.branch}</span>
          <span class="text-muted text-xs">→ {col.agents.join(', ')}</span>
        </div>
      {/each}
    </div>
  {/if}
  <div class="cards-grid">
    {#each agentGroups as group (group.key)}
      <div class="agent-group" class:has-children={group.children.length > 0}>
        {#if group.root}
          <AgentCard
            agent={group.root}
            heartbeatData={heartbeatDataMap.get(group.root.agent_id) ?? []}
            sharedFileAgents={agentOverlaps.get(group.root.agent_id) ?? []}
            ondispatch={onOpenDispatch}
            onnudge={onOpenNudge}
          />
        {/if}
        {#if group.children.length > 0}
          <div class="subagent-list">
            <div class="subagent-header">
              <span class="subagent-rail"></span>
              <span class="subagent-label">
                {group.children.length} subagent{group.children.length === 1 ? '' : 's'}
              </span>
            </div>
            {#each group.children as child (child.agent_id)}
              <div class="subagent-card">
                <AgentCard
                  agent={child}
                  heartbeatData={heartbeatDataMap.get(child.agent_id) ?? []}
                  sharedFileAgents={agentOverlaps.get(child.agent_id) ?? []}
                  ondispatch={onOpenDispatch}
                  onnudge={onOpenNudge}
                />
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {:else}
      <EmptyState icon={'\u25A3'} heading="No registered agents" compact />
    {/each}
  </div>
{:else}
  <div class="presence-grid">
    <div class="card agents-card">
      <div class="card-header">
        <span class="card-title">Agent Presence</span>
        <span class="count-badge">{sortedAgents.length}</span>
      </div>
      {#if hiddenOfflineCount > 0}
        <div class="status-banner">
          <span class="status-banner-label">Live focus</span>
          <span class="status-banner-copy">{hiddenOfflineCount} offline {hiddenOfflineCount === 1 ? 'entry is' : 'entries are'} hidden from the table.</span>
        </div>
      {:else if showOfflineAgents && offlineCount > 0}
        <div class="status-banner status-banner-muted">
          <span class="status-banner-label">Lifecycle view</span>
          <span class="status-banner-copy">Offline agents stay visible here for cleanup and churn debugging.</span>
        </div>
      {/if}
      {#if branchCollisions.length > 0}
        <div class="conflict-banner">
          <span class="conflict-icon">⚠</span>
          <span>Branch collision: multiple agents on same branch</span>
          {#each branchCollisions as col}
            <div class="conflict-detail">
              <span class="text-mono text-xs">{col.branch}</span>
              <span class="text-muted text-xs">→ {col.agents.join(', ')}</span>
            </div>
          {/each}
        </div>
      {/if}
      {#if flatGroupedAgents.length === 0}
        <EmptyState icon={'\u25A3'} heading="No registered agents" compact />
      {:else}
        <DataTable
          columns={agentColumns}
          rows={flatGroupedAgents}
          idKey="id"
        >
          {#snippet row({ row })}
            {@const agent = row.agent}
            <td class="text-mono" class:subagent-row={row.depth > 0}>
              {#if row.depth > 0}
                <span class="subagent-indent" aria-hidden="true">└─</span>
              {/if}
              <div>{agent.agent_id}</div>
              <div class="agent-evidence">
                <span class="agent-evidence-chip" class:evidence-on={agent.has_presence}>presence</span>
                <span class="agent-evidence-chip" class:evidence-on={agent.has_session}>session</span>
                {#if agent.has_spawn}
                  <span class="agent-evidence-chip evidence-on">spawn</span>
                {/if}
              </div>
            </td>
            <td>
              <StatusDot status={presenceStatus(agent.status)} />
              <span class="status-label">{agent.status}</span>
            </td>
            <td>
              <span class="agent-type-chip" style:color={agentColor(agent.agent_type)}>
                {agent.agent_type || '---'}
              </span>
            </td>
            <td class="truncate" title={agent.current_task}>{agent.current_task || '---'}</td>
            <td class="text-mono text-muted">
              {#if agent.pr_url}
                <a href={agent.pr_url} target="_blank" rel="noopener" class="pr-link" title={agent.pr_url}>
                  PR
                </a>
              {/if}
              {agent.branch || '---'}
            </td>
            <td class="text-mono text-muted" title={formatTime(agent.last_heartbeat)}>{reactiveRelativeTime(agent.last_heartbeat)}</td>
            <td class="actions-cell">
              {#if agent.session_id}
                <button class="btn btn-xs btn-ghost" onclick={() => router.navigate('agents', 'fleet', agent.session_id)} title="Open session detail">
                  Session
                </button>
              {/if}
              <button class="btn btn-xs btn-ghost" onclick={() => router.navigate('activity', 'traces', agent.agent_id)} title="Open trace view for agent">
                Traces
              </button>
              {#if agent.status === 'active'}
                <button class="btn btn-xs btn-nudge" onclick={() => onOpenNudge(agent.agent_id)} title="Send nudge to agent">
                  Nudge
                </button>
                <button class="btn btn-xs btn-dispatch" onclick={() => onOpenDispatch(agent.agent_id)} title="Dispatch task to agent">
                  Dispatch
                </button>
              {/if}
            </td>
          {/snippet}
        </DataTable>
      {/if}
    </div>

    <div class="stats-grid">
      <div class="stat-card" style="--accent-color: var(--success)">
        <div class="metric-value">{activeCount}</div>
        <div class="metric-label">Active</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--warning)">
        <div class="metric-value">{idleCount}</div>
        <div class="metric-label">Idle</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--error)">
        <div class="metric-value">{offlineCount}</div>
        <div class="metric-label">Offline</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--info)">
        <div class="metric-value">{claims.length}</div>
        <div class="metric-label">File Claims</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--accent)">
        <div class="metric-value">{worktrees.length}</div>
        <div class="metric-label">Worktrees</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--tier-short)">
        <div class="metric-value">{claimedFilesCount}</div>
        <div class="metric-label">Claimed Files</div>
      </div>
    </div>
  </div>
{/if}

<style>
  .cards-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: 12px;
    padding: 4px 0;
  }

  .agent-group {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .agent-group.has-children {
    background: color-mix(in srgb, var(--accent) 4%, transparent);
    border: 1px solid color-mix(in srgb, var(--accent) 16%, var(--border));
    border-radius: var(--border-radius);
    padding: 8px;
  }

  .subagent-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding-left: 14px;
    position: relative;
  }

  .subagent-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 2px 0 4px;
  }

  .subagent-rail {
    width: 10px;
    height: 1px;
    background: color-mix(in srgb, var(--accent) 30%, transparent);
  }

  .subagent-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .subagent-card {
    position: relative;
    padding-left: 14px;
  }

  .subagent-card::before {
    content: '';
    position: absolute;
    left: 0;
    top: 16px;
    width: 10px;
    height: 1px;
    background: color-mix(in srgb, var(--accent) 30%, transparent);
  }

  .subagent-card::after {
    content: '';
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 1px;
    background: color-mix(in srgb, var(--accent) 20%, transparent);
  }

  .subagent-card:last-child::after {
    bottom: auto;
    height: 17px;
  }

  .subagent-indent {
    display: inline-block;
    margin-right: 6px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    opacity: 0.7;
  }

  td.subagent-row {
    padding-left: 18px;
  }

  .agent-evidence {
    display: flex;
    gap: 4px;
    margin-top: 4px;
    flex-wrap: wrap;
  }

  .agent-evidence-chip {
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 1px 5px;
    font-size: 9px;
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-family: var(--font-mono);
  }

  .agent-evidence-chip.evidence-on {
    border-color: color-mix(in srgb, var(--accent) 30%, var(--border));
    background: color-mix(in srgb, var(--accent) 8%, transparent);
    color: var(--fg-secondary);
  }

  .presence-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
    height: 100%;
  }

  .agents-card {
    min-height: 200px;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: var(--radius-lg);
  }

  .stats-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-template-rows: 1fr 1fr 1fr;
    gap: 12px;
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 14px 16px;
    border-left: 3px solid var(--accent-color, var(--info));
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .stat-card .metric-value {
    font-size: 22px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .stat-card .metric-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    margin-top: 4px;
  }

  .conflict-banner {
    background: rgba(231, 179, 18, 0.08);
    border: 1px solid rgba(231, 179, 18, 0.2);
    border-radius: var(--border-radius);
    padding: 10px 14px;
    margin: 8px 0;
    font-size: 12px;
    color: var(--warning);
  }

  .status-banner {
    display: flex;
    align-items: baseline;
    gap: 10px;
    padding: 10px 14px;
    margin: 8px 0;
    border-radius: var(--border-radius);
    border: 1px solid color-mix(in srgb, var(--accent) 20%, var(--border));
    background: color-mix(in srgb, var(--accent) 8%, transparent);
  }

  .status-banner-muted {
    border-color: color-mix(in srgb, var(--fg-muted) 18%, var(--border));
    background: color-mix(in srgb, var(--fg-muted) 8%, transparent);
  }

  .status-banner-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--accent);
    font-weight: 700;
    white-space: nowrap;
  }

  .status-banner-muted .status-banner-label {
    color: var(--fg-secondary);
  }

  .status-banner-copy {
    font-size: 12px;
    color: var(--fg-secondary);
  }

  .conflict-icon {
    font-size: 14px;
    margin-right: 4px;
  }

  .conflict-detail {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 3px 0 3px 20px;
  }

  .agent-type-chip {
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 500;
  }

  .status-label {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    margin-left: 4px;
    text-transform: uppercase;
  }

  .pr-link {
    display: inline-block;
    font-size: 10px;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    background: rgba(129, 240, 254, 0.1);
    color: var(--accent);
    text-decoration: none;
    margin-right: 4px;
    border: 1px solid rgba(129, 240, 254, 0.2);
  }

  .pr-link:hover {
    background: rgba(129, 240, 254, 0.2);
    text-decoration: none;
  }

  .actions-cell {
    display: flex;
    gap: 4px;
  }

  .btn-xs {
    padding: 2px 8px;
    font-size: 11px;
  }

  .btn-nudge {
    background: rgba(231, 179, 18, 0.1);
    color: var(--warning);
    border: 1px solid rgba(231, 179, 18, 0.25);
  }

  .btn-nudge:hover {
    background: rgba(231, 179, 18, 0.2);
  }

  .btn-dispatch {
    background: rgba(129, 240, 254, 0.1);
    color: var(--accent);
    border: 1px solid rgba(129, 240, 254, 0.25);
  }

  .btn-dispatch:hover {
    background: rgba(129, 240, 254, 0.2);
  }
</style>
