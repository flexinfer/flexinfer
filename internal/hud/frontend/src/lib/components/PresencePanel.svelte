<script>
  import { presenceStore } from '../stores/presence.svelte.ts';
  import { presenceActionsStore } from '../stores/presenceActions.svelte.ts';
  import Modal from '../widgets/Modal.svelte';
  import PresenceAgentsTab from './presence/PresenceAgentsTab.svelte';
  import PresenceClaimsTab from './presence/PresenceClaimsTab.svelte';
  import PresenceWorktreesTab from './presence/PresenceWorktreesTab.svelte';
  import PresenceHandoffsTab from './presence/PresenceHandoffsTab.svelte';
  import PresenceDiagnosticsTab from './presence/PresenceDiagnosticsTab.svelte';

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

  function setActiveTab(nextTab) {
    activeTab = nextTab;
  }

  $effect(() => {
    if (activeTab === 'handoffs') {
      presenceActionsStore.refreshHandoffs();
    }
  });

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


  // --- Agents view toggle: table vs cards ---
  let agentView = $state('cards');
</script>

<div class="panel presence-panel">
  <!-- Tab bar -->
  <div class="tab-bar">
    <button class="tab-btn" class:active={activeTab === 'agents'} onclick={() => setActiveTab('agents')}>
      Agents <span class="tab-count">{agents.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'claims'} onclick={() => setActiveTab('claims')}>
      Claims <span class="tab-count">{claims.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'worktrees'} onclick={() => setActiveTab('worktrees')}>
      Worktrees <span class="tab-count">{worktrees.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'handoffs'} onclick={() => setActiveTab('handoffs')}>
      Handoffs <span class="tab-count">{presenceActionsStore.handoffs.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'diagnostics'} onclick={() => setActiveTab('diagnostics')}>
      Diagnostics
    </button>
    <div class="tab-spacer"></div>
    {#if fileConflicts.length > 0}
      <span class="conflict-badge" title="{fileConflicts.length} file(s) claimed by multiple agents">
        ⚠ {fileConflicts.length} conflicts
      </span>
    {/if}
    {#if activeTab === 'agents'}
      <div class="view-toggle">
        <button class="toggle-btn" class:active={agentView === 'cards'} onclick={() => { agentView = 'cards'; }} title="Card view">{'\u25A3'}</button>
        <button class="toggle-btn" class:active={agentView === 'table'} onclick={() => { agentView = 'table'; }} title="Table view">{'\u2261'}</button>
      </div>
    {/if}
  </div>

  <div class="tab-content">
    {#if activeTab === 'agents'}
      <PresenceAgentsTab
        {agents}
        {claims}
        {worktrees}
        activeCount={presenceStore.activeCount}
        idleCount={presenceStore.idleCount}
        offlineCount={presenceStore.offlineCount}
        claimedFilesCount={presenceStore.claimedFiles.length}
        {agentView}
        onOpenDispatch={(agentId) => presenceActionsStore.onOpenDispatch(agentId)}
        onOpenNudge={(agentId) => presenceActionsStore.onOpenNudge(agentId)}
      />

    {:else if activeTab === 'claims'}
      <PresenceClaimsTab
        {claims}
        {fileConflicts}
        onReleaseClaim={(agentId, filePath) => presenceActionsStore.onReleaseClaim(agentId, filePath)}
      />

    {:else if activeTab === 'worktrees'}
      <PresenceWorktreesTab {worktrees} />

    {:else if activeTab === 'handoffs'}
      <PresenceHandoffsTab
        handoffs={presenceActionsStore.handoffs}
        templates={presenceActionsStore.templates}
        handoffLoading={presenceActionsStore.handoffLoading}
        handoffError={presenceActionsStore.handoffError}
        onOpenHandoffModal={() => presenceActionsStore.openHandoffModal()}
        onAcceptHandoff={(id) => presenceActionsStore.onAcceptHandoff(id)}
      />

    {:else if activeTab === 'diagnostics'}
      <PresenceDiagnosticsTab {agents} />
    {/if}
  </div>
</div>

<!-- Dispatch Task Modal -->
<Modal title="Dispatch Task" open={presenceActionsStore.showDispatchModal} onclose={() => presenceActionsStore.closeDispatchModal()}>
  <div class="form-group">
    <label class="form-label" for="dispatch-target">Target Agent</label>
    <input id="dispatch-target" type="text" bind:value={presenceActionsStore.dispatchTargetAgent} class="form-input" readonly />
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-title">Title *</label>
    <input id="dispatch-title" type="text" bind:value={presenceActionsStore.dispatchTitle} placeholder="Task title..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-context">Context</label>
    <textarea id="dispatch-context" bind:value={presenceActionsStore.dispatchContext} placeholder="Additional instructions..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-priority">Priority</label>
    <select id="dispatch-priority" bind:value={presenceActionsStore.dispatchPriority} class="form-input">
      <option value="low">Low</option>
      <option value="medium">Medium</option>
      <option value="high">High</option>
      <option value="critical">Critical</option>
    </select>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => presenceActionsStore.closeDispatchModal()}>Cancel</button>
    <button class="btn btn-primary" onclick={() => presenceActionsStore.submitDispatch()} disabled={presenceActionsStore.dispatchSubmitting || !presenceActionsStore.dispatchTitle.trim()}>
      {presenceActionsStore.dispatchSubmitting ? 'Dispatching...' : 'Dispatch'}
    </button>
  </div>
</Modal>

<!-- Nudge Agent Modal -->
<Modal title="Nudge Agent" open={presenceActionsStore.showNudgeModal} onclose={() => presenceActionsStore.closeNudgeModal()}>
  <div class="form-group">
    <label class="form-label" for="nudge-target">Target Agent</label>
    <input id="nudge-target" type="text" bind:value={presenceActionsStore.nudgeTargetAgent} class="form-input" readonly />
  </div>
  <div class="form-group">
    <label class="form-label" for="nudge-type">Type</label>
    <select id="nudge-type" bind:value={presenceActionsStore.nudgeType} class="form-input">
      <option value="message">Message</option>
      <option value="context_inject">Context Inject</option>
      <option value="task_redirect">Task Redirect</option>
      <option value="pause_request">Pause Request</option>
    </select>
  </div>
  <div class="form-group">
    <label class="form-label" for="nudge-content">Content *</label>
    <textarea id="nudge-content" bind:value={presenceActionsStore.nudgeContent} placeholder="Message or context to send to the agent..." class="form-input" rows="4"></textarea>
  </div>
  <div class="nudge-hint">
    Nudge delivered on the agent's next heartbeat (5-15s latency).
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => presenceActionsStore.closeNudgeModal()}>Cancel</button>
    <button class="btn btn-primary" onclick={() => presenceActionsStore.submitNudge()} disabled={presenceActionsStore.nudgeSubmitting || !presenceActionsStore.nudgeContent.trim()}>
      {presenceActionsStore.nudgeSubmitting ? 'Sending...' : 'Send Nudge'}
    </button>
  </div>
</Modal>

<!-- Create Handoff Modal -->
<Modal title="Create Handoff" open={presenceActionsStore.showHandoffModal} onclose={() => presenceActionsStore.closeHandoffModal()}>
  <div class="form-group">
    <label class="form-label" for="handoff-to-agent">To Agent (optional)</label>
    <input id="handoff-to-agent" type="text" bind:value={presenceActionsStore.newHandoffTo} placeholder="Agent ID or leave blank for any..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-summary">Summary *</label>
    <input id="handoff-summary" type="text" bind:value={presenceActionsStore.newHandoffSummary} placeholder="What needs to be done..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-context">Context</label>
    <textarea id="handoff-context" bind:value={presenceActionsStore.newHandoffContext} placeholder="Additional context, findings, decisions..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => presenceActionsStore.closeHandoffModal()}>Cancel</button>
    <button class="btn btn-primary" onclick={() => presenceActionsStore.submitHandoff()} disabled={presenceActionsStore.creatingHandoff || !presenceActionsStore.newHandoffSummary.trim()}>
      {presenceActionsStore.creatingHandoff ? 'Creating...' : 'Create Handoff'}
    </button>
  </div>
</Modal>

<style>
  .presence-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

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
    border-radius: var(--radius-sm);
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
    border-radius: var(--radius-md);
    color: var(--fg-muted);
  }

  .tab-spacer { flex: 1; }

  .view-toggle {
    display: flex;
    gap: 2px;
    background: var(--bg-primary);
    border-radius: var(--radius-sm);
    padding: 2px;
  }

  .toggle-btn {
    padding: 3px 8px;
    font-size: 12px;
    color: var(--fg-muted);
    background: transparent;
    border: none;
    border-radius: var(--radius-sm);
    cursor: pointer;
    line-height: 1;
  }

  .toggle-btn:hover {
    color: var(--fg-primary);
    background: var(--bg-tertiary);
  }

  .toggle-btn.active {
    color: var(--fg-primary);
    background: var(--bg-tertiary);
  }

  .conflict-badge {
    font-size: 11px;
    font-weight: 600;
    color: var(--warning);
    padding: 2px 8px;
    background: rgba(231, 179, 18, 0.12);
    border-radius: var(--radius-sm);
    border: 1px solid rgba(231, 179, 18, 0.25);
  }

  .tab-content {
    flex: 1;
    overflow-y: auto;
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

  .nudge-hint {
    font-size: 11px;
    color: var(--fg-muted);
    font-style: italic;
    margin-top: 8px;
  }

</style>
