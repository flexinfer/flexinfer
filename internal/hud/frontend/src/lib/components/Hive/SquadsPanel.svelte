<script lang="ts">
  import { hiveSquadsStore } from '../../stores/hive_squads.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    hiveSquadsStore.startPolling(15000);
    return () => { hiveSquadsStore.stopPolling(); };
  });

  let entries = $derived(hiveSquadsStore.state);
  let loading = $derived(hiveSquadsStore.loading && entries.length === 0);
  let disabled = $derived(hiveSquadsStore.disabled);
  let error = $derived(hiveSquadsStore.error);
  let details = $derived(hiveSquadsStore.details);

  // expanded squad name → fetch detail (memory + outcomes) on demand. The
  // detail call is fired once per expand, not on every poll, so the table
  // doesn't churn while a card is open.
  let expanded = $state<string | null>(null);

  function toggle(name: string): void {
    if (expanded === name) {
      expanded = null;
      return;
    }
    expanded = name;
    if (!details[name]) {
      void hiveSquadsStore.fetchDetail(name);
    }
  }

  function fmtPct(v: number | undefined): string {
    if (v == null || !Number.isFinite(v)) return '—';
    return `${(v * 100).toFixed(1)}%`;
  }

  function fmtCost(c: number | undefined): string {
    if (c == null || !Number.isFinite(c)) return '—';
    return `$${c.toFixed(3)}`;
  }

  function successClass(rate: number, total: number): string {
    if (total === 0) return 'rate-empty';
    if (rate >= 0.75) return 'rate-good';
    if (rate >= 0.5) return 'rate-warn';
    return 'rate-bad';
  }
</script>

<PanelShell
  title="Squads"
  icon="◈"
  count={entries.length}
  loading={loading}
  empty={entries.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled
    ? 'Hive operator not configured'
    : (error ? 'Failed to load squads' : 'No squads loaded yet')}
  emptyHint={disabled
    ? 'Set LOOM_HIVE_OPERATOR_URL on the HUD to connect.'
    : (error ?? 'Squads load from platform/gitops/k3s/hive/squads/*.yaml on operator boot.')}
