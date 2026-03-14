<script>
  import { router } from '../stores/router.svelte.ts';
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { streamStore } from '../stores/stream.svelte.ts';
  import { sandboxStore } from '../stores/sandbox.svelte.ts';
  import { graphStore } from '../stores/graph.svelte.ts';
  import { costStore } from '../stores/cost.svelte.ts';
  import { rbacStore } from '../stores/rbac.svelte.ts';
  import { coordinationStore } from '../stores/coordination.svelte.ts';
  import SparkLine from '../widgets/SparkLine.svelte';
  import Gauge from '../widgets/Gauge.svelte';
  import DonutChart from '../widgets/DonutChart.svelte';

  /**
   * OverviewPanel renders a KPI strip at the top followed by all panels
   * simultaneously as a compact mini-dashboard grid.
   */

  // --- Daily KPIs ---
  let kpis = $state({ sessions_today: 0, tokens_today: 0, tasks_completed_today: 0, active_agents: 0, pending_approvals: 0, file_conflicts: 0, conflict_details: [] });

  async function fetchKPIs() {
    try {
      const res = await globalThis.fetch('/api/kpis');
      if (res.ok) kpis = await res.json();
    } catch { /* non-critical */ }
  }

  $effect(() => {
    fetchKPIs();
    const t = setInterval(fetchKPIs, 15000);
    return () => clearInterval(t);
  });

  let sessionCount = $derived(fleetStore.activeSessions.length);
  let agentCount = $derived(fleetStore.agents.filter(a => a.status === 'active').length);
  let namespaceCount = $derived(fleetStore.namespaceGroups.length);

  let healthyCount = $derived(healthStore.healthyCount);
  let serverCount = $derived(healthStore.availableCount);
  let downCount = $derived(healthStore.downCount);

  let pendingTasks = $derived(taskStore.pendingCount);
  let activeTasks = $derived(taskStore.inProgressCount);
  let blockedTasks = $derived(taskStore.blockedCount);
  let coordinationSummary = $derived(coordinationStore.summary);
  let attentionAgents = $derived(coordinationStore.topAttentionAgents);
  let riskyNamespaces = $derived(coordinationStore.riskyNamespaces);
  let activeBlockers = $derived(coordinationStore.activeBlockers);
  let topRelations = $derived(coordinationStore.relations.slice(0, 4));

  let workingItems = $derived(memoryStore.stats.working_memory?.items ?? 0);
  let shortItems = $derived(memoryStore.stats.short_term_memory?.items ?? 0);
  let longItems = $derived(memoryStore.stats.long_term_memory?.items ?? 0);
  let totalTokens = $derived(memoryStore.stats.total_tokens ?? 0);
  let compressionRatio = $derived(memoryStore.stats.compression?.ratio ?? 0);

  // --- Daemon status ---
  let daemonRunning = $derived(fleetStore.status?.running ?? false);
  let processCount = $derived(fleetStore.status?.processes?.length ?? 0);

  // --- Graph stats ---
  let graphEntities = $derived(graphStore.stats?.total_entities ?? 0);
  let graphTopTypes = $derived.by(() => {
    const types = graphStore.stats?.entity_types ?? {};
    return Object.entries(types)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 3)
      .map(([name, count]) => `${name}:${count}`)
      .join(' · ') || 'none';
  });

  let streamCount = $derived(streamStore.entries.length);
  let lastStreamAge = $derived.by(() => {
    if (streamStore.entries.length === 0) return null;
    try {
      const t = new Date(streamStore.entries[0].timestamp);
      const diff = Math.floor((Date.now() - t.getTime()) / 1000);
      if (diff < 60) return `${diff}s ago`;
      if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
      return `${Math.floor(diff / 3600)}h ago`;
    } catch { return null; }
  });

  let activeWorkflows = $derived(workflowStore.activeWorkflows.length);
  let pendingApprovals = $derived(
    workflowStore.activeWorkflows.filter(w => w.status === 'waiting_approval').length
  );

  // Cost tracking
  let costEnabled = $derived(costStore.enabled);
  let totalCalls = $derived(costStore.totalCalls);
  let totalCached = $derived(costStore.totalCached);
  let totalDenied = $derived(costStore.totalDenied);
  let totalErrors = $derived(costStore.totalErrors);

  // RBAC
  let rbacEnabled = $derived(rbacStore.enabled);
  let rbacDeniedCount = $derived(rbacStore.deniedCount);
  let auditEnabled = $derived(rbacStore.auditEnabled);

  // OTel status (fetched inline)
  let otelStatus = $state({ otlp_configured: false, traced_servers: 0, total_servers: 0 });
  async function fetchOTelStatus() {
    try {
      const res = await globalThis.fetch('/api/otel');
      if (res.ok) otelStatus = await res.json();
    } catch { /* non-critical */ }
  }
  $effect(() => {
    fetchOTelStatus();
    costStore.startPolling(30000);
    rbacStore.startPolling(30000);
    coordinationStore.startPolling(30000);
    const t = setInterval(fetchOTelStatus, 30000);
    return () => {
      clearInterval(t);
      costStore.stopPolling();
      rbacStore.stopPolling();
      coordinationStore.stopPolling();
    };
  });

  // Rolling history buffers for mini sparklines (plain arrays to avoid circular deps).
  const _healthBuf = [];
  const _memoryBuf = [];
  let healthHistory = $state([]);
  let memoryHistory = $state([]);

  $effect(() => {
    const sc = serverCount;
    const hc = healthyCount;
    if (sc > 0) {
      _healthBuf.push(Math.round((hc / sc) * 100));
      if (_healthBuf.length > 20) _healthBuf.shift();
      healthHistory = [..._healthBuf];
    }
  });

  $effect(() => {
    const total = workingItems + shortItems + longItems;
    if (total > 0 || _memoryBuf.length > 0) {
      _memoryBuf.push(total);
      if (_memoryBuf.length > 20) _memoryBuf.shift();
      memoryHistory = [..._memoryBuf];
    }
  });

  // Rolling token trend buffer.
  const _tokenBuf = [];
  let tokenHistory = $state([]);

  $effect(() => {
    const t = kpis.tokens_today;
    if (t > 0 || _tokenBuf.length > 0) {
      _tokenBuf.push(t);
      if (_tokenBuf.length > 20) _tokenBuf.shift();
      tokenHistory = [..._tokenBuf];
    }
  });

  // Nearest-to-completion active workflow progress.
  let bestWorkflowProgress = $derived.by(() => {
    const active = workflowStore.activeWorkflows;
    if (!active.length) return -1;
    let best = -1;
    for (const wf of active) {
      const p = wf.progress;
      if (typeof p === 'number' && p > best) best = p;
      // Fallback: derive from steps if no progress field.
      if (p == null && wf.steps?.length) {
        const done = wf.steps.filter(s => s.status === 'completed' || s.status === 'approved').length;
        const derived = done / wf.steps.length;
        if (derived > best) best = derived;
      }
    }
    return best;
  });

  // Tick counter to refresh "last updated" text periodically.
  let _tick = $state(0);
  $effect(() => {
    const t = setInterval(() => { _tick++ }, 10000);
    return () => clearInterval(t);
  });

  function agoText(ts) {
    void _tick;
    if (!ts) return '';
    const diff = Math.floor((Date.now() - ts) / 1000);
    if (diff < 10) return 'just now';
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    return `${Math.floor(diff / 3600)}h ago`;
  }

  function navigate(panel) {
    router.navigate(panel);
  }
