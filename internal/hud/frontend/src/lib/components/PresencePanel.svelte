<script>
  import { presenceStore } from '../stores/presence.svelte.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';

  $effect(() => {
    presenceStore.startPolling(5000);
    return () => {
      presenceStore.stopPolling();
    };
  });

  let agents = $derived(presenceStore.agents ?? []);
  let claims = $derived(presenceStore.claims ?? []);
  let worktrees = $derived(presenceStore.worktrees ?? []);

  function presenceStatus(status) {
    const map = {
      active: 'healthy',
      idle: 'degraded',
      offline: 'down',
    };
    return map[status] ?? 'down';
  }

  function claimVariant(type) {
    const map = {
      edit: 'warning',
      review: 'info',
      reserve: 'accent',
    };
    return map[type] ?? 'info';
  }

  function worktreeVariant(status) {
    const map = {
      active: 'success',
      released: 'info',
      orphaned: 'error',
    };
    return map[status] ?? 'info';
  }

  function formatTime(ts) {
    if (!ts) return '--:--:--';
    const d = new Date(ts);
    return d.toLocaleTimeString('en-US', { hour12: false });
  }

  function shortPath(path) {
    if (!path) return '---';
    const parts = path.split('/');
    if (parts.length <= 3) return path;
    return '.../' + parts.slice(-3).join('/');
  }
</script>

<div class="panel presence-panel">
  <div class="presence-grid">
    <!-- LEFT TOP: Agent Presence -->
    <div class="card agents-card">
      <div class="card-header">
        <span class="card-title">Agent Presence</span>
        <span class="count-badge">{presenceStore.activeCount + presenceStore.idleCount}</span>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Agent</th>
              <th>Status</th>
              <th>Type</th>
              <th>Current Task</th>
              <th>Branch</th>
              <th>Heartbeat</th>
            </tr>
          </thead>
          <tbody>
            {#each agents as agent (agent.agent_id)}
              <tr>
                <td class="text-mono">{agent.agent_id}</td>
                <td>
                  <StatusDot status={presenceStatus(agent.status)} />
                </td>
                <td class="text-mono text-muted">{agent.agent_type || '---'}</td>
                <td class="truncate" title={agent.current_task}>{agent.current_task || '---'}</td>
                <td class="text-mono text-muted">{agent.branch || '---'}</td>
                <td class="text-mono text-muted">{formatTime(agent.last_heartbeat)}</td>
              </tr>
            {:else}
              <tr>
                <td colspan="6" class="empty-cell">No registered agents</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>

    <!-- RIGHT TOP: Quick Stats -->
    <div class="stats-grid">
      <div class="stat-card" style="--accent-color: var(--success)">
        <div class="metric-value">{presenceStore.activeCount}</div>
        <div class="metric-label">Active</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--warning)">
        <div class="metric-value">{presenceStore.idleCount}</div>
        <div class="metric-label">Idle</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--error)">
        <div class="metric-value">{presenceStore.offlineCount}</div>
        <div class="metric-label">Offline</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--info)">
        <div class="metric-value">{claims.length}</div>
        <div class="metric-label">File Claims</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--accent)">
        <div class="metric-value">{worktrees.length}</div>
        <div class="metric-label">Worktrees</div>
      </div>
      <div class="stat-card" style="--accent-color: var(--tier-short)">
        <div class="metric-value">{presenceStore.claimedFiles.length}</div>
        <div class="metric-label">Claimed Files</div>
      </div>
    </div>

    <!-- LEFT BOTTOM: File Claims -->
    <div class="card claims-card">
      <div class="card-header">
        <span class="card-title">File Claims</span>
        <span class="count-badge">{claims.length}</span>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>File</th>
              <th>Agent</th>
              <th>Type</th>
              <th>Reason</th>
              <th>Since</th>
            </tr>
          </thead>
          <tbody>
            {#each claims as claim (claim.id)}
              <tr>
                <td class="text-mono" title={claim.file_path}>{shortPath(claim.file_path)}</td>
                <td class="text-mono">{claim.agent_id}</td>
                <td><Badge text={claim.claim_type} variant={claimVariant(claim.claim_type)} /></td>
                <td class="truncate text-muted" title={claim.reason}>{claim.reason || '---'}</td>
                <td class="text-mono text-muted">{formatTime(claim.created_at)}</td>
              </tr>
            {:else}
              <tr>
                <td colspan="5" class="empty-cell">No active file claims</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>

    <!-- RIGHT BOTTOM: Worktrees -->
    <div class="card worktrees-card">
      <div class="card-header">
        <span class="card-title">Git Worktrees</span>
        <span class="count-badge">{worktrees.length}</span>
      </div>
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Branch</th>
              <th>Agent</th>
              <th>Status</th>
              <th>Purpose</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {#each worktrees as wt (wt.assignment_id)}
              <tr>
                <td class="text-mono">{wt.branch}</td>
                <td class="text-mono">{wt.agent_id}</td>
                <td><Badge text={wt.status} variant={worktreeVariant(wt.status)} /></td>
                <td class="truncate text-muted" title={wt.purpose}>{wt.purpose || '---'}</td>
                <td class="text-mono text-muted">{formatTime(wt.created_at)}</td>
              </tr>
            {:else}
              <tr>
                <td colspan="5" class="empty-cell">No active worktrees</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  </div>
</div>

<style>
  .presence-panel {
    overflow-y: auto;
  }

  .presence-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-template-rows: auto auto;
    gap: 16px;
    height: 100%;
  }

  .agents-card,
  .claims-card,
  .worktrees-card {
    min-height: 200px;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .agents-card .table-wrap,
  .claims-card .table-wrap,
  .worktrees-card .table-wrap {
    flex: 1;
    overflow-y: auto;
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: 10px;
  }

  .empty-cell {
    text-align: center;
    color: var(--fg-muted);
    padding: 24px 10px !important;
  }

  /* Stats grid */
  .stats-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-template-rows: 1fr 1fr 1fr;
    gap: 12px;
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 14px 16px;
    border-left: 3px solid var(--accent-color, var(--info));
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .stat-card .metric-value {
    font-size: 22px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .stat-card .metric-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    margin-top: 4px;
  }
</style>
