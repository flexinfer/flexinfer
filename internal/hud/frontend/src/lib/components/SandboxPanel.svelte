<script>
  import { sandboxStore } from '../stores/sandbox.svelte.ts';
  import { labsAuthStore } from '../stores/labsAuth.svelte.ts';
  import { formatTime } from '../utils/format.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import EmptyState from './shared/EmptyState.svelte';
  import LabsAccessBar from './shared/LabsAccessBar.svelte';
  import ConfirmDialog from './shared/ConfirmDialog.svelte';

  $effect(() => {
    sandboxStore.startPolling(15000);
    return () => { sandboxStore.stopPolling(); };
  });

  let available = $derived(sandboxStore.available);
  let summary = $derived(sandboxStore.summary);
  let events = $derived(sandboxStore.recentEvents);
  let running = $derived(sandboxStore.runningCount);
  let paused = $derived(sandboxStore.pausedCount);
  let total = $derived(sandboxStore.totalSandboxes);
  let projects = $derived(sandboxStore.projects);
  let totalExecs = $derived(sandboxStore.totalExecs);
  let totalBuilds = $derived(sandboxStore.totalBuilds);
  let policy = $derived(sandboxStore.policy);
  let capabilities = $derived(sandboxStore.capabilities);
  let capabilitiesLoading = $derived(sandboxStore.capabilitiesLoading);
  let capabilitiesError = $derived(sandboxStore.capabilitiesError);
  let execRuns = $derived(sandboxStore.execRuns);
  let activeExecs = $derived(sandboxStore.activeExecs);
  let error = $derived(sandboxStore.error);
  let lastAction = $derived(sandboxStore.lastAction);
  let lastUpdated = $derived(sandboxStore.lastUpdated);
  let latestEvent = $derived(events[0] ?? null);
  let hasAdminToken = $derived(labsAuthStore.hasToken);
  let offlineReason = $derived(summary?.reason ?? 'mcp-devbox is not running or not connected to the daemon.');
  let offlineHint = $derived(summary?.hint ?? 'Start the devbox service, then return to Labs to provision or inspect sandboxes.');
  let offlineCommand = $derived(summary?.start_command ?? 'loom start devbox');
  let startProject = $state('');
  let startSubmitting = $state(false);
  let execProject = $state('');
  let execCommand = $state('');
  let execTimeout = $state('10m');
  let execSubmitting = $state(false);
  /** @type {string | null} */
  let stopConfirmProject = $state(null);

  $effect(() => {
    if (!startProject && projects.length === 1) {
      startProject = projects[0];
    }
    if (!execProject) {
      if (startProject.trim()) {
        execProject = startProject.trim();
      } else if (projects.length === 1) {
        execProject = projects[0];
      }
    }
  });

  function formatUptime(seconds) {
    if (!seconds || seconds <= 0) return '---';
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    if (h > 0) return `${h}h ${m}m`;
    return `${m}m`;
  }

  function eventIcon(type) {
    switch (type) {
      case 'exec':  return '\u25B6';  // ▶
      case 'build': return '\u2692';  // ⚒
      case 'start': return '\u25C9';  // ◉
      case 'stop':  return '\u25CB';  // ○
      default:      return '\u25C8';  // ◈
    }
  }

  function eventTime(ts) {
    return formatTime(ts);
  }

  function formatExecDuration(ms) {
    if (!ms || ms <= 0) return 'pending';
    if (ms < 1000) return `${ms}ms`;
    const seconds = Math.floor(ms / 1000);
    const minutes = Math.floor(seconds / 60);
    if (minutes > 0) return `${minutes}m ${seconds % 60}s`;
    return `${seconds}s`;
  }

  function execStatusTone(status) {
    switch (status) {
      case 'running': return 'info';
      case 'completed': return 'success';
      case 'failed': return 'error';
      default: return 'muted';
    }
  }

  async function handleStartSandbox() {
    const project = startProject.trim();
    if (!project || startSubmitting) return;
    startSubmitting = true;
    await sandboxStore.startSandbox(project);
    if (!sandboxStore.error) {
      startProject = '';
    }
    startSubmitting = false;
  }

  function handleStartKeydown(event) {
    if (event.key === 'Enter') {
      event.preventDefault();
      handleStartSandbox();
    }
  }

  async function handleRunExec() {
    const project = execProject.trim();
    const command = execCommand.trim();
    if (!project || !command || execSubmitting) return;
    execSubmitting = true;
    await sandboxStore.startExec(project, command, execTimeout.trim() || '10m');
    if (!sandboxStore.error) {
      execCommand = '';
    }
    execSubmitting = false;
  }

  function handleExecKeydown(event) {
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      handleRunExec();
    }
  }
