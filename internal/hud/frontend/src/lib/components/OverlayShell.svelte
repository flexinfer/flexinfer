<script>
  import { onMount, onDestroy } from 'svelte';
  import { overlayStore } from '../stores/overlay.svelte.ts';
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { streamStore } from '../stores/stream.svelte.ts';
  import { eventStore } from '../stores/events.svelte.ts';

  onMount(() => {
    eventStore.connect();
    fleetStore.startPolling();
    healthStore.startPolling();
    taskStore.startPolling();
    workflowStore.startPolling();
    memoryStore.startPolling();
    streamStore.startPolling();
  });

  onDestroy(() => {
    eventStore.disconnect();
    fleetStore.stopPolling();
    healthStore.stopPolling();
    taskStore.stopPolling();
    workflowStore.stopPolling();
    memoryStore.stopPolling();
    streamStore.stopPolling();
  });

  // Derived values
  let daemonOnline = $derived(fleetStore.status.running);
  let serverCount = $derived(fleetStore.status.servers);
  let healthyCount = $derived(healthStore.healthyCount);
  let sessionCount = $derived(fleetStore.activeSessions.length);
  let pendingTasks = $derived(taskStore.pendingCount);
  let inProgressTasks = $derived(taskStore.inProgressCount);
  let activeWorkflows = $derived(workflowStore.activeWorkflows.length);
  let workingMemory = $derived(memoryStore.stats.working_memory?.items ?? 0);
  let lastStreamTime = $derived(() => {
    const entries = streamStore.entries;
    if (entries.length === 0) return null;
    try {
      return new Date(entries[0].timestamp);
    } catch {
      return null;
    }
  });

  function formatTime(d) {
    if (!d) return '--:--';
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  }

  function shortTime(ts) {
    if (!ts) return '--:--';
    try {
      return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    } catch {
      return '--:--';
    }
  }

  function statusDotClass(status) {
    switch (status) {
      case 'healthy': return 'dot-healthy';
      case 'degraded': return 'dot-degraded';
      case 'down': return 'dot-down';
      case 'idle': return 'dot-idle';
      default: return 'dot-idle';
    }
  }

  function priorityClass(p) {
    switch (p) {
      case 'critical': return 'badge-critical';
      case 'high': return 'badge-high';
      case 'medium': return 'badge-medium';
      case 'low': return 'badge-low';
      default: return 'badge-medium';
    }
  }

  // Sections config
  const sections = [
    { id: 'fleet',     label: 'FLEET',     icon: '\u25C8' },
    { id: 'servers',   label: 'SERVERS',   icon: '\u2665' },
    { id: 'tasks',     label: 'TASKS',     icon: '\u2611' },
    { id: 'workflows', label: 'WORKFLOWS', icon: '\u2699' },
    { id: 'memory',    label: 'MEMORY',    icon: '\u29BE' },
    { id: 'stream',    label: 'STREAM',    icon: '\u2261' },
  ];

  function sectionSummary(id) {
    switch (id) {
      case 'fleet':     return `${sessionCount} session${sessionCount !== 1 ? 's' : ''}`;
      case 'servers':   return `${healthyCount}/${serverCount} \u25CF`;
      case 'tasks':     return `${pendingTasks} pend`;
      case 'workflows': return `${activeWorkflows} active`;
      case 'memory':    return `${workingMemory} working`;
      case 'stream': {
        const t = lastStreamTime();
        return t ? `last: ${formatTime(t)}` : 'no data';
      }
      default: return '';
    }
  }
</script>

