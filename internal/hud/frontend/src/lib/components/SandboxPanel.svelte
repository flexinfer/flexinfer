<script>
  import { sandboxStore } from '../stores/sandbox.svelte.ts';
  import { formatTime } from '../utils/format.ts';
  import StatusDot from '../widgets/StatusDot.svelte';

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
</script>

<div class="panel sandbox-panel">
  {#if !available}
    <!-- Not available state -->
    <div class="unavailable">
      <div class="unavailable-icon">{'\u2B22'}</div>
      <div class="unavailable-title">Devbox Offline</div>
      <div class="unavailable-hint">
        mcp-devbox is not running or not connected to the daemon.
        <br />Start it with <code>loom start devbox</code> or check your registry config.
      </div>
    </div>
  {:else}
    <!-- Header bar with counts -->
    <div class="header-bar">
      <div class="header-stats">
        <span class="header-total text-mono">{total} sandbox{total !== 1 ? 'es' : ''}</span>
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
      </div>
    </div>

    <!-- Main content: projects + policy + activity -->
    <div class="sandbox-content">
      <!-- Project list -->
      <div class="projects-section">
        <div class="section-title">Projects</div>
        {#if projects.length === 0}
          <div class="empty-state">No sandbox projects</div>
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
                    onclick={() => sandboxStore.stopSandbox(project)}>
                    {'\u25A0'}
                  </button>
                </span>
              </div>
            {/each}
          </div>
          <div class="start-section">
            <button class="action-btn action-start" title="Start sandbox for project"
              onclick={() => {
                const name = prompt('Project name:');
                if (name) sandboxStore.startSandbox(name);
              }}>
              + Start Sandbox
            </button>
          </div>
        {/if}

        <!-- Sandbox Policy -->
        {#if policy?.configured}
          <div class="section-title" style="margin-top: 8px;">Policy</div>
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
        {/if}
      </div>

      <!-- Recent activity -->
      <div class="activity-section">
        <div class="section-title">Recent Activity</div>
        {#if events.length === 0}
          <div class="empty-state">No recent activity</div>
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
    </div>
  {/if}
</div>

<style>
  .sandbox-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  /* ---- Unavailable State ---- */
  .unavailable {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    flex: 1;
    gap: 12px;
    padding: 48px 24px;
    text-align: center;
  }

  .unavailable-icon {
    font-size: 36px;
    color: var(--fg-muted);
    opacity: 0.4;
  }

  .unavailable-title {
    font-size: 16px;
    font-weight: 600;
    color: var(--fg-secondary);
  }

  .unavailable-hint {
    font-size: 12px;
    color: var(--fg-muted);
    max-width: 320px;
    line-height: 1.6;
  }

  .unavailable-hint code {
    font-family: var(--font-mono);
    font-size: 11px;
    padding: 2px 5px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    color: var(--accent);
  }

  /* ---- Header Bar ---- */
  .header-bar {
    padding: 8px 0;
    border-bottom: 1px solid var(--border);
  }

  .header-stats {
    display: flex;
    align-items: center;
    gap: 16px;
    font-size: 12px;
  }

  .header-total {
    font-weight: 600;
    color: var(--fg-primary);
  }

  .header-stat {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--fg-secondary);
  }

  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }

  .dot-running {
    background: var(--success);
    box-shadow: 0 0 4px var(--success);
  }

  .dot-paused {
    background: var(--warning);
  }

  .stat-icon {
    font-size: 10px;
  }

  .uptime-stat {
    margin-left: auto;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 11px;
  }

  /* ---- Content Layout ---- */
  .sandbox-content {
    display: grid;
    grid-template-columns: 260px 1fr;
    flex: 1;
    overflow: hidden;
    gap: 0;
  }

  .section-title {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-muted);
    padding: 10px 0 6px;
    border-bottom: 1px solid var(--border);
  }

  .empty-state {
    padding: 24px 0;
    text-align: center;
    color: var(--fg-muted);
    font-size: 12px;
    font-style: italic;
  }

  /* ---- Projects ---- */
  .projects-section {
    border-right: 1px solid var(--border);
    padding-right: 12px;
    overflow-y: auto;
  }

  .project-list {
    display: flex;
    flex-direction: column;
  }

  .project-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 4px;
    font-size: 12px;
    border-bottom: 1px solid rgba(3, 89, 100, 0.15);
    transition: background var(--transition-fast, 0.1s);
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
    font-size: 9px;
    padding: 1px 5px;
    border-radius: var(--radius-sm);
    background: rgba(0, 188, 212, 0.1);
    color: var(--accent);
    border: 1px solid rgba(0, 188, 212, 0.2);
    flex-shrink: 0;
  }

  .project-actions {
    display: flex;
    gap: 4px;
    flex-shrink: 0;
    opacity: 0;
    transition: opacity var(--transition-fast, 0.1s);
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
    font-size: 10px;
    padding: 2px 6px;
    transition: all var(--transition-fast, 0.1s);
  }

  .action-btn:hover {
    color: var(--fg-primary);
    border-color: var(--fg-secondary);
  }

  .action-stop:hover {
    color: var(--error);
    border-color: var(--error);
  }

  .start-section {
    padding: 8px 0;
  }

  .action-start {
    width: 100%;
    padding: 4px 8px;
    font-size: 11px;
    text-align: center;
  }

  .action-start:hover {
    color: var(--success);
    border-color: var(--success);
  }

  /* ---- Policy ---- */
  .policy-section {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 6px 0;
  }

  .policy-row {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    color: var(--fg-secondary);
  }

  .policy-icon {
    font-size: 11px;
    color: var(--fg-muted);
    width: 14px;
    text-align: center;
    flex-shrink: 0;
  }

  .policy-group {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px;
    padding: 2px 0;
  }

  .policy-group-label {
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--fg-muted);
    width: 100%;
    margin-top: 2px;
  }

  .policy-tag {
    font-size: 10px;
    font-family: var(--font-mono);
    padding: 1px 5px;
    border-radius: var(--radius-sm);
  }

  .policy-tag-require {
    background: rgba(233, 93, 116, 0.1);
    color: var(--error);
    border: 1px solid rgba(233, 93, 116, 0.2);
  }

  .policy-tag-recommend {
    background: rgba(231, 179, 18, 0.1);
    color: var(--warning);
    border: 1px solid rgba(231, 179, 18, 0.2);
  }

  /* ---- Activity ---- */
  .activity-section {
    padding-left: 12px;
    overflow-y: auto;
  }

  .activity-list {
    display: flex;
    flex-direction: column;
  }

  .activity-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 4px;
    font-size: 11px;
    border-bottom: 1px solid rgba(3, 89, 100, 0.15);
    transition: background var(--transition-fast, 0.1s);
  }

  .activity-row:hover {
    background: var(--bg-tertiary);
  }

  .activity-row.fresh {
    animation: freshPulse 0.5s ease-out;
  }

  @keyframes freshPulse {
    from { background: rgba(0, 188, 212, 0.08); }
    to { background: transparent; }
  }

  .activity-icon {
    font-size: 11px;
    color: var(--accent);
    flex-shrink: 0;
    width: 16px;
    text-align: center;
  }

  .activity-type {
    font-size: 10px;
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
    color: var(--fg-muted);
    min-width: 0;
  }

  .activity-time {
    font-size: 10px;
    color: var(--fg-muted);
    flex-shrink: 0;
  }

  .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @media (max-width: 600px) {
    .sandbox-content {
      grid-template-columns: 1fr;
    }
    .projects-section {
      border-right: none;
      border-bottom: 1px solid var(--border);
      padding-right: 0;
      padding-bottom: 8px;
      max-height: 200px;
    }
    .activity-section {
      padding-left: 0;
    }
  }
</style>
