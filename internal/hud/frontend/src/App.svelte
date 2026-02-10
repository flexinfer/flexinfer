<script>
  import { onMount, onDestroy } from 'svelte';
  import { router } from './lib/stores/router.svelte.ts';
  import { fleetStore } from './lib/stores/fleet.svelte.ts';
  import { healthStore } from './lib/stores/health.svelte.ts';
  import { streamStore } from './lib/stores/stream.svelte.ts';
  import { eventStore } from './lib/stores/events.svelte.ts';
  import { overlayStore } from './lib/stores/overlay.svelte.ts';
  import FleetPanel from './lib/components/FleetPanel.svelte';
  import ServersPanel from './lib/components/ServersPanel.svelte';
  import TasksPanel from './lib/components/TasksPanel.svelte';
  import WorkflowsPanel from './lib/components/WorkflowsPanel.svelte';
  import MemoryPanel from './lib/components/MemoryPanel.svelte';
  import GraphPanel from './lib/components/GraphPanel.svelte';
  import StreamPanel from './lib/components/StreamPanel.svelte';
  import PresencePanel from './lib/components/PresencePanel.svelte';
  import ReasoningPanel from './lib/components/ReasoningPanel.svelte';
  import CommandPalette from './lib/components/CommandPalette.svelte';
  import ConnectionBanner from './lib/components/ConnectionBanner.svelte';
  import OverviewPanel from './lib/components/OverviewPanel.svelte';
  import OverlayShell from './lib/components/OverlayShell.svelte';
  import Toast from './lib/widgets/Toast.svelte';

  const panels = [
    { id: 'fleet',     label: 'Fleet',     key: '1', icon: '\u25C8' },
    { id: 'servers',   label: 'Servers',   key: '2', icon: '\u2665' },
    { id: 'tasks',     label: 'Tasks',     key: '3', icon: '\u2611' },
    { id: 'workflows', label: 'Workflows', key: '4', icon: '\u2699' },
    { id: 'memory',    label: 'Memory',    key: '5', icon: '\u29BE' },
    { id: 'graph',     label: 'Graph',     key: '6', icon: '\u2B21' },
    { id: 'stream',    label: 'Stream',    key: '7', icon: '\u2261' },
    { id: 'presence',  label: 'Presence',  key: '8', icon: '\u25C9' },
    { id: 'reasoning', label: 'Reasoning', key: '0', icon: '\u2726' },
  ];

  let showCommandPalette = $state(false);
  let showKeyboardHelp = $state(false);

  onMount(() => {
    // Detect overlay mode from URL query parameter (?overlay=1).
    overlayStore.init();

    // In overlay mode, OverlayShell manages its own store lifecycle.
    if (overlayStore.enabled) return;

    // Initialize hash-based router.
    router.init();

    // Connect SSE and start stores with 30s fallback polling.
    eventStore.connect();
    fleetStore.fetch();
    healthStore.fetch();
  });
  onDestroy(() => {
    eventStore.disconnect();
  });

  // Keyboard shortcuts (number keys for panel switching — disabled in overlay mode)
  function handleKeydown(e) {
    if (overlayStore.enabled) return;
    const tag = e.target?.tagName;
    const isInput = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';

    // Escape: close any open modal/detail view
    if (e.key === 'Escape') {
      if (showCommandPalette) {
        showCommandPalette = false;
        return;
      }
      if (showKeyboardHelp) {
        showKeyboardHelp = false;
        return;
      }
      return;
    }

    // Number keys 1-8, 0 for panel switching, ` or o for overview (only when not in an input)
    if (!isInput && !e.metaKey && !e.ctrlKey && !e.altKey) {
      if (e.key === '0') {
        router.navigate('reasoning');
        return;
      }
      if (e.key === '`' || e.key === 'o') {
        router.navigate('overview');
        return;
      }
      // / → focus search/filter in current panel
      if (e.key === '/') {
        e.preventDefault();
        const searchInput = document.querySelector('.panel-search-input');
        if (searchInput) searchInput.focus();
        return;
      }
      // r → force refresh current panel data
      if (e.key === 'r') {
        fleetStore.fetch();
        healthStore.fetch();
        return;
      }
      // ? → show keyboard shortcut overlay
      if (e.key === '?') {
        showKeyboardHelp = !showKeyboardHelp;
        return;
      }
      const num = parseInt(e.key);
      if (num >= 1 && num <= 8) {
        router.navigate(panels[num - 1].id);
        return;
      }
    }
  }

  // Handle command palette selections
  function handleCommand(item) {
    // Panel navigation
    const panelIds = panels.map(p => p.id);
    if (panelIds.includes(item.id)) {
      router.navigate(item.id);
      return;
    }

    // Actions
    switch (item.id) {
      case 'refresh-all':
        fleetStore.fetch();
        healthStore.fetch();
        break;
      case 'pause-stream':
        streamStore.togglePause();
        break;
      case 'create-task':
        router.navigate('tasks');
        break;
      case 'seed-entity':
        router.navigate('graph');
        break;
      case 'create-handoff':
        router.navigate('presence');
        break;
      case 'approve-workflow':
        router.navigate('workflows');
        break;
      case 'reject-workflow':
        router.navigate('workflows');
        break;
      case 'promote-memory':
        router.navigate('memory');
        break;
      case 'demote-memory':
        router.navigate('memory');
        break;
      case 'add-memory':
        router.navigate('memory');
        break;
      case 'toggle-scanlines':
        document.body.classList.toggle('scanlines');
        break;
    }
  }

  // Status bar derived values
  let daemonOnline = $derived(fleetStore.status.running);
  let serverCount = $derived(fleetStore.status.servers);
  let activeSessionCount = $derived(fleetStore.activeSessions.length);
  let healthySrv = $derived(healthStore.healthyCount);
  let availableSrv = $derived(healthStore.availableCount);
  let degradedSrv = $derived(healthStore.degradedCount);
  let downSrv = $derived(healthStore.downCount);

  function formatTime(d) {
    if (!d) return '--:--';
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if overlayStore.enabled}
  <OverlayShell />
{:else}
<div class="hud-shell">
  <!-- Top navigation bar -->
  <header class="nav-bar">
    <div class="nav-brand">
      <span class="nav-logo">{'\u25C8'}</span>
      <span class="nav-title">LOOM HUD</span>
    </div>

    <nav class="nav-tabs">
      {#each panels as panel}
        <button
          class="nav-tab"
          class:active={router.panel === panel.id}
          onclick={() => { router.navigate(panel.id); }}
          title="{panel.label} ({panel.key})"
        >
          <span class="nav-tab-icon">{panel.icon}</span>
          <span class="nav-tab-label">{panel.label}</span>
          <span class="nav-tab-key">{panel.key}</span>
        </button>
      {/each}
    </nav>

    <div class="nav-actions">
      <button
        class="btn btn-ghost"
        onclick={() => { showCommandPalette = true; }}
        title="Command Palette (Cmd+K)"
      >
        {'\u2318'}K
      </button>
    </div>
  </header>

  <!-- Connection state banner (hidden when connected) -->
  <ConnectionBanner />

  <!-- Panel content area (keyed block triggers crossfade animation on panel switch) -->
  <main class="panel-area">
    {#key router.panel}
      <div class="panel-enter">
        {#if router.panel === 'overview'}
          <OverviewPanel />
        {:else if router.panel === 'fleet'}
          <FleetPanel />
        {:else if router.panel === 'servers'}
          <ServersPanel />
        {:else if router.panel === 'tasks'}
          <TasksPanel />
        {:else if router.panel === 'workflows'}
          <WorkflowsPanel />
        {:else if router.panel === 'memory'}
          <MemoryPanel />
        {:else if router.panel === 'graph'}
          <GraphPanel />
        {:else if router.panel === 'stream'}
          <StreamPanel />
        {:else if router.panel === 'presence'}
          <PresencePanel />
        {:else if router.panel === 'reasoning'}
          <ReasoningPanel />
        {/if}
      </div>
    {/key}
  </main>

  <!-- Status bar -->
  <footer class="status-bar">
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
      <span class="status-text text-mono">{formatTime(fleetStore.lastUpdated)}</span>
    </div>
  </footer>

  <!-- Command Palette (standalone component with fuzzy search, grouping, arrow-key nav) -->
  <CommandPalette bind:open={showCommandPalette} onselect={handleCommand} />

  <!-- Keyboard shortcut help overlay -->
  {#if showKeyboardHelp}
    <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
    <div class="keyboard-help-overlay" role="presentation" onclick={() => { showKeyboardHelp = false; }}>
      <!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
      <div class="keyboard-help" role="dialog" aria-label="Keyboard shortcuts" onclick={(e) => e.stopPropagation()}>
        <div class="help-title">Keyboard Shortcuts</div>
        <div class="help-grid">
          <div class="help-section">
            <div class="help-section-title">Navigation</div>
            <div class="help-row"><kbd>1</kbd>-<kbd>8</kbd> <span>Switch panels</span></div>
            <div class="help-row"><kbd>0</kbd> <span>Reasoning</span></div>
            <div class="help-row"><kbd>`</kbd> / <kbd>o</kbd> <span>Overview</span></div>
          </div>
          <div class="help-section">
            <div class="help-section-title">Actions</div>
            <div class="help-row"><kbd>/</kbd> <span>Focus search</span></div>
            <div class="help-row"><kbd>r</kbd> <span>Refresh data</span></div>
            <div class="help-row"><kbd>⌘K</kbd> <span>Command palette</span></div>
          </div>
          <div class="help-section">
            <div class="help-section-title">General</div>
            <div class="help-row"><kbd>?</kbd> <span>Toggle this help</span></div>
            <div class="help-row"><kbd>Esc</kbd> <span>Close modal</span></div>
          </div>
        </div>
      </div>
    </div>
  {/if}

  <!-- Toast notifications overlay -->
  <Toast />
</div>
{/if}

<style>
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
    padding: 0 12px;
    flex-shrink: 0;
    gap: 12px;
    z-index: 100;
  }

  .nav-brand {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
  }

  .nav-logo {
    font-size: 16px;
    color: var(--accent);
  }

  .nav-title {
    font-size: 11px;
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
  }

  .nav-tab {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 4px 10px;
    border-radius: var(--radius-sm);
    font-size: 12px;
    color: var(--fg-secondary);
    transition: background var(--transition-fast, 0.12s), color var(--transition-fast, 0.12s);
    position: relative;
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
    left: 8px;
    right: 8px;
    height: 2px;
    background: var(--accent);
    border-radius: 1px;
  }

  .nav-tab-icon {
    font-size: 12px;
    opacity: 0.7;
  }

  .nav-tab-label {
    font-weight: 500;
  }

  .nav-tab-key {
    font-size: 9px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    padding: 1px 3px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    line-height: 1;
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
    padding: 0 12px;
    flex-shrink: 0;
    z-index: 100;
  }

  .status-bar-left,
  .status-bar-right {
    display: flex;
    align-items: center;
    gap: 8px;
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
    font-size: 11px;
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
    padding: 20px 24px;
    max-width: 480px;
    width: 90%;
    box-shadow: var(--shadow-lg);
  }

  .help-title {
    font-size: 14px;
    font-weight: 700;
    color: var(--fg-primary);
    margin-bottom: 16px;
    letter-spacing: 0.04em;
    text-shadow: 0 0 8px rgba(129, 240, 254, 0.2);
  }

  .help-grid {
    display: grid;
    grid-template-columns: 1fr 1fr 1fr;
    gap: 16px;
  }

  .help-section-title {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-muted);
    margin-bottom: 8px;
  }

  .help-row {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--fg-secondary);
    margin-bottom: 4px;
  }

  .help-row kbd {
    font-family: var(--font-mono);
    font-size: 10px;
    padding: 2px 5px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    color: var(--fg-primary);
    min-width: 18px;
    text-align: center;
  }
</style>