>
  <div class="squad-grid">
    {#each entries as entry (entry.squad.Name)}
      {@const stats = entry.outcome_stats}
      {@const detail = details[entry.squad.Name]}
      <article
        class="squad-card"
        class:expanded={expanded === entry.squad.Name}
        class:disabled={entry.squad.Enabled === false}
      >
        <header class="squad-header">
          <button
            type="button"
            class="squad-name-btn"
            onclick={() => toggle(entry.squad.Name)}
            aria-expanded={expanded === entry.squad.Name}
          >
            <span class="squad-toggle">{expanded === entry.squad.Name ? '▾' : '▸'}</span>
            <span class="squad-name">{entry.squad.Name}</span>
          </button>
          {#if entry.squad.Enabled === false}
            <span class="squad-badge badge-disabled">disabled</span>
          {/if}
          {#if entry.squad.RecursionEnabled}
            <span class="squad-badge badge-recursion">recursion</span>
          {/if}
        </header>

        <div class="squad-metrics">
          <div class="metric">
            <span class="metric-label">success rate</span>
            <span class="metric-value {successClass(stats.success_rate, stats.total)}">
              {fmtPct(stats.success_rate)}
            </span>
            <span class="metric-sub">window of {stats.window}</span>
          </div>
          <div class="metric">
            <span class="metric-label">total</span>
            <span class="metric-value">{stats.total}</span>
            <span class="metric-sub">
              {stats.merged_clean} clean / {stats.failed} failed
            </span>
          </div>
          <div class="metric">
            <span class="metric-label">in flight</span>
            <span class="metric-value">{stats.in_flight}</span>
            <span class="metric-sub">{fmtCost(stats.total_cost_usd)} window cost</span>
          </div>
        </div>

        {#if expanded === entry.squad.Name}
          <div class="squad-detail">
            {#if detail && detail.recent_memory && detail.recent_memory.length > 0}
              <h3 class="detail-heading">Top memory</h3>
              <ul class="memory-list">
                {#each detail.recent_memory.slice(0, 5) as mem (mem.ID)}
                  <li class="memory-item">
                    <span class="memory-kind kind-{mem.Kind}">{mem.Kind}</span>
                    <span class="memory-title">{mem.Title}</span>
                    <span class="memory-importance">imp {mem.Importance.toFixed(2)}</span>
                  </li>
                {/each}
              </ul>
            {:else if detail}
              <p class="detail-empty">No memory entries yet.</p>
            {:else}
              <p class="detail-empty">Loading detail…</p>
            {/if}
          </div>
        {/if}
      </article>
    {/each}
  </div>
</PanelShell>

<style>
  .squad-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(20rem, 1fr));
    gap: 0.75rem;
    padding: 0.5rem 0.25rem;
  }
  .squad-card {
    background: var(--bg-subtle, #1a1f2a);
    border: 1px solid var(--border-subtle, #233);
    border-radius: 6px;
    padding: 0.75rem 0.9rem;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .squad-card.expanded {
    border-color: var(--border-strong, #345);
  }
  .squad-card.disabled {
    opacity: 0.65;
  }
  .squad-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }
  .squad-name-btn {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    background: transparent;
    border: none;
    padding: 0;
    color: var(--text-default, #eef);
    cursor: pointer;
    font-size: 0.95rem;
    font-weight: 600;
  }
  .squad-name-btn:hover .squad-name {
    color: var(--text-link, #8cc8ff);
  }
  .squad-toggle {
    color: var(--text-muted, #889);
    font-size: 0.8rem;
    width: 0.9rem;
    text-align: center;
  }
  .squad-badge {
    font-size: 0.7rem;
    padding: 0.05rem 0.4rem;
    border-radius: 3px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .badge-disabled {
    background: rgba(220, 80, 80, 0.15);
    color: rgb(240, 130, 130);
  }
  .badge-recursion {
    background: rgba(120, 200, 240, 0.15);
    color: rgb(160, 220, 250);
  }
  .squad-metrics {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 0.5rem;
  }
  .metric {
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
  }
  .metric-label {
    font-size: 0.7rem;
    color: var(--text-muted, #889);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .metric-value {
    font-size: 1.1rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .metric-sub {
    font-size: 0.7rem;
    color: var(--text-muted, #889);
  }
  .rate-good { color: rgb(120, 220, 160); }
  .rate-warn { color: rgb(240, 220, 120); }
  .rate-bad  { color: rgb(240, 130, 130); }
  .rate-empty { color: var(--text-muted, #889); }
  .squad-detail {
    border-top: 1px solid var(--border-subtle, #233);
    padding-top: 0.5rem;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .detail-heading {
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted, #889);
    margin: 0;
  }
  .detail-empty {
    font-size: 0.8rem;
    color: var(--text-muted, #889);
    margin: 0;
  }
  .memory-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
  }
  .memory-item {
    display: grid;
    grid-template-columns: auto 1fr auto;
    gap: 0.5rem;
    align-items: baseline;
    font-size: 0.8rem;
  }
  .memory-kind {
    font-size: 0.65rem;
    padding: 0.05rem 0.35rem;
    border-radius: 3px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    background: var(--bg-default, #233);
    color: var(--text-muted, #aab);
  }
  .kind-merge      { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .kind-tech_debt  { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }
  .kind-convention { background: rgba(180, 140, 240, 0.15); color: rgb(210, 180, 250); }
  .kind-followup   { background: rgba(120, 200, 240, 0.15); color: rgb(160, 220, 250); }
  .memory-title {
    color: var(--text-default, #eef);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .memory-importance {
    color: var(--text-muted, #889);
    font-size: 0.7rem;
    font-variant-numeric: tabular-nums;
  }
</style>
