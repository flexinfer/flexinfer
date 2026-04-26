<script lang="ts">
  import { hiveStore } from '../../stores/hive.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    hiveStore.startPolling(15000);
    return () => { hiveStore.stopPolling(); };
  });

  let items = $derived(hiveStore.backlog);
  let loading = $derived(hiveStore.loading && hiveStore.backlog.length === 0);
  let disabled = $derived(hiveStore.disabled);
  let error = $derived(hiveStore.error);
</script>

<PanelShell
  title="Backlog"
  icon="☑"
  count={items.length}
  loading={loading}
  empty={items.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled ? 'Hive operator not configured' : (error ? 'Failed to load backlog' : 'No backlog items')}
  emptyHint={disabled ? 'Set LOOM_HIVE_OPERATOR_URL on the HUD to connect.' : (error ?? 'The council has not produced any items yet.')}
>
  <table class="hive-table">
    <thead>
      <tr>
        <th>ID</th>
        <th>State</th>
        <th>P</th>
        <th>Title</th>
        <th>Labels</th>
      </tr>
    </thead>
    <tbody>
      {#each items as item (item.ID)}
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
        </tr>
      {/each}
    </tbody>
  </table>
</PanelShell>

<style>
  .hive-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .hive-table th, .hive-table td {
    text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-subtle, #233);
  }
  .hive-table th { font-weight: 600; color: var(--text-muted, #889); }
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
</style>
