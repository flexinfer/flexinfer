<script>
  import { timelineStore } from '../../stores/timeline.svelte.ts';
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
    { key: 'actions', label: 'Actions', width: '120px' },
  ];
</script>

{#if agentView === 'cards'}
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
    {#each agents as agent (agent.agent_id)}
      <AgentCard
        {agent}
        heartbeatData={heartbeatDataMap.get(agent.agent_id) ?? []}
        sharedFileAgents={agentOverlaps.get(agent.agent_id) ?? []}
        ondispatch={onOpenDispatch}
        onnudge={onOpenNudge}
      />
    {:else}
      <EmptyState icon={'\u25A3'} heading="No registered agents" compact />
    {/each}
  </div>
{:else}
  <div class="presence-grid">
    <div class="card agents-card">
      <div class="card-header">
        <span class="card-title">Agent Presence</span>
        <span class="count-badge">{activeCount + idleCount}</span>
      </div>
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
      {#if agents.length === 0}
        <EmptyState icon={'\u25A3'} heading="No registered agents" compact />
      {:else}
        <DataTable
          columns={agentColumns}
          rows={agents}
          idKey="agent_id"
        >
          {#snippet row({ row: agent })}
            <td class="text-mono">{agent.agent_id}</td>
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
