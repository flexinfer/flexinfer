<script lang="ts">
  import type { CostEstimate } from '../../stores/mills.svelte.ts';
  import { millsStore } from '../../stores/mills.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    millsStore.startPolling(15000);
    return () => { millsStore.stopPolling(); };
  });

  let items = $derived(millsStore.backlog);
  let loading = $derived(millsStore.loading && millsStore.backlog.length === 0);
  let disabled = $derived(millsStore.disabled);
  let error = $derived(millsStore.error);
  let previews = $derived(millsStore.costPreviews);

  // Track the last set of backlog ids we've fetched a preview for so we
  // don't refetch on every poll tick. Cost previews are stable for a
  // given (backlog, policy) pair, so a one-shot per-id fetch is plenty.
  // Phase 7 slice 7.4.
  let fetchedIDs = $state<Set<string>>(new Set());

  $effect(() => {
    if (disabled) return;
    const missing = items
      .map((i) => i.ID)
      .filter((id) => id && !fetchedIDs.has(id));
    if (missing.length === 0) return;
    // Mark as fetched up front so a re-render mid-flight doesn't kick
    // off a duplicate fetch storm. Failures are silent (returned null
    // by the store), and the missing-row UX falls through to "—".
    const next = new Set(fetchedIDs);
    for (const id of missing) next.add(id);
    fetchedIDs = next;
    void Promise.all(missing.map((id) => millsStore.fetchCostPreview(id)));
  });

  function fmtUSD(n: number | undefined): string {
    if (typeof n !== 'number' || !Number.isFinite(n)) return '—';
    return `$${n.toFixed(2)}`;
  }

  function confidenceLabel(c: CostEstimate['confidence']): string {
    return c === 'medium' ? 'med' : c;
  }
</script>

<PanelShell
  title="Backlog"
  icon="☑"
  count={items.length}
  loading={loading}
  empty={items.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled ? 'Mills operator not configured' : (error ? 'Failed to load backlog' : 'No backlog items')}
  emptyHint={disabled ? 'Set LOOM_MILLS_OPERATOR_URL on the HUD to connect.' : (error ?? 'The council has not produced any items yet.')}
>
  <table class="mills-table">
    <thead>
      <tr>
        <th>ID</th>
        <th>State</th>
        <th>P</th>
        <th>Title</th>
        <th>Labels</th>
        <th class="est-col">Est.</th>
      </tr>
    </thead>
    <tbody>
      {#each items as item (item.ID)}
        {@const est = previews[item.ID]}
        <tr>
          <td class="mono">{item.ID}</td>
          <td><span class="state state-{item.State}">{item.State}</span></td>
          <td>{item.Priority}</td>
          <td>{item.Title}</td>
          <td>
            {#if item.Labels && item.Labels.length}
              {#each item.Labels as label}
                <span class="label">{label}</span>
              {/each}
            {/if}
          </td>
          <td class="est-cell">
            {#if est}
              <span class="est-value mono" title="path_class: {est.path_class}, sample_size: {est.sample_size}">
                {fmtUSD(est.estimate_usd)}
              </span>
              <span class="confidence conf-{est.confidence}" title="confidence band">
                {confidenceLabel(est.confidence)}
              </span>
              {#if est.capped_by_policy}
                <span class="capped" title="Estimate capped by ensemble policy">⚠ capped</span>
              {/if}
            {:else}
              <span class="est-pending">—</span>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</PanelShell>

<style>
  .mills-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .mills-table th, .mills-table td {
    text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-subtle, #233);
  }
  .mills-table th { font-weight: 600; color: var(--text-muted, #889); }
  .mono { font-family: ui-monospace, monospace; }
  .state { padding: 0.1rem 0.4rem; border-radius: 3px; font-size: 0.75rem; }
  .state-queued    { background: var(--bg-subtle, #233); color: var(--text-muted, #aab); }
  .state-running   { background: rgba(64, 144, 240, 0.15); color: rgb(120, 180, 240); }
  .state-merged    { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .state-escalated { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }
  .state-paused    { background: rgba(180, 180, 60, 0.15); color: rgb(220, 220, 120); }
  .label {
    display: inline-block; margin-right: 0.3rem; padding: 0.05rem 0.4rem;
    background: var(--bg-subtle, #1a2030); border-radius: 999px;
    font-size: 0.7rem; color: var(--text-muted, #aab);
  }

  /* Cost preview column — value + small confidence pill + optional cap warn. */
  .est-col, .est-cell { white-space: nowrap; }
  .est-value { color: var(--text, #cdd); margin-right: 0.35rem; }
  .est-pending { color: var(--text-muted, #889); font-family: ui-monospace, monospace; }
  .confidence {
    display: inline-block; padding: 0.02rem 0.35rem; border-radius: 3px;
    font-size: 0.65rem; font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.04em; vertical-align: middle;
  }
  .conf-low    { background: rgba(220, 80, 80, 0.15);  color: rgb(240, 150, 150); }
  .conf-medium { background: rgba(220, 180, 60, 0.18); color: rgb(230, 200, 110); }
  .conf-high   { background: rgba(72, 200, 128, 0.15); color: rgb(150, 220, 170); }
  .capped {
    display: inline-block; margin-left: 0.35rem;
    padding: 0.02rem 0.35rem; border-radius: 3px;
    font-size: 0.65rem; font-weight: 600;
    background: rgba(220, 80, 80, 0.18); color: rgb(240, 140, 140);
  }
</style>