</script>

<div class="panel sandbox-panel">
  <LabsAccessBar />

  {#if !available}
    <div class="unavailable-shell">
      <section class="unavailable-card">
        <div class="unavailable-eyebrow">Labs / Sandbox</div>
        <div class="unavailable-head">
          <div class="unavailable-icon">{'\u2B22'}</div>
          <div>
            <div class="unavailable-title">Devbox is offline</div>
            <div class="unavailable-hint">{offlineReason}</div>
          </div>
        </div>
        <div class="offline-command">
          <span class="offline-command-label">Start command</span>
          <code>{offlineCommand}</code>
        </div>
        <div class="unavailable-copy">{offlineHint}</div>
      </section>

      <aside class="unavailable-sidecard">
        <div class="section-title">Why it matters</div>
        <div class="offline-points">
          <div class="offline-point">Sandbox availability controls `devbox_build`, `devbox_exec`, and the HUD’s project activity feed.</div>
          <div class="offline-point">When the backend reconnects, project inventory and recent build or exec events repopulate automatically.</div>
        </div>
      </aside>
    </div>
  {:else}
    <!-- Header bar with counts -->
    <div class="header-bar">
      <div class="header-stats">
        <span class="header-total text-mono">{total} sandbox{total !== 1 ? 'es' : ''}</span>
        {#if summary?.backend}
          <span class="header-stat backend-stat">
            backend: <span class="text-mono">{summary.backend}</span>
          </span>
        {/if}
        <span class="header-stat running-stat">
          <span class="dot dot-running"></span>
          {running} running
        </span>
        {#if paused > 0}
          <span class="header-stat paused-stat">
            <span class="dot dot-paused"></span>
            {paused} paused
          </span>
        {/if}
        <span class="header-stat exec-stat">
          <span class="stat-icon">{'\u25B6'}</span>
          {totalExecs} execs
        </span>
        <span class="header-stat build-stat">
          <span class="stat-icon">{'\u2692'}</span>
          {totalBuilds} builds
        </span>
        {#if summary?.uptime_seconds}
          <span class="header-stat uptime-stat">
            uptime: {formatUptime(summary.uptime_seconds)}
          </span>
        {/if}
        {#if lastUpdated}
          <span class="header-stat updated-stat">
            updated {eventTime(lastUpdated)}
          </span>
        {/if}
      </div>
    </div>

    <div class="capability-strip">
      <span class="capability-chip" class:ready={capabilities?.available}>
        {capabilities?.available ? 'Devbox live' : 'Devbox offline'}
      </span>
      <span class="capability-chip" class:ready={capabilities?.notes?.async_exec}>
        Async exec
      </span>
      <span class="capability-chip" class:ready={hasAdminToken}>
        {hasAdminToken ? 'Token loaded' : 'Token required'}
      </span>
      {#if capabilities?.backend}
        <span class="capability-meta">backend {capabilities.backend}</span>
      {/if}
      {#if capabilitiesLoading}
        <span class="capability-meta">checking capabilities…</span>
      {:else if capabilitiesError}
        <span class="capability-meta capability-error">{capabilitiesError}</span>
      {:else if capabilities?.supported_actions?.length}
        <span class="capability-meta">actions {capabilities.supported_actions.join(', ')}</span>
      {/if}
    </div>

    <div class="start-toolbar">
      <label class="start-form">
        <span class="start-label">Project</span>
        <input
          class="start-input"
          type="text"
          bind:value={startProject}
          placeholder="services/loom-core"
          onkeydown={handleStartKeydown}
        />
      </label>
      <button
        class="action-btn action-start start-submit"
        disabled={!hasAdminToken || !startProject.trim() || startSubmitting}
        title="Start sandbox for project"
        onclick={handleStartSandbox}
      >
        {startSubmitting ? 'Starting…' : 'Start Sandbox'}
      </button>
    </div>

    <div class="exec-toolbar">
      <label class="start-form">
        <span class="start-label">Exec Project</span>
        <input
          class="start-input"
          type="text"
          bind:value={execProject}
          placeholder="loom-core"
        />
      </label>
      <label class="start-form exec-command-form">
        <span class="start-label">Command</span>
        <input
          class="start-input"
          type="text"
          bind:value={execCommand}
          placeholder="make test-sandbox"
          onkeydown={handleExecKeydown}
        />
      </label>
      <label class="start-form exec-timeout-form">
        <span class="start-label">Timeout</span>
        <input
          class="start-input"
          type="text"
          bind:value={execTimeout}
          placeholder="10m"
        />
      </label>
      <button
        class="action-btn action-start exec-submit"
        disabled={!hasAdminToken || !capabilities?.notes?.async_exec || !execProject.trim() || !execCommand.trim() || execSubmitting}
        title="Run command in sandbox"
        onclick={handleRunExec}
      >
        {execSubmitting ? 'Queueing…' : 'Run Command'}
      </button>
    </div>

    <div class="exec-helper">
      Uses `devbox_exec_async` with polling. Press <span class="text-mono">Cmd/Ctrl+Enter</span> in the command field to queue the run quickly.
    </div>

    {#if lastAction}
      <div class="action-banner">
        <span class="action-banner-kind">
          {#if lastAction.kind === 'start'}
            Start
          {:else if lastAction.kind === 'stop'}
            Stop
          {:else}
            Exec
          {/if}
        </span>
        <span class="action-banner-copy">
          {lastAction.project}: {lastAction.message}
          {#if lastAction.buildId}
            <strong>{lastAction.buildId}</strong>
          {:else if lastAction.execId}
            <strong>{lastAction.execId}</strong>
          {/if}
        </span>
      </div>
    {/if}

    {#if error}
      <div class="error-banner">
        <span class="error-icon">{'\u26A0'}</span>
        <span class="error-copy">{error}</span>
        <button class="error-dismiss" onclick={() => sandboxStore.clearError()}>Dismiss</button>
      </div>
    {/if}

    <!-- Main content: projects + activity + summary rail -->
    <div class="sandbox-content">
      <!-- Project list -->
      <div class="projects-section">
        <div class="section-title">Projects</div>
        {#if projects.length === 0}
          <div class="projects-empty">
            <EmptyState icon={'\u2B22'} heading="No sandbox projects" compact />
            <div class="empty-copy">
              Start one from the project field above. The HUD will attach it here once the daemon reports the sandbox.
            </div>
          </div>
        {:else}
          <div class="project-list">
            {#each projects as project}
              <div class="project-row">
                <StatusDot status="healthy" />
                <span class="project-name text-mono">{project}</span>
                {#if summary?.agent_labels?.[project]}
                  <span class="agent-badge text-mono">{summary.agent_labels[project]}</span>
                {/if}
                <span class="project-actions">
                  <button class="action-btn action-stop" title="Stop sandbox"
                    disabled={!hasAdminToken}
                    onclick={() => (stopConfirmProject = project)}>
                    {'\u25A0'}
                  </button>
                </span>
              </div>
            {/each}
          </div>
        {/if}
      </div>

      <!-- Recent activity -->
      <div class="activity-section">
        <div class="section-title">Recent Activity</div>
        {#if events.length === 0}
          <EmptyState icon={'\u25B6'} heading="No recent activity" compact />
        {:else}
          <div class="activity-list">
            {#each events as evt, i}
              <div class="activity-row" class:fresh={i === 0}>
                <span class="activity-icon">{eventIcon(evt.type)}</span>
                <span class="activity-type text-mono">{evt.type}</span>
                <span class="activity-project">{evt.project}</span>
                <span class="activity-detail truncate">{evt.detail}</span>
                <span class="activity-time text-mono">{eventTime(evt.timestamp)}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>

      <aside class="sandbox-rail">
        <section class="rail-card">
          <div class="section-title">Execution</div>
          <div class="exec-run-summary">
            <span class="exec-run-summary-value text-mono">{activeExecs.length}</span>
            <span class="exec-run-summary-label">running now</span>
          </div>
          {#if execRuns.length === 0}
            <div class="rail-empty">Queue a command above to exercise the async devbox path and inspect its output tail here.</div>
          {:else}
            <div class="exec-run-list">
              {#each execRuns as run (run.exec_id)}
                <article class="exec-run-card" class:is-running={run.status === 'running'}>
                  <div class="exec-run-head">
                    <span class="exec-run-status" data-tone={execStatusTone(run.status)}>{run.status}</span>
                    <span class="exec-run-project text-mono">{run.project}</span>
                    {#if run.exit_code !== undefined}
                      <span class="exec-run-exit text-mono">exit {run.exit_code}</span>
                    {/if}
                  </div>
                  <div class="exec-run-command text-mono">{run.command}</div>
                  <div class="exec-run-meta text-mono">
                    <span>{run.exec_id}</span>
                    <span>{formatExecDuration(run.status === 'running' ? run.elapsed_ms : (run.duration_ms ?? run.elapsed_ms))}</span>
                  </div>
                  {#if run.stdout_tail}
                    <pre class="exec-run-tail">{run.stdout_tail}</pre>
                  {/if}
                  {#if run.stderr_tail}
                    <pre class="exec-run-tail exec-run-tail-error">{run.stderr_tail}</pre>
                  {/if}
                  {#if run.error}
                    <div class="exec-run-error">{run.error}</div>
                  {/if}
                </article>
              {/each}
            </div>
          {/if}
        </section>

        <section class="rail-card">
          <div class="section-title">Sandbox Summary</div>
          <div class="summary-grid">
            <div class="summary-stat">
              <span class="summary-value text-mono">{projects.length}</span>
              <span class="summary-label">Projects</span>
            </div>
            <div class="summary-stat">
              <span class="summary-value text-mono">{running}</span>
              <span class="summary-label">Running</span>
            </div>
            <div class="summary-stat">
              <span class="summary-value text-mono">{totalExecs}</span>
              <span class="summary-label">Execs</span>
            </div>
            <div class="summary-stat">
              <span class="summary-value text-mono">{totalBuilds}</span>
              <span class="summary-label">Builds</span>
            </div>
          </div>
          {#if latestEvent}
            <div class="latest-event">
              <div class="latest-event-label">Latest event</div>
              <div class="latest-event-row">
                <span class="activity-icon">{eventIcon(latestEvent.type)}</span>
                <span class="latest-event-text">
                  <strong>{latestEvent.project}</strong> {latestEvent.detail}
                </span>
              </div>
              <div class="latest-event-time text-mono">{eventTime(latestEvent.timestamp)}</div>
            </div>
          {:else}
            <div class="rail-empty">New exec and build activity will accumulate here as the daemon emits sandbox events.</div>
          {/if}
        </section>

        {#if policy?.configured}
          <section class="rail-card">
            <div class="section-title">Policy</div>
            <div class="policy-section">
              {#if policy.auto_provision}
                <div class="policy-row">
                  <span class="policy-icon">{'\u2713'}</span>
                  <span class="policy-text">Auto-provision on session start</span>
                </div>
              {/if}
              {#if policy.default_backend}
                <div class="policy-row">
                  <span class="policy-icon">{'\u2B22'}</span>
                  <span class="policy-text">Backend: <span class="text-mono">{policy.default_backend}</span></span>
                </div>
              {/if}
              {#if policy.require_sandbox?.length}
                <div class="policy-group">
                  <span class="policy-group-label">Required</span>
                  {#each policy.require_sandbox as cmd}
                    <span class="policy-tag policy-tag-require">{cmd}</span>
                  {/each}
                </div>
              {/if}
              {#if policy.recommend_sandbox?.length}
                <div class="policy-group">
                  <span class="policy-group-label">Recommended</span>
                  {#each policy.recommend_sandbox as cmd}
                    <span class="policy-tag policy-tag-recommend">{cmd}</span>
                  {/each}
                </div>
              {/if}
            </div>
          </section>
        {/if}
      </aside>
    </div>
  {/if}

  <ConfirmDialog
    open={stopConfirmProject !== null}
    title="Stop sandbox?"
    message={`This will stop the sandbox for "${stopConfirmProject ?? ''}". Running exec jobs will be terminated.`}
    confirmLabel="Stop"
    variant="danger"
    onConfirm={() => { const p = stopConfirmProject; stopConfirmProject = null; if (p) sandboxStore.stopSandbox(p); }}
    onCancel={() => (stopConfirmProject = null)}
  />
</div>

<style>
  .sandbox-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .unavailable-shell {
    display: grid;
    grid-template-columns: minmax(0, 1.4fr) minmax(260px, 0.8fr);
    gap: var(--space-4);
    align-items: start;
    padding-top: var(--space-2);
  }

  .unavailable-card,
  .unavailable-sidecard {
    min-width: 0;
    padding: clamp(18px, 2vw, 28px);
    border-radius: var(--radius-xl);
    border: 1px solid var(--border);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.02), transparent 40%),
      var(--bg-secondary);
    box-shadow: var(--shadow-xs);
  }

  .unavailable-card {
    border-color: color-mix(in srgb, var(--warning) 20%, var(--border));
    background:
      radial-gradient(circle at top right, rgba(255, 184, 48, 0.1), transparent 28%),
      linear-gradient(180deg, rgba(255, 255, 255, 0.02), transparent 40%),
      var(--bg-secondary);
  }

  .start-toolbar {
    display: flex;
    align-items: end;
    gap: 12px;
    padding: 10px 0 14px;
    border-bottom: 1px solid color-mix(in srgb, var(--accent) 10%, var(--border));
    margin-bottom: 14px;
  }

  .exec-toolbar {
    display: grid;
    grid-template-columns: minmax(0, 180px) minmax(0, 1fr) 120px 140px;
    gap: 12px;
    align-items: end;
    padding-bottom: 8px;
  }

  .start-form {
    display: flex;
    flex-direction: column;
    gap: 6px;
    flex: 1;
    min-width: 0;
  }

  .start-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .start-input {
    width: 100%;
    min-width: 0;
    padding: 9px 11px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: color-mix(in srgb, var(--bg-secondary) 92%, black);
    color: var(--fg-primary);
    font: inherit;
  }

  .start-input:focus {
    outline: none;
    border-color: color-mix(in srgb, var(--accent) 46%, var(--border));
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--accent) 24%, transparent);
  }

  .start-submit:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .exec-submit:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .exec-command-form {
    min-width: 0;
  }

  .exec-timeout-form {
    min-width: 0;
  }

  .exec-helper {
    margin-bottom: 14px;
    font-size: var(--text-sm);
    color: var(--fg-muted);
    line-height: 1.5;
  }

  .capability-strip {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    padding: 10px 0 12px;
  }

  .capability-chip {
    padding: 4px 9px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .capability-chip.ready {
    border-color: color-mix(in srgb, var(--success) 28%, var(--border));
    color: var(--success);
    background: color-mix(in srgb, var(--success) 10%, var(--bg-secondary));
  }

  .capability-meta {
    font-size: var(--text-xs);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .capability-error {
    color: var(--error);
  }

  .error-banner {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 12px;
    margin-bottom: 14px;
    border-radius: var(--radius-sm);
    border: 1px solid color-mix(in srgb, var(--error) 30%, var(--border));
    background: color-mix(in srgb, var(--error) 10%, var(--bg-secondary));
    color: var(--fg-secondary);
  }

  .error-icon {
    color: var(--error);
  }

  .error-copy {
    min-width: 0;
    flex: 1;
  }

  .error-dismiss {
    flex-shrink: 0;
    padding: 4px 10px;
    border-radius: var(--radius-xs);
    border: 1px solid color-mix(in srgb, var(--error) 30%, var(--border));
    background: transparent;
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    cursor: pointer;
    transition: color var(--transition-fast), border-color var(--transition-fast);
  }

  .error-dismiss:hover {
    color: var(--fg-primary);
    border-color: var(--fg-secondary);
  }

  .action-banner {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: 10px 12px;
    margin-bottom: 14px;
    border-radius: var(--radius-lg);
    border: 1px solid color-mix(in srgb, var(--success) 24%, var(--border));
    background: color-mix(in srgb, var(--success) 10%, var(--bg-secondary));
    color: var(--fg-secondary);
  }

  .action-banner-kind {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--success);
  }

  .action-banner-copy {
    min-width: 0;
    line-height: 1.5;
  }

  .action-banner-copy strong {
    margin-left: var(--space-2);
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .projects-empty {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .empty-copy {
    font-size: var(--text-sm);
    color: var(--fg-muted);
    line-height: var(--leading-relaxed);
  }

  /* ---- Unavailable State ---- */
  .unavailable-icon {
    font-size: 36px;
    color: var(--warning);
    opacity: 0.9;
    width: 56px;
    height: 56px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    border-radius: 50%;
    border: 1px solid color-mix(in srgb, var(--warning) 24%, var(--border));
    background: rgba(255, 184, 48, 0.08);
  }

  .unavailable-title {
    font-size: clamp(22px, 2.4vw, 32px);
    font-weight: 700;
    color: var(--fg-primary);
  }

  .unavailable-hint {
    font-size: var(--text-base);
    color: var(--fg-secondary);
    max-width: 48ch;
    line-height: var(--leading-loose);
  }

  .unavailable-eyebrow {
    font-size: var(--text-xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--warning);
    margin-bottom: var(--space-3);
  }

  .unavailable-head {
    display: flex;
    align-items: start;
    gap: var(--space-3);
  }

  .offline-command {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    margin-top: var(--space-4);
    padding: var(--space-3);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.02);
  }

  .offline-command-label {
    font-size: var(--text-xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .offline-command code {
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    color: var(--accent);
  }

  .unavailable-copy {
    margin-top: var(--space-3);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.6;
  }

  .offline-points {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding-top: var(--space-3);
  }

  .offline-point {
    padding-left: var(--space-4);
    position: relative;
    color: var(--fg-secondary);
    line-height: 1.6;
    font-size: var(--text-sm);
  }

  .offline-point::before {
    content: '';
    position: absolute;
    left: 0;
    top: 8px;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--warning);
    box-shadow: 0 0 8px var(--glow-warning);
  }

  /* ---- Header Bar ---- */
  .header-bar {
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border);
    position: relative;
  }

  .header-bar::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .header-stats {
    display: flex;
    align-items: center;
    gap: var(--space-4);
    font-size: var(--text-sm);
  }

  .header-total {
    font-weight: 600;
    color: var(--fg-primary);
  }

  .header-stat {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    color: var(--fg-secondary);
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .dot-running {
    background: var(--success);
    box-shadow: 0 0 4px var(--glow-success);
  }

  .dot-paused {
    background: var(--warning);
    box-shadow: 0 0 4px var(--glow-warning);
  }

  .stat-icon {
    font-size: var(--text-xs);
    color: var(--fg-muted);
  }

  .uptime-stat {
    margin-left: auto;
    color: var(--fg-dim);
    font-family: var(--font-mono);
    font-size: var(--text-sm);
  }

  /* ---- Content Layout ---- */
  .sandbox-content {
    display: grid;
    grid-template-columns: 220px minmax(0, 1fr) 280px;
    flex: 1;
    overflow: hidden;
    gap: var(--space-3);
  }

  .section-title {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    padding: var(--space-3) 0 var(--space-2);
    border-bottom: 1px solid var(--border);
  }

  /* ---- Projects ---- */
  .projects-section {
    border-right: 1px solid var(--border-subtle);
    padding-right: var(--space-3);
    overflow-y: auto;
  }

  .project-list {
    display: flex;
    flex-direction: column;
  }

  .project-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-2) var(--space-1);
    font-size: var(--text-sm);
    border-bottom: 1px solid var(--border-subtle);
    transition: background var(--transition-fast);
  }

  .project-row:hover {
    background: var(--bg-tertiary);
  }

  .project-name {
    color: var(--fg-primary);
    font-weight: 500;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .agent-badge {
    font-size: var(--text-2xs);
    padding: 1px var(--space-1);
    border-radius: var(--radius-sm);
    background: var(--accent-dim);
    color: var(--accent);
    border: 1px solid rgba(255, 107, 53, 0.2);
    flex-shrink: 0;
    font-weight: 600;
  }

  .project-actions {
    display: flex;
    gap: var(--space-1);
    flex-shrink: 0;
    opacity: 0;
    transition: opacity var(--transition-fast);
  }

  .project-row:hover .project-actions {
    opacity: 1;
  }

  .action-btn {
    background: none;
    border: 1px solid var(--border);
    color: var(--fg-muted);
    cursor: pointer;
    border-radius: var(--radius-sm);
    font-size: var(--text-xs);
    padding: 2px var(--space-2);
    transition: color var(--transition-fast), border-color var(--transition-fast);
  }

  .action-btn:hover {
    color: var(--fg-primary);
    border-color: var(--fg-secondary);
  }

  .action-stop:hover {
    color: var(--error);
    border-color: var(--error);
    box-shadow: 0 0 6px var(--glow-error);
  }

  .action-start {
    width: 100%;
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-sm);
    text-align: center;
  }

  .action-start:hover {
    color: var(--success);
    border-color: var(--success);
    box-shadow: 0 0 6px var(--glow-success);
  }

  /* ---- Policy ---- */
  .policy-section {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-2) 0;
  }

  .policy-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
  }

  .policy-icon {
    font-size: var(--text-sm);
    color: var(--fg-muted);
    width: 14px;
    text-align: center;
    flex-shrink: 0;
  }

  .policy-group {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-1);
    padding: 2px 0;
  }

  .policy-group-label {
    font-size: var(--text-2xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    width: 100%;
    margin-top: 2px;
  }

  .policy-tag {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    padding: 1px var(--space-1);
    border-radius: var(--radius-sm);
  }

  .policy-tag-require {
    background: var(--error-dim);
    color: var(--error);
    border: 1px solid rgba(255, 61, 113, 0.2);
  }

  .policy-tag-recommend {
    background: var(--warning-dim);
    color: var(--warning);
    border: 1px solid rgba(255, 184, 48, 0.25);
  }

  /* ---- Activity ---- */
  .activity-section {
    min-width: 0;
    overflow-y: auto;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 0 var(--space-3) var(--space-3);
  }

  .sandbox-rail {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    overflow-y: auto;
  }

  .rail-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: 0 var(--space-3) var(--space-3);
    position: relative;
  }

  .rail-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .summary-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-3);
    padding-top: var(--space-3);
  }

  .summary-stat {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--space-3);
    background: var(--bg-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
  }

  .summary-value {
    font-size: 18px;
    color: var(--fg-primary);
  }

  .summary-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
  }

  .latest-event {
    margin-top: var(--space-3);
    padding-top: var(--space-3);
    border-top: 1px solid color-mix(in srgb, var(--border) 70%, transparent);
  }

  .latest-event-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
    margin-bottom: var(--space-2);
  }

  .latest-event-row {
    display: flex;
    gap: var(--space-2);
    align-items: flex-start;
  }

  .latest-event-text {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: var(--leading-normal);
  }

  .latest-event-text strong {
    color: var(--fg-primary);
  }

  .latest-event-time {
    margin-top: var(--space-2);
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
  }

  .rail-empty {
    padding-top: var(--space-3);
    font-size: var(--text-sm);
    color: var(--fg-dim);
    line-height: var(--leading-normal);
  }

  .exec-run-summary {
    display: flex;
    align-items: baseline;
    gap: 8px;
    padding-top: var(--space-3);
  }

  .exec-run-summary-value {
    font-size: 20px;
    color: var(--fg-primary);
  }

  .exec-run-summary-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
  }

  .exec-run-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding-top: var(--space-3);
  }

  .exec-run-card {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: var(--space-3);
    border-radius: var(--radius-md);
    border: 1px solid var(--border-subtle);
    background: var(--bg-primary);
  }

  .exec-run-card.is-running {
    border-color: color-mix(in srgb, var(--info) 28%, var(--border));
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--info) 16%, transparent);
  }

  .exec-run-head,
  .exec-run-meta {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }

  .exec-run-status {
    padding: 2px 6px;
    border-radius: 999px;
    font-size: var(--text-2xs);
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    border: 1px solid var(--border);
    color: var(--fg-secondary);
  }

  .exec-run-status[data-tone='info'] {
    color: var(--info);
    border-color: color-mix(in srgb, var(--info) 28%, var(--border));
    background: color-mix(in srgb, var(--info) 10%, var(--bg-primary));
  }

  .exec-run-status[data-tone='success'] {
    color: var(--success);
    border-color: color-mix(in srgb, var(--success) 28%, var(--border));
    background: color-mix(in srgb, var(--success) 10%, var(--bg-primary));
  }

  .exec-run-status[data-tone='error'] {
    color: var(--error);
    border-color: color-mix(in srgb, var(--error) 28%, var(--border));
    background: color-mix(in srgb, var(--error) 10%, var(--bg-primary));
  }

  .exec-run-project,
  .exec-run-exit {
    font-size: var(--text-xs);
    color: var(--fg-muted);
  }

  .exec-run-command {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    word-break: break-word;
  }

  .exec-run-meta {
    justify-content: space-between;
    font-size: var(--text-2xs);
    color: var(--fg-dim);
  }

  .exec-run-tail {
    margin: 0;
    padding: 10px;
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--bg-secondary) 90%, black);
    border: 1px solid var(--border-subtle);
    color: var(--fg-secondary);
    font-size: 11px;
    line-height: 1.45;
    overflow-x: auto;
    white-space: pre-wrap;
    word-break: break-word;
  }

  .exec-run-tail-error {
    border-color: color-mix(in srgb, var(--error) 22%, var(--border));
    color: color-mix(in srgb, var(--error) 70%, var(--fg-primary));
  }

  .exec-run-error {
    font-size: var(--text-xs);
    color: var(--error);
    line-height: 1.5;
  }

  .activity-list {
    display: flex;
    flex-direction: column;
  }

  .activity-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    padding: var(--space-1) var(--space-1);
    font-size: var(--text-sm);
    border-bottom: 1px solid var(--border-subtle);
    transition: background var(--transition-fast);
  }

  .activity-row:hover {
    background: var(--bg-tertiary);
  }

  .activity-row.fresh {
    animation: freshPulse 0.5s ease-out;
  }

  @keyframes freshPulse {
    from { background: var(--info-dim); }
    to { background: transparent; }
  }

  .activity-icon {
    font-size: var(--text-sm);
    color: var(--accent);
    flex-shrink: 0;
    width: var(--space-4);
    text-align: center;
  }

  .activity-type {
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    width: 40px;
    flex-shrink: 0;
  }

  .activity-project {
    color: var(--fg-primary);
    font-weight: 500;
    flex-shrink: 0;
    max-width: 120px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .activity-detail {
    flex: 1;
    color: var(--fg-dim);
    min-width: 0;
  }

  .activity-time {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    flex-shrink: 0;
  }

  .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @media (max-width: 1220px) {
    .unavailable-shell {
      grid-template-columns: 1fr;
    }

    .sandbox-content {
      grid-template-columns: 220px minmax(0, 1fr);
    }

    .sandbox-rail {
      grid-column: 1 / -1;
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
      overflow: visible;
    }

    .exec-toolbar {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (max-width: 600px) {
    .sandbox-content {
      grid-template-columns: 1fr;
    }

    .sandbox-rail {
      grid-template-columns: 1fr;
    }

    .projects-section {
      border-right: none;
      border-bottom: 1px solid var(--border);
      padding-right: 0;
      padding-bottom: var(--space-2);
      max-height: 200px;
    }
    .activity-section {
      padding-left: var(--space-3);
    }

    .exec-toolbar {
      grid-template-columns: 1fr;
    }
  }
</style>
