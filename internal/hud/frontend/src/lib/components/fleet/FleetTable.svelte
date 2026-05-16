<script>
  /**
   * FleetTable — agent fleet table with grouping toggle. Pure presentational
   * shell on top of shared/DataTable; consumes pre-built FleetRow[] from
   * lib/utils/fleetRows.ts and the fleet store sort/group state.
   *
   * @type {{
   *   rows: import('../../utils/fleetRows.ts').FleetRow[],
   *   loading: boolean,
   *   ungroupedStartIndex: number,
   *   ungroupedCount: number,
   *   spawnByAgentId: Map<string, import('../../stores/spawn.svelte.ts').SpawnState>,
   *   expiringClaims: Map<string, string[]>,
   *   onRowClick: (row: import('../../utils/fleetRows.ts').FleetRow) => void,
   *   onSessionClick: (sessionId: string) => void,
   *   onTraceClick: (agentId: string) => void,
   *   onSpawnClick: (e: Event, spawnId: string) => void,
   * }}
   */
  let {
    rows,
    loading,
    ungroupedStartIndex,
    ungroupedCount,
    spawnByAgentId,
    expiringClaims,
    onRowClick,
    onSessionClick,
    onTraceClick,
    onSpawnClick,
  } = $props();

  import { fleetStore } from '../../stores/fleet.svelte.ts';
  import { formatTime, relativeTime, sanitizeText, inferAgentType } from '../../utils/format.ts';
  import { VIRTUAL_SCROLL_THRESHOLD } from '../../utils/tokens.ts';
  import StatusDot from '../../widgets/StatusDot.svelte';
  import DataTable from '../shared/DataTable.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  const columns = [
    { key: 'agent', label: 'Agent', sortable: true, width: '200px' },
    { key: 'status', label: 'Status', sortable: true, width: '70px' },
    { key: 'evidence', label: 'Evidence', sortable: true, width: '110px' },
    { key: 'namespace', label: 'Namespace', sortable: true, width: '180px' },
    { key: 'activity', label: 'Activity', sortable: false, width: '220px' },
    { key: 'heartbeat', label: 'Heartbeat', sortable: true, width: '90px' },
    { key: 'actions', label: 'Actions', sortable: false, width: '190px' },
  ];

  function unifiedAgentStatus(agent) {
    if (agent.status === 'active') return 'healthy';
    if (agent.status === 'idle') return 'degraded';
    return 'down';
  }

  function sessionLabel(session) {
    if (!session) return 'unknown';
    return sanitizeText(session.agent || session.agent_id || session.id.slice(0, 8));
  }
</script>

