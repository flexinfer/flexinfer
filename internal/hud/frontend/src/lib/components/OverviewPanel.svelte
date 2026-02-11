<script>
  import { router } from '../stores/router.svelte.ts';
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { streamStore } from '../stores/stream.svelte.ts';
  import { sandboxStore } from '../stores/sandbox.svelte.ts';
  import SparkLine from '../widgets/SparkLine.svelte';

  /**
   * OverviewPanel renders all panels simultaneously as a compact
   * mini-dashboard grid. Each tile is clickable → navigates to full panel.
   */

  let sessionCount = $derived(fleetStore.activeSessions.length);
  let agentCount = $derived(fleetStore.agents.filter(a => a.status === 'active').length);
  let namespaceCount = $derived(fleetStore.namespaceGroups.length);

  let healthyCount = $derived(healthStore.healthyCount);
  let serverCount = $derived(healthStore.availableCount);
  let downCount = $derived(healthStore.downCount);

  let pendingTasks = $derived(taskStore.pendingCount);
  let activeTasks = $derived(taskStore.inProgressCount);
  let blockedTasks = $derived(taskStore.blockedCount);

  let workingItems = $derived(memoryStore.stats.working_memory?.items ?? 0);
  let shortItems = $derived(memoryStore.stats.short_term_memory?.items ?? 0);
  let longItems = $derived(memoryStore.stats.long_term_memory?.items ?? 0);
  let totalTokens = $derived(memoryStore.stats.total_tokens ?? 0);

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
  <div class="overview-grid">
    <!-- Fleet tile -->
    <button class="tile" onclick={() => navigate('fleet')}>
      <div class="tile-header">
        <span class="tile-icon">◈</span>
        <span class="tile-title">Fleet</span>
      </div>
      <div class="tile-body">
        <div class="tile-metric">{sessionCount} <span class="tile-unit">sessions</span></div>
        <div class="tile-detail">{agentCount} agents · {namespaceCount} ns</div>
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
            <SparkLine data={healthHistory} width={40} height={16} color="var(--success)" />
          {/if}
        </div>
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
        <div class="tile-metric">{pendingTasks} <span class="tile-unit">pending</span></div>
        <div class="tile-detail">{activeTasks} active · {blockedTasks} blocked</div>
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
            <span class="tier-w">{workingItems}</span>
            <span class="tier-s">{shortItems}</span>
            <span class="tier-l">{longItems}</span>
          </div>
          {#if memoryHistory.length >= 2}
            <SparkLine data={memoryHistory} width={40} height={16} color="var(--tier-short)" />
          {/if}
        </div>
        <div class="tile-detail">{totalTokens.toLocaleString()} tokens</div>
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
      </div>
      {#if agoText(workflowStore.lastUpdated)}<div class="tile-footer">{agoText(workflowStore.lastUpdated)}</div>{/if}
    </button>
  </div>
</div>

<style>
  .overview-panel {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
  }

  .overview-grid {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 12px;
    max-width: 720px;
    width: 100%;
  }

  .tile {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 14px 16px;
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
    margin-bottom: 10px;
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

  @media (max-width: 600px) {
    .overview-grid {
      grid-template-columns: repeat(2, 1fr);
    }
  }
</style>
