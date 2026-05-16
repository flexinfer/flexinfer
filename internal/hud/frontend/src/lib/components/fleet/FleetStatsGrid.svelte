<script>
  /**
   * FleetStatsGrid — 7-card stats strip on the right side of the fleet
   * overview. Pulls live counts from the existing stores; computes the
   * minimal derived data needed inside the grid.
   */
  import { fleetStore } from '../../stores/fleet.svelte.ts';
  import { taskStore } from '../../stores/tasks.svelte.ts';
  import { workflowStore } from '../../stores/workflows.svelte.ts';
  import { memoryStore } from '../../stores/memory.svelte.ts';
  import { graphStore } from '../../stores/graph.svelte.ts';
  import { healthStore } from '../../stores/health.svelte.ts';
  import { formatNumber } from '../../utils/format.ts';

  let fleetAgents = $derived(fleetStore.liveAgents ?? []);
  let agentsWithSession = $derived(fleetAgents.filter((a) => a.has_session).length);
  let agentsWithoutSession = $derived(fleetAgents.length - agentsWithSession);
  let orphanCount = $derived(fleetAgents.filter((a) => a.is_orphan).length);
  let idleAgentCount = $derived(Math.max(0, agentsWithoutSession - orphanCount));

  let sessions = $derived(fleetStore.sessions ?? []);
  let totalTokens = $derived(sessions.reduce((sum, s) => sum + (s.tokens_used ?? 0), 0));

  let tasks = $derived(taskStore.tasks ?? []);
  let workflows = $derived(workflowStore.workflows ?? []);
  let memStats = $derived(memoryStore.stats ?? {});
  let graphStats = $derived(graphStore.stats ?? {});

  let workingItems = $derived(memStats.working_memory?.items ?? 0);
  let shortItems = $derived(memStats.short_term_memory?.items ?? 0);
  let longItems = $derived(memStats.long_term_memory?.items ?? 0);
  let totalMemItems = $derived(workingItems + shortItems + longItems);
  let graphTotal = $derived(graphStats.total_entities ?? 0);
  let graphTopTypes = $derived.by(() => {
    const types = graphStats?.entity_types ?? {};
    return Object.entries(types).sort((a, b) => b[1] - a[1]).slice(0, 3);
  });

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

  /** @type {{ rootGroupCount: number, ungroupedCount: number }} */
  let { rootGroupCount = 0, ungroupedCount = 0 } = $props();

  // Infrastructure stats — refreshed in the parent panel via healthStore;
  // we just read the current values here to keep this component side-
  // effect-free (no extra polling timers).
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
</script>

<div class="stats-grid">
  <div class="stat-card" style="--accent-color: var(--info)">
    {#key agentsWithSession}<div class="metric-value data-updated">{agentsWithSession}</div>{/key}
    <div class="metric-label">Sessions</div>
    {#if orphanCount > 0}
      <div class="metric-sub metric-sub-alert" title="Heartbeating presence without an active session past 2 min. Reaped automatically after 10 min.">
        {orphanCount} orphan{orphanCount === 1 ? '' : 's'}{idleAgentCount > 0 ? ` · ${idleAgentCount} idle` : ''}
      </div>
    {:else if agentsWithoutSession > 0}
      <div class="metric-sub">{agentsWithoutSession} idle between sessions</div>
    {/if}
    {#if fleetStore.groupByRootSession && rootGroupCount > 0}
      <div class="metric-sub" title="Distinct root sessions (subagents grouped under their parent).">
        {rootGroupCount} root group{rootGroupCount === 1 ? '' : 's'}{ungroupedCount > 0 ? ` · ${ungroupedCount} ungrouped` : ''}
      </div>
    {/if}
  </div>
  <div class="stat-card" style="--accent-color: var(--warning)">
    {#key tasks.length}<div class="metric-value data-updated">{tasks.length}</div>{/key}
    <div class="metric-label">Tasks</div>
    {#if tasks.length > 0}
      <div class="metric-sub">
        {#if taskPriorityDist.critical > 0}<span class="priority-crit">{taskPriorityDist.critical} crit</span>{/if}
        {#if taskPriorityDist.high > 0}<span class="priority-high">{taskPriorityDist.high} high</span>{/if}
        {#if taskPriorityDist.blocked > 0}<span class="priority-blocked">{taskPriorityDist.blocked} blocked</span>{/if}
      </div>
    {/if}
  </div>
  <div class="stat-card" style="--accent-color: var(--accent)">
    {#key totalTokens}
      <div class="metric-value data-updated" class:metric-empty={totalTokens === 0}>
        {totalTokens === 0 ? '—' : formatNumber(totalTokens)}
      </div>
    {/key}
    <div class="metric-label">Tokens</div>
  </div>
  <div class="stat-card" style="--accent-color: var(--success)">
    {#key workflows.length}<div class="metric-value data-updated">{workflows.length}</div>{/key}
    <div class="metric-label">Workflows</div>
  </div>
  <div class="stat-card" style="--accent-color: var(--tier-short)">
    {#key totalMemItems}
      <div class="metric-value data-updated" class:metric-empty={totalMemItems === 0}>
        {totalMemItems === 0 ? '—' : formatNumber(totalMemItems)}
      </div>
    {/key}
    <div class="metric-label">Memory Items</div>
  </div>
  <div class="stat-card" style="--accent-color: var(--tier-long)">
    {#key graphTotal}
      <div class="metric-value data-updated" class:metric-empty={graphTotal === 0}>
        {graphTotal === 0 ? '—' : formatNumber(graphTotal)}
      </div>
    {/key}
    <div class="metric-label">Graph Entities</div>
    {#if graphTopTypes.length > 0}
      <div class="metric-sub">{graphTopTypes.map(([t, c]) => `${t}:${c}`).join(' · ')}</div>
    {/if}
  </div>
  <div class="stat-card" style="--accent-color: var(--fg-muted)">
    {#key tunnelCount + cacheHitRate}
      <div class="metric-value data-updated" class:metric-empty={tunnelCount === 0 && cacheHitRate === 0}>
        {#if tunnelCount > 0 || cacheHitRate > 0}
          {tunnelCount} <span class="metric-unit">tunnels</span> · {(cacheHitRate * 100).toFixed(0)}%
        {:else}
          {'—'}
        {/if}
      </div>
    {/key}
    <div class="metric-label">Infrastructure</div>
    {#if tunnelCount === 0 && cacheHitRate === 0}
      <div class="metric-sub">no tunnels · no cache stats</div>
    {/if}
  </div>
</div>

<style>
  .stats-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr));
    min-width: 0;
    gap: var(--space-3);
  }

  .stat-card {
    min-width: 0;
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

  .metric-value.metric-empty {
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

  .metric-sub.metric-sub-alert {
    color: var(--warning);
  }

  .priority-crit { color: var(--error); margin-right: 4px; }
  .priority-high { color: var(--warning); margin-right: 4px; }
  .priority-blocked { color: var(--error); opacity: 0.8; }

  .metric-unit {
    font-size: var(--text-xs);
    font-weight: 400;
    color: var(--fg-dim);
  }
</style>
