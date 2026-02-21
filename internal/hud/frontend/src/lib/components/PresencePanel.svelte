<script>
  import { presenceStore } from '../stores/presence.svelte.ts';
  import { presenceActionsStore } from '../stores/presenceActions.svelte.ts';
  import PresenceAgentsTab from './presence/PresenceAgentsTab.svelte';
  import PresenceClaimsTab from './presence/PresenceClaimsTab.svelte';
  import PresenceWorktreesTab from './presence/PresenceWorktreesTab.svelte';
  import PresenceHandoffsTab from './presence/PresenceHandoffsTab.svelte';
  import PresenceDiagnosticsTab from './presence/PresenceDiagnosticsTab.svelte';
  import DispatchTaskModal from './presence/DispatchTaskModal.svelte';
  import NudgeAgentModal from './presence/NudgeAgentModal.svelte';
  import CreateHandoffModal from './presence/CreateHandoffModal.svelte';

  $effect(() => {
    presenceStore.startPolling(5000);
    return () => {
      presenceStore.stopPolling();
    };
  });

  let agents = $derived(presenceStore.agents ?? []);
  let claims = $derived(presenceStore.claims ?? []);
  let worktrees = $derived(presenceStore.worktrees ?? []);
  let fileConflicts = $derived(presenceStore.fileConflicts);

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

<DispatchTaskModal />
<NudgeAgentModal />
<CreateHandoffModal />

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
</style>
