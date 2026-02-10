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

  // Make html/body transparent so native NSVisualEffectView vibrancy shows through.
  function transparentBody(_node) {
    document.documentElement.style.background = 'transparent';
    document.body.style.background = 'transparent';
    return {
      destroy() {
        document.documentElement.style.background = '';
        document.body.style.background = '';
      }
    };
  }

  let activityUnsubs = [];

  onMount(() => {
    eventStore.connect();
    fleetStore.startPolling();
    healthStore.startPolling();
    taskStore.startPolling();
    workflowStore.startPolling();
    memoryStore.startPolling();
    streamStore.startPolling();

    // Wire granular agent SSE events to overlay activity tracking.
    function pushAgentEvent(e) {
      const data = e.data || {};
      const agentId = data.agent_id || 'unknown';
      overlayStore.pushEvent(e.type, agentId);
    }
    activityUnsubs = [
      eventStore.on('agent.heartbeat', pushAgentEvent),
      eventStore.on('agent.session.start', pushAgentEvent),
      eventStore.on('agent.session.end', pushAgentEvent),
      eventStore.on('agent.task.update', pushAgentEvent),
    ];
  });

  onDestroy(() => {
    for (const unsub of activityUnsubs) unsub();
    activityUnsubs = [];
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
  let blockedTasks = $derived(taskStore.blockedCount);
  let totalTasks = $derived(taskStore.tasks.length);
  let activeWorkflows = $derived(workflowStore.activeWorkflows.length);
  let definitionCount = $derived(workflowStore.uniqueDefinitions.length);
  let totalMemoryItems = $derived(memoryStore.stats.total_items ?? 0);
  let lastStreamTime = $derived(() => {
    const entries = streamStore.entries;
    if (entries.length === 0) return null;
    try {
      return new Date(entries[0].timestamp);
    } catch {
      return null;
    }
  });

  let activeAgentCount = $derived(fleetStore.agents.filter((a) => a.status === 'active').length);

  // Last 5 activity events for the compact activity log.
  let activityLog = $derived(overlayStore.recentEvents.slice(-5).reverse());

  function activityShortTime(ts) {
    try {
      return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    } catch {
      return '--:--';
    }
  }

  /** Strip "agent." prefix for compact display. */
  function activityLabel(type) {
    return type.replace('agent.', '');
  }

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

  function taskStatusDot(status) {
    switch (status) {
      case 'in_progress': return 'dot-healthy';
      case 'blocked':     return 'dot-degraded';
      case 'pending':     return 'dot-idle';
      case 'completed':   return 'dot-completed';
      case 'cancelled':   return 'dot-down';
      default:            return 'dot-idle';
    }
  }

  function sessionAgentDot(session) {
    switch (session.agentStatus) {
      case 'active':  return 'dot-healthy';
      case 'idle':    return 'dot-idle';
      case 'offline': return 'dot-down';
      default:        return session.status === 'active' ? 'dot-healthy' : 'dot-idle';
    }
  }

  // Smart-default: auto-collapse idle namespaces/sessions when FLEET opens
  let prevFleetOpen = false;
  $effect(() => {
    const isOpen = overlayStore.expandedSection === 'fleet';
    if (isOpen && !prevFleetOpen) {
      // Section just opened — collapse idle groups
      const groups = fleetStore.namespaceGroups;
      const collapseNs = new Set();
      const collapseSess = new Set();
      for (const g of groups) {
        if (!g.hasActiveWork) {
          collapseNs.add(g.project);
        } else {
          for (const s of g.sessions) {
            const hasActiveTasks = s.tasks.some((t) => t.status === 'in_progress');
            if (s.agentStatus !== 'active' && !hasActiveTasks) {
              collapseSess.add(s.id);
            }
          }
        }
      }
      overlayStore.collapsedNamespaces = collapseNs;
      overlayStore.collapsedSessions = collapseSess;
    } else if (!isOpen && prevFleetOpen) {
      overlayStore.resetSubGroups();
    }
    prevFleetOpen = isOpen;
  });

  function workflowStatusDot(status) {
    switch (status) {
      case 'running':          return 'dot-healthy';
      case 'waiting_approval': return 'dot-degraded';
      case 'pending':          return 'dot-idle';
      case 'completed':        return 'dot-completed';
      case 'failed':           return 'dot-down';
      case 'cancelled':        return 'dot-down';
      default:                 return 'dot-idle';
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
    { id: 'activity',  label: 'ACTIVITY',  icon: '\u26A1' },
  ];

  function sectionSummary(id) {
    switch (id) {
      case 'fleet': {
        const groups = fleetStore.namespaceGroups;
        const activeCount = groups.filter((g) => g.hasActiveWork).length;
        if (groups.length === 0) return 'no activity';
        return `${groups.length} ns${activeCount > 0 ? ` · ${activeCount} active` : ''}`;
      }
      case 'servers':   return `${healthyCount}/${serverCount} \u25CF`;
      case 'tasks': {
        if (totalTasks === 0) return 'none';
        const parts = [];
        if (inProgressTasks > 0) parts.push(`${inProgressTasks} active`);
        if (pendingTasks > 0) parts.push(`${pendingTasks} pend`);
        if (blockedTasks > 0) parts.push(`${blockedTasks} blocked`);
        return parts.length > 0 ? parts.join(' \u00B7 ') : `${totalTasks} total`;
      }
      case 'workflows': {
        const parts = [];
        if (activeWorkflows > 0) parts.push(`${activeWorkflows} active`);
        if (definitionCount > 0) parts.push(`${definitionCount} defs`);
        return parts.length > 0 ? parts.join(' \u00B7 ') : 'none';
      }
      case 'memory': {
        const w = memoryStore.stats.working_memory?.items ?? 0;
        const s = memoryStore.stats.short_term_memory?.items ?? 0;
        const l = memoryStore.stats.long_term_memory?.items ?? 0;
        return `${w}w ${s}s ${l}l`;
      }
      case 'stream': {
        const t = lastStreamTime();
        return t ? `last: ${formatTime(t)}` : 'no data';
      }
      case 'activity': {
        const count = overlayStore.recentEvents.length;
        return count > 0 ? `${count} events` : 'none';
      }
      default: return '';
    }
  }
</script>

<div class="overlay-shell" use:transparentBody>
  <!-- Draggable header -->
  <header class="overlay-header">
    <span class="overlay-logo">{'\u25C8'}</span>
    <span class="overlay-title">LOOM HUD</span>
  </header>

  <!-- Status strip -->
  <div class="overlay-status">
    <span class="status-dot" class:online={daemonOnline} class:offline={!daemonOnline}></span>
    <span class="status-label">{daemonOnline ? 'Connected' : 'Disconnected'}</span>
    {#if activeAgentCount > 0}
      <span class="status-agents">{activeAgentCount} agent{activeAgentCount !== 1 ? 's' : ''}</span>
    {/if}
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
            {#if fleetStore.namespaceGroups.length === 0}
              <div class="empty-row">No active sessions <span class="empty-hint">— agents register on connect</span></div>
            {:else}
              {#each fleetStore.namespaceGroups as group (group.project)}
                <!-- Level 1: Namespace group header -->
                <button
                  class="ns-header"
                  class:ns-active={group.hasActiveWork}
                  onclick={() => overlayStore.toggleNamespace(group.project)}
                >
                  <span class="ns-chevron">{overlayStore.isNamespaceExpanded(group.project) ? '\u25BE' : '\u25B8'}</span>
                  <span class="ns-name">{group.project}</span>
                  <span class="ns-summary">{group.sessionCount}s {group.taskCount}t</span>
                  <span class="ns-tokens">{group.totalTokens} tok</span>
                </button>

                {#if overlayStore.isNamespaceExpanded(group.project)}
                  <!-- Level 2: Sessions -->
                  {#each group.sessions.slice(0, 4) as session (session.id)}
                    <button
                      class="session-row"
                      onclick={() => overlayStore.toggleSession(session.id)}
                    >
                      <span class="row-dot {sessionAgentDot(session)}" class:pulsing={overlayStore.activeAgentIds.has(session.agent_id)}></span>
                      <span class="session-chevron">{overlayStore.isSessionExpanded(session.id) ? '\u25BE' : '\u25B8'}</span>
                      <span class="row-primary truncate">{session.agent_id || session.agent || 'unknown'}</span>
                      {#if session.branch}
                        <span class="session-branch">{session.branch}</span>
                      {/if}
                      <span class="row-badge">{session.total_tokens ?? 0} tok</span>
                    </button>

                    {#if overlayStore.isSessionExpanded(session.id)}
                      <!-- Level 3: Tasks under this session -->
                      {#each session.tasks.slice(0, 3) as task (task.id)}
                        <div class="task-row">
                          <span class="row-dot {taskStatusDot(task.status)}"></span>
                          <span class="row-primary truncate">{task.title}</span>
                          <span class="row-status-label">{task.status === 'in_progress' ? 'active' : task.status}</span>
                        </div>
                      {/each}
                      {#if session.tasks.length > 3}
                        <div class="overflow-row nested">+{session.tasks.length - 3} more tasks</div>
                      {/if}
                      {#if session.tasks.length === 0}
                        <div class="task-row empty-task">no tasks</div>
                      {/if}
                    {/if}
                  {/each}
                  {#if group.sessions.length > 4}
                    <div class="overflow-row nested">+{group.sessions.length - 4} more sessions</div>
                  {/if}

                  <!-- Orphan tasks (namespace match, no session) -->
                  {#each group.orphanTasks.slice(0, 3) as task (task.id)}
                    <div class="task-row orphan">
                      <span class="row-dot {taskStatusDot(task.status)}"></span>
                      <span class="row-primary truncate">{task.title}</span>
                      <span class="row-status-label">{task.status === 'in_progress' ? 'active' : task.status}</span>
                    </div>
                  {/each}
                  {#if group.orphanTasks.length > 3}
                    <div class="overflow-row nested">+{group.orphanTasks.length - 3} more orphan tasks</div>
                  {/if}
                {/if}
              {/each}
            {/if}

          {:else if section.id === 'servers'}
            {#if healthStore.servers.length === 0}
              <div class="empty-row">No servers <span class="empty-hint">— check daemon config</span></div>
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
              <div class="empty-row">No tasks <span class="empty-hint">— use agent_task_add to create</span></div>
            {:else}
              {#each taskStore.filteredTasks.slice(0, 8) as task (task.id)}
                <div class="detail-row">
                  <span class="row-dot {taskStatusDot(task.status)}"></span>
                  <span class="row-badge-pill {priorityClass(task.priority)}">{task.priority?.charAt(0).toUpperCase()}</span>
                  <span class="row-primary truncate">{task.title}</span>
                  <span class="row-status-label">{task.status === 'in_progress' ? 'active' : task.status}</span>
                </div>
              {/each}
              {#if totalTasks > 8}
                <div class="overflow-row">+{totalTasks - 8} more</div>
              {/if}
            {/if}

          {:else if section.id === 'workflows'}
            <!-- Running instances -->
            {#if workflowStore.activeWorkflows.length > 0}
              {#each workflowStore.activeWorkflows.slice(0, 5) as wf (wf.id)}
                <div class="detail-row">
                  <span class="row-dot {workflowStatusDot(wf.status)}"></span>
                  <span class="row-primary truncate">{wf.name || wf.id}</span>
                  <span class="row-secondary">{wf.current_step || ''}</span>
                  <span class="row-status-label">{wf.status === 'waiting_approval' ? 'approval' : wf.status}</span>
                </div>
              {/each}
            {/if}
            <!-- Registered definitions -->
            {#if workflowStore.uniqueDefinitions.length > 0}
              {#if workflowStore.activeWorkflows.length > 0}
                <div class="sub-header">Definitions</div>
              {/if}
              {#each workflowStore.uniqueDefinitions as def (def.id)}
                <div class="detail-row def-row">
                  <span class="row-dot dot-idle"></span>
                  <span class="row-primary truncate">{def.name}</span>
                  <span class="row-secondary">{def.step_count} steps</span>
                </div>
              {/each}
            {:else if workflowStore.activeWorkflows.length === 0}
              <div class="empty-row">No workflows <span class="empty-hint">— define in .agents/workflows/</span></div>
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
              <div class="empty-row">No activity <span class="empty-hint">— context entries appear here</span></div>
            {:else}
              {#each streamStore.entries.slice(0, 8) as entry}
                <div class="detail-row stream-row">
                  <span class="row-time">{shortTime(entry.timestamp)}</span>
                  <span class="row-type">{entry.entry_type}</span>
                  <span class="row-primary truncate">{entry.agent || ''}</span>
                </div>
              {/each}
            {/if}

          {:else if section.id === 'activity'}
            {#if activityLog.length === 0}
              <div class="empty-row">No agent events yet <span class="empty-hint">— heartbeats and session events appear here</span></div>
            {:else}
              {#each activityLog as event}
                <div class="detail-row activity-row">
                  <span class="row-time">{activityShortTime(event.timestamp)}</span>
                  <span class="row-type">{activityLabel(event.type)}</span>
                  <span class="row-primary truncate">{event.agentId}</span>
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
    background: var(--glass-bg);
    backdrop-filter: blur(var(--glass-blur-heavy));
    -webkit-backdrop-filter: blur(var(--glass-blur-heavy));
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
    border-bottom: 1px solid var(--glass-border);
    background: rgba(0, 34, 39, 0.75);
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
    border-bottom: 1px solid var(--glass-border);
    background: rgba(0, 23, 26, 0.5);
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
    transition: background var(--transition-fast, 0.1s), color var(--transition-fast, 0.1s);
    -webkit-app-region: no-drag;
    font-size: 10px;
    text-align: left;
  }

  .section-header:hover {
    background: var(--bg-tertiary);
  }

  .section-header.expanded {
    background: rgba(0, 46, 52, 0.5);
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
    background: rgba(0, 23, 26, 0.3);
    animation: expandFadeIn 0.18s ease-out;
  }

  @keyframes expandFadeIn {
    from {
      opacity: 0;
      transform: translateY(-4px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .empty-row {
    padding: 8px 12px 8px 24px;
    color: var(--fg-muted);
    font-style: italic;
    font-size: 10px;
  }

  .empty-hint {
    opacity: 0.5;
    font-size: 9px;
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
    background: rgba(0, 46, 52, 0.4);
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

  .dot-healthy { background: var(--success); box-shadow: 0 0 3px var(--success); }
  .dot-degraded { background: var(--warning); box-shadow: 0 0 3px var(--warning); }
  .dot-down { background: var(--error); }
  .dot-idle { background: var(--fg-muted); }
  .dot-completed { background: var(--info, #4a9eff); opacity: 0.5; }

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
    border-radius: var(--radius-sm);
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

  .row-status-label {
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 8px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    flex-shrink: 0;
  }

  .overflow-row {
    padding: 3px 12px 5px 24px;
    color: var(--fg-muted);
    font-size: 9px;
    font-family: var(--font-mono);
    opacity: 0.7;
  }

  .overflow-row.nested {
    padding-left: 44px;
  }

  /* ---- Namespace Group (Level 1) ---- */
  .ns-header {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    padding: 4px 12px 4px 18px;
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    text-align: left;
    cursor: pointer;
    border: none;
    background: none;
    color: var(--fg-secondary);
    border-left: 2px solid transparent;
    transition: background var(--transition-fast, 0.1s);
  }

  .ns-header:hover {
    background: rgba(0, 46, 52, 0.4);
  }

  .ns-header.ns-active {
    border-left-color: var(--success);
  }

  .ns-chevron {
    width: 10px;
    color: var(--fg-muted);
    font-size: 9px;
    flex-shrink: 0;
  }

  .ns-name {
    font-weight: 600;
    color: var(--fg-primary);
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .ns-summary {
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 9px;
    flex-shrink: 0;
  }

  .ns-tokens {
    color: var(--fg-muted);
    font-family: var(--font-mono);
    font-size: 9px;
    flex-shrink: 0;
    margin-left: 4px;
  }

  /* ---- Session Row (Level 2) ---- */
  .session-row {
    display: flex;
    align-items: center;
    gap: 5px;
    width: 100%;
    padding: 3px 12px 3px 28px;
    font-size: 11px;
    text-align: left;
    cursor: pointer;
    border: none;
    background: none;
    color: var(--fg-primary);
    transition: background var(--transition-fast, 0.1s);
  }

  .session-row:hover {
    background: rgba(0, 46, 52, 0.4);
  }

  .session-chevron {
    width: 10px;
    color: var(--fg-muted);
    font-size: 9px;
    flex-shrink: 0;
  }

  .session-branch {
    color: var(--accent);
    font-family: var(--font-mono);
    font-size: 9px;
    flex-shrink: 0;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    background: rgba(0, 188, 212, 0.1);
    max-width: 80px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* ---- Task Row (Level 3) ---- */
  .task-row {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 2px 12px 2px 44px;
    font-size: 10px;
    color: var(--fg-primary);
    min-height: 20px;
  }

  .task-row:hover {
    background: rgba(0, 46, 52, 0.3);
  }

  .task-row.orphan {
    padding-left: 34px;
    opacity: 0.8;
    font-style: italic;
  }

  .task-row.empty-task {
    color: var(--fg-muted);
    font-style: italic;
    font-size: 9px;
  }

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

  /* ---- Workflow Definition Rows ---- */
  .sub-header {
    padding: 4px 12px 2px 24px;
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.5px;
    text-transform: uppercase;
    color: var(--fg-muted);
    border-top: 1px solid var(--border);
    margin-top: 2px;
  }

  .def-row {
    opacity: 0.75;
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

  .status-agents {
    color: var(--success);
    font-family: var(--font-mono);
    font-size: 10px;
  }

  /* ---- Activity Row ---- */
  .activity-row {
    font-size: 10px;
  }

  /* ---- Pulse animation for active agent dots ---- */
  .row-dot.pulsing {
    animation: activityPulse 0.6s ease-in-out;
  }

  @keyframes activityPulse {
    0% { transform: scale(1); }
    50% { transform: scale(1.8); box-shadow: 0 0 8px var(--success); }
    100% { transform: scale(1); }
  }

  @keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
  }
</style>
