<script lang="ts">
  import { millsStore } from '../../stores/mills.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    millsStore.startPolling(15000);
    return () => { millsStore.stopPolling(); };
  });

  let runs = $derived(millsStore.councilRuns);
  let policy = $derived(millsStore.policy);
  let loading = $derived(millsStore.loading && millsStore.councilRuns.length === 0);
  let disabled = $derived(millsStore.disabled);
  let error = $derived(millsStore.error);

  // Per-run expansion state. Toggling a row triggers a lazy
  // loadDebate() that caches in millsStore.debateByRun, so subsequent
  // expansions on the same row are instant.
  let expanded = $state<Record<string, boolean>>({});

  function toggle(runID: string): void {
    expanded = { ...expanded, [runID]: !expanded[runID] };
    if (expanded[runID]) {
      void millsStore.loadDebate(runID);
    }
  }

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
  function roleLabel(role: string): string {
    switch (role) {
      case 'editor_proposes': return 'Editor proposes';
      case 'reviewer_critiques': return 'Reviewer critiques';
      case 'moderator_decision': return 'Moderator decision';
      case 'editor_revises': return 'Editor revises';
      default: return role;
    }
  }
</script>

<PanelShell
  title="Council"
  icon="◇"
  count={runs.length}
  loading={loading}
  empty={runs.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled ? 'Mills operator not configured' : (error ? 'Failed to load council runs' : 'No council runs yet')}
  emptyHint={disabled ? 'Set LOOM_MILLS_OPERATOR_URL on the HUD to connect.' : (error ?? 'The council fires on cron + roadmap events.')}
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

  <table class="mills-table">
    <thead>
      <tr>
        <th aria-label="expand"></th>
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
        {@const isOpen = !!expanded[r.ID]}
        {@const debate = millsStore.debateByRun[r.ID]}
        <tr class="row-summary" on:click={() => toggle(r.ID)}>
          <td class="expander">
            <span class="caret" class:open={isOpen} aria-hidden="true">▸</span>
          </td>
          <td class="mono">{r.ID}</td>
          <td>{r.Trigger}</td>
          <td><span class="outcome outcome-{r.Outcome}">{r.Outcome}</span></td>
          <td>{fmtCost(r.CostUSD)}</td>
          <td>{fmtTime(r.StartedAt)}</td>
          <td>{fmtTime(r.EndedAt)}</td>
        </tr>
        {#if isOpen}
          <tr class="row-debate">
            <td></td>
            <td colspan="6">
              {#if !debate || debate.status === 'idle' || debate.status === 'loading'}
                <div class="debate-status">Loading debate transcript…</div>
              {:else if debate.status === 'error'}
                <div class="debate-status error">
                  Failed to load debate: {debate.message}
                  <button type="button" on:click|stopPropagation={() => millsStore.loadDebate(r.ID)}>retry</button>
                </div>
              {:else if debate.rounds.length === 0}
                <div class="debate-status muted">No debate ran for this council run (single-pass).</div>
              {:else}
                {@const totalCost = debate.rounds.reduce((s, x) => s + (x.CostUSD ?? 0), 0)}
                <div class="debate-summary">
                  <strong>Debate Rounds</strong>
                  <span class="muted">·</span>
                  <span>{debate.rounds.length} entries</span>
                  <span class="muted">·</span>
                  <span>{fmtCost(totalCost)} total</span>
                </div>
                <ol class="debate-list">
                  {#each debate.rounds as row (row.ID)}
                    <li>
                      <span class="round-pill">R{row.RoundIndex}</span>
                      <span class="role">{roleLabel(row.Role)}</span>
                      <span class="cost">{fmtCost(row.CostUSD)}</span>
                      {#if row.Summary}
                        <span class="summary">{row.Summary}</span>
                      {/if}
                      {#if row.ArtifactDeltas && row.ArtifactDeltas.length > 0}
                        <span class="deltas">
                          {#each row.ArtifactDeltas as d}
                            <code class="delta">
                              {d.action ?? 'edit'} {d.path ?? '?'}{d.line_range ? `:${d.line_range}` : ''}
                            </code>
                          {/each}
                        </span>
                      {/if}
                    </li>
                  {/each}
                </ol>
              {/if}
            </td>
          </tr>
        {/if}
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
  .mills-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
  .mills-table th, .mills-table td {
    text-align: left; padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--border-subtle, #233);
  }
  .mills-table th { font-weight: 600; color: var(--text-muted, #889); }
  .mono { font-family: ui-monospace, monospace; }
  .outcome { padding: 0.1rem 0.4rem; border-radius: 3px; font-size: 0.75rem; }
  .outcome-success  { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .outcome-partial  { background: rgba(220, 200, 60, 0.15); color: rgb(240, 220, 120); }
  .outcome-error    { background: rgba(220, 80, 80, 0.15); color: rgb(240, 130, 130); }
  .outcome-conflict { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }

  .row-summary { cursor: pointer; }
  .row-summary:hover { background: rgba(255, 255, 255, 0.03); }
  .expander { width: 1.2rem; padding-right: 0; }
  .caret { display: inline-block; transition: transform 120ms ease; color: var(--text-muted, #889); }
  .caret.open { transform: rotate(90deg); }
  .row-debate td { background: rgba(255, 255, 255, 0.015); border-bottom: 1px solid var(--border-subtle, #233); }
  .debate-status { padding: 0.5rem 0.25rem; color: var(--text-muted, #889); font-size: 0.85rem; }
  .debate-status.error { color: rgb(240, 130, 130); }
  .debate-status.muted { color: var(--text-muted, #889); }
  .debate-status button {
    margin-left: 0.5rem; background: transparent; border: 1px solid var(--border-subtle, #233);
    color: inherit; cursor: pointer; padding: 0.1rem 0.5rem; border-radius: 3px; font-size: 0.75rem;
  }
  .debate-summary { padding: 0.4rem 0.25rem 0.25rem; font-size: 0.85rem; display: flex; gap: 0.5rem; align-items: center; }
  .debate-summary .muted { color: var(--text-muted, #889); }
  .debate-list {
    list-style: none; padding: 0 0 0.4rem 0.25rem; margin: 0;
    display: flex; flex-direction: column; gap: 0.3rem;
    font-size: 0.85rem;
  }
  .debate-list li { display: flex; gap: 0.5rem; align-items: baseline; flex-wrap: wrap; }
  .round-pill {
    display: inline-block; min-width: 1.6rem; text-align: center;
    background: rgba(120, 160, 220, 0.15); color: rgb(150, 180, 230);
    padding: 0.05rem 0.35rem; border-radius: 3px; font-size: 0.7rem; font-family: ui-monospace, monospace;
  }
  .role { color: var(--text, #ddd); font-weight: 600; }
  .cost { color: var(--text-muted, #889); font-family: ui-monospace, monospace; font-size: 0.8rem; }
  .summary { color: var(--text-muted, #ccc); flex: 1 1 100%; padding-left: 2.1rem; font-size: 0.82rem; }
  .deltas { display: flex; gap: 0.3rem; flex-wrap: wrap; flex: 1 1 100%; padding-left: 2.1rem; }
  .delta {
    background: rgba(255, 255, 255, 0.05); padding: 0.05rem 0.3rem; border-radius: 2px;
    font-size: 0.7rem; color: var(--text-muted, #aaa);
  }
</style>