<div class="overlay-shell">
  <!-- Draggable header -->
  <header class="overlay-header">
    <span class="overlay-logo">{'\u25C8'}</span>
    <span class="overlay-title">LOOM HUD</span>
  </header>

  <!-- Status strip -->
  <div class="overlay-status">
    <span class="status-dot" class:online={daemonOnline} class:offline={!daemonOnline}></span>
    <span class="status-label">{daemonOnline ? 'Connected' : 'Disconnected'}</span>
    <span class="status-count">{serverCount} servers</span>
  </div>

  <!-- Accordion sections -->
  <div class="overlay-sections">
    {#each sections as section}
      <button
        class="section-header"
        class:expanded={overlayStore.expandedSection === section.id}
        onclick={() => overlayStore.toggleSection(section.id)}
      >
        <span class="section-arrow">{overlayStore.expandedSection === section.id ? '\u25BE' : '\u25B8'}</span>
        <span class="section-label">{section.label}</span>
        <span class="section-summary">{sectionSummary(section.id)}</span>
      </button>

      {#if overlayStore.expandedSection === section.id}
        <div class="section-body">
          {#if section.id === 'fleet'}
            {#if fleetStore.activeSessions.length === 0}
              <div class="empty-row">No active sessions</div>
            {:else}
              {#each fleetStore.activeSessions.slice(0, 8) as session}
                <div class="detail-row">
                  <span class="row-icon">{'\u25C9'}</span>
                  <span class="row-primary truncate">{session.agent_id || session.agent || 'unknown'}</span>
                  <span class="row-secondary truncate">{session.namespace || ''}</span>
                  <span class="row-badge">{session.total_tokens ?? 0} tok</span>
                </div>
              {/each}
            {/if}

          {:else if section.id === 'servers'}
            {#if healthStore.servers.length === 0}
              <div class="empty-row">No servers</div>
            {:else}
              {#each healthStore.servers as server}
                <div class="detail-row">
                  <span class="row-dot {statusDotClass(server.status)}"></span>
                  <span class="row-primary truncate">{server.name}</span>
                  <span class="row-secondary">{server.latency > 0 ? server.latency + 'ms' : ''}</span>
                  <span class="row-badge">{server.tool_count > 0 ? server.tool_count + ' tools' : ''}</span>
                </div>
              {/each}
            {/if}

          {:else if section.id === 'tasks'}
            {#if taskStore.tasks.length === 0}
              <div class="empty-row">No tasks</div>
            {:else}
              {#each taskStore.filteredTasks.slice(0, 5) as task}
                <div class="detail-row">
                  <span class="row-badge-pill {priorityClass(task.priority)}">{task.priority?.charAt(0).toUpperCase()}</span>
                  <span class="row-primary truncate">{task.title}</span>
                  <span class="row-secondary">{task.agent || ''}</span>
                </div>
              {/each}
            {/if}

          {:else if section.id === 'workflows'}
            {#if workflowStore.workflows.length === 0}
              <div class="empty-row">No workflows</div>
            {:else}
              {#each workflowStore.activeWorkflows.slice(0, 5) as wf}
                <div class="detail-row">
                  <span class="row-primary truncate">{wf.name || wf.id}</span>
                  <span class="row-secondary">{wf.current_step || ''}</span>
                </div>
              {/each}
              {#if workflowStore.activeWorkflows.length === 0}
                <div class="empty-row">No active workflows</div>
              {/if}
            {/if}

          {:else if section.id === 'memory'}
            <div class="tier-row">
              <span class="tier-label" style="color: var(--tier-working);">Working</span>
              <span class="tier-count">{memoryStore.stats.working_memory?.items ?? 0} items</span>
              <span class="tier-tokens">{memoryStore.stats.working_memory?.tokens ?? 0} tok</span>
            </div>
            <div class="tier-row">
              <span class="tier-label" style="color: var(--tier-short);">Short</span>
              <span class="tier-count">{memoryStore.stats.short_term_memory?.items ?? 0} items</span>
              <span class="tier-tokens">{memoryStore.stats.short_term_memory?.tokens ?? 0} tok</span>
            </div>
            <div class="tier-row">
              <span class="tier-label" style="color: var(--tier-long);">Long</span>
              <span class="tier-count">{memoryStore.stats.long_term_memory?.items ?? 0} items</span>
              <span class="tier-tokens">{memoryStore.stats.long_term_memory?.tokens ?? 0} tok</span>
            </div>

          {:else if section.id === 'stream'}
            {#if streamStore.entries.length === 0}
              <div class="empty-row">No activity</div>
            {:else}
              {#each streamStore.entries.slice(0, 8) as entry}
                <div class="detail-row stream-row">
                  <span class="row-time">{shortTime(entry.timestamp)}</span>
                  <span class="row-type">{entry.entry_type}</span>
                  <span class="row-primary truncate">{entry.agent || ''}</span>
                </div>
              {/each}
            {/if}
          {/if}
        </div>
      {/if}
    {/each}
  </div>
</div>

<style>
  .overlay-shell {
    display: flex;
    flex-direction: column;
    height: 100vh;
    width: 100vw;
    overflow-y: auto;
    overflow-x: hidden;
    font-size: 11px;
    background: transparent;
  }

  /* ---- Draggable Header ---- */
  .overlay-header {
    display: flex;
    align-items: center;
    gap: 6px;
    height: 32px;
    padding: 0 12px;
    flex-shrink: 0;
    -webkit-app-region: drag;
    border-bottom: 1px solid var(--border);
    background: rgba(22, 27, 34, 0.6);
  }

  .overlay-logo {
    font-size: 14px;
    color: var(--accent);
  }

  .overlay-title {
    font-size: 10px;
    font-weight: 700;
    letter-spacing: 1.5px;
    color: var(--fg-secondary);
    text-transform: uppercase;
  }

  /* ---- Status Strip ---- */
  .overlay-status {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    background: rgba(13, 17, 23, 0.4);
    flex-shrink: 0;
  }

  .status-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .status-dot.online {
    background: var(--success);
    box-shadow: 0 0 4px var(--success);
  }

  .status-dot.offline {
    background: var(--error);
    box-shadow: 0 0 4px var(--error);
    animation: pulse 1.5s infinite;
  }

  .status-label {
    color: var(--fg-secondary);
    font-size: 11px;
  }

  .status-count {
    margin-left: auto;
    color: var(--fg-muted);
    font-size: 10px;
    font-family: var(--font-mono);
  }

  /* ---- Accordion Sections ---- */
  .overlay-sections {
    flex: 1;
    overflow-y: auto;
  }

  .section-header {
    display: flex;
    align-items: center;
    width: 100%;
    padding: 6px 12px;
    border-bottom: 1px solid var(--border);
    cursor: pointer;
    transition: background var(--transition-fast, 0.1s);
    -webkit-app-region: no-drag;
    font-size: 10px;
    text-align: left;
  }

  .section-header:hover {
    background: var(--bg-tertiary);
  }

  .section-header.expanded {
    background: rgba(33, 38, 45, 0.5);
  }

  .section-arrow {
    width: 12px;
    color: var(--fg-muted);
    font-size: 10px;
    flex-shrink: 0;
  }

  .section-label {
    font-weight: 600;
    letter-spacing: 1px;
    color: var(--fg-secondary);
    text-transform: uppercase;
  }

  .section-summary {
    margin-left: auto;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 10px;
  }

  /* ---- Section Body ---- */
  .section-body {
    border-bottom: 1px solid var(--border);
    background: rgba(13, 17, 23, 0.3);
    animation: expandIn 0.15s ease-out;
  }

  @keyframes expandIn {
    from {
      opacity: 0;
      max-height: 0;
    }
    to {
      opacity: 1;
      max-height: 500px;
    }
  }

  .empty-row {
    padding: 8px 12px 8px 24px;
    color: var(--fg-muted);
    font-style: italic;
    font-size: 10px;
  }

  /* ---- Detail Rows ---- */
  .detail-row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 12px 4px 24px;
    font-size: 11px;
    min-height: 24px;
  }

  .detail-row:hover {
    background: rgba(33, 38, 45, 0.4);
  }

  .row-icon {
    color: var(--accent);
    font-size: 10px;
    flex-shrink: 0;
  }

  .row-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .dot-healthy { background: var(--success); }
  .dot-degraded { background: var(--warning); }
  .dot-down { background: var(--error); }
  .dot-idle { background: var(--fg-muted); }

  .row-primary {
    flex: 1;
    color: var(--fg-primary);
    min-width: 0;
  }

  .row-secondary {
    color: var(--fg-muted);
    font-size: 10px;
    flex-shrink: 0;
    max-width: 80px;
  }

  .row-badge {
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 9px;
    flex-shrink: 0;
  }

  .row-badge-pill {
    font-size: 9px;
    font-weight: 700;
    width: 14px;
    height: 14px;
    border-radius: 3px;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    color: var(--bg-primary);
  }

  .badge-critical { background: var(--error); }
  .badge-high { background: var(--warning); }
  .badge-medium { background: var(--info); }
  .badge-low { background: var(--fg-muted); }

  /* ---- Tier Rows (Memory) ---- */
  .tier-row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 12px 4px 24px;
    font-size: 11px;
  }

  .tier-label {
    font-weight: 600;
    font-size: 10px;
    width: 50px;
    flex-shrink: 0;
  }

  .tier-count {
    color: var(--fg-primary);
    font-family: var(--font-mono);
    font-size: 10px;
  }

  .tier-tokens {
    margin-left: auto;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 9px;
  }

  /* ---- Stream Rows ---- */
  .stream-row {
    font-size: 10px;
  }

  .row-time {
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 9px;
    flex-shrink: 0;
    width: 40px;
  }

  .row-type {
    color: var(--accent);
    font-size: 9px;
    font-family: var(--font-mono);
    flex-shrink: 0;
    width: 60px;
  }

  /* ---- Utilities ---- */
  .truncate {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
  }
</style>
