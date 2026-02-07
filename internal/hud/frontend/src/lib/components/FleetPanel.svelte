<script>
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { graphStore } from '../stores/graph.svelte.ts';
  import { streamStore } from '../stores/stream.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { router } from '../stores/router.svelte.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';
  import Gauge from '../widgets/Gauge.svelte';

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

  let workingItems = $derived(memStats.tiers?.working?.items ?? 0);
  let workingTokens = $derived(memStats.tiers?.working?.tokens ?? 0);
  let workingMax = $derived(memStats.tiers?.working?.max_items ?? 100);
  let shortItems = $derived(memStats.tiers?.short_term?.items ?? 0);
  let shortTokens = $derived(memStats.tiers?.short_term?.tokens ?? 0);
  let shortMax = $derived(memStats.tiers?.short_term?.max_items ?? 500);
  let longItems = $derived(memStats.tiers?.long_term?.items ?? 0);
  let longTokens = $derived(memStats.tiers?.long_term?.tokens ?? 0);
  let longMax = $derived(memStats.tiers?.long_term?.max_items ?? 2000);

  function navigateToSession(sessionId) {
    router.navigate('fleet', sessionId);
  }

  function backToFleet() {
    router.back();
  }

  function formatTime(ts) {
    if (!ts) return '--:--:--';
    const d = new Date(ts);
    return d.toLocaleTimeString('en-US', { hour12: false });
  }

  function relativeTime(ts) {
    if (!ts) return '---';
    const now = Date.now();
    const then = new Date(ts).getTime();
    const diff = now - then;
    const secs = Math.floor(diff / 1000);
    if (secs < 60) return secs + 's ago';
    const mins = Math.floor(secs / 60);
    if (mins < 60) return mins + 'm ago';
    const hours = Math.floor(mins / 60);
    if (hours < 24) return hours + 'h ago';
    const days = Math.floor(hours / 24);
    return days + 'd ago';
  }

  function entryVariant(type) {
    const map = {
      decision: 'accent',
      finding: 'info',
      error: 'error',
      task: 'warning',
      file_read: 'info',
      note: 'success',
    };
    return map[type] ?? 'info';
  }

  function sessionStatus(session) {
    if (session.ended_at) return 'down';
    if (session.active) return 'healthy';
    return 'degraded';
  }

  function formatNumber(n) {
    if (n == null) return '0';
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
    if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
    return String(n);
  }
</script>