<div class="card fleet-table-card">
  <div class="card-header">
    <span class="card-title">Live Agents</span>
    <div class="card-header-tools">
      <button
        class="header-toggle"
        class:header-toggle-active={fleetStore.groupByRootSession}
        onclick={() => fleetStore.toggleGrouping()}
        title={fleetStore.groupByRootSession ? 'Show hierarchy grouped by root session' : 'Show a flat agent list'}
      >
        {fleetStore.groupByRootSession ? 'Grouped by root' : 'Flat list'}
      </button>
      <span class="count-badge">{rows.length}</span>
    </div>
  </div>
  {#if rows.length === 0 && fleetStore.lastUpdated}
    <EmptyState icon={'◈'} heading="No active agents" compact />
  {:else}
    <DataTable
      {columns}
      {rows}
      sortKey={fleetStore.sortKey}
      sortDir={fleetStore.sortDir}
      rowLabel="agent"
      stableLayout={true}
      {loading}
      skeletonRows={4}
      maxRows={VIRTUAL_SCROLL_THRESHOLD}
      onSort={(key, dir) => fleetStore.setSort(key, dir)}
      onRowClick={(row) => onRowClick(row)}
    >
      {#snippet row({ row, index })}
        {@const agent = row.agent}
        {@const linkedSpawn = spawnByAgentId.get(agent.agent_id)}
        {@const showUngroupedDivider = fleetStore.groupByRootSession && row.ungrouped && index === ungroupedStartIndex && ungroupedStartIndex > 0}
        <td class="text-mono agent-cell" class:subagent-row={row.depth > 0} class:ungrouped-divider={showUngroupedDivider} title={sanitizeText(agent.agent_id ?? '---')}>
          {#if showUngroupedDivider}
            <span class="ungrouped-label" aria-hidden="true">No active session match{ungroupedCount > 1 ? ` · ${ungroupedCount}` : ''}</span>
          {/if}
          {#if fleetStore.groupByRootSession && row.depth > 0}
            <span class="subagent-indent" aria-hidden="true">└─</span>
          {/if}
          {sanitizeText(agent.agent_id ?? '---')}
          {#if linkedSpawn}
            <button
              class="spawn-link-icon"
              title="Spawned agent — click to view spawn detail"
              onclick={(e) => onSpawnClick(e, linkedSpawn.spawn_id)}
            >{'⬢'}</button>
          {/if}
          {#if expiringClaims.has(agent.agent_id)}
            <span class="expiring-icon" title={`Expiring: ${expiringClaims.get(agent.agent_id).join(', ')}`}>{'⏰'}</span>
          {/if}
          <div class="agent-meta-row">
            <span>{inferAgentType(agent.agent_id, agent.agent_type)}</span>
            <span>{agent.source}</span>
          </div>
          {#if row.session}
            <div class="agent-hierarchy-row">
              {#if row.parentSession}
                <span class="hierarchy-pill hierarchy-pill-child">child of {sessionLabel(row.parentSession)}</span>
              {:else if row.rootSession?.id === row.session.id}
                <span class="hierarchy-pill hierarchy-pill-root">root session</span>
              {/if}
              {#if row.rootSession && row.rootSession.id !== row.session.id}
                <span class="hierarchy-pill">root {sessionLabel(row.rootSession)}</span>
              {/if}
              {#if row.totalChildCount > 0}
                <span class="hierarchy-pill">{row.liveChildCount}/{row.totalChildCount} child{row.totalChildCount === 1 ? '' : 'ren'}</span>
              {/if}
            </div>
          {/if}
        </td>
        <td class:ungrouped-divider={showUngroupedDivider}>
          <StatusDot status={unifiedAgentStatus(agent)} />
        </td>
        <td class="evidence-cell" class:ungrouped-divider={showUngroupedDivider}>
          <span class="evidence-pill" class:evidence-pill-active={agent.has_presence}>presence</span>
          <span class="evidence-pill" class:evidence-pill-active={agent.has_session}>session</span>
          {#if agent.has_spawn}
            <span class="evidence-pill evidence-pill-active">spawn</span>
          {/if}
          {#if agent.is_orphan}
            <span
              class="evidence-pill evidence-pill-orphan"
              title={`Heartbeating without an active session for ${Math.round(agent.orphan_age_seconds / 60)}m. Auto-reaped at 10m.`}
            >orphan</span>
          {/if}
        </td>
        <td class="text-mono text-muted namespace-cell" class:ungrouped-divider={showUngroupedDivider} title={sanitizeText(agent.namespace ?? agent.project ?? '---')}>
          {sanitizeText(agent.namespace ?? agent.project ?? '---')}
        </td>
        <td class="text-muted text-xs description-cell" class:ungrouped-divider={showUngroupedDivider} title={sanitizeText(agent.current_task || agent.description || '')}>
          {sanitizeText(agent.current_task || agent.description || '---')}
        </td>
        <td class="text-mono text-muted" class:ungrouped-divider={showUngroupedDivider} title={formatTime(agent.last_heartbeat || agent.session_started_at)}>
          {relativeTime(agent.last_heartbeat || agent.session_started_at)}
        </td>
        <td class="actions-cell" class:ungrouped-divider={showUngroupedDivider}>
          {#if agent.session_id}
            <button class="btn btn-xs btn-ghost" onclick={(e) => { e.stopPropagation(); onSessionClick(agent.session_id); }}>
              Session
            </button>
          {/if}
          <button class="btn btn-xs btn-ghost" onclick={(e) => { e.stopPropagation(); onTraceClick(agent.agent_id); }}>
            Traces
          </button>
        </td>
      {/snippet}
    </DataTable>
  {/if}
</div>

<style>
  .fleet-table-card {
    min-width: 0;
    min-height: 200px;
    /* U10: Allow the agent list to scroll so the "Showing X of Y" footer
       and overflowing rows stay reachable when the card is cropped. */
    max-height: 60vh;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    font-weight: 600;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 2px 8px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border-subtle);
  }

  .card-header-tools {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .header-toggle {
    border: 1px solid var(--border);
    background: var(--bg-tertiary);
    color: var(--fg-muted);
    border-radius: 999px;
    padding: 4px 10px;
    font-size: 10px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    cursor: pointer;
    transition: color var(--transition-fast), border-color var(--transition-fast), background var(--transition-fast);
  }

  .header-toggle:hover,
  .header-toggle-active {
    color: var(--fg-primary);
    border-color: color-mix(in srgb, var(--accent) 30%, var(--border));
    background: color-mix(in srgb, var(--accent) 8%, var(--bg-tertiary));
  }

  .agent-cell,
  .namespace-cell {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .agent-cell {
    position: relative;
    white-space: normal;
    word-break: break-word;
  }

  .namespace-cell {
    white-space: nowrap;
  }

  .agent-meta-row {
    display: flex;
    gap: 8px;
    margin-top: 4px;
    font-size: 10px;
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .agent-hierarchy-row {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    margin-top: 6px;
  }

  .hierarchy-pill {
    border: 1px solid color-mix(in srgb, var(--accent) 16%, var(--border));
    border-radius: 999px;
    padding: 1px 6px;
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--fg-dim);
    background: color-mix(in srgb, var(--accent) 6%, transparent);
    white-space: nowrap;
  }

  .hierarchy-pill-root {
    color: var(--fg-secondary);
  }

  .hierarchy-pill-child {
    border-color: color-mix(in srgb, var(--info) 24%, var(--border));
    background: color-mix(in srgb, var(--info) 8%, transparent);
  }

  .subagent-row {
    padding-left: 18px;
  }

  .subagent-indent {
    position: absolute;
    left: 0;
    top: 2px;
    color: var(--fg-dim);
    font-size: 11px;
  }

  /* Visual separator above the first ungrouped agent row, applied to every
     cell of that row so the divider spans the full table width. The label
     sits below the dashed border (inside the agent cell), giving the row
     a clear two-band header instead of a floating chip that overlapped
     the previous row. */
  .ungrouped-divider {
    border-top: 1px dashed color-mix(in srgb, var(--warning) 40%, var(--border)) !important;
    padding-top: var(--space-3);
  }

  .ungrouped-label {
    display: block;
    width: fit-content;
    margin-bottom: 6px;
    font-size: 9px;
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--warning);
    background: color-mix(in srgb, var(--warning) 10%, transparent);
    border: 1px solid color-mix(in srgb, var(--warning) 30%, var(--border));
    padding: 1px 6px;
    border-radius: var(--radius-sm);
    white-space: nowrap;
  }

  .evidence-cell {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
  }

  .evidence-pill {
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 1px 6px;
    font-size: 9px;
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-family: var(--font-mono);
  }

  .evidence-pill.evidence-pill-active {
    border-color: color-mix(in srgb, var(--accent) 30%, var(--border));
    background: color-mix(in srgb, var(--accent) 8%, transparent);
    color: var(--fg-secondary);
  }

  .evidence-pill.evidence-pill-orphan {
    border-color: color-mix(in srgb, var(--warning) 40%, var(--border));
    background: color-mix(in srgb, var(--warning) 12%, transparent);
    color: var(--warning);
  }

  .actions-cell {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }

  .spawn-link-icon {
    display: inline-flex;
    align-items: center;
    background: none;
    border: none;
    color: var(--accent);
    font-size: 10px;
    margin-left: 4px;
    padding: 0 2px;
    cursor: pointer;
    opacity: 0.7;
    transition: opacity var(--transition-fast);
  }

  .spawn-link-icon:hover {
    opacity: 1;
  }

  .expiring-icon {
    color: var(--warning);
    font-size: 12px;
    margin-left: 4px;
    cursor: help;
    animation: glowPulse 2s ease-in-out infinite;
  }

  .description-cell {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 180px;
  }
</style>
