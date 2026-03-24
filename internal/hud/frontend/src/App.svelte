<script>
  import { onMount, onDestroy } from 'svelte';
  import { router, views, overviewId } from './lib/stores/router.svelte.ts';
  import { fleetStore } from './lib/stores/fleet.svelte.ts';
  import { healthStore } from './lib/stores/health.svelte.ts';
  import { taskStore } from './lib/stores/tasks.svelte.ts';
  import { streamStore } from './lib/stores/stream.svelte.ts';
  import { eventStore } from './lib/stores/events.svelte.ts';
  import { overlayStore } from './lib/stores/overlay.svelte.ts';
  import { formatTime as fmtTime } from './lib/utils/format.ts';
  import ViewShell from './lib/components/shared/ViewShell.svelte';
  import FleetPanel from './lib/components/FleetPanel.svelte';
  import ServersPanel from './lib/components/ServersPanel.svelte';
  import TasksPanel from './lib/components/TasksPanel.svelte';
  import WorkflowsPanel from './lib/components/WorkflowsPanel.svelte';
  import MemoryPanel from './lib/components/MemoryPanel.svelte';
  import StreamPanel from './lib/components/StreamPanel.svelte';
  import PresencePanel from './lib/components/PresencePanel.svelte';
  import ReasoningPanel from './lib/components/ReasoningPanel.svelte';
  import KnowledgePanel from './lib/components/KnowledgePanel.svelte';
  import SandboxPanel from './lib/components/SandboxPanel.svelte';
  import SpawnPanel from './lib/components/SpawnPanel.svelte';
  import CatalogPanel from './lib/components/CatalogPanel.svelte';
  import DispatchPanel from './lib/components/DispatchPanel.svelte';
  import TimelinePanel from './lib/components/TimelinePanel.svelte';
  import EmptyState from './lib/components/shared/EmptyState.svelte';
  import CommandPalette from './lib/components/CommandPalette.svelte';
  import ConnectionBanner from './lib/components/ConnectionBanner.svelte';
  import OverviewPanel from './lib/components/OverviewPanel.svelte';
  import OverlayShell from './lib/components/OverlayShell.svelte';
  import Toast from './lib/widgets/Toast.svelte';

  let showCommandPalette = $state(false);
  let showKeyboardHelp = $state(false);

  onMount(() => {
    overlayStore.init();
    if (overlayStore.enabled) return;

    router.init();
    eventStore.connect();
    fleetStore.fetch();
    healthStore.fetch();
  });
  onDestroy(() => {
    eventStore.disconnect();
  });

  function isVisibleElement(node) {
    return !!node && !!(node.offsetWidth || node.offsetHeight || node.getClientRects?.().length);
  }

  function focusPrimaryPanelSearch() {
    const main = document.getElementById('main-content');
    if (!main) return false;
    const candidates = main.querySelectorAll('[data-panel-search="primary"], .panel-search-input');
    for (const candidate of candidates) {
      if (candidate instanceof HTMLInputElement && isVisibleElement(candidate)) {
        candidate.focus();
        candidate.select();
        return true;
      }
    }
    return false;
  }

  // Keyboard shortcuts — view switching + sub-view switching
  function handleKeydown(e) {
    if (overlayStore.enabled) return;
    const tag = e.target?.tagName;
    const isInput =
      tag === 'INPUT' ||
      tag === 'TEXTAREA' ||
      tag === 'SELECT' ||
      e.target?.isContentEditable;

    if (e.key === 'Escape') {
      if (showCommandPalette) { showCommandPalette = false; return; }
      if (showKeyboardHelp) { showKeyboardHelp = false; return; }
      // Clear detail if open
      if (router.detail) { router.back(); return; }
      return;
    }

    // Cmd/Ctrl + F → focus search
    if ((e.metaKey || e.ctrlKey) && e.key === 'f' && !e.altKey) {
      if (focusPrimaryPanelSearch()) {
        e.preventDefault();
        return;
      }
    }

    if (!isInput && !e.metaKey && !e.ctrlKey && !e.altKey) {
      // ` or o → Overview
      if (e.key === '`' || e.key === 'o') {
        router.navigate(overviewId);
        return;
      }
      // / → focus search
      if (e.key === '/') {
        e.preventDefault();
        focusPrimaryPanelSearch();
        return;
      }
      // r → refresh
      if (e.key === 'r') {
        fleetStore.fetch();
        healthStore.fetch();
        return;
      }
      // ? → keyboard help
      if (e.key === '?') {
        showKeyboardHelp = !showKeyboardHelp;
        return;
      }

      // 1-6 → view switching
      const num = parseInt(e.key);
      if (num >= 1 && num <= views.length) {
        router.navigate(views[num - 1].id);
        return;
      }

      // a-d → sub-view switching within current view
      const subIdx = e.key.charCodeAt(0) - 'a'.charCodeAt(0);
      if (subIdx >= 0 && subIdx <= 3) {
        const vd = router.currentViewDef;
        if (vd && subIdx < vd.subViews.length) {
          router.navigateSub(vd.subViews[subIdx].id);
          return;
        }
      }
    }
  }

  // Command palette handler — uses legacy navigate (auto-redirects)
  function handleCommand(item) {
    switch (item.id) {
      case 'refresh-all':
        fleetStore.fetch();
        healthStore.fetch();
        break;
      case 'pause-stream':
        streamStore.togglePause();
        break;
      case 'toggle-scanlines':
        document.body.classList.toggle('scanlines');
        break;
      default:
        // Navigate: works for both view IDs and legacy panel IDs
        router.navigate(item.id);
        break;
    }
  }

  // Render the correct sub-panel based on router.subView (router.panel alias)
  function renderSubPanel(subView) {
    return subView;
  }

  // Status bar derived values
  let daemonOnline = $derived(fleetStore.status.running);
  let serverCount = $derived(fleetStore.status.servers);
  let activeSessionCount = $derived(fleetStore.activeSessions.length);
  let healthySrv = $derived(healthStore.healthyCount);
  let availableSrv = $derived(healthStore.availableCount);
  let degradedSrv = $derived(healthStore.degradedCount);
  let downSrv = $derived(healthStore.downCount);

  // Badge counts for nav tabs
  let badgeCounts = $derived({
    agents: fleetStore.activeSessions.length,
    infra: healthStore.degradedCount + healthStore.downCount,
    tasks: taskStore.pendingCount + taskStore.inProgressCount,
  });
