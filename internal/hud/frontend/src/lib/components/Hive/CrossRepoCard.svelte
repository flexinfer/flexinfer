<script lang="ts">
  import { hiveCrossRepoStore } from '../../stores/hive_crossrepo.svelte.ts';
  import { inFlightStates, terminalStates, type CrossRepoRun, type CrossRepoState } from '../../stores/hive_crossrepo_types.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    hiveCrossRepoStore.startPolling(15000);
    return () => { hiveCrossRepoStore.stopPolling(); };
  });

  let runs = $derived(hiveCrossRepoStore.runs);
  let loading = $derived(hiveCrossRepoStore.loading && runs.length === 0);
  let disabled = $derived(hiveCrossRepoStore.disabled);
  let storeError = $derived(hiveCrossRepoStore.error);
  let atomicityRate = $derived(hiveCrossRepoStore.atomicityRate);
  let inFlightCount = $derived(hiveCrossRepoStore.inFlightCount);
  let mergedToday = $derived(hiveCrossRepoStore.mergedTodayCount);
  let revertedToday = $derived(hiveCrossRepoStore.revertedTodayCount);

  let expanded = $state<string | null>(null);
  let actionError = $state<string | null>(null);
  let confirming = $state<string | null>(null);
  let aborting = $state<string | null>(null);

  function toggle(id: string): void {
    expanded = expanded === id ? null : id;
  }

  function rateClass(rate: number | null): string {
    if (rate == null) return 'rate-empty';
    if (rate >= 0.9) return 'rate-good';
    if (rate >= 0.7) return 'rate-warn';
    return 'rate-bad';
  }

  function fmtPct(v: number | null): string {
    if (v == null || !Number.isFinite(v)) return '—';
    return `${(v * 100).toFixed(1)}%`;
  }

  function relTime(iso: string): string {
    const t = Date.parse(iso);
    if (!Number.isFinite(t)) return '—';
    const diff = Date.now() - t;
    const s = Math.floor(diff / 1000);
    if (s < 60) return `${s}s ago`;
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    const d = Math.floor(h / 24);
    return `${d}d ago`;
  }

  function trunc(s: string, n: number): string {
    if (!s) return '—';
    return s.length <= n ? s : s.slice(0, n - 1) + '…';
  }

  function reposChips(run: CrossRepoRun): string[] {
    return run.repos.map((r) => r.repo_name || `p${r.project_id}`);
  }

  function isInFlight(state: CrossRepoState): boolean {
    return inFlightStates.has(state);
  }

  function isTerminal(state: CrossRepoState): boolean {
    return terminalStates.has(state);
  }

  function stateBadgeClass(state: CrossRepoState): string {
    return `state-badge state-${state}`;
  }

  async function requestAbort(run: CrossRepoRun): Promise<void> {
    if (isTerminal(run.state)) return;
    confirming = run.id;
  }

  async function confirmAbort(run: CrossRepoRun): Promise<void> {
    actionError = null;
    aborting = run.id;
    try {
      const token = globalThis.prompt(
        `Admin token to abort ${run.id}? (sent as Bearer; leave blank to cancel)`,
        '',
      );
      if (!token) {
        confirming = null;
        return;
      }
      await hiveCrossRepoStore.abort(run.id, token);
      confirming = null;
      await hiveCrossRepoStore.refresh();
    } catch (e) {
      actionError = e instanceof Error ? e.message : String(e);
    } finally {
      aborting = null;
    }
  }

  function cancelAbort(): void {
    confirming = null;
  }
</script>

<PanelShell
  title="Cross-Repo"
  icon="⇄"
  count={runs.length}
  loading={loading}
  empty={runs.length === 0}
  emptyIcon={disabled ? '◯' : '□'}
  emptyMessage={disabled
    ? 'Hive operator not configured'
    : (storeError ? 'Failed to load cross-repo runs' : 'No cross-repo runs yet')}
  emptyHint={disabled
    ? 'Set LOOM_HIVE_OPERATOR_URL on the HUD to connect.'
    : (storeError ?? 'Cross-repo coordination is gated by policy.cross_repo.enabled in operator config.')}
