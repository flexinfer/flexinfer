<script>
  /**
   * MemoryTiersCard — three working/short/long memory gauges. Reads
   * memoryStore directly; renders a compact empty state when no tier
   * has been populated yet.
   */
  import { memoryStore } from '../../stores/memory.svelte.ts';
  import { formatNumber } from '../../utils/format.ts';
  import Gauge from '../../widgets/Gauge.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let memStats = $derived(memoryStore.stats ?? {});
  let workingItems = $derived(memStats.working_memory?.items ?? 0);
  let workingTokens = $derived(memStats.working_memory?.tokens ?? 0);
  let workingMax = $derived(memStats.working_memory?.max_items ?? 100);
  let shortItems = $derived(memStats.short_term_memory?.items ?? 0);
  let shortTokens = $derived(memStats.short_term_memory?.tokens ?? 0);
  let shortMax = $derived(memStats.short_term_memory?.max_items ?? 500);
  let longItems = $derived(memStats.long_term_memory?.items ?? 0);
  let longTokens = $derived(memStats.long_term_memory?.tokens ?? 0);
  let longMax = $derived(memStats.long_term_memory?.max_items ?? 2000);
  let memoryEmpty = $derived(workingItems === 0 && shortItems === 0 && longItems === 0);
</script>

<div class="card memory-gauges-card">
  <div class="card-header">
    <span class="card-title">Memory Tiers</span>
  </div>
  {#if memoryEmpty}
    <EmptyState icon={'○'} heading="No memory items yet" description="Tiers populate as agents record decisions, findings, and recall context." compact />
  {:else}
    <div class="gauges-container">
      <div class="gauge-item">
        <Gauge value={workingItems} max={workingMax} label="Working" color="var(--tier-working)" showPercentage={true} />
        <div class="gauge-detail text-mono text-xs">{formatNumber(workingTokens)} tokens</div>
      </div>
      <div class="gauge-item">
        <Gauge value={shortItems} max={shortMax} label="Short-Term" color="var(--tier-short)" showPercentage={true} />
        <div class="gauge-detail text-mono text-xs">{formatNumber(shortTokens)} tokens</div>
      </div>
      <div class="gauge-item">
        <Gauge value={longItems} max={longMax} label="Long-Term" color="var(--tier-long)" showPercentage={true} />
        <div class="gauge-detail text-mono text-xs">{formatNumber(longTokens)} tokens</div>
      </div>
    </div>
  {/if}
</div>

<style>
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
</style>