</script>

<svelte:window onkeydown={handleKeydown} />

{#if overlayStore.enabled}
  <OverlayShell />
{:else}
<a class="skip-link" href="#main-content">Skip to content</a>
<div class="hud-shell">
  <!-- Top navigation bar -->
  <header class="nav-bar">
    <div class="nav-brand">
      <span class="nav-logo">{'\u25C8'}</span>
      <span class="nav-title">LOOM HUD</span>
    </div>

    <nav class="nav-tabs" aria-label="Main navigation">
      <!-- Overview tab -->
      <button
        class="nav-tab"
        class:active={router.view === overviewId}
        onclick={() => { router.navigate(overviewId); }}
        aria-current={router.view === overviewId ? 'page' : undefined}
        title="Overview (` or o)"
      >
        <span class="nav-tab-icon">{'\u25A3'}</span>
        <span class="nav-tab-label">Overview</span>
        <kbd class="nav-tab-key">o</kbd>
      </button>

      <span class="nav-divider"></span>

      <!-- Grouped view tabs -->
      {#each views as v}
        <button
          class="nav-tab"
          class:active={router.view === v.id}
          onclick={() => { router.navigate(v.id); }}
          aria-current={router.view === v.id ? 'page' : undefined}
          title="{v.label} ({v.key})"
        >
          <span class="nav-tab-icon">{v.icon}</span>
          <span class="nav-tab-label">{v.label}</span>
          {#if badgeCounts[v.id] > 0}
            <span class="nav-badge">{badgeCounts[v.id]}</span>
          {/if}
          <kbd class="nav-tab-key">{v.key}</kbd>
        </button>
      {/each}
    </nav>

    <div class="nav-actions">
      <button
        class="btn btn-ghost"
        onclick={() => { showCommandPalette = true; }}
        title="Command Palette (Cmd+K)"
        aria-label="Open command palette"
      >
        {'\u2318'}K
      </button>
    </div>
  </header>

  <ConnectionBanner />

  <!-- Main content area -->
  <main class="panel-area" id="main-content">
    {#key router.view}
      <div class="panel-enter">
        {#if router.view === overviewId}
          <OverviewPanel />
        {:else}
          {#key router.subView}
            {@const vd = router.currentViewDef}
            {#if vd}
              <ViewShell
                subViews={vd.subViews}
                activeSubView={router.subView}
                onSwitch={(id) => router.navigateSub(id)}
              >
                {#if router.subView === 'fleet'}
                  <FleetPanel />
                {:else if router.subView === 'dispatch'}
                  <DispatchPanel />
                {:else if router.subView === 'servers'}
                  <ServersPanel />
                {:else if router.subView === 'catalog'}
                  <CatalogPanel />
                {:else if router.subView === 'tasks'}
                  <TasksPanel />
                {:else if router.subView === 'workflows'}
                  <WorkflowsPanel />
                {:else if router.subView === 'feed'}
                  <KnowledgePanel />
                {:else if router.subView === 'memory'}
                  <MemoryPanel />
                {:else if router.subView === 'graph'}
                  {#await import('./lib/components/GraphPanel.svelte')}
                    <div class="panel-loading"><div class="loading-bar"><div class="loading-bar-inner"></div></div></div>
                  {:then { default: GraphPanel }}
                    <GraphPanel />
                  {:catch}
                    <EmptyState icon="!" heading="Failed to load panel" compact />
                  {/await}
                {:else if router.subView === 'timeline'}
                  <TimelinePanel />
                {:else if router.subView === 'stream'}
                  <StreamPanel />
                {:else if router.subView === 'presence'}
                  <PresencePanel />
                {:else if router.subView === 'sandbox'}
                  <SandboxPanel />
                {:else if router.subView === 'spawn'}
                  <SpawnPanel />
                {:else if router.subView === 'reasoning'}
                  <ReasoningPanel />
                {:else if router.subView === 'topology'}
                  {#await import('./lib/components/TopologyPanel.svelte')}
                    <div class="panel-loading"><div class="loading-bar"><div class="loading-bar-inner"></div></div></div>
                  {:then { default: TopologyPanel }}
                    <TopologyPanel />
                  {:catch}
                    <EmptyState icon="!" heading="Failed to load panel" compact />
                  {/await}
                {:else if router.subView === 'lifecycle'}
                  {#await import('./lib/components/LifecyclePanel.svelte')}
                    <div class="panel-loading"><div class="loading-bar"><div class="loading-bar-inner"></div></div></div>
                  {:then { default: LifecyclePanel }}
                    <LifecyclePanel />
                  {:catch}
                    <EmptyState icon="!" heading="Failed to load panel" compact />
                  {/await}
                {/if}
              </ViewShell>
            {/if}
          {/key}
        {/if}
      </div>
    {/key}
  </main>

  <!-- Status bar -->
  <footer class="status-bar" role="status" aria-label="System status">
    <div class="status-bar-left">
      <span class="status-indicator" class:online={daemonOnline} class:offline={!daemonOnline}></span>
      <span class="status-text">{daemonOnline ? 'Connected' : 'Disconnected'}</span>
      <span class="status-divider"></span>
      <span class="status-text">{serverCount} servers</span>
      {#if degradedSrv > 0}
        <span class="status-text" style="color: var(--warning);">({degradedSrv} degraded)</span>
      {/if}
      <span class="status-divider"></span>
      <span class="status-text">{activeSessionCount} active session{activeSessionCount !== 1 ? 's' : ''}</span>
    </div>
    <div class="status-bar-right">
      <span class="status-text text-muted">{availableSrv}/{serverCount} healthy</span>
      {#if downSrv > 0}
        <span class="status-text" style="color: var(--error);">({downSrv} down)</span>
      {/if}
      <span class="status-divider"></span>
      <span class="status-text text-mono">{fmtTime(fleetStore.lastUpdated)}</span>
    </div>
  </footer>

  <CommandPalette bind:open={showCommandPalette} onselect={handleCommand} />

  <!-- Keyboard shortcut help overlay -->
  {#if showKeyboardHelp}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="keyboard-help-overlay" role="presentation" onclick={() => { showKeyboardHelp = false; }}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="keyboard-help" role="dialog" aria-label="Keyboard shortcuts" tabindex="-1" onclick={(e) => e.stopPropagation()}>
        <div class="help-title">Keyboard Shortcuts</div>
        <div class="help-grid">
          <div class="help-section">
            <div class="help-section-title">Views</div>
            <div class="help-row"><kbd>`</kbd> / <kbd>o</kbd> <span>Overview</span></div>
            {#each views as v}
              <div class="help-row"><kbd>{v.key}</kbd> <span>{v.label}</span></div>
            {/each}
          </div>
          <div class="help-section">
            <div class="help-section-title">Sub-views</div>
            <div class="help-row"><kbd>a</kbd>-<kbd>d</kbd> <span>Switch within view</span></div>
            <div class="help-section-title" style="margin-top: var(--space-3);">Actions</div>
            <div class="help-row"><kbd>/</kbd> <span>Focus search</span></div>
            <div class="help-row"><kbd>r</kbd> <span>Refresh data</span></div>
            <div class="help-row"><kbd>{'\u2318'}K</kbd> <span>Command palette</span></div>
          </div>
          <div class="help-section">
            <div class="help-section-title">General</div>
            <div class="help-row"><kbd>?</kbd> <span>Toggle this help</span></div>
            <div class="help-row"><kbd>Esc</kbd> <span>Close / back</span></div>
          </div>
        </div>
      </div>
    </div>
  {/if}

  <Toast />
</div>
{/if}

<style>
  .skip-link {
    position: absolute;
    top: -40px;
    left: 0;
    padding: 8px 16px;
    background: var(--accent);
    color: var(--bg-primary);
    font-weight: 600;
    font-size: 13px;
    z-index: 1000;
    transition: top 0.15s ease;
  }

  .skip-link:focus {
    top: 0;
  }

  .hud-shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
    width: 100vw;
    overflow: hidden;
  }

  /* ---- Nav Bar ---- */
  .nav-bar {
    display: flex;
    align-items: center;
    height: var(--header-height);
    background: var(--bg-secondary);
    border-bottom: 1px solid var(--border);
    padding: 0 var(--space-3);
    flex-shrink: 0;
    gap: var(--space-3);
    z-index: 100;
  }

  .nav-brand {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    flex-shrink: 0;
  }

  .nav-logo {
    font-size: var(--text-lg);
    color: var(--accent);
  }

  .nav-title {
    font-size: var(--text-sm);
    font-weight: 700;
    letter-spacing: 1.5px;
    color: var(--fg-secondary);
    text-transform: uppercase;
  }

  .nav-tabs {
    display: flex;
    gap: 2px;
    flex: 1;
    justify-content: center;
    align-items: center;
  }

  .nav-divider {
    width: 1px;
    height: 16px;
    background: var(--border);
    margin: 0 var(--space-1);
  }

  .nav-tab {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: var(--space-1) var(--space-2);
    border-radius: var(--radius-sm);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    transition: background var(--transition-fast), color var(--transition-fast);
    position: relative;
    cursor: pointer;
    background: none;
    border: none;
  }

  .nav-tab:hover {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
  }

  .nav-tab.active {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
    box-shadow: 0 0 8px var(--glow-accent);
  }

  .nav-tab.active::after {
    content: '';
    position: absolute;
    bottom: -1px;
    left: var(--space-2);
    right: var(--space-2);
    height: 2px;
    background: var(--accent);
    border-radius: 1px;
  }

  .nav-tab-icon {
    font-size: var(--text-sm);
    opacity: 0.7;
  }

  .nav-tab-label {
    font-weight: 500;
  }

  .nav-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    font-weight: 600;
    line-height: 1;
    color: var(--bg-primary);
    background: var(--accent);
    border-radius: 8px;
  }

  .nav-tab-key {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    padding: 1px 3px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    line-height: 1;
    background: none;
  }

  .nav-actions {
    flex-shrink: 0;
  }

  /* ---- Panel Area ---- */
  .panel-area {
    flex: 1;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  /* ---- Status Bar ---- */
  .status-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: var(--statusbar-height);
    background: var(--bg-secondary);
    border-top: 1px solid var(--border);
    padding: 0 var(--space-3);
    flex-shrink: 0;
    z-index: 100;
  }

  .status-bar-left,
  .status-bar-right {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .status-indicator {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .status-indicator.online {
    background: var(--success);
    box-shadow: 0 0 4px var(--success);
  }

  .status-indicator.offline {
    background: var(--error);
    box-shadow: 0 0 4px var(--error);
    animation: pulse 1.5s infinite;
  }

  .status-text {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
  }

  .status-divider {
    width: 1px;
    height: 12px;
    background: var(--border);
  }

  /* ---- Keyboard Help Overlay ---- */
  .keyboard-help-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 23, 26, 0.75);
    backdrop-filter: blur(4px);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 1000;
    animation: fadeIn 0.15s ease-out;
  }

  .keyboard-help {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-lg);
    padding: var(--space-6) var(--space-6);
    max-width: 520px;
    width: 90%;
    box-shadow: var(--elevation-3);
  }

  .help-title {
    font-size: var(--text-base);
    font-weight: 700;
    color: var(--fg-primary);
    margin-bottom: var(--space-4);
    letter-spacing: var(--tracking-normal);
    text-shadow: 0 0 8px rgba(129, 240, 254, 0.2);
  }

  .help-grid {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: var(--space-4);
  }

  .help-section-title {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    margin-bottom: var(--space-2);
  }

  .help-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    margin-bottom: var(--space-1);
  }

  .help-row kbd {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    padding: 2px 5px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    color: var(--fg-primary);
    min-width: 18px;
    text-align: center;
  }

  /* ---- Lazy panel loading ---- */
  .panel-loading {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: var(--space-6);
    min-height: 200px;
  }

  .loading-bar {
    width: 120px;
    height: 3px;
    background: var(--bg-tertiary);
    border-radius: 2px;
    overflow: hidden;
  }

  .loading-bar-inner {
    width: 40%;
    height: 100%;
    background: var(--accent);
    border-radius: 2px;
    animation: loadSlide 1s ease-in-out infinite;
  }

  @keyframes loadSlide {
    0%   { transform: translateX(-100%); }
    100% { transform: translateX(350%); }
  }
</style>
