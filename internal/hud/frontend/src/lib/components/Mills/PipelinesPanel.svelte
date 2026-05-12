<script lang="ts">
  import type { PipelineRun } from '../../stores/mills.svelte.ts';
  import { millsStore } from '../../stores/mills.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    millsStore.startPolling(15000);
    return () => { millsStore.stopPolling(); };
  });

  let runs = $derived(millsStore.pipelineRuns);
  let counts = $derived(millsStore.pipelinesByState);
  let loading = $derived(millsStore.loading && millsStore.pipelineRuns.length === 0);
  let disabled = $derived(millsStore.disabled);
  let error = $derived(millsStore.error);
  let autonomyBlocked = $derived(millsStore.autonomyBlocked);
  let blockers = $derived(millsStore.autonomyBlockers);
  let status = $derived(millsStore.status);

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

  function emptyMessage(): string {
    if (disabled) return 'Mills operator not configured';
    if (error) return 'Failed to load pipeline runs';
    if (autonomyBlocked) return 'Mills autonomy is blocked';
    return 'No pipeline runs yet';
  }

  function emptyHint(): string {
    if (disabled) return 'Set LOOM_MILLS_OPERATOR_URL on the HUD to connect.';
    if (error) return error;
    if (autonomyBlocked) return blockers.slice(0, 2).join(' · ');
    if (status?.autonomy_ready) {
      return `Operator ready · queue ${status.queue_depth ?? 0} · active ${status.active_pipeline_runs ?? 0}`;
    }
    return 'Waiting for operator readiness.';
  }
</script>

<PanelShell
  title="Pipelines"
  icon="⛓"
  count={runs.length}
  loading={loading}
  empty={runs.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={emptyMessage()}
  emptyHint={emptyHint()}
>
  {#snippet header()}
    {#if status}
      <div class="readiness-banner" class:ready={status.autonomy_ready} role="status">
        <span class="readiness-kicker">{status.autonomy_ready ? 'Ready' : 'Fail-closed'}</span>
        <span class="readiness-main">{status.autonomy_ready ? 'Autonomy gate passing' : 'Autonomy paused'}</span>
        <span class="readiness-meta">
          queue {status.queue_depth ?? 0} · active runs {status.active_pipeline_runs ?? 0}
        </span>
        {#if autonomyBlocked}
          <ul>
            {#each blockers.slice(0, 3) as blocker}
              <li>{blocker}</li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}
    {#if Object.keys(counts).length > 0}
      <div class="counts-row">
        {#each Object.entries(counts) as [state, n]}
          <span class="count-pill state-{state}">{state}: {n}</span>
        {/each}
      </div>
    {/if}
  {/snippet}

  <table class="mills-table">
    <thead>
      <tr>
        <th>Run ID</th>
        <th>Depth</th>
        <th>Backlog</th>
        <th>State</th>
        <th>Stage</th>
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
          <td>
            {#if r.CurrentStage}
              <span class="stage-chip" title="active stage">{r.CurrentStage}</span>
            {:else}
              <span class="stage-chip stage-empty" title="not yet driving a stage">—</span>
            {/if}
          </td>
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
  .readiness-banner {
    display: grid;
    grid-template-columns: auto auto minmax(0, 1fr);
    gap: 0.35rem 0.75rem;
    align-items: center;
    color: var(--fg-secondary, #9ab);
  }
  .readiness-kicker {
    padding: 0.08rem 0.45rem;
    border-radius: 999px;
    border: 1px solid rgba(240, 130, 80, 0.35);
    color: rgb(240, 150, 105);
    font-size: 0.68rem;
    font-weight: 700;
    text-transform: uppercase;
  }
  .readiness-banner.ready .readiness-kicker {
    border-color: color-mix(in srgb, var(--success) 42%, var(--border));
    color: var(--success);
  }
  .readiness-main {
    color: var(--fg-primary, #dfe);
    font-weight: 700;
  }
  .readiness-meta {
    min-width: 0;
    font-family: ui-monospace, monospace;
    font-size: 0.75rem;
    color: var(--fg-muted, #789);
  }
  .readiness-banner ul {
    grid-column: 1 / -1;
    margin: 0.25rem 0 0;
    padding-left: 1rem;
    font-size: 0.78rem;
  }
  .readiness-banner li + li { margin-top: 0.15rem; }
  .count-pill {
    padding: 0.1rem 0.5rem; border-radius: 999px; font-size: 0.75rem;
    background: var(--bg-subtle, #233); color: var(--text-muted, #aab);
  }
  .mills-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .mills-table th, .mills-table td {
    text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-subtle, #233);
  }
  .mills-table th { font-weight: 600; color: var(--text-muted, #889); }
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

  .stage-chip {
    padding: 0.05rem 0.4rem;
    border-radius: 3px;
    border: 1px solid color-mix(in srgb, var(--accent, #58a) 32%, var(--border-subtle, #233));
    background: color-mix(in srgb, var(--accent, #58a) 10%, transparent);
    color: var(--fg-secondary, #9ab);
    font-family: ui-monospace, monospace;
    font-size: 0.72rem;
    letter-spacing: 0.02em;
    white-space: nowrap;
  }
  .stage-chip.stage-empty {
    border-color: var(--border-subtle, #233);
    background: transparent;
    color: var(--text-muted, #889);
  }
</style>
