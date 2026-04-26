<script lang="ts">
  import { hiveStore } from '../../stores/hive.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    hiveStore.startPolling(15000);
    return () => { hiveStore.stopPolling(); };
  });

  let runs = $derived(hiveStore.councilRuns);
  let policy = $derived(hiveStore.policy);
  let loading = $derived(hiveStore.loading && hiveStore.councilRuns.length === 0);
  let disabled = $derived(hiveStore.disabled);
  let error = $derived(hiveStore.error);

  function fmtTime(ts?: string): string {
    if (!ts) return '—';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    return d.toLocaleString();
  }
  function fmtCost(c?: number): string {
    if (c == null) return '—';
    return `$${c.toFixed(3)}`;
  }
</script>

<PanelShell
  title="Council"
  icon="◇"
  count={runs.length}
  loading={loading}
  empty={runs.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled ? 'Hive operator not configured' : (error ? 'Failed to load council runs' : 'No council runs yet')}
  emptyHint={disabled ? 'Set LOOM_HIVE_OPERATOR_URL on the HUD to connect.' : (error ?? 'The council fires on cron + roadmap events.')}
>
  {#snippet header()}
    <div class="policy-row">
      <span class="kill-switch" class:enabled={policy?.enabled}>
        kill switch: <strong>{policy?.enabled ? 'enabled' : 'disabled'}</strong>
      </span>
      {#if policy?.version != null}
        <span class="policy-version">policy v{policy.version}</span>
      {/if}
    </div>
  {/snippet}

  <table class="hive-table">
    <thead>
      <tr>
        <th>Run ID</th>
        <th>Trigger</th>
        <th>Outcome</th>
        <th>Cost</th>
        <th>Started</th>
        <th>Ended</th>
      </tr>
    </thead>
    <tbody>
      {#each runs as r (r.ID)}
        <tr>
          <td class="mono">{r.ID}</td>
          <td>{r.Trigger}</td>
          <td><span class="outcome outcome-{r.Outcome}">{r.Outcome}</span></td>
          <td>{fmtCost(r.CostUSD)}</td>
          <td>{fmtTime(r.StartedAt)}</td>
          <td>{fmtTime(r.EndedAt)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</PanelShell>

<style>
  .policy-row { display: flex; gap: 0.75rem; align-items: center; font-size: 0.85rem; }
  .kill-switch { color: var(--text-muted, #889); }
  .kill-switch.enabled { color: rgb(120, 220, 160); }
  .kill-switch strong { color: inherit; }
  .policy-version { color: var(--text-muted, #889); font-size: 0.75rem; }
  .hive-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .hive-table th, .hive-table td {
    text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-subtle, #233);
  }
  .hive-table th { font-weight: 600; color: var(--text-muted, #889); }
  .mono { font-family: ui-monospace, monospace; }
  .outcome { padding: 0.1rem 0.4rem; border-radius: 3px; font-size: 0.75rem; }
  .outcome-success  { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .outcome-partial  { background: rgba(220, 200, 60, 0.15); color: rgb(240, 220, 120); }
  .outcome-error    { background: rgba(220, 80, 80, 0.15); color: rgb(240, 130, 130); }
  .outcome-conflict { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }
</style>
