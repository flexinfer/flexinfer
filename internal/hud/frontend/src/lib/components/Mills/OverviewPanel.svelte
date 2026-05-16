<script lang="ts">
  import type { MillsCapabilityRow, SystemHealth } from '../../stores/mills.svelte.ts';
  import { millsStore } from '../../stores/mills.svelte.ts';
  import { router } from '../../stores/router.svelte.ts';
  import MetricCard from '../shared/MetricCard.svelte';
  import PanelShell from '../shared/PanelShell.svelte';

  $effect(() => {
    millsStore.startPolling(15000);
    return () => { millsStore.stopPolling(); };
  });

  let status = $derived(millsStore.status);
  let policy = $derived(millsStore.policy);
  let kpis = $derived(millsStore.kpis);
  let capabilities = $derived(status?.capabilities ?? []);
  let requiredCaps = $derived(capabilities.filter((cap) => cap.required_for_autonomy));
  let optionalWarnings = $derived(capabilities.filter((cap) => !cap.required_for_autonomy && cap.status !== 'green'));
  let requiredGreen = $derived(requiredCaps.filter((cap) => cap.status === 'green').length);
  let requiredTotal = $derived(requiredCaps.length);
  let counts = $derived(capCounts(capabilities));
  let loading = $derived(millsStore.loading && !status);
  let disabled = $derived(millsStore.disabled);
  let error = $derived(millsStore.error);
  let blockers = $derived(millsStore.autonomyBlockers);
  let metrics = $derived(kpis?.metrics ?? {});
  let health = $derived(millsStore.systemHealth);
  // Suppress the banner only when everything is genuinely fine — green
  // health is already conveyed by the existing "Autonomy ready" chip and
  // KPI cards, so stacking another green pill is just visual noise.
  let showBanner = $derived(health.state !== 'healthy');

  function goto(subView: string): void {
    router.navigate('mills', subView);
  }

  // gotoBacklogEscalated mirrors `goto('backlog')` but also drops the
  // escalated-state filter into the URL hash so BacklogPanel can pick it
  // up. We rely on router.navigate's `detail` slot here because the
  // existing BacklogPanel reads from window.location for deep links;
  // worst-case (panel doesn't honor the param yet) it falls through to
  // an unfiltered backlog view, which is still the right next step.
  function gotoBacklogEscalated(): void {
    router.navigate('mills', 'backlog', 'state=escalated');
  }

  function bannerHeadline(h: SystemHealth): string {
    switch (h.state) {
      case 'broken':
        return `${h.escalations_24h} escalated · 0 merged in 24h`;
      case 'in_flight':
        return `${h.active_runs} ${h.active_runs === 1 ? 'pipeline' : 'pipelines'} in flight`;
      case 'idle':
        return 'Council has never run';
      default:
        return '';
    }
  }

  function bannerDetail(h: SystemHealth): string {
    switch (h.state) {
      case 'broken':
        return h.last_successful_merge_at
          ? `Last successful merge: ${fmtTime(h.last_successful_merge_at)}`
          : 'No successful merge on record';
      case 'in_flight':
        return h.queued > 0 ? `${h.queued} queued behind active runs` : 'Pipelines progressing';
      case 'idle':
        return 'Next scheduled: 0 5 * * * UTC. Trigger manually via `loom mills council run`.';
      default:
        return '';
    }
  }

  function bannerActionLabel(h: SystemHealth): string {
    switch (h.state) {
      case 'broken': return 'View escalations';
      case 'in_flight': return 'Open pipelines';
      case 'idle': return 'Open council';
      default: return '';
    }
  }

  function runBannerAction(h: SystemHealth): void {
    switch (h.state) {
      case 'broken':
        gotoBacklogEscalated();
        return;
      case 'in_flight':
        goto('pipelines');
        return;
      case 'idle':
        goto('council');
        return;
    }
  }

  function fmtNumber(v: number | undefined | null): string {
    if (typeof v !== 'number' || !Number.isFinite(v)) return '0';
    return String(v);
  }

  function fmtPct(v: number | undefined): string {
    if (typeof v !== 'number' || !Number.isFinite(v)) return '-';
    return `${(v * 100).toFixed(1)}%`;
  }

  function fmtUSD(v: number | undefined): string {
    if (typeof v !== 'number' || !Number.isFinite(v)) return '-';
    return `$${v.toFixed(v >= 10 ? 1 : 2)}`;
  }

  function fmtTime(ts?: string | null): string {
    if (!ts) return 'none';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return ts;
    return d.toLocaleString([], {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    });
  }

  function capCounts(rows: MillsCapabilityRow[]): { green: number; yellow: number; red: number; unknown: number } {
    const counts = { green: 0, yellow: 0, red: 0, unknown: 0 };
    for (const row of rows) {
      if (row.status === 'green') counts.green += 1;
      else if (row.status === 'yellow') counts.yellow += 1;
      else if (row.status === 'red') counts.red += 1;
      else counts.unknown += 1;
    }
    return counts;
  }

  // capModeLabel returns the short label rendered as the right-hand
  // pill on each capability row. "real" mode at green status is the
  // expected resting state — we suppress the pill in that case so the
  // row reads as just "name · context" instead of "name … REAL". Stub,
  // disabled, and degraded modes still surface so the operator can
  // distinguish a misconfigured capability from a healthy one.
  function capModeLabel(cap: MillsCapabilityRow): string | null {
    const mode = (cap.mode ?? '').toLowerCase();
    const status = (cap.status ?? '').toLowerCase();
    if (!mode) return status === 'green' ? null : (status || 'unknown');
    if (mode === 'real') return status === 'green' ? null : 'degraded';
    if (mode === 'stub' || mode === 'fake' || mode === 'mock') return 'stub';
    if (mode === 'disabled' || mode === 'off') return 'disabled';
    return mode;
  }

  function capModeIntent(cap: MillsCapabilityRow): 'neutral' | 'warning' | 'error' | 'muted' {
    const label = capModeLabel(cap);
    if (!label) return 'neutral';
    if (label === 'stub' || label === 'degraded' || label === 'yellow') return 'warning';
    if (label === 'red' || label === 'unknown') return 'error';
    if (label === 'disabled' || label === 'off') return 'muted';
    return 'neutral';
  }

  // capContext is the small secondary line under the capability name
  // (e.g., the config_key or the source path) so each row identifies
  // what it is bound to. Falls back to message when nothing else is
  // available so the cell never collapses to just an id.
  function capContext(cap: MillsCapabilityRow): string {
    if (cap.config_key) return cap.config_key;
    if (cap.source) return cap.source;
    if (cap.message) return cap.message;
    return '';
  }
