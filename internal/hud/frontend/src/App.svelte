<script>
  import { onMount, onDestroy } from 'svelte';
  import { fleetStore } from './lib/stores/fleet.svelte.ts';
  import { healthStore } from './lib/stores/health.svelte.ts';
  import { streamStore } from './lib/stores/stream.svelte.ts';
  import { eventStore } from './lib/stores/events.svelte.ts';
  import FleetPanel from './lib/components/FleetPanel.svelte';
  import ServersPanel from './lib/components/ServersPanel.svelte';
  import TasksPanel from './lib/components/TasksPanel.svelte';
  import WorkflowsPanel from './lib/components/WorkflowsPanel.svelte';
  import MemoryPanel from './lib/components/MemoryPanel.svelte';
  import GraphPanel from './lib/components/GraphPanel.svelte';
  import StreamPanel from './lib/components/StreamPanel.svelte';
  import PresencePanel from './lib/components/PresencePanel.svelte';
  import CommandPalette from './lib/components/CommandPalette.svelte';

  const panels = [
    { id: 'fleet',     label: 'Fleet',     key: '1', icon: '\u25C8' },
    { id: 'servers',   label: 'Servers',   key: '2', icon: '\u2665' },
    { id: 'tasks',     label: 'Tasks',     key: '3', icon: '\u2611' },
    { id: 'workflows', label: 'Workflows', key: '4', icon: '\u2699' },
    { id: 'memory',    label: 'Memory',    key: '5', icon: '\u29BE' },
    { id: 'graph',     label: 'Graph',     key: '6', icon: '\u2B21' },
    { id: 'stream',    label: 'Stream',    key: '7', icon: '\u2261' },
    { id: 'presence',  label: 'Presence',  key: '8', icon: '\u25C9' },
  ];

  let activePanel = $state('fleet');
  let showCommandPalette = $state(false);

  // Lightweight status polling for the status bar
  let statusPollTimer = null;
  onMount(() => {
    eventStore.connect();
    fleetStore.fetch();
    healthStore.fetch();
    statusPollTimer = setInterval(() => {
      fleetStore.fetch();
      healthStore.fetch();
    }, 8000);
  });
  onDestroy(() => {
    eventStore.disconnect();
    if (statusPollTimer) clearInterval(statusPollTimer);
  });

  // Keyboard shortcuts (number keys for panel switching)
  function handleKeydown(e) {
    const tag = e.target?.tagName;
    const isInput = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';

    // Number keys 1-7 for panel switching (only when not in an input)
    if (!isInput && !e.metaKey && !e.ctrlKey && !e.altKey) {
      const num = parseInt(e.key);
      if (num >= 1 && num <= 8) {
        activePanel = panels[num - 1].id;
        return;
      }
    }
  }

  // Handle command palette selections
  function handleCommand(item) {
    // Panel navigation
    const panelIds = panels.map(p => p.id);
    if (panelIds.includes(item.id)) {
      activePanel = item.id;
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
      case 'approve-workflow':
        activePanel = 'workflows';
        break;
      case 'reject-workflow':
        activePanel = 'workflows';
        break;
      case 'promote-memory':
        activePanel = 'memory';
        break;
      case 'demote-memory':
        activePanel = 'memory';
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
          class:active={activePanel === panel.id}
          onclick={() => { activePanel = panel.id; }}
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

  <!-- Panel content area -->
  <main class="panel-area">
    {#if activePanel === 'fleet'}
      <FleetPanel />
    {:else if activePanel === 'servers'}
      <ServersPanel />
    {:else if activePanel === 'tasks'}
      <TasksPanel />
    {:else if activePanel === 'workflows'}
      <WorkflowsPanel />
    {:else if activePanel === 'memory'}
      <MemoryPanel />
    {:else if activePanel === 'graph'}
      <GraphPanel />
    {:else if activePanel === 'stream'}
      <StreamPanel />
    {:else if activePanel === 'presence'}
      <PresencePanel />
    {/if}
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
</div>

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
    border-radius: 4px;
    font-size: 12px;
    color: var(--fg-secondary);
    transition: background 0.12s, color 0.12s;
    position: relative;
  }

  .nav-tab:hover {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
  }

  .nav-tab.active {
    background: var(--bg-tertiary);
    color: var(--fg-primary);
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
    border-radius: 2px;
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
</style>
