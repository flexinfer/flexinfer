<script lang="ts">
  /**
   * ServersHeader — header bar with running/idle/degraded/down + tools
   * counts. Reads healthStore directly.
   */
  import { healthStore } from '../../stores/health.svelte.ts';

  let servers = $derived(healthStore.servers ?? []);
  let healthyCt = $derived(healthStore.healthyCount);
  let idleCt = $derived(healthStore.idleCount);
  let degradedCt = $derived(healthStore.degradedCount);
  let downCt = $derived(healthStore.downCount);
  let totalTools = $derived(servers.reduce((sum, s) => sum + (s.tool_count ?? 0), 0));
</script>

<div class="header-bar">
  <div class="header-stats">
    <span class="header-total text-mono">{servers.length} servers</span>
    <span class="header-stat healthy-stat">
      <span class="dot dot-healthy"></span>
      {healthyCt} running
    </span>
    <span class="header-stat idle-stat">
      <span class="dot dot-idle"></span>
      {idleCt} idle
    </span>
    {#if degradedCt > 0}
      <span class="header-stat degraded-stat">
        <span class="dot dot-degraded"></span>
        {degradedCt} degraded
      </span>
    {/if}
    {#if downCt > 0}
      <span class="header-stat down-stat">
        <span class="dot dot-down"></span>
        {downCt} down
      </span>
    {/if}
    <span class="header-stat tools-stat">
      <span class="tools-icon">{'⚙'}</span>
      {totalTools} tools
    </span>
  </div>
</div>

<style>
  .header-bar {
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border);
    margin-bottom: 0;
    position: relative;
  }

  .header-bar::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .header-stats {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    font-size: var(--text-sm);
  }

  .header-total {
    font-weight: 600;
    color: var(--fg-primary);
  }

  .header-stat {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--fg-secondary);
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
  }

  .dot-healthy { background: var(--success); box-shadow: 0 0 4px var(--glow-success); }
  .dot-idle { background: var(--fg-muted); }
  .dot-degraded { background: var(--warning); box-shadow: 0 0 4px var(--glow-warning); }
  .dot-down { background: var(--error); box-shadow: 0 0 4px var(--glow-error); }

  .tools-icon {
    font-size: 11px;
    opacity: 0.7;
  }

  @media (max-width: 768px) {
    .header-stats {
      flex-wrap: wrap;
      gap: var(--space-2);
    }
  }
</style>
