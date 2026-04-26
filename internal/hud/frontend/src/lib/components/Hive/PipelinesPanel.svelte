<script lang="ts">
  import { hiveStore } from '../../stores/hive.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    hiveStore.startPolling(15000);
    return () => { hiveStore.stopPolling(); };
  });

  let runs = $derived(hiveStore.pipelineRuns);
  let counts = $derived(hiveStore.pipelinesByState);
  let loading = $derived(hiveStore.loading && hiveStore.pipelineRuns.length === 0);
  let disabled = $derived(hiveStore.disabled);
  let error = $derived(hiveStore.error);

  function fmtTime(ts?: string): string {
    if (!ts) return '—';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    return d.toLocaleTimeString();
  }
</script>

<PanelShell
  title="Pipelines"
  icon="⛓"
  count={runs.length}
  loading={loading}
  empty={runs.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled ? 'Hive operator not configured' : (error ? 'Failed to load pipeline runs' : 'No pipeline runs yet')}
  emptyHint={disabled ? 'Set LOOM_HIVE_OPERATOR_URL on the HUD to connect.' : (error ?? 'Once the council queues backlog items, runs will land here.')}
>
  {#snippet header()}
    <div class="counts-row">
      {#each Object.entries(counts) as [state, n]}
        <span class="count-pill state-{state}">{state}: {n}</span>
      {/each}
    </div>
  {/snippet}

  <table class="hive-table">
    <thead>
      <tr>
        <th>Run ID</th>
        <th>Backlog</th>
        <th>State</th>
        <th>Template</th>
        <th>Attempts</th>
        <th>Started</th>
        <th>Ended</th>
      </tr>
    </thead>
    <tbody>
      {#each runs as r (r.ID)}
        <tr>
          <td class="mono">{r.ID}</td>
          <td class="mono">{r.BacklogID}</td>
          <td><span class="state state-{r.State}">{r.State}</span></td>
          <td>{r.Template}</td>
          <td>{r.Attempts}</td>
          <td>{fmtTime(r.StartedAt)}</td>
          <td>{fmtTime(r.EndedAt)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</PanelShell>

<style>
  .counts-row { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .count-pill {
    padding: 0.1rem 0.5rem; border-radius: 999px; font-size: 0.75rem;
    background: var(--bg-subtle, #233); color: var(--text-muted, #aab);
  }
  .hive-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .hive-table th, .hive-table td {
    text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-subtle, #233);
  }
  .hive-table th { font-weight: 600; color: var(--text-muted, #889); }
  .mono { font-family: ui-monospace, monospace; }
  .state { padding: 0.1rem 0.4rem; border-radius: 3px; font-size: 0.75rem; }
  .state-queued   { background: var(--bg-subtle, #233); color: var(--text-muted, #aab); }
  .state-running, .state-planning, .state-slicing, .state-implementing, .state-testing, .state-reviewing {
    background: rgba(64, 144, 240, 0.15); color: rgb(120, 180, 240);
  }
  .state-merged   { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .state-failed   { background: rgba(220, 80, 80, 0.15); color: rgb(240, 130, 130); }
  .state-escalated { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }
  .state-paused   { background: rgba(180, 180, 60, 0.15); color: rgb(220, 220, 120); }
</style>
