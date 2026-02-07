<script>
  import { presenceStore } from '../stores/presence.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';
  import Modal from '../widgets/Modal.svelte';

  $effect(() => {
    presenceStore.startPolling(5000);
    return () => {
      presenceStore.stopPolling();
    };
  });

  let agents = $derived(presenceStore.agents ?? []);
  let claims = $derived(presenceStore.claims ?? []);
  let worktrees = $derived(presenceStore.worktrees ?? []);

  // --- Tab management ---
  let activeTab = $state('agents');

  // --- Handoffs state ---
  let handoffs = $state([]);
  let templates = $state([]);
  let handoffLoading = $state(false);
  let showHandoffModal = $state(false);
  let newHandoffTo = $state('');
  let newHandoffSummary = $state('');
  let newHandoffContext = $state('');
  let creatingHandoff = $state(false);

  async function fetchHandoffs() {
    handoffLoading = true;
    try {
      const [hRes, tRes] = await Promise.all([
        globalThis.fetch('/api/handoffs'),
        globalThis.fetch('/api/templates'),
      ]);
      if (hRes.ok) {
        const hData = await hRes.json();
        handoffs = hData.handoffs ?? [];
      }
      if (tRes.ok) {
        const tData = await tRes.json();
        templates = tData.templates ?? [];
      }
    } catch (e) {
      // Silently fail — handoffs are supplementary
    } finally {
      handoffLoading = false;
    }
  }

  // Fetch handoffs when tab is shown
  $effect(() => {
    if (activeTab === 'handoffs') {
      fetchHandoffs();
    }
  });

  async function submitHandoff() {
    if (!newHandoffSummary.trim()) return;
    creatingHandoff = true;
    try {
      const res = await globalThis.fetch('/api/handoffs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          to_agent: newHandoffTo.trim() || undefined,
          summary: newHandoffSummary.trim(),
          context: newHandoffContext.trim() || undefined,
        }),
      });
      if (!res.ok) throw new Error(`Create handoff: ${res.status}`);
      toastStore.success('Handoff created');
      showHandoffModal = false;
      newHandoffTo = '';
      newHandoffSummary = '';
      newHandoffContext = '';
      await fetchHandoffs();
    } catch (e) {
      toastStore.error('Failed to create handoff');
    } finally {
      creatingHandoff = false;
    }
  }

  async function acceptHandoff(id) {
    try {
      const res = await globalThis.fetch(`/api/handoffs/${id}/accept`, { method: 'POST' });
      if (!res.ok) throw new Error(`Accept: ${res.status}`);
      toastStore.success('Handoff accepted');
      await fetchHandoffs();
    } catch (e) {
      toastStore.error('Failed to accept handoff');
    }
  }

  // --- File conflict detection ---
  let fileConflicts = $derived.by(() => {
    const fileCounts = {};
    claims.forEach(c => {
      if (!fileCounts[c.file_path]) fileCounts[c.file_path] = [];
      fileCounts[c.file_path].push(c.agent_id);
    });
    return Object.entries(fileCounts)
      .filter(([, agents]) => agents.length > 1)
      .map(([path, agents]) => ({ path, agents: [...new Set(agents)] }));
  });

  // --- Agent type colors ---
  const AGENT_COLORS = {
    claude: 'var(--agent-claude, #BC8CFF)',
    codex: 'var(--agent-codex, #3FB950)',
    gemini: 'var(--agent-gemini, #58A6FF)',
    copilot: 'var(--agent-copilot, #F78166)',
  };

  function agentColor(agentType) {
    if (!agentType) return 'var(--fg-secondary)';
    const lower = agentType.toLowerCase();
    for (const [key, color] of Object.entries(AGENT_COLORS)) {
      if (lower.includes(key)) return color;
    }
    return 'var(--fg-secondary)';
  }

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

  function handoffStatusVariant(status) {
    const map = {
      pending: 'warning',
      accepted: 'success',
      expired: 'error',
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
  <!-- Tab bar -->
  <div class="tab-bar">
    <button class="tab-btn" class:active={activeTab === 'agents'} onclick={() => { activeTab = 'agents'; }}>
      Agents <span class="tab-count">{agents.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'claims'} onclick={() => { activeTab = 'claims'; }}>
      Claims <span class="tab-count">{claims.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'worktrees'} onclick={() => { activeTab = 'worktrees'; }}>
      Worktrees <span class="tab-count">{worktrees.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'handoffs'} onclick={() => { activeTab = 'handoffs'; }}>
      Handoffs <span class="tab-count">{handoffs.length}</span>
    </button>
    <div class="tab-spacer"></div>
    {#if fileConflicts.length > 0}
      <span class="conflict-badge" title="{fileConflicts.length} file(s) claimed by multiple agents">
        ⚠ {fileConflicts.length} conflicts
      </span>
    {/if}
  </div>

  <div class="tab-content">
    {#if activeTab === 'agents'}
      <!-- Agent Presence -->
      <div class="presence-grid">
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
                    <td>
                      <span class="agent-type-chip" style:color={agentColor(agent.agent_type)}>
                        {agent.agent_type || '---'}
                      </span>
                    </td>
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

        <!-- Quick Stats -->
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
      </div>

    {:else if activeTab === 'claims'}
      <!-- File Claims -->
      <div class="card">
        <div class="card-header">
          <span class="card-title">File Claims</span>
          <span class="count-badge">{claims.length}</span>
        </div>

        {#if fileConflicts.length > 0}
          <div class="conflict-banner">
            <span class="conflict-icon">⚠</span>
            <span>{fileConflicts.length} file(s) claimed by multiple agents:</span>
            {#each fileConflicts as conflict}
              <div class="conflict-detail">
                <span class="text-mono text-xs">{shortPath(conflict.path)}</span>
                <span class="text-muted text-xs">→ {conflict.agents.join(', ')}</span>
              </div>
            {/each}
          </div>
        {/if}

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

    {:else if activeTab === 'worktrees'}
      <!-- Worktrees -->
      <div class="card">
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

    {:else if activeTab === 'handoffs'}
      <!-- Handoffs -->
      <div class="card">
        <div class="card-header">
          <span class="card-title">Agent Handoffs</span>
          <span class="count-badge">{handoffs.length}</span>
          <div class="card-actions">
            <button class="btn btn-sm" onclick={() => { showHandoffModal = true; }}>+ Handoff</button>
          </div>
        </div>

        {#if handoffLoading}
          <div class="loading-bar"><div class="loading-bar-inner"></div></div>
        {/if}

        <div class="table-wrap">
          <table>
            <thead>
              <tr>
                <th>From</th>
                <th>To</th>
                <th>Summary</th>
                <th>Status</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {#each handoffs as handoff (handoff.id)}
                <tr>
                  <td class="text-mono">{handoff.from_agent || '---'}</td>
                  <td class="text-mono">{handoff.to_agent || 'any'}</td>
                  <td class="truncate" title={handoff.summary}>{handoff.summary}</td>
                  <td><Badge text={handoff.status} variant={handoffStatusVariant(handoff.status)} /></td>
                  <td class="text-mono text-muted">{formatTime(handoff.created_at)}</td>
                  <td>
                    {#if handoff.status === 'pending'}
                      <button class="btn btn-xs btn-success" onclick={() => acceptHandoff(handoff.id)}>
                        Accept
                      </button>
                    {:else}
                      <span class="text-muted text-xs">{handoff.accepted_at ? formatTime(handoff.accepted_at) : '---'}</span>
                    {/if}
                  </td>
                </tr>
              {:else}
                <tr>
                  <td colspan="6" class="empty-cell">No handoffs</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        {#if templates.length > 0}
          <div class="templates-section">
            <div class="section-header">
              <span class="section-title">Session Templates</span>
            </div>
            <div class="template-list">
              {#each templates as tpl (tpl.id)}
                <div class="template-chip">
                  <span class="template-name text-mono">{tpl.name}</span>
                  <span class="text-muted text-xs">{tpl.description}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </div>
    {/if}
  </div>
</div>

<!-- Create Handoff Modal -->
<Modal title="Create Handoff" open={showHandoffModal} onclose={() => { showHandoffModal = false; }}>
  <div class="form-group">
    <label class="form-label">To Agent (optional)</label>
    <input type="text" bind:value={newHandoffTo} placeholder="Agent ID or leave blank for any..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label">Summary *</label>
    <input type="text" bind:value={newHandoffSummary} placeholder="What needs to be done..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label">Context</label>
    <textarea bind:value={newHandoffContext} placeholder="Additional context, findings, decisions..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => { showHandoffModal = false; }}>Cancel</button>
    <button class="btn btn-primary" onclick={submitHandoff} disabled={creatingHandoff || !newHandoffSummary.trim()}>
      {creatingHandoff ? 'Creating...' : 'Create Handoff'}
    </button>
  </div>
</Modal>

<style>
  .presence-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  /* Tab bar */
  .tab-bar {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 6px 0;
    border-bottom: 1px solid var(--border);
    margin-bottom: 8px;
  }

  .tab-btn {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 12px;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 500;
    color: var(--fg-secondary);
    cursor: pointer;
    border: none;
    background: transparent;
    transition: background 0.1s, color 0.1s;
  }

  .tab-btn:hover {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
  }

  .tab-btn.active {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
  }

  .tab-count {
    font-family: var(--font-mono);
    font-size: 10px;
    background: var(--bg-primary);
    padding: 1px 5px;
    border-radius: 8px;
    color: var(--fg-muted);
  }

  .tab-spacer { flex: 1; }

  .conflict-badge {
    font-size: 11px;
    font-weight: 600;
    color: var(--warning);
    padding: 2px 8px;
    background: rgba(210, 153, 34, 0.12);
    border-radius: 4px;
    border: 1px solid rgba(210, 153, 34, 0.25);
  }

  .tab-content {
    flex: 1;
    overflow-y: auto;
  }

  /* Presence grid (agents tab) */
  .presence-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
    height: 100%;
  }

  .agents-card {
    min-height: 200px;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .agents-card .table-wrap {
    flex: 1;
    overflow-y: auto;
  }

  .agent-type-chip {
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 500;
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

  /* Conflict banner */
  .conflict-banner {
    background: rgba(210, 153, 34, 0.08);
    border: 1px solid rgba(210, 153, 34, 0.2);
    border-radius: var(--border-radius);
    padding: 10px 14px;
    margin: 8px 0;
    font-size: 12px;
    color: var(--warning);
  }

  .conflict-icon {
    font-size: 14px;
    margin-right: 4px;
  }

  .conflict-detail {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 3px 0 3px 20px;
  }

  /* Card actions */
  .card-actions {
    margin-left: auto;
  }

  /* Handoff button */
  .btn-xs {
    padding: 2px 8px;
    font-size: 11px;
  }

  .btn-success {
    background: rgba(63, 185, 80, 0.15);
    color: var(--success);
    border: 1px solid rgba(63, 185, 80, 0.3);
  }

  .btn-success:hover {
    background: rgba(63, 185, 80, 0.25);
  }

  /* Templates section */
  .templates-section {
    border-top: 1px solid var(--border);
    padding: 12px 0 0;
    margin-top: 12px;
  }

  .section-header {
    margin-bottom: 8px;
  }

  .section-title {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
  }

  .template-list {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .template-chip {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 10px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
  }

  .template-name {
    font-size: 12px;
    font-weight: 500;
    color: var(--fg-primary);
  }

  /* Loading bar */
  .loading-bar {
    height: 2px;
    background: var(--bg-tertiary);
    border-radius: 1px;
    overflow: hidden;
    margin-bottom: 4px;
  }

  .loading-bar-inner {
    width: 40%;
    height: 100%;
    background: var(--accent);
    border-radius: 1px;
    animation: loadingSlide 1s ease-in-out infinite;
  }

  @keyframes loadingSlide {
    0% { transform: translateX(-100%); }
    100% { transform: translateX(300%); }
  }

  /* Form styles */
  .form-group {
    margin-bottom: 12px;
  }

  .form-label {
    display: block;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.3px;
    color: var(--fg-muted);
    margin-bottom: 4px;
  }

  .form-input {
    width: 100%;
    box-sizing: border-box;
  }

  textarea.form-input {
    resize: vertical;
    font-family: var(--font-sans);
    font-size: 13px;
  }

  .form-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
  }
</style>