<div class="panel fleet-panel">
  {#if detailSessionId}
    <!-- SESSION DETAIL VIEW -->
    <div class="session-detail">
      <div class="detail-header">
        <button class="btn btn-ghost" onclick={backToFleet}>&#8592; Back</button>
        <span class="detail-title text-mono">
          {detailSession?.agent ?? detailSessionId.slice(0, 12)}
        </span>
        {#if detailSession}
          <StatusDot status={sessionStatus(detailSession)} />
          <span class="text-muted text-xs">{detailSession.namespace ?? ''}</span>
        {/if}
      </div>

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

      <div class="section-header" style="margin-top: 12px">
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
            <div class="empty-state">
              <span class="text-muted">No context entries for this session</span>
            </div>
          {/if}
        {/each}
      </div>
    </div>
  {:else}
    <!-- FLEET OVERVIEW -->
    <div class="fleet-grid">
      <!-- LEFT TOP: Agent Fleet Table -->
      <div class="card fleet-table-card">
        <div class="card-header">
          <span class="card-title">Agent Fleet</span>
          <span class="count-badge">{sessions.length}</span>
        </div>
        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Agent</th>
                <th>Status</th>
                <th>Namespace</th>
                <th>Tasks</th>
                <th>Tokens</th>
                <th>Memory</th>
              </tr>
            </thead>
            <tbody>
              {#each sessions as session (session.id)}
                <tr class="clickable-row" onclick={() => navigateToSession(session.id)}>
                  <td class="text-mono">{session.agent ?? session.id?.slice(0, 8) ?? '---'}</td>
                  <td>
                    <StatusDot status={sessionStatus(session)} />
                  </td>
                  <td class="text-mono text-muted">{session.namespace ?? '---'}</td>
                  <td class="text-mono">{session.task_count ?? 0}</td>
                  <td class="text-mono">{formatNumber(session.tokens_used ?? 0)}</td>
                  <td class="text-mono">{session.memory_items ?? 0}</td>
                </tr>
              {:else}
                <tr>
                  <td colspan="6" class="empty-cell">No active agents</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </div>

      <!-- RIGHT TOP: Quick Stats -->
      <div class="stats-grid">
        <div class="stat-card" style="--accent-color: var(--info)">
          <div class="metric-value">{sessions.length}</div>
          <div class="metric-label">Sessions</div>
        </div>
        <div class="stat-card" style="--accent-color: var(--warning)">
          <div class="metric-value">{tasks.length}</div>
          <div class="metric-label">Tasks</div>
        </div>
        <div class="stat-card" style="--accent-color: var(--accent)">
          <div class="metric-value">{formatNumber(totalTokens)}</div>
          <div class="metric-label">Tokens</div>
        </div>
        <div class="stat-card" style="--accent-color: var(--success)">
          <div class="metric-value">{workflows.length}</div>
          <div class="metric-label">Workflows</div>
        </div>
        <div class="stat-card" style="--accent-color: var(--tier-short)">
          <div class="metric-value">{formatNumber(workingItems + shortItems + longItems)}</div>
          <div class="metric-label">Memory Items</div>
        </div>
        <div class="stat-card" style="--accent-color: var(--tier-long)">
          <div class="metric-value">{formatNumber(graphStats.total_entities ?? 0)}</div>
          <div class="metric-label">Graph Entities</div>
        </div>
        <div class="stat-card" style="--accent-color: var(--fg-muted)">
          <div class="metric-value">{tunnelCount}</div>
          <div class="metric-label">Tunnels</div>
        </div>
        <div class="stat-card" style="--accent-color: var(--info)">
          <div class="metric-value">{(cacheHitRate * 100).toFixed(0)}%</div>
          <div class="metric-label">Cache Hit</div>
        </div>
      </div>

      <!-- LEFT BOTTOM: Recent Activity -->
      <div class="card activity-card">
        <div class="card-header">
          <span class="card-title">Recent Activity</span>
          <span class="count-badge">{recentActivity.length}</span>
        </div>
        <div class="activity-feed">
          {#each recentActivity as entry (entry.id ?? entry.timestamp)}
            <div class="activity-row">
              <span class="activity-time text-mono">{formatTime(entry.timestamp)}</span>
              <Badge text={entry.entry_type ?? 'note'} variant={entryVariant(entry.entry_type)} />
              <span class="activity-agent text-mono">{entry.agent ?? '---'}</span>
              <span class="activity-title truncate">{entry.title ?? entry.content?.slice(0, 60) ?? '---'}</span>
            </div>
          {:else}
            <div class="empty-state">
              <span class="text-muted">No recent activity</span>
            </div>
          {/each}
        </div>
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
  {/if}
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

  .fleet-table-card .table-wrap {
    flex: 1;
    overflow-y: auto;
  }

  .clickable-row {
    cursor: pointer;
  }

  .clickable-row:hover td {
    background: var(--bg-tertiary);
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: 10px;
  }

  .empty-cell {
    text-align: center;
    color: var(--fg-muted);
    padding: 24px 10px !important;
  }

  /* Stats grid */
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

  /* Activity feed */
  .activity-card {
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .activity-feed {
    flex: 1;
    overflow-y: auto;
  }

  .activity-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px;
    border-bottom: 1px solid var(--border);
    font-size: 12px;
  }

  .activity-row:nth-child(even) {
    background: rgba(22, 27, 34, 0.5);
  }

  .activity-row:last-child {
    border-bottom: none;
  }

  .activity-time {
    color: var(--fg-muted);
    font-size: 11px;
    flex-shrink: 0;
    width: 65px;
  }

  .activity-agent {
    color: var(--fg-secondary);
    font-size: 11px;
    flex-shrink: 0;
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .activity-title {
    color: var(--fg-primary);
    flex: 1;
    min-width: 0;
  }

  /* Memory gauges */
  .memory-gauges-card {
    display: flex;
    flex-direction: column;
  }

  .gauges-container {
    display: flex;
    flex-direction: column;
    gap: 16px;
    flex: 1;
    justify-content: center;
    padding: 8px 0;
  }

  .gauge-item {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
  }

  .gauge-detail {
    color: var(--fg-muted);
  }

  /* Session detail view */
  .session-detail {
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .detail-header {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
  }

  .detail-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--fg-primary);
  }

  .detail-stats {
    display: flex;
    gap: 12px;
    padding: 10px 0;
    flex-wrap: wrap;
  }

  .stat-chip {
    display: flex;
    align-items: baseline;
    gap: 4px;
    background: var(--bg-secondary);
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
    flex: 1;
    overflow-y: auto;
    padding: 8px 0;
  }

  .timeline-entry {
    display: flex;
    gap: 12px;
    padding: 8px 0;
    border-bottom: 1px solid rgba(48, 54, 61, 0.3);
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
</style>
