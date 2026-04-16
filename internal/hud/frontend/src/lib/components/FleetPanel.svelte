<script>
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { spawnStore } from '../stores/spawn.svelte.ts';
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { graphStore } from '../stores/graph.svelte.ts';
  import { streamStore } from '../stores/stream.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { traceStore } from '../stores/traces.svelte.ts';
  import { router } from '../stores/router.svelte.ts';
  import { formatTime, relativeTime, formatNumber, entryVariant, sanitizeText, inferAgentType } from '../utils/format.ts';
  import { formatTraceDuration, traceBreakdown, traceStatusVariant } from '../utils/traces.ts';
  import { VIRTUAL_SCROLL_THRESHOLD } from '../utils/tokens.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';
  import Gauge from '../widgets/Gauge.svelte';
  import DataTable from './shared/DataTable.svelte';
  import DetailDrawer from './shared/DetailDrawer.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  const fleetPollingOwner = Symbol('FleetPanel');
  const tracePollingOwner = Symbol('FleetPanelTraces');

  $effect(() => {
    fleetStore.startPolling(5000, fleetPollingOwner);
    traceStore.startPolling(15000, tracePollingOwner);
    taskStore.startPolling(5000);
    workflowStore.startPolling(10000);
    memoryStore.startPolling(10000);
    graphStore.startPolling(15000);
    streamStore.startPolling(3000);
    spawnStore.startPolling(15000);

    return () => {
      fleetStore.stopPolling(fleetPollingOwner);
      traceStore.stopPolling(tracePollingOwner);
      taskStore.stopPolling();
      workflowStore.stopPolling();
      memoryStore.stopPolling();
      graphStore.stopPolling();
      streamStore.stopPolling();
      spawnStore.stopPolling();
    };
  });

  // Drill-down: session detail view
  let detailSessionId = $derived(router.detail);
  let sessionEntries = $state([]);
  let sessionEvents = $state([]);
  let sessionTraceEntries = $state([]);
  let sessionTraceErrors = $state([]);
  let sessionTraceMeta = $state(null);
  let loadingEntries = $state(false);
  let drawerError = $derived(fleetStore.drawerError);

  async function loadSessionTrace(sessionId, limit = 100) {
    loadingEntries = true;
    try {
      const data = await fleetStore.fetchSessionTrace(sessionId, limit);
      sessionEntries = data?.entries ?? [];
      sessionEvents = data?.events ?? [];
      sessionTraceEntries = data?.traces ?? [];
      sessionTraceErrors = data?.errors ?? [];
      sessionTraceMeta = data;
    } finally {
      loadingEntries = false;
    }
  }

  function retrySessionEntries() {
    if (!detailSessionId) return;
    void loadSessionTrace(detailSessionId, 100);
  }

  // Fetch session entries when detail changes
  $effect(() => {
    if (detailSessionId) {
      void loadSessionTrace(detailSessionId, 100);
    } else {
      sessionEntries = [];
      sessionEvents = [];
      sessionTraceEntries = [];
      sessionTraceErrors = [];
      sessionTraceMeta = null;
      fleetStore.clearDrawerError();
    }
  });

  let detailSession = $derived(
    detailSessionId ? (fleetStore.sessions ?? []).find(s => s.id === detailSessionId) : null
  );
  let detailTraceEntries = $derived.by(() => {
    if (sessionTraceEntries.length > 0) return sessionTraceEntries.slice(0, 5);
    const agentId = (detailSession?.agent_id ?? '').trim();
    if (!agentId) return [];
    return (traceStore.entries ?? []).filter((entry) => (entry.agent_id ?? '') === agentId).slice(0, 5);
  });
  let detailAgent = $derived(detailSession ? agentLookup.get(detailSession.agent_id) : null);
  let detailLineage = $derived(detailSession ? fleetStore.sessionLineage(detailSession.id) : []);
  let detailChildren = $derived(detailSession ? fleetStore.childSessions(detailSession.id) : []);
  let detailParentSession = $derived(detailSession ? fleetStore.parentSession(detailSession.id) : null);
  let detailRootSession = $derived(detailSession ? fleetStore.rootSession(detailSession.id) : null);

  let fleetAgents = $derived(fleetStore.liveAgents ?? []);
  let sessions = $derived(fleetStore.sessions ?? []);
  let tasks = $derived(taskStore.tasks ?? []);
  let workflows = $derived(workflowStore.workflows ?? []);
  let memStats = $derived(memoryStore.stats ?? {});
  let graphStats = $derived(graphStore.stats ?? {});
  let entries = $derived(streamStore.entries ?? []);
  let traceError = $derived(traceStore.error);
  let traceLoading = $derived(traceStore.loading);

  let totalTokens = $derived(
    sessions.reduce((sum, s) => sum + (s.tokens_used ?? 0), 0)
  );

  let recentActivity = $derived(entries.slice(0, 10));

  // Agent lookup for enriching session rows with presence metadata.
  let agentLookup = $derived.by(() => {
    const map = new Map();
    for (const a of fleetAgents) {
      map.set(a.agent_id, a);
    }
    return map;
  });

  // Task priority distribution for stat card.
  let taskPriorityDist = $derived.by(() => {
    const dist = { critical: 0, high: 0, medium: 0, low: 0 };
    let blocked = 0;
    for (const t of tasks) {
      const p = t.priority ?? 'medium';
      if (p in dist) dist[p]++;
      if (t.status === 'blocked') blocked++;
    }
    return { ...dist, blocked };
  });

  // Graph entity type breakdown (top 3).
  let graphTopTypes = $derived.by(() => {
    const types = graphStats?.entity_types ?? {};
    return Object.entries(types)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 3);
  });

  // Infrastructure stats for Fleet overview
  let tunnelCount = $state(0);
  let cacheHitRate = $state(0);

  $effect(() => {
    async function loadInfra() {
      const [tunnels, cache] = await Promise.all([
        healthStore.fetchTunnels(),
        healthStore.fetchCacheStats(),
      ]);
      tunnelCount = tunnels.length;
      cacheHitRate = cache?.hit_rate ?? 0;
    }
    loadInfra();
    const timer = setInterval(loadInfra, 30000);
    return () => clearInterval(timer);
  });

  // Expiring claims: claims expiring within 5 minutes.
  let expiringClaims = $derived.by(() => {
    const map = new Map();
    const cutoff = Date.now() + 5 * 60 * 1000;
    for (const claim of fleetStore.fileClaims) {
      if (!claim.expires_at) continue;
      const exp = new Date(claim.expires_at).getTime();
      if (exp > Date.now() && exp <= cutoff) {
        const arr = map.get(claim.agent_id) ?? [];
        arr.push(claim.file_path);
        map.set(claim.agent_id, arr);
      }
    }
    return map;
  });

  let workingItems = $derived(memStats.working_memory?.items ?? 0);
  let workingTokens = $derived(memStats.working_memory?.tokens ?? 0);
  let workingMax = $derived(memStats.working_memory?.max_items ?? 100);
  let shortItems = $derived(memStats.short_term_memory?.items ?? 0);
  let shortTokens = $derived(memStats.short_term_memory?.tokens ?? 0);
  let shortMax = $derived(memStats.short_term_memory?.max_items ?? 500);
  let longItems = $derived(memStats.long_term_memory?.items ?? 0);
  let longTokens = $derived(memStats.long_term_memory?.tokens ?? 0);
  let longMax = $derived(memStats.long_term_memory?.max_items ?? 2000);

  // Sort state for fleet DataTable
  let fleetSortKey = $state('agent');
  let fleetSortDir = $state('asc');
  let groupByRootSession = $state(true);

  function handleFleetSort(key, dir) {
    fleetSortKey = key;
    fleetSortDir = dir;
  }

  const fleetColumns = [
    { key: 'agent', label: 'Agent', sortable: true, width: '130px' },
    { key: 'status', label: 'Status', sortable: true, width: '70px' },
    { key: 'evidence', label: 'Evidence', sortable: true, width: '110px' },
    { key: 'namespace', label: 'Namespace', sortable: true, width: '180px' },
    { key: 'activity', label: 'Activity', sortable: false, width: '220px' },
    { key: 'heartbeat', label: 'Heartbeat', sortable: true, width: '90px' },
    { key: 'actions', label: 'Actions', sortable: false, width: '190px' },
  ];

  // Activity feed columns (no sort — chronological)
  const activityColumns = [
    { key: 'time', label: 'Time', width: '70px' },
    { key: 'type', label: 'Type', width: '80px' },
    { key: 'agent', label: 'Agent', width: '90px' },
    { key: 'title', label: 'Title' },
  ];

  function navigateToSession(sessionId) {
    router.navigate('agents', 'fleet', sessionId);
  }

  function navigateToTrace(agentId) {
    router.navigate('activity', 'traces', agentId);
  }

  function openAgentDetail(agent) {
    if (agent.session_id) {
      navigateToSession(agent.session_id);
      return;
    }
    navigateToTrace(agent.agent_id);
  }

  function backToFleet() {
    router.back();
  }

  function unifiedAgentStatus(agent) {
    if (agent.status === 'active') return 'healthy';
    if (agent.status === 'idle') return 'degraded';
    return 'down';
  }

  function sessionLabel(session) {
    if (!session) return 'unknown';
    return sanitizeText(session.agent || session.agent_id || session.id.slice(0, 8));
  }

  function sessionMetaLabel(session) {
    if (!session) return '---';
    const state = session.active ? 'active' : sanitizeText(session.status || 'ended');
    return `${state} · ${relativeTime(session.started_at)}`;
  }

  function compareFleetAgents(left, right) {
    let cmp = 0;
    switch (fleetSortKey) {
      case 'agent':
        cmp = sanitizeText(left.agent_id ?? '').localeCompare(sanitizeText(right.agent_id ?? ''));
        break;
      case 'status': {
        const order = { active: 0, idle: 1, offline: 2 };
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
    return fleetSortDir === 'desc' ? -cmp : cmp;
  }

  // Cross-reference: build spawn lookup by agent_id for fleet rows.
  let spawnByAgentId = $derived.by(() => {
    const map = new Map();
    for (const s of spawnStore.spawns) {
      map.set(s.agent_id, s);
    }
    return map;
  });

  function navigateToSpawn(e, spawnId) {
    e.stopPropagation();
    router.navigate('sandbox', 'spawn', spawnId);
  }

  function buildFleetRow(agent, depth = 0) {
    const session = agent.session_id ? fleetStore.sessionById.get(agent.session_id) : null;
    const parentSession = session ? fleetStore.parentSession(session.id) : null;
    const rootSession = session ? fleetStore.rootSession(session.id) : null;
    const childSessions = session ? fleetStore.childSessions(session.id) : [];
    const lineage = session ? fleetStore.sessionLineage(session.id) : [];
    const liveChildCount = childSessions.filter((child) => agentLookup.has(child.agent_id)).length;
    return {
      id: agent.agent_id,
      agent,
      depth,
      session,
      parentSession,
      rootSession,
      childSessions,
      lineage,
      liveChildCount,
      totalChildCount: childSessions.length,
    };
  }

  function leadAgentForNode(node, agentBySessionId) {
    const direct = agentBySessionId.get(node.session.id);
    if (direct) return direct;
    for (const child of node.children ?? []) {
      const nested = leadAgentForNode(child, agentBySessionId);
      if (nested) return nested;
    }
    return null;
  }

  function flattenSessionNode(node, agentBySessionId, depth = 0) {
    const rows = [];
    const directAgent = agentBySessionId.get(node.session.id);
    if (directAgent) rows.push(buildFleetRow(directAgent, depth));
    const sortedChildren = [...(node.children ?? [])].sort((left, right) => {
      const leftLead = leadAgentForNode(left, agentBySessionId);
      const rightLead = leadAgentForNode(right, agentBySessionId);
      if (leftLead && rightLead) return compareFleetAgents(leftLead, rightLead);
      if (leftLead) return -1;
      if (rightLead) return 1;
      return new Date(left.session.started_at ?? 0).getTime() - new Date(right.session.started_at ?? 0).getTime();
    });
    for (const child of sortedChildren) {
      rows.push(...flattenSessionNode(child, agentBySessionId, depth + 1));
    }
    return rows;
  }

  let fleetRows = $derived.by(() => {
    const flatRows = [...fleetAgents].sort(compareFleetAgents).map((agent) => buildFleetRow(agent, 0));
    if (!groupByRootSession) return flatRows;

    const agentBySessionId = new Map();
    for (const agent of fleetAgents) {
      if (agent.session_id && fleetStore.sessionById.has(agent.session_id)) {
        agentBySessionId.set(agent.session_id, agent);
      }
    }

    const groupedRows = [];
    const seenAgents = new Set();
    const sortedRoots = [...fleetStore.sessionTree].sort((left, right) => {
      const leftLead = leadAgentForNode(left, agentBySessionId);
      const rightLead = leadAgentForNode(right, agentBySessionId);
      if (leftLead && rightLead) return compareFleetAgents(leftLead, rightLead);
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

    for (const row of flatRows) {
      if (!seenAgents.has(row.agent.agent_id)) groupedRows.push(row);
    }

    return groupedRows;
  });

  let rootGroupCount = $derived.by(() => {
    const groupKeys = new Set();
    for (const row of fleetRows) {
      const groupKey = row.rootSession?.id || row.session?.id || row.id;
      groupKeys.add(groupKey);
    }
    return groupKeys.size;
  });
</script>

<div class="panel fleet-panel">
  <!-- FLEET OVERVIEW (always visible) -->
  <div class="fleet-grid">
    <!-- LEFT TOP: Agent Fleet Table -->
    <div class="card fleet-table-card">
      <div class="card-header">
        <span class="card-title">Live Agents</span>
        <div class="card-header-tools">
          <button
            class="header-toggle"
            class:header-toggle-active={groupByRootSession}
            onclick={() => {
              groupByRootSession = !groupByRootSession;
            }}
            title={groupByRootSession ? 'Show hierarchy grouped by root session' : 'Show a flat agent list'}
          >
            {groupByRootSession ? 'Grouped by root' : 'Flat list'}
          </button>
          <span class="count-badge">{fleetAgents.length}</span>
        </div>
      </div>
      {#if fleetAgents.length === 0 && fleetStore.lastUpdated}
        <EmptyState icon={'\u25C8'} heading="No active agents" compact />
      {:else}
        <DataTable
          columns={fleetColumns}
          rows={fleetRows}
          sortKey={fleetSortKey}
          sortDir={fleetSortDir}
          rowLabel="agent"
          stableLayout={true}
          loading={!fleetStore.lastUpdated}
          skeletonRows={4}
          maxRows={VIRTUAL_SCROLL_THRESHOLD}
          onSort={handleFleetSort}
          onRowClick={(row) => openAgentDetail(row)}
        >
          {#snippet row({ row })}
            {@const agent = row.agent}
            {@const linkedSpawn = spawnByAgentId.get(agent.agent_id)}
            <td class="text-mono agent-cell" class:subagent-row={row.depth > 0} title={sanitizeText(agent.agent_id ?? '---')}>
              {#if groupByRootSession && row.depth > 0}
                <span class="subagent-indent" aria-hidden="true">└─</span>
              {/if}
              {sanitizeText(agent.agent_id ?? '---')}
              {#if linkedSpawn}
                <button
                  class="spawn-link-icon"
                  title="Spawned agent — click to view spawn detail"
                  onclick={(e) => navigateToSpawn(e, linkedSpawn.spawn_id)}
                >{'\u2B22'}</button>
              {/if}
              {#if expiringClaims.has(agent.agent_id)}
                <span class="expiring-icon" title={`Expiring: ${expiringClaims.get(agent.agent_id).join(', ')}`}>{'\u23F0'}</span>
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
            <td>
              <StatusDot status={unifiedAgentStatus(agent)} />
            </td>
            <td class="evidence-cell">
              <span class="evidence-pill" class:evidence-pill-active={agent.has_presence}>presence</span>
              <span class="evidence-pill" class:evidence-pill-active={agent.has_session}>session</span>
              {#if agent.has_spawn}
                <span class="evidence-pill evidence-pill-active">spawn</span>
              {/if}
            </td>
            <td class="text-mono text-muted namespace-cell" title={sanitizeText(agent.namespace ?? agent.project ?? '---')}>
              {sanitizeText(agent.namespace ?? agent.project ?? '---')}
            </td>
            <td class="text-muted text-xs description-cell" title={sanitizeText(agent.current_task || agent.description || '')}>
              {sanitizeText(agent.current_task || agent.description || '---')}
            </td>
            <td class="text-mono text-muted" title={formatTime(agent.last_heartbeat || agent.session_started_at)}>
              {relativeTime(agent.last_heartbeat || agent.session_started_at)}
            </td>
            <td class="actions-cell">
              {#if agent.session_id}
                <button class="btn btn-xs btn-ghost" onclick={(e) => { e.stopPropagation(); navigateToSession(agent.session_id); }}>
                  Session
                </button>
              {/if}
              <button class="btn btn-xs btn-ghost" onclick={(e) => { e.stopPropagation(); navigateToTrace(agent.agent_id); }}>
                Traces
              </button>
            </td>
          {/snippet}
        </DataTable>
      {/if}
    </div>

    <!-- RIGHT TOP: Quick Stats -->
    <div class="stats-grid">
      <div class="stat-card" style="--accent-color: var(--info)">
        {#key sessions.length}<div class="metric-value data-updated">{sessions.length}</div>{/key}
        <div class="metric-label">Sessions</div>
        {#if fleetAgents.length > sessions.length}
          <div class="metric-sub">{fleetAgents.length - sessions.length} live without session</div>
        {:else if groupByRootSession}
          <div class="metric-sub">{rootGroupCount} root group{rootGroupCount === 1 ? '' : 's'}</div>
          {/if}
      </div>
      <div class="stat-card" style="--accent-color: var(--warning)">
        {#key tasks.length}<div class="metric-value data-updated">{tasks.length}</div>{/key}
        <div class="metric-label">Tasks</div>
        {#if tasks.length > 0}
          <div class="metric-sub">
            {#if taskPriorityDist.critical > 0}<span class="priority-crit">{taskPriorityDist.critical}c</span>{/if}
            {#if taskPriorityDist.high > 0}<span class="priority-high">{taskPriorityDist.high}h</span>{/if}
            {#if taskPriorityDist.blocked > 0}<span class="priority-blocked">{taskPriorityDist.blocked} blocked</span>{/if}
          </div>
        {/if}
      </div>
      <div class="stat-card" style="--accent-color: var(--accent)">
        {#key totalTokens}<div class="metric-value data-updated">{formatNumber(totalTokens)}</div>{/key}
        <div class="metric-label">Tokens</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--success)">
        {#key workflows.length}<div class="metric-value data-updated">{workflows.length}</div>{/key}
        <div class="metric-label">Workflows</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--tier-short)">
        {#key workingItems + shortItems + longItems}<div class="metric-value data-updated">{formatNumber(workingItems + shortItems + longItems)}</div>{/key}
        <div class="metric-label">Memory Items</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--tier-long)">
        {#key graphStats.total_entities}<div class="metric-value data-updated">{formatNumber(graphStats.total_entities ?? 0)}</div>{/key}
        <div class="metric-label">Graph Entities</div>
        {#if graphTopTypes.length > 0}
          <div class="metric-sub">{graphTopTypes.map(([t, c]) => `${t}:${c}`).join(' · ')}</div>
        {/if}
      </div>
      <div class="stat-card" style="--accent-color: var(--fg-muted)">
        {#key tunnelCount + cacheHitRate}
          <div class="metric-value data-updated">
            {#if tunnelCount > 0 || cacheHitRate > 0}
              {tunnelCount} <span class="metric-unit">tunnels</span> · {(cacheHitRate * 100).toFixed(0)}%
            {:else}
              <span class="metric-unit">no data</span>
            {/if}
          </div>
        {/key}
        <div class="metric-label">Infrastructure</div>
      </div>
    </div>

    <!-- LEFT BOTTOM: Recent Activity -->
    <div class="card activity-card">
      <div class="card-header">
        <span class="card-title">Recent Activity</span>
        <span class="count-badge">{recentActivity.length}</span>
      </div>
      {#if recentActivity.length === 0}
        <EmptyState icon={'\u25CB'} heading="No recent activity" compact />
      {:else}
        <DataTable
          columns={activityColumns}
          rows={recentActivity}
          stableLayout={true}
          idKey="id"
        >
          {#snippet row({ row: entry })}
            <td class="activity-time text-mono">{formatTime(entry.timestamp)}</td>
            <td><Badge text={sanitizeText(entry.entry_type ?? 'note')} variant={entryVariant(entry.entry_type)} /></td>
            <td class="activity-agent text-mono" title={sanitizeText(entry.agent ?? '---')}>{sanitizeText(entry.agent ?? '---')}</td>
            <td class="activity-title truncate" title={sanitizeText(entry.title ?? entry.content?.slice(0, 120) ?? '---')}>
              {sanitizeText(entry.title ?? entry.content?.slice(0, 60) ?? '---')}
            </td>
          {/snippet}
        </DataTable>
      {/if}
    </div>

    <!-- RIGHT BOTTOM: Memory Tier Gauges -->
    <div class="card memory-gauges-card">
      <div class="card-header">
        <span class="card-title">Memory Tiers</span>
      </div>
      <div class="gauges-container">
        <div class="gauge-item">
          <Gauge
            value={workingItems}
            max={workingMax}
            label="Working"
            color="var(--tier-working)"
            showPercentage={true}
          />
          <div class="gauge-detail text-mono text-xs">
            {formatNumber(workingTokens)} tokens
          </div>
        </div>
        <div class="gauge-item">
          <Gauge
            value={shortItems}
            max={shortMax}
            label="Short-Term"
            color="var(--tier-short)"
            showPercentage={true}
          />
          <div class="gauge-detail text-mono text-xs">
            {formatNumber(shortTokens)} tokens
          </div>
        </div>
        <div class="gauge-item">
          <Gauge
            value={longItems}
            max={longMax}
            label="Long-Term"
            color="var(--tier-long)"
            showPercentage={true}
          />
          <div class="gauge-detail text-mono text-xs">
            {formatNumber(longTokens)} tokens
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- SESSION DETAIL DRAWER -->
  <DetailDrawer
    open={!!detailSessionId}
    title={sanitizeText(detailSession?.agent ?? detailSessionId?.slice(0, 12) ?? '')}
    subtitle={sanitizeText(detailSession?.namespace ?? '')}
    onClose={backToFleet}
  >
    {#snippet header()}
      {#if detailSession}
        <div class="detail-stats">
          <div class="stat-chip">
            <span class="stat-chip-value">{detailSession.entry_count ?? 0}</span>
            <span class="stat-chip-label">entries</span>
          </div>
          <div class="stat-chip">
            <span class="stat-chip-value">{detailSession.task_count ?? 0}</span>
            <span class="stat-chip-label">tasks</span>
          </div>
          <div class="stat-chip">
            <span class="stat-chip-value">{formatNumber(detailSession.tokens_used ?? 0)}</span>
            <span class="stat-chip-label">tokens</span>
          </div>
          <div class="stat-chip">
            <span class="stat-chip-value">{detailSession.memory_items ?? 0}</span>
            <span class="stat-chip-label">memory</span>
          </div>
          <div class="stat-chip">
            <span class="stat-chip-value">{relativeTime(detailSession.started_at)}</span>
            <span class="stat-chip-label">started</span>
          </div>
          {#if inferAgentType(detailSession.agent_id)}
            <div class="stat-chip">
              <span class="stat-chip-value">{inferAgentType(detailSession.agent_id)}</span>
              <span class="stat-chip-label">type</span>
            </div>
          {/if}
          {#if detailAgent?.current_task}
            <div class="stat-chip">
              <span class="stat-chip-value">{detailAgent.current_task}</span>
              <span class="stat-chip-label">current task</span>
            </div>
          {/if}
          {#if detailAgent?.pr_url}
            <div class="stat-chip">
              <a href={detailAgent.pr_url} target="_blank" rel="noopener" class="stat-chip-value pr-link">PR</a>
              <span class="stat-chip-label">pull request</span>
            </div>
          {/if}
          {#if detailAgent?.agent_id}
            <div class="stat-chip spawn-chip">
              <button class="stat-chip-value spawn-chip-link" onclick={() => navigateToTrace(detailAgent.agent_id)}>
                {'\u25A6'} Traces
              </button>
              <span class="stat-chip-label">agent trace view</span>
            </div>
          {/if}
          {#if detailSession && spawnByAgentId.has(detailSession.agent_id)}
            {@const detailSpawn = spawnByAgentId.get(detailSession.agent_id)}
            <div class="stat-chip spawn-chip">
              <button class="stat-chip-value spawn-chip-link" onclick={(e) => navigateToSpawn(e, detailSpawn.spawn_id)}>
                {'\u2B22'} Spawn
              </button>
              <span class="stat-chip-label">{detailSpawn.status}</span>
            </div>
          {/if}
        </div>
        {#if detailSession.description}
          <div class="detail-description text-sm text-secondary">{sanitizeText(detailSession.description)}</div>
        {/if}
        {#if sessionTraceErrors.length > 0}
          <div class="trace-health-banner">
            <span class="trace-health-label">Partial trace</span>
            <span class="trace-health-copy">
              {sessionTraceErrors.map((err) => `${sanitizeText(err.source)}: ${sanitizeText(err.message)}`).join(' · ')}
            </span>
          </div>
        {/if}
        {#if detailLineage.length > 0}
          <div class="hierarchy-section">
            <div class="section-header">
              <span class="section-title">Session Path</span>
              <span class="text-mono text-xs text-muted">{detailLineage.length} level{detailLineage.length === 1 ? '' : 's'}</span>
            </div>
            <div class="session-breadcrumbs">
              {#each detailLineage as lineageSession, index (lineageSession.id)}
                <button
                  class="session-crumb"
                  class:session-crumb-current={detailSession && lineageSession.id === detailSession.id}
                  onclick={() => navigateToSession(lineageSession.id)}
                  title={lineageSession.namespace || lineageSession.id}
                >
                  {sessionLabel(lineageSession)}
                </button>
                {#if index < detailLineage.length - 1}
                  <span class="session-crumb-sep">›</span>
                {/if}
              {/each}
            </div>
          </div>
        {/if}
        {#if detailParentSession || detailRootSession}
          <div class="hierarchy-section hierarchy-section-inline">
            {#if detailRootSession}
              <button class="session-link-chip" onclick={() => navigateToSession(detailRootSession.id)}>
                Root · {sessionLabel(detailRootSession)}
              </button>
            {/if}
            {#if detailParentSession}
              <button class="session-link-chip" onclick={() => navigateToSession(detailParentSession.id)}>
                Parent · {sessionLabel(detailParentSession)}
              </button>
            {/if}
          </div>
        {/if}
        {#if detailChildren.length > 0}
          <div class="hierarchy-section">
            <div class="section-header">
              <span class="section-title">Child Sessions</span>
              <span class="text-mono text-xs text-muted">{detailChildren.length}</span>
            </div>
            <div class="child-session-list">
              {#each detailChildren as childSession (childSession.id)}
                <button class="child-session-chip" onclick={() => navigateToSession(childSession.id)}>
                  <span class="child-session-name">{sessionLabel(childSession)}</span>
                  <span class="child-session-meta">{sessionMetaLabel(childSession)}</span>
                </button>
              {/each}
            </div>
          </div>
        {/if}
        {#if detailSession?.agent_id}
          <div class="hierarchy-section">
            <div class="section-header">
              <span class="section-title">Recent Traces</span>
              <div class="section-header-tools">
                <span class="text-mono text-xs text-muted">{detailTraceEntries.length} shown</span>
                <button class="session-link-chip" onclick={() => navigateToTrace(detailSession.agent_id)}>
                  Open full traces
                </button>
              </div>
            </div>
            {#if traceLoading && detailTraceEntries.length === 0}
              <div class="trace-preview-empty text-sm text-muted">Loading recent traces...</div>
            {:else if traceError}
              <div class="trace-preview-empty trace-preview-error text-sm">{sanitizeText(traceError)}</div>
            {:else if sessionTraceMeta && !sessionTraceMeta.trace_enabled && detailTraceEntries.length === 0}
              <div class="trace-preview-empty text-sm text-muted">Daemon audit trace stream unavailable.</div>
            {:else if !sessionTraceMeta && !traceStore.enabled}
              <div class="trace-preview-empty text-sm text-muted">Trace stream unavailable.</div>
            {:else if detailTraceEntries.length === 0}
              <div class="trace-preview-empty text-sm text-muted">No recent traces for this agent yet.</div>
            {:else}
              <div class="trace-preview-list">
                {#each detailTraceEntries as trace, index (`${trace.timestamp}-${trace.server}-${trace.tool}-${index}`)}
                  <div class="trace-preview-row">
                    <div class="trace-preview-top">
                      <div class="trace-preview-id">
                        <span class="text-mono text-xs text-muted">{formatTime(trace.timestamp)}</span>
                        <span class="trace-preview-server">{sanitizeText(trace.server)}</span>
                        <span class="trace-preview-tool">{sanitizeText(trace.tool)}</span>
                      </div>
                      <div class="trace-preview-badges">
                        <span class="trace-preview-duration">{formatTraceDuration(trace.duration_ms)}</span>
                        <Badge text={sanitizeText(trace.status)} variant={traceStatusVariant(trace.status)} />
                      </div>
                    </div>
                    <div class="trace-preview-meta">
                      {#if trace.pipeline_stage}
                        <span class="trace-preview-chip">{sanitizeText(trace.pipeline_stage)}</span>
                      {/if}
                      {#if traceBreakdown(trace)}
                        <span class="trace-preview-chip trace-preview-breakdown">{traceBreakdown(trace)}</span>
                      {/if}
                    </div>
                    {#if trace.error}
                      <div class="trace-preview-error text-sm">{sanitizeText(trace.error)}</div>
                    {/if}
                  </div>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
      {/if}
    {/snippet}

    <div class="section-header" style="margin-top: 4px">
      <span class="section-title">Session Events</span>
      <span class="text-mono text-xs text-muted">{sessionEvents.length} events</span>
    </div>

    <div class="session-event-list">
      {#each sessionEvents.slice(0, 12) as event, index (`${event.timestamp}-${event.event_type}-${index}`)}
        <div class="session-event-row">
          <div class="timeline-dot event-dot"></div>
          <div class="session-event-content">
            <div class="timeline-meta">
              <span class="text-mono text-xs text-muted">{formatTime(event.timestamp)}</span>
              <Badge text={sanitizeText(event.event_type ?? 'event')} variant="info" />
            </div>
            {#if event.agent_id}
              <div class="timeline-title">{sanitizeText(event.agent_id)}</div>
            {/if}
            {#if event.data}
              <div class="timeline-body text-sm text-muted">{sanitizeText(JSON.stringify(event.data)).slice(0, 180)}</div>
            {/if}
          </div>
        </div>
      {:else}
        {#if !loadingEntries}
          <EmptyState icon={'\u25CB'} heading="No lifecycle events for this session" compact />
        {/if}
      {/each}
    </div>

    <div class="section-header" style="margin-top: 4px">
      <span class="section-title">Context Entries</span>
      <span class="text-mono text-xs text-muted">{sessionEntries.length} entries</span>
    </div>

    {#if loadingEntries}
      <div class="loading-bar"><div class="loading-bar-inner"></div></div>
    {/if}

    <div class="entries-timeline">
      {#if drawerError}
        <div class="drawer-error-card" role="alert">
          <div class="drawer-error-title">Could not load session entries</div>
          <div class="drawer-error-body text-sm text-muted">{sanitizeText(drawerError)}</div>
          <div class="drawer-error-actions">
            <button class="btn btn-xs btn-ghost" onclick={retrySessionEntries}>
              Retry
            </button>
          </div>
        </div>
      {/if}
      {#each sessionEntries as entry (entry.id ?? entry.timestamp)}
        {@const entryContent = sanitizeText(entry.content ?? '')}
        <div class="timeline-entry">
          <div class="timeline-dot" style="background: var(--{entryVariant(entry.entry_type) === 'accent' ? 'accent' : entryVariant(entry.entry_type) === 'error' ? 'error' : entryVariant(entry.entry_type) === 'warning' ? 'warning' : entryVariant(entry.entry_type) === 'success' ? 'success' : 'info'})"></div>
          <div class="timeline-content">
            <div class="timeline-meta">
              <span class="text-mono text-xs text-muted">{formatTime(entry.timestamp)}</span>
              <Badge text={sanitizeText(entry.entry_type ?? 'note')} variant={entryVariant(entry.entry_type)} />
            </div>
            <div class="timeline-title">{sanitizeText(entry.title ?? '---')}</div>
            {#if entry.file_path}
              <div class="timeline-file text-mono text-xs">
                {entry.file_path}{#if entry.line_start}:{entry.line_start}{#if entry.line_end && entry.line_end !== entry.line_start}-{entry.line_end}{/if}{/if}
                {#if entry.token_count}<span class="text-muted"> ({entry.token_count} tok)</span>{/if}
              </div>
            {/if}
            {#if entryContent}
              <div class="timeline-body text-sm text-muted">
                {entryContent.slice(0, 200)}{entryContent.length > 200 ? '...' : ''}
              </div>
            {/if}
          </div>
        </div>
      {:else}
        {#if !loadingEntries && !drawerError}
          <EmptyState icon={'\u25CB'} heading="No context entries for this session" compact />
        {/if}
      {/each}
    </div>
  </DetailDrawer>
</div>

<style>
  .fleet-panel {
    overflow-y: auto;
  }

  .fleet-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-template-rows: auto auto;
    gap: var(--space-4);
    height: 100%;
  }

  .fleet-table-card {
    min-height: 200px;
    overflow: hidden;
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

  /* Stats grid */
  .stats-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-3);
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    border-left: 3px solid var(--accent-color, var(--info));
    display: flex;
    flex-direction: column;
    justify-content: center;
    position: relative;
    transition: border-color var(--transition-normal), box-shadow var(--transition-normal);
  }

  /* Top-edge highlight */
  .stat-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .stat-card:hover {
    border-color: color-mix(in srgb, var(--accent-color, var(--info)) 40%, var(--border));
    box-shadow: 0 0 12px color-mix(in srgb, var(--accent-color, var(--info)) 15%, transparent);
  }

  .stat-card .metric-value {
    font-size: 20px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .stat-card .metric-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    margin-top: 4px;
  }

  /* Activity card */
  .activity-card {
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .activity-time {
    color: var(--fg-dim);
    font-size: var(--text-xs);
  }

  .activity-agent {
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .activity-title {
    color: var(--fg-primary);
  }

  /* Memory gauges */
  .memory-gauges-card {
    display: flex;
    flex-direction: column;
  }

  .gauges-container {
    display: flex;
    flex-direction: row;
    gap: var(--space-3);
    flex: 1;
    justify-content: center;
    padding: var(--space-2) 0;
  }

  .gauge-item {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
  }

  .gauge-detail {
    color: var(--fg-dim);
  }

  /* Detail drawer content */
  .detail-stats {
    display: flex;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .stat-chip {
    display: flex;
    align-items: baseline;
    gap: 4px;
    background: var(--bg-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 6px var(--space-3);
    position: relative;
  }

  /* Subtle top-edge on stat chips */
  .stat-chip::before {
    content: '';
    position: absolute;
    top: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.1), transparent);
    pointer-events: none;
  }

  .stat-chip-value {
    font-family: var(--font-mono);
    font-weight: 700;
    font-size: 14px;
    color: var(--fg-primary);
  }

  .stat-chip-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
  }

  .detail-description {
    padding: 6px 0;
    line-height: 1.5;
  }

  .hierarchy-section {
    margin-top: var(--space-3);
    padding-top: var(--space-3);
    border-top: 1px solid var(--border-subtle);
  }

  .hierarchy-section-inline {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .section-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    margin-bottom: var(--space-2);
  }

  .section-header-tools {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .section-title {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    font-family: var(--font-mono);
  }

  .session-breadcrumbs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
  }

  .session-crumb,
  .session-link-chip,
  .child-session-chip {
    border: 1px solid var(--border-subtle);
    background: var(--bg-primary);
    color: var(--fg-secondary);
    border-radius: var(--radius-sm);
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
  }

  .session-crumb {
    padding: 6px 8px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .session-crumb:hover,
  .session-link-chip:hover,
  .child-session-chip:hover,
  .session-crumb-current {
    border-color: color-mix(in srgb, var(--accent) 35%, var(--border-subtle));
    color: var(--fg-primary);
    background: color-mix(in srgb, var(--accent) 8%, var(--bg-primary));
  }

  .session-crumb-current {
    cursor: default;
  }

  .session-crumb-sep {
    color: var(--fg-dim);
    font-size: 12px;
  }

  .session-link-chip {
    padding: 6px 10px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .child-session-list {
    display: grid;
    gap: var(--space-2);
  }

  .child-session-chip {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding: 8px 10px;
    text-align: left;
  }

  .child-session-name {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--fg-primary);
  }

  .child-session-meta {
    font-size: 10px;
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .trace-health-banner {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    padding: 8px 10px;
    border-radius: var(--radius-md);
    border: 1px solid color-mix(in srgb, var(--warning) 30%, var(--border));
    background: color-mix(in srgb, var(--warning) 7%, var(--bg-primary));
  }

  .trace-health-label {
    color: var(--warning);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    white-space: nowrap;
  }

  .trace-health-copy {
    color: var(--fg-secondary);
    font-size: var(--text-sm);
    line-height: 1.4;
  }

  /* Timeline */
  .entries-timeline {
    padding: var(--space-2) 0;
  }

  .session-event-list {
    display: grid;
    gap: var(--space-1);
    padding: var(--space-2) 0;
  }

  .session-event-row {
    display: flex;
    gap: var(--space-3);
    padding: 8px 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .session-event-row:last-child {
    border-bottom: none;
  }

  .session-event-content {
    flex: 1;
    min-width: 0;
  }

  .event-dot {
    color: var(--info);
    background: var(--info);
  }

  .trace-preview-list {
    display: grid;
    gap: var(--space-2);
  }

  .trace-preview-row {
    padding: 10px 12px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--bg-secondary) 82%, transparent);
  }

  .trace-preview-top,
  .trace-preview-id,
  .trace-preview-badges,
  .trace-preview-meta {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: center;
  }

  .trace-preview-top {
    justify-content: space-between;
  }

  .trace-preview-server {
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .trace-preview-tool {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .trace-preview-duration,
  .trace-preview-chip {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .trace-preview-meta {
    margin-top: 8px;
  }

  .trace-preview-chip {
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
    color: var(--fg-secondary);
  }

  .trace-preview-breakdown {
    white-space: normal;
  }

  .trace-preview-empty {
    padding: 10px 12px;
    border: 1px dashed var(--border-subtle);
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--bg-secondary) 72%, transparent);
  }

  .trace-preview-error {
    color: var(--error);
  }

  .timeline-entry {
    display: flex;
    gap: var(--space-3);
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .timeline-entry:last-child {
    border-bottom: none;
  }

  .timeline-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
    margin-top: 6px;
    box-shadow: 0 0 6px currentColor;
  }

  .timeline-content {
    flex: 1;
    min-width: 0;
  }

  .timeline-meta {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin-bottom: 2px;
  }

  .timeline-title {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-weight: 500;
  }

  .timeline-body {
    margin-top: 2px;
    line-height: 1.4;
    word-break: break-word;
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

  .spawn-chip-link {
    background: none;
    border: none;
    padding: 0;
    font-family: var(--font-mono);
    font-weight: 700;
    font-size: 14px;
    color: var(--accent);
    cursor: pointer;
    transition: opacity var(--transition-fast);
  }

  .spawn-chip-link:hover {
    opacity: 0.8;
  }

  .expiring-icon {
    color: var(--warning);
    font-size: 12px;
    margin-left: 4px;
    cursor: help;
    animation: glowPulse 2s ease-in-out infinite;
  }

  .metric-unit {
    font-size: var(--text-xs);
    font-weight: 400;
    color: var(--fg-dim);
  }

  .metric-sub {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    color: var(--fg-dim);
    margin-top: 2px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    letter-spacing: var(--tracking-normal);
  }

  .priority-crit { color: var(--error); margin-right: 4px; }
  .priority-high { color: var(--warning); margin-right: 4px; }
  .priority-blocked { color: var(--error); opacity: 0.8; }

  .description-cell {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 180px;
  }

  .pr-link {
    font-size: var(--text-xs);
    text-decoration: none;
    margin-left: 3px;
    color: var(--accent);
    transition: opacity var(--transition-fast);
  }

  .pr-link:hover {
    opacity: 0.8;
    text-shadow: 0 0 6px var(--glow-accent);
  }

  .timeline-file {
    color: var(--fg-secondary);
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    margin-top: 2px;
    display: inline-block;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    border: 1px solid var(--border-subtle);
  }

  /* Loading bar (for drawer entries) */
  .loading-bar {
    height: 2px;
    background: var(--bg-tertiary);
    border-radius: 1px;
    overflow: hidden;
    margin-bottom: 4px;
  }

  .loading-bar-inner {
    width: 40%;
    height: 100%;
    background: linear-gradient(90deg, var(--info), var(--accent));
    border-radius: 1px;
    animation: loadingSlide 1.2s ease-in-out infinite;
  }

  .drawer-error-card {
    margin-bottom: var(--space-3);
    padding: var(--space-3);
    border-radius: var(--radius-md);
    border: 1px solid color-mix(in srgb, var(--error) 30%, var(--border));
    background: color-mix(in srgb, var(--error) 7%, var(--bg-primary));
  }

  .drawer-error-title {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
    margin-bottom: 4px;
  }

  .drawer-error-body {
    line-height: 1.5;
  }

  .drawer-error-actions {
    display: flex;
    gap: var(--space-2);
    margin-top: var(--space-3);
  }

  @keyframes loadingSlide {
    0% { transform: translateX(-100%); }
    100% { transform: translateX(350%); }
  }

  @media (max-width: 1200px) {
    .fleet-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 768px) {
    .stats-grid {
      grid-template-columns: 1fr;
    }
    .stat-card .metric-value {
      font-size: 18px;
    }
  }
</style>
