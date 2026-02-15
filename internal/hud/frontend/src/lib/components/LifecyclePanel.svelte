<script>
  import { lifecycleStore } from '../stores/lifecycle.svelte.ts';
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { timelineStore } from '../stores/timeline.svelte.ts';
  import SwimLaneTimeline from '../widgets/SwimLaneTimeline.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    timelineStore.startPolling(30000);
    fleetStore.startPolling(30000);
    return () => {
      timelineStore.stopPolling();
      fleetStore.stopPolling();
    };
  });

  let lanes = $derived(lifecycleStore.lanes);
  let timeRange = $derived(lifecycleStore.timeRange);
  let zoomPreset = $derived(lifecycleStore.zoomPreset);
  const presets = ['6h', '12h', '24h', '48h'];

  let totalAgents = $derived(lanes.length);
  let totalSessions = $derived(lanes.reduce((s, l) => s + l.sessions.length, 0));
  let activeCount = $derived(lanes.filter((l) => l.current_status === 'active').length);

  let containerEl = $state(null);
  let containerWidth = $state(900);

  $effect(() => {
    if (!containerEl) return;
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        containerWidth = entry.contentRect.width;
      }
    });
    observer.observe(containerEl);
    return () => observer.disconnect();
  });
</script>

<div class="panel lifecycle-panel">
  <div class="lifecycle-header">
    <h2 class="panel-title">{'\u21C6'} Agent Lifecycle</h2>
    <div class="zoom-presets">
      {#each presets as preset}
        <button class="zoom-btn" class:active={zoomPreset === preset} onclick={() => lifecycleStore.setZoom(preset)}>{preset}</button>
      {/each}
    </div>
    <span class="text-muted text-xs">{totalAgents} agents | {totalSessions} sessions | {activeCount} active</span>
  </div>
  <div class="lifecycle-body" bind:this={containerEl}>
    {#if lanes.length === 0}
      <EmptyState icon={'\u21C6'} heading="No agent activity" description="No agent activity in the selected time range" compact />
    {:else}
      <SwimLaneTimeline {lanes} {timeRange} width={containerWidth} />
    {/if}
  </div>
  <footer class="lifecycle-footer">
    <span class="legend-item"><span style="color:var(--success)">{'\u25B2'}</span> Start</span>
    <span class="legend-item"><span style="color:var(--error)">{'\u25A0'}</span> End</span>
    <span class="legend-item"><span style="color:var(--info)">{'\u25CF'}</span> Task</span>
    <span class="legend-item"><span style="color:var(--warning)">{'\u25C6'}</span> Conflict</span>
  </footer>
</div>

<style>
  .lifecycle-panel { display: flex; flex-direction: column; overflow: hidden; }
  .lifecycle-header { display: flex; align-items: center; justify-content: space-between; padding: 8px 12px; border-bottom: 1px solid var(--border); flex-shrink: 0; }
  .panel-title { font-size: 13px; font-weight: 600; color: var(--fg-primary); margin: 0; }
  .zoom-presets { display: flex; gap: 2px; background: var(--bg-tertiary); border-radius: var(--radius-sm); padding: 2px; }
  .zoom-btn { padding: 3px 10px; font-size: 11px; font-family: var(--font-mono); color: var(--fg-secondary); background: transparent; border: none; border-radius: var(--radius-sm); cursor: pointer; }
  .zoom-btn:hover { background: var(--bg-secondary); color: var(--fg-primary); }
  .zoom-btn.active { background: var(--bg-secondary); color: var(--fg-primary); box-shadow: 0 0 4px var(--glow-accent); }
  .lifecycle-body { flex: 1; overflow: auto; padding: 8px 0; }
  .lifecycle-footer { display: flex; align-items: center; gap: 14px; padding: 6px 12px; border-top: 1px solid var(--border); background: var(--bg-secondary); flex-shrink: 0; }
  .legend-item { display: flex; align-items: center; gap: 4px; font-size: 11px; color: var(--fg-secondary); }
</style>