</script>

<PanelShell
  title="Mills Overview"
  icon="❖"
  count={status?.autonomy_ready ? requiredGreen : null}
  loading={loading}
  empty={disabled || (!!error && !status)}
  emptyIcon={disabled ? '◯' : '!'}
  emptyMessage={disabled ? 'Mills operator not configured' : 'Failed to load Mills status'}
  emptyHint={disabled ? 'LOOM_MILLS_OPERATOR_URL is not available to the HUD.' : (error ?? '')}
>
  {#snippet header()}
    {#if showBanner}
      <div
        class="system-health-banner intent-{health.state}"
        role={health.state === 'broken' ? 'alert' : 'status'}
        data-testid="system-health-banner"
        data-state={health.state}
      >
        <span class="banner-dot" aria-hidden="true"></span>
        <div class="banner-text">
          <span class="banner-headline">{bannerHeadline(health)}</span>
          <span class="banner-detail">{bannerDetail(health)}</span>
        </div>
        <button
          type="button"
          class="banner-action"
          onclick={() => runBannerAction(health)}
        >
          {bannerActionLabel(health)} →
        </button>
      </div>
    {/if}
    <div class="overview-status" role="status">
      <div
        class="readiness-chip"
        class:ready={status?.autonomy_ready === true}
        class:blocked={status?.autonomy_ready === false}
      >
        <span class="chip-dot"></span>
        <span>{status?.autonomy_ready ? 'Autonomy ready' : status?.autonomy_ready === false ? 'Autonomy paused' : 'Checking autonomy'}</span>
      </div>
      <div class="status-meta">
        Policy {status?.policy_enabled || policy?.enabled ? 'enabled' : 'disabled'}
        <span class="meta-divider"></span>
        Required caps {requiredGreen}/{requiredTotal || 0}
        <span class="meta-divider"></span>
        Updated {fmtTime(status?.time)}
      </div>
    </div>
  {/snippet}

  <div class="overview-layout">
    <section class="metric-grid" aria-label="Mills operating counters">
      <MetricCard
        label="Queue"
        value={fmtNumber(status?.queue_depth)}
        color={(status?.queue_depth ?? 0) > 0 ? 'var(--warning)' : 'var(--fg-primary)'}
        compact
        onclick={() => goto('backlog')}
      />
      <MetricCard
        label="Active runs"
        value={fmtNumber(status?.active_pipeline_runs)}
        color={(status?.active_pipeline_runs ?? 0) > 0 ? 'var(--info)' : 'var(--fg-primary)'}
        compact
        onclick={() => goto('pipelines')}
      />
      <MetricCard
        label="Pipeline runs"
        value={millsStore.pipelineRuns.length}
        compact
        onclick={() => goto('pipelines')}
      />
      <MetricCard
        label="Council runs"
        value={millsStore.councilRuns.length}
        compact
        onclick={() => goto('council')}
      />
      <MetricCard
        label="Gate pass"
        value={fmtPct(metrics.gate_pass_rate)}
        color={(metrics.gate_pass_rate ?? 1) < 0.85 ? 'var(--warning)' : 'var(--fg-primary)'}
        compact
        onclick={() => goto('eval')}
      />
      <MetricCard
        label="Cost / merged"
        value={fmtUSD(metrics.cost_per_merged_change_usd ?? metrics.cost_per_merged_pipeline_usd)}
        compact
        onclick={() => goto('pipelines')}
      />
    </section>

    {#if blockers.length > 0}
      <section class="attention-panel blocked" aria-label="Autonomy blockers">
        <div class="section-heading">
          <span>Blockers</span>
          <button type="button" class="text-button" onclick={() => goto('policy')}>Policy</button>
        </div>
        <ul class="compact-list">
          {#each blockers.slice(0, 4) as blocker}
            <li>{blocker}</li>
          {/each}
        </ul>
      </section>
    {:else}
      <section class="attention-panel ready" aria-label="Autonomy status">
        <div class="section-heading">
          <span>Ready State</span>
          <button type="button" class="text-button" onclick={() => goto('pipelines')}>Pipelines</button>
        </div>
        <div class="ready-copy">
          <strong>Operator online</strong>
          <span>Queue {status?.queue_depth ?? 0}, active {status?.active_pipeline_runs ?? 0}, required capability gate passing.</span>
        </div>
      </section>
    {/if}

    <section class="status-panel" aria-label="Capability health">
      <div class="section-heading">
        <span>Capabilities</span>
        <span class="mono">{requiredGreen}/{requiredTotal || 0} required</span>
      </div>
      <div class="health-bar" aria-label="Capability statuses">
        <span class="health-segment green" style:flex={counts.green}></span>
        <span class="health-segment yellow" style:flex={counts.yellow}></span>
        <span class="health-segment red" style:flex={counts.red}></span>
        <span class="health-segment unknown" style:flex={counts.unknown}></span>
      </div>
      <div class="capability-grid">
        {#each requiredCaps as cap (cap.id)}
          {@const modeLabel = capModeLabel(cap)}
          {@const context = capContext(cap)}
          <div class="capability-row" title={cap.message ?? cap.source ?? cap.id}>
            <span class="cap-dot status-{cap.status ?? 'unknown'}"></span>
            <div class="cap-text">
              <span class="cap-name">{cap.id}</span>
              {#if context}
                <span class="cap-context">{context}</span>
              {/if}
            </div>
            {#if modeLabel}
              <span class="cap-mode intent-{capModeIntent(cap)}">{modeLabel}</span>
            {/if}
          </div>
        {/each}
      </div>
      {#if optionalWarnings.length > 0}
        <div class="optional-row">
          Optional yellow: {optionalWarnings.map((cap) => cap.id).join(', ')}
        </div>
      {/if}
    </section>

    <section class="status-panel activity-panel" aria-label="Recent Mills activity">
      <div class="section-heading">
        <span>Activity</span>
        <button type="button" class="text-button" onclick={() => goto('council')}>Council</button>
      </div>
      <div class="activity-grid">
        <div>
          <span class="activity-label">Last council</span>
          <span class="activity-value">{fmtTime(status?.last_council_at)}</span>
        </div>
        <div>
          <span class="activity-label">KPI snapshot</span>
          <span class="activity-value">{fmtTime(kpis?.snapshot_at)}</span>
        </div>
        <div>
          <span class="activity-label">Backlog</span>
          <span class="activity-value">{millsStore.backlog.length} items</span>
        </div>
        <div>
          <span class="activity-label">Eval scores</span>
          <span class="activity-value">{millsStore.evalScores.length} rows</span>
        </div>
      </div>
    </section>
  </div>
</PanelShell>

<style>
  .system-health-banner {
    display: grid;
    grid-template-columns: 10px minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--space-3);
    width: 100%;
    padding: var(--space-3) var(--space-4);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    margin-bottom: var(--space-3);
  }

  .system-health-banner.intent-broken {
    border-color: color-mix(in srgb, var(--error) 48%, var(--border));
    background: color-mix(in srgb, var(--error) 8%, var(--bg-tertiary));
    box-shadow: 0 0 18px var(--glow-error);
    color: var(--fg-primary);
  }

  .system-health-banner.intent-in_flight {
    border-color: color-mix(in srgb, var(--info) 38%, var(--border));
    background: color-mix(in srgb, var(--info) 6%, var(--bg-tertiary));
    box-shadow: 0 0 14px var(--glow-accent);
    color: var(--fg-primary);
  }

  .system-health-banner.intent-idle {
    border-color: color-mix(in srgb, var(--warning) 42%, var(--border));
    background: color-mix(in srgb, var(--warning) 8%, var(--bg-tertiary));
    color: var(--fg-primary);
  }

  .banner-dot {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--fg-dim);
    flex: 0 0 auto;
  }

  .system-health-banner.intent-broken .banner-dot {
    background: var(--error);
    box-shadow: 0 0 10px var(--glow-error);
    animation: banner-pulse 1.8s ease-in-out infinite;
  }

  .system-health-banner.intent-in_flight .banner-dot {
    background: var(--info);
    box-shadow: 0 0 10px var(--info-glow);
    animation: banner-pulse 2.4s ease-in-out infinite;
  }

  .system-health-banner.intent-idle .banner-dot {
    background: var(--warning);
    box-shadow: 0 0 10px var(--glow-warning);
  }

  @keyframes banner-pulse {
    0%, 100% { transform: scale(1); opacity: 1; }
    50%      { transform: scale(1.25); opacity: 0.85; }
  }

  .banner-text {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .banner-headline {
    font-weight: 700;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    letter-spacing: var(--tracking-tight);
  }

  .system-health-banner.intent-broken .banner-headline {
    color: var(--error);
  }

  .banner-detail {
    color: var(--fg-muted);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .banner-action {
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    color: var(--fg-secondary);
    border-radius: var(--radius-sm);
    padding: 4px var(--space-3);
    font: inherit;
    font-size: var(--text-xs);
    font-weight: 600;
    letter-spacing: var(--tracking-wide);
    cursor: pointer;
    white-space: nowrap;
  }

  .banner-action:hover,
  .banner-action:focus-visible {
    color: var(--fg-primary);
    border-color: var(--border-focus, var(--accent));
    outline: none;
  }

  .system-health-banner.intent-broken .banner-action {
    border-color: color-mix(in srgb, var(--error) 50%, var(--border));
    color: var(--error);
  }

  .system-health-banner.intent-in_flight .banner-action {
    border-color: color-mix(in srgb, var(--info) 42%, var(--border));
    color: var(--info);
  }

  .system-health-banner.intent-idle .banner-action {
    border-color: color-mix(in srgb, var(--warning) 46%, var(--border));
    color: var(--warning);
  }

  @media (max-width: 720px) {
    .system-health-banner {
      grid-template-columns: 10px minmax(0, 1fr);
      grid-template-rows: auto auto;
    }

    .banner-action {
      grid-column: 1 / -1;
      justify-self: stretch;
    }

    .banner-detail {
      white-space: normal;
    }
  }

  .overview-status {
    display: flex;
    align-items: flex-start;
    flex-direction: column;
    gap: var(--space-3);
    min-width: 0;
  }

  .readiness-chip {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 30px;
    padding: 4px var(--space-3);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    color: var(--fg-secondary);
    background: var(--bg-tertiary);
    font-weight: 700;
    font-size: var(--text-sm);
  }

  .readiness-chip.ready {
    border-color: color-mix(in srgb, var(--success) 38%, var(--border));
    color: var(--success);
    background: var(--success-dim);
  }

  .readiness-chip.blocked {
    border-color: color-mix(in srgb, var(--warning) 42%, var(--border));
    color: var(--warning);
    background: var(--warning-dim);
  }

  .chip-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: currentColor;
    box-shadow: 0 0 10px currentColor;
    flex: 0 0 auto;
  }

  .status-meta {
    display: flex;
    align-items: center;
    justify-content: flex-start;
    flex-wrap: wrap;
    gap: var(--space-2);
    min-width: 0;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .meta-divider {
    width: 1px;
    height: 14px;
    background: var(--border);
  }

  .overview-layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-4);
    align-items: start;
  }

  .metric-grid {
    grid-column: 1 / -1;
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--space-2);
  }

  .attention-panel,
  .status-panel {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-4);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.018), transparent 48%),
      var(--bg-secondary);
    min-width: 0;
  }

  .attention-panel.ready {
    border-color: color-mix(in srgb, var(--success) 24%, var(--border));
  }

  .attention-panel.blocked {
    border-color: color-mix(in srgb, var(--warning) 34%, var(--border));
  }

  .section-heading {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .text-button {
    border: 1px solid var(--border);
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    border-radius: var(--radius-sm);
    padding: 3px var(--space-2);
    font: inherit;
    cursor: pointer;
  }

  .text-button:hover,
  .text-button:focus-visible {
    color: var(--fg-primary);
    border-color: var(--border-focus);
    outline: none;
  }

  .ready-copy {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    color: var(--fg-muted);
    font-size: var(--text-sm);
  }

  .ready-copy strong {
    color: var(--fg-primary);
    font-size: var(--text-base);
  }

  .compact-list {
    margin: 0;
    padding-left: var(--space-4);
    color: var(--fg-secondary);
    font-size: var(--text-sm);
  }

  .compact-list li + li {
    margin-top: var(--space-1);
  }

  .health-bar {
    display: flex;
    width: 100%;
    height: 8px;
    overflow: hidden;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
  }

  .health-segment {
    min-width: 0;
  }

  .health-segment.green { background: var(--success); }
  .health-segment.yellow { background: var(--warning); }
  .health-segment.red { background: var(--error); }
  .health-segment.unknown { background: var(--fg-dim); }

  .capability-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-2);
  }

  .capability-row {
    display: grid;
    grid-template-columns: 8px minmax(0, 1fr) auto;
    gap: var(--space-2);
    align-items: center;
    min-width: 0;
    padding: 5px var(--space-2);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--bg-tertiary) 68%, transparent);
  }

  .cap-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--fg-dim);
  }

  .cap-dot.status-green { background: var(--success); box-shadow: 0 0 8px var(--glow-success); }
  .cap-dot.status-yellow { background: var(--warning); box-shadow: 0 0 8px var(--glow-warning); }
  .cap-dot.status-red { background: var(--error); box-shadow: 0 0 8px var(--glow-error); }

  .cap-text {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }

  .cap-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .cap-context {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: var(--text-2xs);
    letter-spacing: 0;
  }

  .cap-mode {
    padding: 1px 6px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--fg-muted);
    background: color-mix(in srgb, var(--bg-tertiary) 65%, transparent);
    font-family: var(--font-mono);
    font-size: var(--text-2xs);
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    white-space: nowrap;
  }

  .cap-mode.intent-warning {
    color: var(--warning);
    border-color: color-mix(in srgb, var(--warning) 40%, var(--border));
    background: var(--warning-dim, color-mix(in srgb, var(--warning) 12%, transparent));
  }

  .cap-mode.intent-error {
    color: var(--error);
    border-color: color-mix(in srgb, var(--error) 40%, var(--border));
    background: var(--error-dim, color-mix(in srgb, var(--error) 12%, transparent));
  }

  .cap-mode.intent-muted {
    color: var(--fg-dim);
    border-color: var(--border);
    background: transparent;
    text-decoration: line-through;
    text-decoration-thickness: 1px;
    text-underline-offset: 2px;
  }

  .optional-row {
    color: var(--warning);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
  }

  .activity-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-3);
  }

  .activity-label,
  .activity-value {
    display: block;
    min-width: 0;
  }

  .activity-label {
    color: var(--fg-muted);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .activity-value {
    color: var(--fg-primary);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .mono {
    font-family: var(--font-mono);
  }

  @media (max-width: 720px) {
    .metric-grid,
    .capability-grid,
    .activity-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
