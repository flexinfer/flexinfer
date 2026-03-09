<script>
  import { lifecycleStore } from '../stores/lifecycle.svelte.ts';
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { timelineStore } from '../stores/timeline.svelte.ts';
  import { coordinationStore } from '../stores/coordination.svelte.ts';
  import SwimLaneTimeline from '../widgets/SwimLaneTimeline.svelte';
  import EmptyState from './shared/EmptyState.svelte';

  $effect(() => {
    timelineStore.startPolling(30000);
    fleetStore.startPolling(30000);
    coordinationStore.startPolling(30000);
    return () => {
      timelineStore.stopPolling();
      fleetStore.stopPolling();
      coordinationStore.stopPolling();
    };
  });

  let lanes = $derived(lifecycleStore.lanes);
  let timeRange = $derived(lifecycleStore.timeRange);
  let zoomPreset = $derived(lifecycleStore.zoomPreset);
  const presets = ['6h', '12h', '24h', '48h'];

  let totalAgents = $derived(lanes.length);
  let totalSessions = $derived(lanes.reduce((s, l) => s + l.sessions.length, 0));
  let activeCount = $derived(lanes.filter((l) => l.current_status === 'active').length);
  let attentionAgents = $derived(coordinationStore.topAttentionAgents);
  let riskyNamespaces = $derived(coordinationStore.riskyNamespaces);
  let relationWatchlist = $derived(coordinationStore.relations.slice(0, 4));

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
  <div class="lifecycle-layout">
    <div class="lifecycle-body" bind:this={containerEl}>
      {#if lanes.length === 0}
        <EmptyState icon={'\u21C6'} heading="No agent activity" description="No agent activity in the selected time range" compact />
      {:else}
        <SwimLaneTimeline {lanes} {timeRange} width={containerWidth} />
      {/if}
    </div>
    <aside class="attention-rail">
      <div class="rail-section">
        <div class="rail-title">Attention Lanes</div>
        {#if attentionAgents.length > 0}
          {#each attentionAgents.slice(0, 5) as agent}
            <div class="rail-card">
              <div class="rail-card-title">{agent.agent_id}</div>
              <div class="rail-card-meta">{agent.status} · {agent.namespace || 'unscoped'}</div>
              <div class="rail-card-detail">{agent.attention_reasons?.join(' · ') || 'attention required'}</div>
            </div>
          {/each}
        {:else}
          <div class="rail-empty">No agents need attention right now.</div>
        {/if}
      </div>
      <div class="rail-section">
        <div class="rail-title">Risky Namespaces</div>
        {#if riskyNamespaces.length > 0}
          {#each riskyNamespaces.slice(0, 4) as namespace}
            <div class="rail-card">
              <div class="rail-card-title">{namespace.namespace}</div>
              <div class="rail-card-meta">{namespace.blocked_tasks} blocked · {namespace.cross_agent_blockers} cross-agent</div>
              <div class="rail-card-detail">{namespace.attention_reasons?.join(' · ') || 'attention required'}</div>
            </div>
          {/each}
        {:else}
          <div class="rail-empty">No namespaces are under coordination pressure.</div>
        {/if}
      </div>
      <div class="rail-section">
        <div class="rail-title">Relation Watchlist</div>
        {#if relationWatchlist.length > 0}
          {#each relationWatchlist as relation}
            <div class="rail-card" class:rail-card-critical={relation.severity === 'critical'}>
              <div class="rail-card-title">{relation.source_label} ↔ {relation.target_label}</div>
              <div class="rail-card-meta">{relation.kind.replaceAll('_', ' ')} · {relation.namespace || 'global'}</div>
              <div class="rail-card-detail">{relation.detail || 'active coordination hotspot'}</div>
            </div>
          {/each}
        {:else}
          <div class="rail-empty">No hot relations to watch.</div>
        {/if}
      </div>
    </aside>
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
  .lifecycle-layout { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 12px; flex: 1; min-height: 0; padding: 8px 0; }
  .panel-title { font-size: 13px; font-weight: 600; color: var(--fg-primary); margin: 0; }
  .zoom-presets { display: flex; gap: 2px; background: var(--bg-tertiary); border-radius: var(--radius-sm); padding: 2px; }
  .zoom-btn { padding: 3px 10px; font-size: 11px; font-family: var(--font-mono); color: var(--fg-secondary); background: transparent; border: none; border-radius: var(--radius-sm); cursor: pointer; }
  .zoom-btn:hover { background: var(--bg-secondary); color: var(--fg-primary); }
  .zoom-btn.active { background: var(--bg-secondary); color: var(--fg-primary); box-shadow: 0 0 4px var(--glow-accent); }
  .lifecycle-body { overflow: auto; padding: 0; min-width: 0; }
  .attention-rail { display: flex; flex-direction: column; gap: 10px; overflow: auto; padding-right: 12px; }
  .rail-section { display: flex; flex-direction: column; gap: 8px; }
  .rail-title { font-size: 10px; text-transform: uppercase; letter-spacing: 0.08em; color: var(--fg-secondary); font-weight: 700; }
  .rail-card { background: var(--bg-secondary); border: 1px solid var(--border); border-radius: var(--radius-md); padding: 10px; display: flex; flex-direction: column; gap: 4px; }
  .rail-card-critical { border-color: color-mix(in srgb, var(--warning) 45%, var(--border)); }
  .rail-card-title { font-size: 13px; color: var(--fg-primary); font-weight: 600; font-family: var(--font-mono); }
  .rail-card-meta { font-size: 11px; color: var(--accent); font-family: var(--font-mono); }
  .rail-card-detail { font-size: 11px; color: var(--fg-muted); line-height: 1.4; }
  .rail-empty { font-size: 11px; color: var(--fg-muted); background: var(--bg-secondary); border: 1px dashed var(--border); border-radius: var(--radius-md); padding: 10px; }
  .lifecycle-footer { display: flex; align-items: center; gap: 14px; padding: 6px 12px; border-top: 1px solid var(--border); background: var(--bg-secondary); flex-shrink: 0; }
  .legend-item { display: flex; align-items: center; gap: 4px; font-size: 11px; color: var(--fg-secondary); }

  @media (max-width: 1100px) {
    .lifecycle-layout { grid-template-columns: 1fr; }
    .attention-rail { padding: 0 12px 0 12px; }
  }
</style>