</script>

<div class="panel overview-panel">
  <!-- Daily KPI Strip -->
  <div class="kpi-strip">
    <div class="kpi-tile">
      <div class="kpi-value">{kpis.sessions_today}</div>
      <div class="kpi-label">Sessions Today</div>
    </div>
    <div class="kpi-tile">
      <div class="kpi-value-row">
        <div class="kpi-value">{kpis.tokens_today?.toLocaleString?.() ?? kpis.tokens_today}</div>
        {#if tokenHistory.length >= 2}
          <SparkLine data={tokenHistory} width={60} height={24} color="var(--accent)" />
        {/if}
      </div>
      <div class="kpi-label">Tokens Today</div>
    </div>
    <div class="kpi-tile">
      <div class="kpi-value">{kpis.tasks_completed_today}</div>
      <div class="kpi-label">Tasks Done</div>
    </div>
    <div class="kpi-tile">
      <div class="kpi-value">{kpis.active_agents}</div>
      <div class="kpi-label">Active Agents</div>
    </div>
    <div class="kpi-tile" class:kpi-alert={kpis.file_conflicts > 0}>
      <div class="kpi-value">{kpis.file_conflicts}</div>
      <div class="kpi-label">Conflicts</div>
      {#if kpis.conflict_details?.length > 0}
        <div class="conflict-details">
          {#each kpis.conflict_details.slice(0, 3) as cd}
            <div class="conflict-line">{cd.path}: {cd.agents.join(', ')}</div>
          {/each}
          {#if kpis.conflict_details.length > 3}
            <div class="conflict-line conflict-more">+{kpis.conflict_details.length - 3} more</div>
          {/if}
        </div>
      {/if}
    </div>
    <div class="kpi-tile" class:kpi-alert={kpis.pending_approvals > 0}>
      <div class="kpi-value">{kpis.pending_approvals}</div>
      <div class="kpi-label">Approvals</div>
    </div>
  </div>

  <div class="coordination-strip">
    <div class="coord-card">
      <div class="coord-label">Coordination</div>
      <div class="coord-value">{coordinationSummary.shared_branches} shared / {coordinationSummary.conflict_files} conflicted</div>
      <div class="coord-meta">{coordinationSummary.agents_needing_attention} attention agents · {coordinationSummary.idle_claim_holders} idle holders</div>
    </div>
    <div class="coord-card">
      <div class="coord-label">Delivery Lanes</div>
      <div class="coord-value">{coordinationSummary.active_namespaces} active namespaces</div>
      <div class="coord-meta">{coordinationSummary.namespaces_at_risk} at risk · {coordinationSummary.orphan_tasks} orphan tasks</div>
      {#if riskyNamespaces.length > 0}
        <div class="coord-list-item">{riskyNamespaces[0].namespace}: {riskyNamespaces[0].attention_reasons?.[0] ?? 'attention required'}</div>
      {/if}
    </div>
    <div class="coord-card">
      <div class="coord-label">Attention Agents</div>
      {#if attentionAgents.length > 0}
        <div class="coord-list">
          {#each attentionAgents.slice(0, 3) as agent}
            <div class="coord-list-item">{agent.agent_id}: {(agent.attention_reasons?.[0] ?? 'attention')}</div>
          {/each}
        </div>
      {:else}
        <div class="coord-meta">No active coordination pressure</div>
      {/if}
    </div>
    <div class="coord-card">
      <div class="coord-label">Dependency Radar</div>
      <div class="coord-value">{coordinationSummary.cross_agent_blockers} cross-agent blockers</div>
      {#if activeBlockers.length > 0}
        <div class="coord-list">
          {#each activeBlockers.slice(0, 2) as blocker}
            <div class="coord-list-item">{blocker.task_title} → {blocker.blocked_by_task_title || blocker.blocked_by_task_id}</div>
          {/each}
        </div>
      {:else if topRelations.length > 0}
        <div class="coord-list">
          {#each topRelations.slice(0, 2) as relation}
            <div class="coord-list-item">{relation.source_label} ↔ {relation.target_label}</div>
          {/each}
        </div>
      {:else}
        <div class="coord-meta">No active relation hotspots</div>
      {/if}
    </div>
  </div>

  <div class="overview-grid">
    <!-- Fleet tile -->
    <button class="tile" onclick={() => navigate('fleet')}>
      <div class="tile-header">
        <span class="tile-icon">◈</span>
        <span class="tile-title">Fleet</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric">{sessionCount} <span class="tile-unit">sessions</span></div>
        <div class="tile-detail">{agentCount} agents · {namespaceCount} ns · {coordinationSummary.namespaces_at_risk} at risk</div>
      </div>
      {#if agoText(fleetStore.lastUpdated)}<div class="tile-footer">{agoText(fleetStore.lastUpdated)}</div>{/if}
    </button>

    <!-- Health tile -->
    <button class="tile" onclick={() => navigate('servers')}>
      <div class="tile-header">
        <span class="tile-icon">♥</span>
        <span class="tile-title">Health</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric-row">
          <div class="tile-metric">{healthyCount}<span class="tile-unit">/{serverCount} ok</span></div>
          {#if healthHistory.length >= 2}
            <SparkLine data={healthHistory} width={60} height={24} color="var(--success)" />
          {/if}
        </div>
        {#if serverCount > 0}
          <Gauge value={healthyCount} max={serverCount} color="var(--success)" showPercentage={false} />
        {/if}
        <div class="tile-detail" class:tile-alert={downCount > 0}>
          {downCount > 0 ? `${downCount} down` : 'all healthy'}
        </div>
      </div>
      {#if agoText(healthStore.lastUpdated)}<div class="tile-footer">{agoText(healthStore.lastUpdated)}</div>{/if}
    </button>

    <!-- Tasks tile -->
    <button class="tile" onclick={() => navigate('tasks')}>
      <div class="tile-header">
        <span class="tile-icon">☑</span>
        <span class="tile-title">Tasks</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric-row">
          <div>
            <div class="tile-metric">{pendingTasks} <span class="tile-unit">pending</span></div>
            <div class="tile-detail">{activeTasks} active · {blockedTasks} blocked</div>
          </div>
          {#if pendingTasks + activeTasks + blockedTasks > 0}
            <DonutChart
              segments={[
                { label: 'Pending', value: pendingTasks, color: 'var(--warning)' },
                { label: 'Active', value: activeTasks, color: 'var(--success)' },
                { label: 'Blocked', value: blockedTasks, color: 'var(--error)' },
              ]}
              size={48}
              thickness={6}
              centerValue={String(pendingTasks + activeTasks + blockedTasks)}
            />
          {/if}
        </div>
        {#if coordinationSummary.cross_agent_blockers > 0}
          <div class="tile-detail tile-alert">{coordinationSummary.cross_agent_blockers} x-agent blockers</div>
        {/if}
      </div>
      {#if agoText(taskStore.lastUpdated)}<div class="tile-footer">{agoText(taskStore.lastUpdated)}</div>{/if}
    </button>

    <!-- Memory tile -->
    <button class="tile" onclick={() => navigate('memory')}>
      <div class="tile-header">
        <span class="tile-icon">⦾</span>
        <span class="tile-title">Memory</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric-row">
          <div class="tile-metric tile-tier">
            <span class="tier-w" title="Working memory">{workingItems}<span class="tier-label">W</span></span>
            <span class="tier-s" title="Short-term memory">{shortItems}<span class="tier-label">S</span></span>
            <span class="tier-l" title="Long-term memory">{longItems}<span class="tier-label">L</span></span>
          </div>
          {#if memoryHistory.length >= 2}
            <SparkLine data={memoryHistory} width={60} height={24} color="var(--tier-short)" />
          {/if}
        </div>
        <div class="tile-detail">{totalTokens.toLocaleString()} tokens{#if compressionRatio > 0} · {Math.round(compressionRatio * 100)}% compressed{/if}</div>
      </div>
      {#if agoText(memoryStore.lastUpdated)}<div class="tile-footer">{agoText(memoryStore.lastUpdated)}</div>{/if}
    </button>

    <!-- Stream tile -->
    <button class="tile" onclick={() => navigate('stream')}>
      <div class="tile-header">
        <span class="tile-icon">≡</span>
        <span class="tile-title">Stream</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric">{streamCount} <span class="tile-unit">entries</span></div>
        <div class="tile-detail">{lastStreamAge ? `last: ${lastStreamAge}` : 'no data'}</div>
      </div>
      {#if agoText(streamStore.lastUpdated)}<div class="tile-footer">{agoText(streamStore.lastUpdated)}</div>{/if}
    </button>

    <!-- Sandbox tile -->
    <button class="tile" onclick={() => navigate('sandbox')}>
      <div class="tile-header">
        <span class="tile-icon">{'\u2B22'}</span>
        <span class="tile-title">Sandbox</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric">{sandboxStore.runningCount} <span class="tile-unit">running</span></div>
        <div class="tile-detail">{sandboxStore.available ? `${sandboxStore.totalExecs} execs · ${sandboxStore.totalBuilds} builds` : 'offline'}</div>
      </div>
      {#if agoText(sandboxStore.lastUpdated)}<div class="tile-footer">{agoText(sandboxStore.lastUpdated)}</div>{/if}
    </button>

    <!-- Workflows tile -->
    <button class="tile" onclick={() => navigate('workflows')}>
      <div class="tile-header">
        <span class="tile-icon">⚙</span>
        <span class="tile-title">Workflows</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric">{activeWorkflows} <span class="tile-unit">active</span></div>
        <div class="tile-detail" class:tile-alert={pendingApprovals > 0}>
          {pendingApprovals > 0 ? `${pendingApprovals} awaiting approval` : 'none waiting'}
        </div>
        {#if bestWorkflowProgress >= 0}
          <div class="tile-progress-track">
            <div class="tile-progress-fill" style="width: {(bestWorkflowProgress * 100).toFixed(0)}%"></div>
          </div>
        {/if}
      </div>
      {#if agoText(workflowStore.lastUpdated)}<div class="tile-footer">{agoText(workflowStore.lastUpdated)}</div>{/if}
    </button>

    <!-- Cost tile -->
    {#if costEnabled}
      <button class="tile" onclick={() => navigate('servers')}>
        <div class="tile-header">
          <span class="tile-icon">$</span>
          <span class="tile-title">Cost</span>
        </div>
        <div class="tile-body">
          <div class="tile-metric">{totalCalls.toLocaleString()} <span class="tile-unit">calls</span></div>
          <div class="tile-detail">{totalCached} cached · {totalDenied} denied · {totalErrors} errors</div>
        </div>
        {#if costStore.lastUpdated}<div class="tile-footer">{agoText(costStore.lastUpdated)}</div>{/if}
      </button>
    {/if}

    <!-- Daemon tile -->
    <button class="tile" class:tile-alert-bg={!daemonRunning} onclick={() => navigate('servers')}>
      <div class="tile-header">
        <span class="tile-icon">{'\u2699'}</span>
        <span class="tile-title">Daemon</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric">{daemonRunning ? 'Running' : 'Down'}</div>
        <div class="tile-detail">{processCount} processes · {fleetStore.status?.servers ?? 0} servers</div>
        <div class="tile-badges">
          <span class="tile-badge" class:badge-active={rbacEnabled} class:badge-off={!rbacEnabled}>RBAC: {rbacEnabled ? 'active' : 'off'}{#if rbacEnabled && rbacDeniedCount > 0} ({rbacDeniedCount}){/if}</span>
          <span class="tile-badge" class:badge-active={auditEnabled} class:badge-off={!auditEnabled}>Audit: {auditEnabled ? 'active' : 'off'}</span>
          <span class="tile-badge" class:badge-active={otelStatus.otlp_configured} class:badge-off={!otelStatus.otlp_configured}>OTel: {otelStatus.otlp_configured ? 'active' : 'off'}</span>
          <span class="tile-badge" class:badge-active={costEnabled} class:badge-off={!costEnabled}>Cost: {costEnabled ? 'active' : 'off'}</span>
        </div>
      </div>
    </button>

    <!-- Graph tile -->
    <button class="tile" onclick={() => navigate('graph')}>
      <div class="tile-header">
        <span class="tile-icon">{'\u25C8'}</span>
        <span class="tile-title">Graph</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric">{graphEntities} <span class="tile-unit">entities</span></div>
        <div class="tile-detail">{graphTopTypes}</div>
      </div>
      {#if agoText(graphStore.lastUpdated)}<div class="tile-footer">{agoText(graphStore.lastUpdated)}</div>{/if}
    </button>
  </div>
</div>

<style>
  .overview-panel {
    display: flex;
    flex-direction: column;
    padding: 16px;
    gap: 12px;
  }

  /* KPI Strip */
  .kpi-strip {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    width: 100%;
  }

  .kpi-tile {
    flex: 1;
    min-width: 90px;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 8px 10px;
    text-align: center;
  }

  .kpi-value {
    font-size: 18px;
    font-weight: 700;
    font-family: var(--font-mono);
    font-feature-settings: 'tnum';
    color: var(--fg-primary);
    line-height: 1.2;
  }

  .kpi-label {
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--fg-muted);
    margin-top: 2px;
  }

  .kpi-alert .kpi-value {
    color: var(--warning);
  }

  .kpi-alert {
    border-color: rgba(231, 179, 18, 0.3);
  }

  .overview-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
    gap: 12px;
    width: 100%;
  }

  .coordination-strip {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
    gap: 10px;
  }

  .coord-card {
    background: linear-gradient(180deg, color-mix(in srgb, var(--bg-secondary) 88%, var(--accent) 12%), var(--bg-secondary));
    border: 1px solid color-mix(in srgb, var(--border) 75%, var(--accent) 25%);
    border-radius: var(--radius-md);
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 6px;
    min-height: 92px;
  }

  .coord-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-secondary);
    font-weight: 700;
  }

  .coord-value {
    font-size: 16px;
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .coord-meta {
    font-size: 11px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .coord-list {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .coord-list-item {
    font-size: 11px;
    color: var(--fg-primary);
    font-family: var(--font-mono);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .tile {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 10px 12px;
    cursor: pointer;
    text-align: left;
    transition: border-color var(--transition-normal),
                box-shadow var(--transition-normal),
                transform var(--transition-normal);
  }

  .tile:hover {
    border-color: rgba(233, 93, 116, 0.3);
    box-shadow: 0 0 12px var(--glow-accent), var(--shadow-md);
    transform: translateY(-2px);
  }

  .tile-header {
    display: flex;
    align-items: center;
    gap: 6px;
    margin-bottom: 6px;
  }

  .tile-icon {
    font-size: 14px;
    color: var(--accent);
  }

  .tile-title {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-secondary);
  }

  .tile-body {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .tile-metric {
    font-size: 20px;
    font-weight: 700;
    font-family: var(--font-mono);
    font-feature-settings: 'tnum';
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .tile-unit {
    font-size: 11px;
    font-weight: 400;
    color: var(--fg-muted);
  }

  .tile-detail {
    font-size: 11px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .tile-alert {
    color: var(--warning);
  }

  .tile-metric-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .tile-footer {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    margin-top: 6px;
    opacity: 0.7;
  }

  .tile-tier {
    display: flex;
    gap: 10px;
    font-size: 18px;
  }

  .tier-w { color: var(--tier-working); }
  .tier-s { color: var(--tier-short); }
  .tier-l { color: var(--tier-long); }

  .tier-label {
    font-size: 9px;
    font-weight: 400;
    opacity: 0.6;
    margin-left: 1px;
    vertical-align: super;
  }

  .kpi-value-row {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
  }

  .conflict-details {
    margin-top: 4px;
    text-align: left;
  }

  .conflict-line {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--warning);
    line-height: 1.3;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .conflict-more {
    opacity: 0.7;
  }

  .tile-progress-track {
    height: 4px;
    background: var(--bg-tertiary);
    border-radius: 2px;
    overflow: hidden;
    margin-top: 4px;
  }

  .tile-progress-fill {
    height: 100%;
    background: var(--success);
    border-radius: 2px;
    transition: width 0.3s ease;
  }

  .tile-progress-fill.health-warn {
    background: var(--warning);
  }

  .tile-alert-bg {
    border-color: rgba(231, 68, 68, 0.3);
    background: rgba(231, 68, 68, 0.06);
  }

  .tile-alert-bg .tile-metric {
    color: var(--error, #e74444);
  }

  .tile-badges {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
    margin-top: 4px;
  }

  .tile-badge {
    font-size: 9px;
    font-family: var(--font-mono);
    padding: 1px 5px;
    border-radius: 3px;
    letter-spacing: 0.03em;
  }

  .badge-active {
    background: rgba(76, 175, 80, 0.15);
    color: var(--success, #4caf50);
  }

  .badge-off {
    background: var(--bg-tertiary);
    color: var(--fg-muted);
  }

  @media (max-width: 600px) {
    .overview-grid {
      grid-template-columns: repeat(2, 1fr);
    }
  }
</style>
