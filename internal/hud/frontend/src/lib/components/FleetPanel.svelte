<script>
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { graphStore } from '../stores/graph.svelte.ts';
  import { streamStore } from '../stores/stream.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { router } from '../stores/router.svelte.ts';
  import { formatTime, relativeTime, formatNumber, entryVariant } from '../utils/format.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';
  import Gauge from '../widgets/Gauge.svelte';
  import SparkLine from '../widgets/SparkLine.svelte';
  import DataTable from './shared/DataTable.svelte';
  import DetailDrawer from './shared/DetailDrawer.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    fleetStore.startPolling(5000);
    taskStore.startPolling(5000);
    workflowStore.startPolling(10000);
    memoryStore.startPolling(10000);
    graphStore.startPolling(15000);
    streamStore.startPolling(3000);

    return () => {
      fleetStore.stopPolling();
      taskStore.stopPolling();
      workflowStore.stopPolling();
      memoryStore.stopPolling();
      graphStore.stopPolling();
      streamStore.stopPolling();
    };
  });

  // Drill-down: session detail view
  let detailSessionId = $derived(router.detail);
  let sessionEntries = $state([]);
  let loadingEntries = $state(false);

  // Fetch session entries when detail changes
  $effect(() => {
    if (detailSessionId) {
      loadingEntries = true;
      fleetStore.fetchSessionEntries(detailSessionId, 100).then(data => {
        sessionEntries = data ?? [];
        loadingEntries = false;
      });
    } else {
      sessionEntries = [];
    }
  });

  let detailSession = $derived(
    detailSessionId ? (fleetStore.sessions ?? []).find(s => s.id === detailSessionId) : null
  );

  let sessions = $derived(fleetStore.sessions ?? []);
  let tasks = $derived(taskStore.tasks ?? []);
  let workflows = $derived(workflowStore.workflows ?? []);
  let memStats = $derived(memoryStore.stats ?? {});
  let graphStats = $derived(graphStore.stats ?? {});
  let entries = $derived(streamStore.entries ?? []);

  let totalTokens = $derived(
    sessions.reduce((sum, s) => sum + (s.tokens_used ?? 0), 0)
  );

  let recentActivity = $derived(entries.slice(0, 10));

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

  // Token sparkline buffers per session.
  let tokenBuffers = new Map();
  let tokenHistories = $state(new Map());

  $effect(() => {
    for (const s of sessions) {
      const buf = tokenBuffers.get(s.id) ?? [];
      buf.push(s.tokens_used ?? 0);
      if (buf.length > 20) buf.shift();
      tokenBuffers.set(s.id, buf);
    }
    tokenHistories = new Map(tokenBuffers);
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

  let workingItems = $derived(memStats.tiers?.working?.items ?? 0);
  let workingTokens = $derived(memStats.tiers?.working?.tokens ?? 0);
  let workingMax = $derived(memStats.tiers?.working?.max_items ?? 100);
  let shortItems = $derived(memStats.tiers?.short_term?.items ?? 0);
  let shortTokens = $derived(memStats.tiers?.short_term?.tokens ?? 0);
  let shortMax = $derived(memStats.tiers?.short_term?.max_items ?? 500);
  let longItems = $derived(memStats.tiers?.long_term?.items ?? 0);
  let longTokens = $derived(memStats.tiers?.long_term?.tokens ?? 0);
  let longMax = $derived(memStats.tiers?.long_term?.max_items ?? 2000);

  // Sort state for fleet DataTable
  let fleetSortKey = $state('agent');
  let fleetSortDir = $state('asc');

  function handleFleetSort(key, dir) {
    fleetSortKey = key;
    fleetSortDir = dir;
  }

  let sortedSessions = $derived.by(() => {
    const rows = [...sessions];
    rows.sort((a, b) => {
      let cmp = 0;
      switch (fleetSortKey) {
        case 'agent':
          cmp = (a.agent ?? '').localeCompare(b.agent ?? '');
          break;
        case 'status': {
          const order = { healthy: 0, degraded: 1, down: 2 };
          cmp = (order[sessionStatus(a)] ?? 9) - (order[sessionStatus(b)] ?? 9);
          break;
        }
        case 'namespace':
          cmp = (a.namespace ?? '').localeCompare(b.namespace ?? '');
          break;
        case 'task_count':
          cmp = (a.task_count ?? 0) - (b.task_count ?? 0);
          break;
        case 'tokens_used':
          cmp = (a.tokens_used ?? 0) - (b.tokens_used ?? 0);
          break;
        case 'memory_items':
          cmp = (a.memory_items ?? 0) - (b.memory_items ?? 0);
          break;
        default:
          break;
      }
      return fleetSortDir === 'desc' ? -cmp : cmp;
    });
    return rows;
  });

  const fleetColumns = [
    { key: 'agent', label: 'Agent', sortable: true },
    { key: 'status', label: 'Status', sortable: true, width: '70px' },
    { key: 'namespace', label: 'Namespace', sortable: true },
    { key: 'task_count', label: 'Tasks', sortable: true, width: '60px' },
    { key: 'tokens_used', label: 'Tokens', sortable: true, width: '100px' },
    { key: 'memory_items', label: 'Memory', sortable: true, width: '70px' },
  ];

  // Activity feed columns (no sort — chronological)
  const activityColumns = [
    { key: 'time', label: 'Time', width: '70px' },
    { key: 'type', label: 'Type', width: '80px' },
    { key: 'agent', label: 'Agent', width: '90px' },
    { key: 'title', label: 'Title' },
  ];

  function navigateToSession(sessionId) {
    router.navigate('fleet', sessionId);
  }

  function backToFleet() {
    router.back();
  }

  function sessionStatus(session) {
    if (session.ended_at) return 'down';
    if (session.active) return 'healthy';
    return 'degraded';
  }
</script>

<div class="panel fleet-panel">
  <!-- FLEET OVERVIEW (always visible) -->
  <div class="fleet-grid">
    <!-- LEFT TOP: Agent Fleet Table -->
    <div class="card fleet-table-card">
      <div class="card-header">
        <span class="card-title">Agent Fleet</span>
        <span class="count-badge">{sessions.length}</span>
      </div>
      {#if sessions.length === 0 && fleetStore.lastUpdated}
        <EmptyState icon={'\u25C8'} heading="No active agents" compact />
      {:else}
        <DataTable
          columns={fleetColumns}
          rows={sortedSessions}
          sortKey={fleetSortKey}
          sortDir={fleetSortDir}
          loading={!fleetStore.lastUpdated}
          skeletonRows={4}
          onSort={handleFleetSort}
          onRowClick={(row) => navigateToSession(row.id)}
        >
          {#snippet row({ row: session })}
            <td class="text-mono">
              {session.agent ?? session.id?.slice(0, 8) ?? '---'}
              {#if expiringClaims.has(session.agent_id)}
                <span class="expiring-icon" title={`Expiring: ${expiringClaims.get(session.agent_id).join(', ')}`}>{'\u23F0'}</span>
              {/if}
            </td>
            <td>
              <StatusDot status={sessionStatus(session)} />
            </td>
            <td class="text-mono text-muted">{session.namespace ?? '---'}</td>
            <td class="text-mono">{session.task_count ?? 0}</td>
            <td class="text-mono token-cell">
              {#key session.tokens_used}<span class="data-updated">{formatNumber(session.tokens_used ?? 0)}</span>{/key}
              {#if tokenHistories.get(session.id)?.length >= 2}
                <SparkLine data={tokenHistories.get(session.id)} width={40} height={16} color="var(--accent)" />
              {/if}
            </td>
            <td class="text-mono">{session.memory_items ?? 0}</td>
          {/snippet}
        </DataTable>
      {/if}
    </div>

    <!-- RIGHT TOP: Quick Stats -->
    <div class="stats-grid">
      <div class="stat-card" style="--accent-color: var(--info)">
        {#key sessions.length}<div class="metric-value data-updated">{sessions.length}</div>{/key}
        <div class="metric-label">Sessions</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--warning)">
        {#key tasks.length}<div class="metric-value data-updated">{tasks.length}</div>{/key}
        <div class="metric-label">Tasks</div>
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
      </div>
      <div class="stat-card" style="--accent-color: var(--fg-muted)">
        {#key tunnelCount + cacheHitRate}<div class="metric-value data-updated">{tunnelCount}<span class="metric-unit">t</span> · {(cacheHitRate * 100).toFixed(0)}%</div>{/key}
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
          idKey="id"
        >
          {#snippet row({ row: entry })}
            <td class="activity-time text-mono">{formatTime(entry.timestamp)}</td>
            <td><Badge text={entry.entry_type ?? 'note'} variant={entryVariant(entry.entry_type)} /></td>
            <td class="activity-agent text-mono">{entry.agent ?? '---'}</td>
            <td class="activity-title truncate">{entry.title ?? entry.content?.slice(0, 60) ?? '---'}</td>
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
    title={detailSession?.agent ?? detailSessionId?.slice(0, 12) ?? ''}
    subtitle={detailSession?.namespace ?? ''}
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
        </div>
        {#if detailSession.description}
          <div class="detail-description text-sm text-secondary">{detailSession.description}</div>
        {/if}
      {/if}
    {/snippet}

    <div class="section-header" style="margin-top: 4px">
      <span class="section-title">Context Entries</span>
      <span class="text-mono text-xs text-muted">{sessionEntries.length} entries</span>
    </div>

    {#if loadingEntries}
      <div class="loading-bar"><div class="loading-bar-inner"></div></div>
    {/if}

    <div class="entries-timeline">
      {#each sessionEntries as entry (entry.id ?? entry.timestamp)}
        <div class="timeline-entry">
          <div class="timeline-dot" style="background: var(--{entryVariant(entry.entry_type) === 'accent' ? 'accent' : entryVariant(entry.entry_type) === 'error' ? 'error' : entryVariant(entry.entry_type) === 'warning' ? 'warning' : entryVariant(entry.entry_type) === 'success' ? 'success' : 'info'})"></div>
          <div class="timeline-content">
            <div class="timeline-meta">
              <span class="text-mono text-xs text-muted">{formatTime(entry.timestamp)}</span>
              <Badge text={entry.entry_type ?? 'note'} variant={entryVariant(entry.entry_type)} />
            </div>
            <div class="timeline-title">{entry.title ?? '---'}</div>
            {#if entry.content}
              <div class="timeline-body text-sm text-muted">{entry.content.slice(0, 200)}{entry.content.length > 200 ? '...' : ''}</div>
            {/if}
          </div>
        </div>
      {:else}
        {#if !loadingEntries}
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
    gap: 16px;
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
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: var(--radius-lg);
  }

  /* Stats grid */
  .stats-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 10px 12px;
    border-left: 3px solid var(--accent-color, var(--info));
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .stat-card .metric-value {
    font-size: 18px;
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

  /* Activity card */
  .activity-card {
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .activity-time {
    color: var(--fg-muted);
    font-size: 11px;
  }

  .activity-agent {
    color: var(--fg-secondary);
    font-size: 11px;
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
    gap: 12px;
    flex: 1;
    justify-content: center;
    padding: 8px 0;
  }

  .gauge-item {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
  }

  .gauge-detail {
    color: var(--fg-muted);
  }

  /* Detail drawer content */
  .detail-stats {
    display: flex;
    gap: 12px;
    flex-wrap: wrap;
  }

  .stat-chip {
    display: flex;
    align-items: baseline;
    gap: 4px;
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 6px 10px;
  }

  .stat-chip-value {
    font-family: var(--font-mono);
    font-weight: 700;
    font-size: 14px;
    color: var(--fg-primary);
  }

  .stat-chip-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
  }

  .detail-description {
    padding: 6px 0;
    line-height: 1.5;
  }

  /* Timeline */
  .entries-timeline {
    padding: 8px 0;
  }

  .timeline-entry {
    display: flex;
    gap: 12px;
    padding: 8px 0;
    border-bottom: 1px solid rgba(3, 89, 100, 0.3);
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
  }

  .timeline-content {
    flex: 1;
    min-width: 0;
  }

  .timeline-meta {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 2px;
  }

  .timeline-title {
    font-size: 12px;
    color: var(--fg-primary);
    font-weight: 500;
  }

  .timeline-body {
    margin-top: 2px;
    line-height: 1.4;
    word-break: break-word;
  }

  .token-cell {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .expiring-icon {
    color: var(--warning);
    font-size: 12px;
    margin-left: 4px;
    cursor: help;
  }

  .metric-unit {
    font-size: 11px;
    font-weight: 400;
    color: var(--fg-muted);
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
    background: var(--accent);
    border-radius: 1px;
    animation: loadingSlide 1s ease-in-out infinite;
  }

  @keyframes loadingSlide {
    0% { transform: translateX(-100%); }
    100% { transform: translateX(300%); }
  }
</style>
