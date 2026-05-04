<script lang="ts">
  import { millsStore } from '../../stores/mills.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    millsStore.startPolling(15000);
    return () => { millsStore.stopPolling(); };
  });

  let scores = $derived(millsStore.evalScores);
  let loading = $derived(millsStore.loading && millsStore.evalScores.length === 0);
  let disabled = $derived(millsStore.disabled);
  let error = $derived(millsStore.error);

  // Group by Loop letter so Loop A / B / C trends show separately.
  let byLoop = $derived.by(() => {
    const out: Record<string, { count: number; mean: number; latest: number | null }> = {};
    for (const s of scores) {
      const k = s.Loop ?? '?';
      if (!out[k]) out[k] = { count: 0, mean: 0, latest: null };
      out[k].count += 1;
      out[k].mean = (out[k].mean * (out[k].count - 1) + s.Score) / out[k].count;
      out[k].latest = s.Score;
    }
    return out;
  });

  function fmtTime(ts?: string): string {
    if (!ts) return '—';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    return d.toLocaleString();
  }
  function fmtScore(s: number): string {
    return s.toFixed(3);
  }
</script>

<PanelShell
  title="Eval"
  icon="✓"
  count={scores.length}
  loading={loading}
  empty={scores.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled ? 'Mills operator not configured' : (error ? 'Failed to load eval scores' : 'No eval scores yet')}
  emptyHint={disabled ? 'Set LOOM_MILLS_OPERATOR_URL on the HUD to connect.' : (error ?? 'Loop A scores artifacts; Loop B scores merges; Loop C runs weekly.')}
>
  {#snippet header()}
    <div class="loop-row">
      {#each Object.entries(byLoop) as [loop, agg]}
        <span class="loop-pill">
          Loop {loop}: <strong>{fmtScore(agg.mean)}</strong> mean ({agg.count})
        </span>
      {/each}
    </div>
  {/snippet}

  <table class="mills-table">
    <thead>
      <tr>
        <th>Loop</th>
        <th>Subject</th>
        <th>Score</th>
        <th>Notes</th>
        <th>When</th>
      </tr>
    </thead>
    <tbody>
      {#each scores as s (s.ID)}
        <tr>
          <td><span class="loop loop-{s.Loop}">{s.Loop}</span></td>
          <td class="mono">{s.Subject}</td>
          <td>{fmtScore(s.Score)}</td>
          <td>{s.Notes ?? ''}</td>
          <td>{fmtTime(s.CreatedAt)}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</PanelShell>

<style>
  .loop-row { display: flex; gap: 0.5rem; flex-wrap: wrap; font-size: 0.8rem; }
  .loop-pill {
    padding: 0.1rem 0.5rem; border-radius: 999px;
    background: var(--bg-subtle, #233); color: var(--text-muted, #aab);
  }
  .loop-pill strong { color: var(--text-default, #eef); }
  .mills-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .mills-table th, .mills-table td {
    text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-subtle, #233);
  }
  .mills-table th { font-weight: 600; color: var(--text-muted, #889); }
  .mono { font-family: ui-monospace, monospace; }
  .loop {
    display: inline-block; min-width: 1.2rem; text-align: center;
    padding: 0.05rem 0.35rem; border-radius: 3px; font-size: 0.75rem;
    font-weight: 600;
  }
  .loop-A { background: rgba(120, 200, 240, 0.15); color: rgb(160, 220, 250); }
  .loop-B { background: rgba(180, 140, 240, 0.15); color: rgb(210, 180, 250); }
  .loop-C { background: rgba(240, 180, 100, 0.15); color: rgb(250, 210, 150); }
</style>
