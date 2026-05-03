<script lang="ts">
  import type { PipelineRun } from '../../stores/hive.svelte.ts';
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

  // sortedRuns groups subruns directly under their parent (Phase 6
  // slice 6.3). Top-level runs are rendered in their original order;
  // each subrun follows its parent so the depth indicator + indent
  // reads as a parent→child tree without a recursive layout.
  let sortedRuns = $derived.by(() => {
    const byParent = new Map<string, PipelineRun[]>();
    const tops: PipelineRun[] = [];
    for (const r of runs) {
      const parent = r.ParentRunID ?? null;
      if (parent) {
        if (!byParent.has(parent)) byParent.set(parent, []);
        byParent.get(parent)!.push(r);
      } else {
        tops.push(r);
      }
    }
    const out: PipelineRun[] = [];
    const visit = (r: PipelineRun) => {
      out.push(r);
      const kids = byParent.get(r.ID);
      if (kids) {
        for (const k of kids) visit(k);
      }
    };
    for (const r of tops) visit(r);
    // Orphans (parent not in current page) — append at the end so
    // they aren't dropped from the panel entirely.
    for (const r of runs) {
      if (!out.includes(r)) out.push(r);
    }
    return out;
  });

  function fmtTime(ts?: string): string {
    if (!ts) return '—';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    return d.toLocaleTimeString();
  }

  function depthIndent(d?: number): string {
    if (!d || d <= 0) return '';
    // 1.25rem per level — keeps the run id visible at depth 3 in a
    // typical column width.
    return `padding-left:${1.25 * d}rem`;
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
        <th>Depth</th>
        <th>Backlog</th>
        <th>State</th>
        <th>Template</th>
        <th>Attempts</th>
        <th>Started</th>
        <th>Ended</th>
      </tr>
    </thead>
    <tbody>
      {#each sortedRuns as r (r.ID)}
        <tr class:subrun={(r.Depth ?? 0) > 0}>
          <td class="mono" style={depthIndent(r.Depth)} title={r.ParentRunID ? `subrun of ${r.ParentRunID}` : 'top-level run'}>
            {#if (r.Depth ?? 0) > 0}<span class="tree-glyph" aria-hidden="true">└─ </span>{/if}{r.ID}
          </td>
          <td>
            {#if (r.Depth ?? 0) > 0}
              <span class="depth-pill" title="recursion depth (parent + 1 per level)">d{r.Depth}</span>
            {:else}
              <span class="depth-pill depth-root">root</span>
            {/if}
          </td>
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
  .subrun { background: rgba(120, 140, 200, 0.04); }
  .tree-glyph { color: var(--text-muted, #889); margin-right: 0.15rem; }
  .depth-pill {
    padding: 0.05rem 0.35rem; border-radius: 3px; font-size: 0.7rem;
    background: rgba(120, 144, 200, 0.18); color: rgb(160, 180, 230);
    font-family: ui-monospace, monospace;
  }
  .depth-pill.depth-root { background: var(--bg-subtle, #233); color: var(--text-muted, #889); }
</style>
