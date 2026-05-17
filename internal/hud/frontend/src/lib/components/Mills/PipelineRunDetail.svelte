<script lang="ts">
  // PipelineRunDetail — right-edge drawer showing the full
  // {run, stages, gates} payload from GET /api/mills/pipeline/runs/{id}
  // for the currently selected pipeline run. Driven entirely off
  // millsStore.selectedRunID + millsStore.openPipelineDetail so the
  // drawer is reactive to background refresh ticks without re-wiring.

  import {
    millsStore,
    type GateOutcome,
    type StageResult,
  } from '../../stores/mills.svelte.ts';

  let load = $derived(millsStore.openPipelineDetail);
  let open = $derived(millsStore.selectedRunID !== null);
  let detail = $derived(load && load.status === 'loaded' ? load.detail : null);

  // expandedStages tracks which stage rows have the log+artifacts
  // panel open. Stored locally (not on the store) because expansion
  // is per-view UX state, not data.
  let expandedStages = $state<Set<number>>(new Set());

  function close(): void {
    millsStore.closeRunDetail();
    expandedStages = new Set();
  }

  function handleKeydown(event: KeyboardEvent): void {
    if (event.key === 'Escape' && open) close();
  }

  function toggleStage(id: number): void {
    const next = new Set(expandedStages);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    expandedStages = next;
  }

  function fmtTime(ts?: string | null): string {
    if (!ts) return '—';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return ts;
    return d.toLocaleTimeString();
  }

  function fmtDuration(start?: string | null, end?: string | null): string {
    if (!start) return '—';
    const s = new Date(start).getTime();
    if (isNaN(s)) return '—';
    const e = end ? new Date(end).getTime() : Date.now();
    if (isNaN(e) || e < s) return '—';
    const ms = e - s;
    if (ms < 1000) return `${ms}ms`;
    if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
    const mins = Math.floor(ms / 60_000);
    const secs = Math.floor((ms % 60_000) / 1000);
    return `${mins}m ${secs}s`;
  }

  function fmtCost(usd?: number): string {
    if (usd == null || !Number.isFinite(usd)) return '—';
    if (usd === 0) return '$0';
    if (usd < 0.01) return `<$0.01`;
    return `$${usd.toFixed(2)}`;
  }

  function stageOutcomeLabel(s: StageResult): string {
    if (!s.EndedAt) return 'in flight';
    if (!s.Outcome) return 'pending';
    return s.Outcome;
  }

  function gitlabMRURL(mrIID?: number | null): string | null {
    if (!mrIID) return null;
    // The HUD doesn't know the project path; this matches the existing
    // services/loom-core repo, which is the only project Mills currently
    // drives. Operator + UI co-evolve, so hardcoding is acceptable
    // until cross-repo runs grow beyond loom-core.
    return `https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/${mrIID}`;
  }

  function stageArtifactEntries(s: StageResult): Array<[string, string]> {
    if (!s.Artifacts) return [];
    return Object.entries(s.Artifacts).map(([k, v]) => [
      k,
      typeof v === 'string' ? v : JSON.stringify(v),
    ]);
  }

  function gateOutcomeRank(o: GateOutcome['Outcome']): number {
    // Sort fails first, then skips, then passes — fails are what the
    // user opened the drawer for.
    if (o === 'fail') return 0;
    if (o === 'skip') return 1;
    return 2;
  }

  let sortedGates = $derived.by(() => {
    if (!detail) return [] as GateOutcome[];
    return [...detail.gates].sort(
      (a, b) => gateOutcomeRank(a.Outcome) - gateOutcomeRank(b.Outcome),
    );
  });

  // copyRunID drops the full run id onto the clipboard so users can
  // share or paste into a terminal without manually selecting the
  // mono text in the table. Falls back silently if clipboard access
  // is denied (e.g. non-HTTPS dev contexts).
  async function copyRunID(): Promise<void> {
    const id = detail?.run.ID;
    if (!id) return;
    try {
      await navigator.clipboard.writeText(id);
    } catch {
      /* swallow — UI feedback isn't worth a toast for a missing perm */
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
  <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
  <div class="run-scrim" onclick={close}></div>
  <aside class="run-drawer" role="dialog" aria-label="Pipeline run detail" aria-modal="true">
    <header class="run-header">
      <div class="run-title">
        <span class="run-kicker">Pipeline Run</span>
        {#if detail}
          <span class="run-state state-{detail.run.State}">{detail.run.State}</span>
        {/if}
      </div>
      <button type="button" class="run-close" onclick={close} aria-label="Close run detail">✕</button>
    </header>

    {#if load?.status === 'loading' && !detail}
      <div class="run-loading">Loading run detail…</div>
    {:else if load?.status === 'error'}
      <div class="run-error">
        <div class="run-error-title">Couldn't load run detail</div>
        <div class="run-error-msg">{load.message}</div>
        <button type="button" class="run-retry" onclick={() => millsStore.openRunDetail(millsStore.selectedRunID ?? '')}>
          Retry
        </button>
      </div>
    {:else if detail}
      <section class="run-summary">
        <dl class="run-meta">
          <div class="run-meta-row">
            <dt>Run ID</dt>
            <dd class="mono run-id">
              <span title={detail.run.ID}>{detail.run.ID}</span>
              <button type="button" class="run-copy" onclick={copyRunID} title="Copy run id">⧉</button>
            </dd>
          </div>
          <div class="run-meta-row">
            <dt>Backlog</dt>
            <dd class="mono">{detail.run.BacklogID}</dd>
          </div>
          <div class="run-meta-row">
            <dt>Template</dt>
            <dd class="mono">{detail.run.Template || '—'}</dd>
          </div>
          <div class="run-meta-row">
            <dt>Current stage</dt>
            <dd>
              {#if detail.run.CurrentStage}
                <span class="stage-chip">{detail.run.CurrentStage}</span>
              {:else}
                <span class="muted">—</span>
              {/if}
            </dd>
          </div>
          <div class="run-meta-row">
            <dt>Started</dt>
            <dd class="mono">{fmtTime(detail.run.StartedAt)}</dd>
          </div>
          <div class="run-meta-row">
            <dt>Ended</dt>
            <dd class="mono">{fmtTime(detail.run.EndedAt)}</dd>
          </div>
          <div class="run-meta-row">
            <dt>Duration</dt>
            <dd class="mono">{fmtDuration(detail.run.StartedAt, detail.run.EndedAt)}</dd>
          </div>
          <div class="run-meta-row">
            <dt>Attempts</dt>
            <dd class="mono">{detail.run.Attempts}</dd>
          </div>
          <div class="run-meta-row">
            <dt>Cost</dt>
            <dd class="mono cost">{fmtCost(detail.run.CostUSD)}</dd>
          </div>
          {#if detail.run.WorktreePath}
            <div class="run-meta-row">
              <dt>Worktree</dt>
              <dd class="mono path" title={detail.run.WorktreePath}>{detail.run.WorktreePath}</dd>
            </div>
          {/if}
          {#if detail.run.MRIID}
            <div class="run-meta-row">
              <dt>Merge Request</dt>
              <dd class="mono">
                <a href={gitlabMRURL(detail.run.MRIID)} target="_blank" rel="noreferrer noopener">
                  !{detail.run.MRIID}
                </a>
              </dd>
            </div>
          {/if}
          {#if detail.run.ParentRunID}
            <div class="run-meta-row">
              <dt>Parent run</dt>
              <dd class="mono path" title={detail.run.ParentRunID}>{detail.run.ParentRunID}</dd>
            </div>
          {/if}
        </dl>
      </section>

      <section class="run-section">
        <h3 class="run-section-title">
          Stages
          <span class="section-count">{detail.stages.length}</span>
        </h3>
        {#if detail.stages.length === 0}
          <div class="section-empty">No stage attempts recorded yet.</div>
        {:else}
          <ol class="stage-list">
            {#each detail.stages as stage (stage.ID)}
              {@const expanded = expandedStages.has(stage.ID)}
              {@const artifacts = stageArtifactEntries(stage)}
              <li class="stage-row" data-outcome={stage.Outcome ?? 'pending'}>
                <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
                <button
                  type="button"
                  class="stage-head"
                  aria-expanded={expanded}
                  onclick={() => toggleStage(stage.ID)}
                >
                  <span class="stage-glyph">{expanded ? '▾' : '▸'}</span>
                  <span class="stage-name">{stage.Stage}</span>
                  <span class="stage-attempt">try {stage.Attempt}</span>
                  <span class="stage-outcome o-{stage.Outcome ?? 'pending'}">
                    {stageOutcomeLabel(stage)}
                  </span>
                  <span class="stage-duration mono">{fmtDuration(stage.StartedAt, stage.EndedAt)}</span>
                  <span class="stage-cost mono">{fmtCost(stage.CostUSD)}</span>
                </button>
                {#if expanded}
                  <div class="stage-body">
                    <div class="stage-times mono">
                      {fmtTime(stage.StartedAt)} → {fmtTime(stage.EndedAt)}
                      {#if stage.SpawnID}
                        <span class="stage-spawn" title="Spawn id">spawn: {stage.SpawnID}</span>
                      {/if}
                    </div>
                    {#if artifacts.length > 0}
                      <div class="stage-artifacts">
                        <div class="kv-title">Artifacts</div>
                        <dl class="kv">
                          {#each artifacts as [k, v]}
                            <dt>{k}</dt>
                            <dd class="mono">{v}</dd>
                          {/each}
                        </dl>
                      </div>
                    {/if}
                    {#if stage.LogTail && stage.LogTail.trim().length > 0}
                      <div class="stage-logs">
                        <div class="kv-title">Log tail</div>
                        <pre class="logtail">{stage.LogTail}</pre>
                      </div>
                    {/if}
                    {#if artifacts.length === 0 && (!stage.LogTail || stage.LogTail.trim().length === 0)}
                      <div class="stage-empty-detail">No artifacts or log captured for this attempt.</div>
                    {/if}
                  </div>
                {/if}
              </li>
            {/each}
          </ol>
        {/if}
      </section>

      <section class="run-section">
        <h3 class="run-section-title">
          Gates
          <span class="section-count">{detail.gates.length}</span>
        </h3>
        {#if detail.gates.length === 0}
          <div class="section-empty">No gate evaluations yet.</div>
        {:else}
          <ul class="gate-list">
            {#each sortedGates as gate (gate.ID)}
              <li class="gate-row" data-outcome={gate.Outcome}>
                <div class="gate-head">
                  <span class="gate-outcome o-{gate.Outcome}">{gate.Outcome}</span>
                  <span class="gate-name">{gate.GateName}</span>
                  <span class="gate-after mono">after {gate.AfterStage}</span>
                  <span class="gate-time mono">{fmtTime(gate.EvaluatedAt)}</span>
                </div>
                {#if gate.JudgedBy}
                  <div class="gate-judge mono">judged by {gate.JudgedBy}</div>
                {/if}
                {#if gate.Reasons && gate.Reasons.length > 0}
                  <ul class="gate-reasons">
                    {#each gate.Reasons as reason}
                      <li>{reason}</li>
                    {/each}
                  </ul>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/if}
  </aside>
{/if}

<style>
  .run-scrim {
    position: fixed;
    inset: 0;
    background: rgba(6, 12, 16, 0.55);
    z-index: 900;
    animation: fadeIn 0.15s ease-out;
  }

  .run-drawer {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    width: min(640px, 96vw);
    background: var(--bg-secondary);
    border-left: 1px solid var(--border);
    box-shadow: -16px 0 32px rgba(0, 0, 0, 0.4);
    z-index: 901;
    display: flex;
    flex-direction: column;
    animation: slideIn 0.18s ease-out;
    overflow-y: auto;
  }

  .run-header {
    position: sticky;
    top: 0;
    z-index: 1;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    border-bottom: 1px solid var(--border);
    background: var(--bg-secondary);
  }

  .run-title {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .run-kicker {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .run-state {
    padding: 0.1rem 0.45rem;
    border-radius: 3px;
    font-size: 0.72rem;
    font-family: ui-monospace, monospace;
    background: var(--bg-tertiary);
    color: var(--fg-muted);
  }
  .run-state.state-queued   { background: var(--bg-subtle, #233); color: var(--text-muted, #aab); }
  .run-state.state-running,
  .run-state.state-planning,
  .run-state.state-slicing,
  .run-state.state-implementing,
  .run-state.state-testing,
  .run-state.state-reviewing,
  .run-state.state-mr,
  .run-state.state-ci,
  .run-state.state-merging {
    background: rgba(64, 144, 240, 0.15); color: rgb(120, 180, 240);
  }
  .run-state.state-merged    { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .run-state.state-done      { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .run-state.state-failed    { background: rgba(220, 80, 80, 0.15); color: rgb(240, 130, 130); }
  .run-state.state-escalated { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }
  .run-state.state-paused    { background: rgba(180, 180, 60, 0.15); color: rgb(220, 220, 120); }

  .run-close {
    padding: 4px 10px;
    border: 1px solid var(--border);
    background: transparent;
    color: var(--fg-secondary);
    border-radius: var(--radius-xs);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
  }
  .run-close:hover {
    color: var(--fg-primary);
    border-color: var(--border-active);
    background: var(--bg-tertiary);
  }

  .run-loading,
  .run-error,
  .section-empty {
    padding: var(--space-4);
    font-size: var(--text-sm);
    color: var(--fg-muted);
  }
  .run-error-title { color: var(--error, rgb(240, 130, 130)); font-weight: 600; }
  .run-error-msg { margin-top: 0.4rem; font-family: ui-monospace, monospace; font-size: var(--text-xs); }
  .run-retry {
    margin-top: 0.8rem;
    padding: 4px 10px;
    border: 1px solid var(--border);
    background: transparent;
    color: var(--fg-secondary);
    border-radius: var(--radius-xs);
    cursor: pointer;
  }

  .run-summary {
    padding: var(--space-3) var(--space-4);
    border-bottom: 1px solid var(--border-subtle);
  }

  .run-meta {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.35rem 0.9rem;
    margin: 0;
  }
  .run-meta-row {
    display: contents;
  }
  .run-meta dt {
    color: var(--text-muted, #889);
    font-size: 0.78rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }
  .run-meta dd {
    margin: 0;
    color: var(--fg-primary);
    font-size: 0.85rem;
    min-width: 0;
  }
  .run-meta dd.path {
    word-break: break-all;
  }
  .run-meta dd a {
    color: rgb(120, 180, 240);
    text-decoration: none;
  }
  .run-meta dd a:hover {
    text-decoration: underline;
  }
  .run-id {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    word-break: break-all;
  }
  .run-copy {
    background: transparent;
    border: 1px solid var(--border-subtle);
    color: var(--text-muted);
    border-radius: var(--radius-xs);
    padding: 1px 6px;
    cursor: pointer;
    font-size: 0.8rem;
  }
  .run-copy:hover {
    color: var(--fg-primary);
    border-color: var(--border-active);
  }
  .cost { color: rgb(220, 200, 140); }
  .stage-chip {
    padding: 0.05rem 0.4rem;
    border-radius: 3px;
    border: 1px solid color-mix(in srgb, var(--accent, #58a) 32%, var(--border-subtle, #233));
    background: color-mix(in srgb, var(--accent, #58a) 10%, transparent);
    color: var(--fg-secondary, #9ab);
    font-family: ui-monospace, monospace;
    font-size: 0.72rem;
  }
  .muted { color: var(--text-muted); }
  .mono { font-family: ui-monospace, monospace; }

  .run-section {
    padding: var(--space-3) var(--space-4);
    border-bottom: 1px solid var(--border-subtle);
  }
  .run-section:last-child { border-bottom: none; }

  .run-section-title {
    margin: 0 0 0.6rem;
    font-size: 0.85rem;
    font-weight: 600;
    color: var(--fg-primary);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .section-count {
    padding: 0.05rem 0.45rem;
    border-radius: 999px;
    font-size: 0.7rem;
    background: var(--bg-tertiary, #233);
    color: var(--text-muted, #889);
    font-family: ui-monospace, monospace;
  }

  .stage-list,
  .gate-list {
    list-style: none;
    padding: 0;
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }

  .stage-row {
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-xs, 3px);
    background: var(--bg-primary, #0d1417);
    overflow: hidden;
  }
  .stage-row[data-outcome="error"]     { border-color: rgba(220, 80, 80, 0.4); }
  .stage-row[data-outcome="gate_fail"] { border-color: rgba(220, 140, 60, 0.4); }
  .stage-row[data-outcome="success"]   { border-color: rgba(72, 200, 128, 0.3); }

  .stage-head {
    width: 100%;
    background: transparent;
    border: none;
    padding: 0.5rem 0.7rem;
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto auto auto auto;
    gap: 0.55rem;
    align-items: center;
    color: var(--fg-primary);
    cursor: pointer;
    font-size: 0.82rem;
    text-align: left;
  }
  .stage-head:hover { background: var(--bg-tertiary, #1a242a); }
  .stage-glyph { color: var(--text-muted); font-size: 0.75rem; }
  .stage-name {
    font-family: ui-monospace, monospace;
    color: var(--fg-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .stage-attempt {
    font-size: 0.7rem;
    color: var(--text-muted);
    font-family: ui-monospace, monospace;
  }
  .stage-outcome {
    padding: 0.05rem 0.4rem;
    border-radius: 3px;
    font-size: 0.7rem;
    font-family: ui-monospace, monospace;
  }
  .stage-outcome.o-success   { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .stage-outcome.o-error     { background: rgba(220, 80, 80, 0.15); color: rgb(240, 130, 130); }
  .stage-outcome.o-gate_fail { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }
  .stage-outcome.o-pending   { background: var(--bg-subtle, #233); color: var(--text-muted, #aab); }
  .stage-duration,
  .stage-cost {
    font-size: 0.75rem;
    color: var(--text-muted);
  }

  .stage-body {
    padding: 0.5rem 0.8rem 0.8rem;
    background: rgba(255, 255, 255, 0.015);
    border-top: 1px solid var(--border-subtle);
    display: flex;
    flex-direction: column;
    gap: 0.55rem;
  }
  .stage-times {
    font-size: 0.75rem;
    color: var(--text-muted);
    display: flex;
    gap: 0.8rem;
    flex-wrap: wrap;
  }
  .stage-spawn {
    color: var(--fg-secondary);
  }
  .kv-title {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-muted);
    margin-bottom: 0.25rem;
  }
  .kv {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 0.2rem 0.6rem;
    margin: 0;
    font-size: 0.78rem;
  }
  .kv dt { color: var(--text-muted); }
  .kv dd {
    margin: 0;
    color: var(--fg-primary);
    word-break: break-all;
  }
  .logtail {
    margin: 0;
    padding: 0.5rem 0.7rem;
    background: var(--bg-primary, #0d1417);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-xs, 3px);
    font-size: 0.74rem;
    color: var(--fg-secondary);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 320px;
    overflow-y: auto;
  }
  .stage-empty-detail {
    font-size: 0.78rem;
    color: var(--text-muted);
    font-style: italic;
  }

  .gate-row {
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-xs, 3px);
    padding: 0.5rem 0.7rem;
    background: var(--bg-primary, #0d1417);
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
  }
  .gate-row[data-outcome="fail"] { border-color: rgba(220, 80, 80, 0.4); }
  .gate-row[data-outcome="skip"] { border-color: rgba(180, 180, 60, 0.4); }

  .gate-head {
    display: grid;
    grid-template-columns: auto minmax(0, 1fr) auto auto;
    gap: 0.55rem;
    align-items: center;
    font-size: 0.82rem;
  }
  .gate-outcome {
    padding: 0.05rem 0.45rem;
    border-radius: 3px;
    font-size: 0.7rem;
    font-family: ui-monospace, monospace;
    text-transform: uppercase;
  }
  .gate-outcome.o-pass { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .gate-outcome.o-fail { background: rgba(220, 80, 80, 0.15); color: rgb(240, 130, 130); }
  .gate-outcome.o-skip { background: rgba(180, 180, 60, 0.15); color: rgb(220, 220, 120); }
  .gate-name {
    font-family: ui-monospace, monospace;
    color: var(--fg-primary);
  }
  .gate-after,
  .gate-time {
    font-size: 0.72rem;
    color: var(--text-muted);
  }
  .gate-judge {
    font-size: 0.72rem;
    color: var(--text-muted);
  }
  .gate-reasons {
    margin: 0.15rem 0 0;
    padding-left: 1rem;
    font-size: 0.78rem;
    color: var(--fg-secondary);
  }
  .gate-reasons li + li { margin-top: 0.15rem; }

  @keyframes fadeIn {
    from { opacity: 0; }
    to   { opacity: 1; }
  }

  @keyframes slideIn {
    from { transform: translateX(100%); }
    to   { transform: translateX(0); }
  }

  @media (max-width: 480px) {
    .run-drawer { width: 100vw; }
  }
</style>