>
  <div class="kpi-row">
    <div class="kpi">
      <span class="kpi-label">atomicity rate</span>
      <span class="kpi-value {rateClass(atomicityRate)}">{fmtPct(atomicityRate)}</span>
      <span class="kpi-sub">last 30 terminal runs</span>
    </div>
    <div class="kpi">
      <span class="kpi-label">in flight</span>
      <span class="kpi-value">{inFlightCount}</span>
      <span class="kpi-sub">open / gates_green / merging</span>
    </div>
    <div class="kpi">
      <span class="kpi-label">merged today</span>
      <span class="kpi-value rate-good">{mergedToday}</span>
      <span class="kpi-sub">since 00:00 local</span>
    </div>
    <div class="kpi">
      <span class="kpi-label">reverted today</span>
      <span class="kpi-value" class:rate-bad={revertedToday > 0}>{revertedToday}</span>
      <span class="kpi-sub">since 00:00 local</span>
    </div>
  </div>

  {#if actionError}
    <div class="action-error" role="alert">{actionError}</div>
  {/if}

  <table class="runs-table">
    <thead>
      <tr>
        <th>state</th>
        <th>id</th>
        <th>backlog</th>
        <th>repos</th>
        <th>created</th>
        <th class="col-actions">actions</th>
      </tr>
    </thead>
    <tbody>
      {#each runs as run (run.id)}
        <tr
          class="run-row"
          class:expanded={expanded === run.id}
          class:terminal={isTerminal(run.state)}
        >
          <td><span class={stateBadgeClass(run.state)}>{run.state}</span></td>
          <td class="mono">
            <button
              type="button"
              class="row-toggle"
              onclick={() => toggle(run.id)}
              aria-expanded={expanded === run.id}
            >
              <span class="toggle-arrow">{expanded === run.id ? '▾' : '▸'}</span>
              {trunc(run.id, 8)}
            </button>
          </td>
          <td class="mono">{trunc(run.backlog_item_id, 18)}</td>
          <td>
            <span class="repos-chips">
              {#each reposChips(run) as name (name)}
                <span class="repo-chip">{name}</span>
              {/each}
            </span>
          </td>
          <td class="dim">{relTime(run.created_at)}</td>
          <td class="col-actions">
            {#if confirming === run.id}
              <span class="confirm-strip">
                <button type="button" class="btn-danger" disabled={aborting === run.id}
                  onclick={() => confirmAbort(run)}>
                  {aborting === run.id ? 'aborting…' : 'confirm'}
                </button>
                <button type="button" class="btn-quiet" onclick={cancelAbort}>cancel</button>
              </span>
            {:else if !isTerminal(run.state)}
              <button type="button" class="btn-quiet" onclick={() => requestAbort(run)}>abort</button>
            {:else}
              <span class="dim">—</span>
            {/if}
          </td>
        </tr>
        {#if expanded === run.id}
          <tr class="run-detail-row">
            <td colspan="6">
              <div class="run-detail">
                <div class="detail-meta">
                  <span><strong>atomicity</strong>: {run.atomicity_strategy || '—'}</span>
                  <span><strong>updated</strong>: {relTime(run.updated_at)}</span>
                </div>
                {#if confirming === run.id}
                  <p class="confirm-line">
                    Abort cross-repo run {run.id}? This marks it failed; per-repo MRs are NOT closed.
                  </p>
                {/if}
                <table class="detail-table">
                  <thead>
                    <tr><th>repo</th><th>project</th><th>branch</th><th>mr</th><th>ci</th><th>gate</th></tr>
                  </thead>
                  <tbody>
                    {#each run.repos as repo (repo.project_id + ':' + repo.branch)}
                      <tr>
                        <td>{repo.repo_name || '—'}</td>
                        <td class="mono">{repo.project_id}</td>
                        <td class="mono">{repo.branch}</td>
                        <td class="mono">{repo.mr_iid != null ? `!${repo.mr_iid}` : '—'}</td>
                        <td>{repo.ci_status || '—'}</td>
                        <td>{repo.gate_status || '—'}</td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            </td>
          </tr>
        {/if}
      {/each}
    </tbody>
  </table>
</PanelShell>

<style>
  .kpi-row {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
    gap: 0.6rem;
    padding: 0.5rem 0.25rem 0.75rem;
  }
  .kpi {
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
    background: var(--bg-subtle, #1a1f2a);
    border: 1px solid var(--border-subtle, #233);
    border-radius: 6px;
    padding: 0.5rem 0.75rem;
  }
  .kpi-label {
    font-size: 0.7rem;
    color: var(--text-muted, #889);
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }
  .kpi-value {
    font-size: 1.4rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .kpi-sub {
    font-size: 0.7rem;
    color: var(--text-muted, #889);
  }
  .rate-good  { color: rgb(120, 220, 160); }
  .rate-warn  { color: rgb(240, 220, 120); }
  .rate-bad   { color: rgb(240, 130, 130); }
  .rate-empty { color: var(--text-muted, #889); }

  .runs-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.85rem;
  }
  .runs-table thead th {
    text-align: left;
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted, #889);
    border-bottom: 1px solid var(--border-subtle, #233);
    padding: 0.35rem 0.5rem;
    font-weight: 500;
  }
  .runs-table tbody td {
    padding: 0.4rem 0.5rem;
    border-bottom: 1px solid var(--border-subtle, #1d232c);
    vertical-align: middle;
  }
  .run-row.terminal {
    opacity: 0.75;
  }
  .run-row.expanded {
    background: var(--bg-subtle, #1a1f2a);
  }
  .col-actions {
    text-align: right;
    width: 8rem;
  }
  .row-toggle {
    background: transparent;
    border: none;
    color: var(--text-default, #eef);
    cursor: pointer;
    padding: 0;
    font: inherit;
    font-family: ui-monospace, SFMono-Regular, monospace;
    display: inline-flex;
    align-items: center;
    gap: 0.35rem;
  }
  .row-toggle:hover { color: var(--text-link, #8cc8ff); }
  .toggle-arrow { color: var(--text-muted, #889); width: 0.9rem; text-align: center; }
  .mono { font-family: ui-monospace, SFMono-Regular, monospace; }
  .dim  { color: var(--text-muted, #889); font-size: 0.78rem; }

  .state-badge {
    display: inline-block;
    font-size: 0.7rem;
    padding: 0.1rem 0.45rem;
    border-radius: 3px;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    font-weight: 600;
    background: var(--bg-default, #233);
    color: var(--text-muted, #aab);
  }
  .state-planning    { background: rgba(180, 140, 240, 0.15); color: rgb(210, 180, 250); }
  .state-open        { background: rgba(120, 200, 240, 0.15); color: rgb(160, 220, 250); }
  .state-gates_green { background: rgba(120, 200, 160, 0.18); color: rgb(160, 230, 200); }
  .state-merging     { background: rgba(220, 180, 80, 0.18);  color: rgb(245, 215, 130); }
  .state-merged      { background: rgba(72, 200, 128, 0.15);  color: rgb(120, 220, 160); }
  .state-reverted    { background: rgba(220, 140, 60, 0.18);  color: rgb(240, 180, 100); }
  .state-failed      { background: rgba(220, 80, 80, 0.18);   color: rgb(240, 130, 130); }

  .repos-chips { display: inline-flex; flex-wrap: wrap; gap: 0.25rem; }
  .repo-chip {
    font-size: 0.72rem;
    padding: 0.05rem 0.4rem;
    border-radius: 3px;
    background: var(--bg-default, #233);
    color: var(--text-default, #cde);
    font-family: ui-monospace, SFMono-Regular, monospace;
  }

  .btn-quiet, .btn-danger {
    font-size: 0.75rem;
    padding: 0.2rem 0.6rem;
    border-radius: 3px;
    border: 1px solid var(--border-subtle, #233);
    background: transparent;
    color: var(--text-default, #eef);
    cursor: pointer;
  }
  .btn-quiet:hover { border-color: var(--border-strong, #345); }
  .btn-danger {
    background: rgba(220, 80, 80, 0.18);
    border-color: rgba(220, 80, 80, 0.4);
    color: rgb(245, 175, 175);
  }
  .btn-danger:disabled { opacity: 0.6; cursor: progress; }
  .confirm-strip { display: inline-flex; gap: 0.35rem; }

  .run-detail-row td {
    background: var(--bg-default, #131720);
    padding: 0.5rem 1rem 0.75rem;
  }
  .run-detail {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }
  .detail-meta {
    display: flex;
    gap: 1.5rem;
    font-size: 0.75rem;
    color: var(--text-muted, #889);
  }
  .confirm-line {
    margin: 0;
    font-size: 0.8rem;
    color: rgb(245, 215, 130);
  }
  .detail-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 0.78rem;
  }
  .detail-table th {
    text-align: left;
    font-size: 0.65rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted, #889);
    border-bottom: 1px solid var(--border-subtle, #233);
    padding: 0.25rem 0.5rem;
    font-weight: 500;
  }
  .detail-table td {
    padding: 0.25rem 0.5rem;
    border-bottom: 1px solid var(--border-subtle, #1d232c);
  }
  .action-error {
    margin: 0 0.25rem 0.5rem;
    padding: 0.4rem 0.6rem;
    background: rgba(220, 80, 80, 0.12);
    border: 1px solid rgba(220, 80, 80, 0.35);
    border-radius: 4px;
    color: rgb(245, 175, 175);
    font-size: 0.8rem;
  }
</style>
