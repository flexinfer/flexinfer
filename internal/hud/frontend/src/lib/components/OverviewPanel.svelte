<script>
  // OverviewPanel - triage-first composed shell (Slice A2 of HUD UX overhaul).
  //
  // The 1504-line monolith was decomposed into:
  //   - InstrumentStrip   - 4 signal-ring gauges + today chip
  //   - HeroSummary       - command headline + attention lanes
  //   - InboxDeck         - 7-kind operator inbox with action cards
  //   - inbox.ts          - typed selectors that derive cards from stores
  //   - existing MillsKPIRow + LiveSessionsCard for non-triage surfaces
  //
  // This file keeps store polling bootstrap, derived state, and layout shell.
  // Logic that varies per card kind lives in inbox.ts; theme/CSS for extracted
  // sections lives in their respective components.

  import { router } from '../stores/router.svelte.ts';
  import { fleetStore } from '../stores/fleet.svelte.ts';
  import { healthStore } from '../stores/health.svelte.ts';
  import { taskStore } from '../stores/tasks.svelte.ts';
  import { workflowStore } from '../stores/workflows.svelte.ts';
  import { memoryStore } from '../stores/memory.svelte.ts';
  import { streamStore } from '../stores/stream.svelte.ts';
  import { sandboxStore } from '../stores/sandbox.svelte.ts';
  import { graphStore } from '../stores/graph.svelte.ts';
  import { costStore } from '../stores/cost.svelte.ts';
  import { rbacStore } from '../stores/rbac.svelte.ts';
  import { coordinationStore } from '../stores/coordination.svelte.ts';
  import { mergeQueueStore } from '../stores/mergeQueue.svelte.ts';
  import { shuttleStore } from '../stores/shuttle.svelte.ts';
  import { millsStore } from '../stores/mills.svelte.ts';
  import { otelStore } from '../stores/otel.svelte.ts';
  import { liveSessionsStore } from '../stores/liveSessions.svelte.ts';
  import MillsKPIRow from './Mills/MillsKPIRow.svelte';
  import LiveSessionsCard from './LiveSessionsCard.svelte';
  import InstrumentStrip from './overview/InstrumentStrip.svelte';
  import HeroSummary from './overview/HeroSummary.svelte';
  import InboxDeck from './overview/InboxDeck.svelte';
  import SupportingStrip from './overview/SupportingStrip.svelte';
  import { selectInboxCards } from '../utils/inbox.ts';
  import { navigateToAgentSessionOrTraces } from '../utils/drilldown.ts';

  const fleetPollingOwner = Symbol('OverviewPanel');
  const otelPollingOwner = Symbol('OverviewPanel');

  let initialLoad = $state(true);
  let kpis = $state({
    sessions_today: 0,
    tokens_today: 0,
    tasks_completed_today: 0,
    active_agents: 0,
    pending_approvals: 0,
    file_conflicts: 0,
    conflict_details: [],
  });

  async function fetchKPIs() {
    try {
      const res = await globalThis.fetch('/api/kpis');
      if (res.ok) kpis = await res.json();
    } catch {
      // Non-critical: live stores still drive the dashboard.
    } finally {
      initialLoad = false;
    }
  }

  $effect(() => {
    fetchKPIs();
    const t = setInterval(fetchKPIs, 15000);
    return () => clearInterval(t);
  });

  // Polling bootstrap: this is currently the landing view, so eagerly start
  // every store the dashboard reads. The 5 decomposed stores (fleet, tasks,
  // sandbox, spawn, health) now use SSE-first 60s watchdog polling per
  // Slice B3 — the intervals below are just safety nets for SSE disconnects.
  $effect(() => {
    fleetStore.startPolling(60000, fleetPollingOwner);
    healthStore.startPolling(60000);
    taskStore.startPolling(60000);
    memoryStore.startPolling(30000);
    streamStore.startPolling(15000);
    costStore.startPolling(30000);
    rbacStore.startPolling(30000);
    coordinationStore.startPolling(30000);
    mergeQueueStore.startPolling(30000);
    shuttleStore.startPolling(30000);
    millsStore.startPolling(30000);
    otelStore.startPolling(30000, otelPollingOwner);
    liveSessionsStore.connect();
    return () => {
      fleetStore.stopPolling(fleetPollingOwner);
      healthStore.stopPolling();
      taskStore.stopPolling();
      memoryStore.stopPolling();
      streamStore.stopPolling();
      costStore.stopPolling();
      rbacStore.stopPolling();
      coordinationStore.stopPolling();
      mergeQueueStore.stopPolling();
      shuttleStore.stopPolling();
      millsStore.stopPolling();
      otelStore.stopPolling(otelPollingOwner);
    };
  });

  let _tick = $state(0);
  $effect(() => {
    const t = setInterval(() => { _tick++; }, 10000);
    return () => clearInterval(t);
  });

  function agoText(ts) {
    void _tick;
    if (!ts) return '';
    const diff = Math.floor((Date.now() - ts) / 1000);
    if (diff < 10) return 'just now';
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    return `${Math.floor(diff / 3600)}h ago`;
  }

  function navigate(panel) { router.navigate(panel); }

  /* ── Derived counts ── */
  let sessionCount = $derived(fleetStore.activeSessions.length);
  let agentSummary = $derived(fleetStore.unifiedSummary);
  let agentCount = $derived(agentSummary.live_agents);

  let healthyCount = $derived(healthStore.healthyCount);
  let serverCount = $derived(healthStore.availableCount);
  let downCount = $derived(healthStore.downCount);

  let pendingTasks = $derived(taskStore.pendingCount);
  let activeTasks = $derived(taskStore.inProgressCount);
  let blockedTasks = $derived(taskStore.blockedCount);
  let coordinationSummary = $derived(coordinationStore.summary);
  let activeBlockers = $derived(coordinationStore.activeBlockers);
  let topAttentionAgents = $derived(coordinationStore.topAttentionAgents);

  let workingItems = $derived(memoryStore.stats.working_memory?.items ?? 0);
  let shortItems = $derived(memoryStore.stats.short_term_memory?.items ?? 0);
  let longItems = $derived(memoryStore.stats.long_term_memory?.items ?? 0);
  let totalTokens = $derived(memoryStore.stats.total_tokens ?? 0);
  let daemonRunning = $derived(fleetStore.status?.running ?? false);
  let processCount = $derived(fleetStore.status?.processes?.length ?? 0);
  let graphEntities = $derived(graphStore.stats?.total_entities ?? 0);
  let graphTopTypes = $derived.by(() => {
    const types = graphStore.stats?.entity_types ?? {};
    return Object.entries(types)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 1)
      .map(([n, c]) => `${n}:${c}`)
      .join('') || 'empty';
  });
  let streamCount = $derived(streamStore.entries.length);
  let lastStreamAge = $derived.by(() => {
    if (streamStore.entries.length === 0) return null;
    try {
      const t = new Date(streamStore.entries[0].timestamp);
      const diff = Math.floor((Date.now() - t.getTime()) / 1000);
      if (diff < 60) return `${diff}s ago`;
      if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
      return `${Math.floor(diff / 3600)}h ago`;
    } catch { return null; }
  });

  let pendingApprovals = $derived(
    workflowStore.activeWorkflows.filter(w => w.status === 'waiting_approval').length
  );

  let completedTaskCount = $derived.by(() => {
    const storeCount = taskStore.tasks.filter(t => t.status === 'completed').length;
    return storeCount > 0 ? storeCount : kpis.tasks_completed_today;
  });

  let lastRefreshed = $derived.by(() => {
    const candidates = [
      fleetStore.lastUpdated,
      healthStore.lastUpdated,
      taskStore.lastUpdated,
    ].filter(Boolean);
    if (candidates.length === 0) return null;
    return new Date(Math.max(...candidates.map(d => d.getTime())));
  });

  /* ── Instruments (signal strip) ── */
  let instruments = $derived.by(() => [
    {
      label: 'Active Agents',
      value: agentCount,
      max: Math.max(agentCount, sessionCount, 4),
      color: 'var(--info)',
      route: 'fleet',
    },
    {
      label: 'Tasks Done',
      value: completedTaskCount,
      max: Math.max(completedTaskCount, pendingTasks + activeTasks, 8),
      color: 'var(--success)',
      route: 'tasks',
    },
    {
      label: 'System Load',
      value: parseInt(shuttleStore.systemLoadPct) || 0,
      max: 100,
      suffix: '%',
      color: (parseInt(shuttleStore.systemLoadPct) || 0) > 80
        ? 'var(--error)'
        : (parseInt(shuttleStore.systemLoadPct) || 0) > 60
          ? 'var(--warning)'
          : 'var(--info)',
      route: 'dispatch',
    },
    {
      label: 'Running',
      value: healthyCount,
      max: Math.max(serverCount, 1),
      suffix: `/${serverCount}`,
      color: downCount > 0 ? 'var(--error)' : 'var(--success)',
      route: 'servers',
    },
  ]);

  /* ── Hero summary ── */
  let heroSummary = $derived.by(() => {
    const storeConflicts = coordinationSummary.conflict_files ?? 0;
    const kpiConflicts = kpis.file_conflicts ?? 0;
    const conflictCount = storeConflicts > 0 ? storeConflicts : kpiConflicts;

    if (conflictCount > 0) {
      const conflict = kpis.conflict_details?.[0];
      return {
        eyebrow: 'Coordination pressure',
        headline: 'File conflicts need attention',
        detail: conflict
          ? `${conflict.path} is shared by ${conflict.agents.join(', ')}`
          : `${conflictCount} file conflict${conflictCount === 1 ? '' : 's'} detected`,
        tone: 'alert',
        action: { label: 'Resolve conflicts', route: 'dispatch' },
      };
    }
    if (downCount > 0) {
      return {
        eyebrow: 'Infrastructure watch',
        headline: 'Server health needs attention',
        detail: `${downCount} server${downCount === 1 ? '' : 's'} down · ${serverCount} monitored`,
        tone: 'alert',
        action: { label: 'Check servers', route: 'servers' },
      };
    }
    if (blockedTasks > 0 || coordinationSummary.cross_agent_blockers > 0) {
      return {
        eyebrow: 'Work queue',
        headline: 'Blocked work needs attention',
        detail: `${blockedTasks} blocked task${blockedTasks === 1 ? '' : 's'} · ${coordinationSummary.cross_agent_blockers} cross-agent blocker${coordinationSummary.cross_agent_blockers === 1 ? '' : 's'}`,
        tone: 'alert',
        action: { label: 'Unblock tasks', route: 'dispatch' },
      };
    }
    if (pendingApprovals > 0) {
      return {
        eyebrow: 'Approvals pending',
        headline: 'Workflow approvals are waiting',
        detail: `${pendingApprovals} workflow decision${pendingApprovals === 1 ? '' : 's'} ready for review`,
        tone: 'alert',
        action: { label: 'Review approvals', route: 'workflows' },
      };
    }
    return {
      eyebrow: 'System nominal',
      headline: 'No active pressure',
      detail: 'All systems operating within normal parameters.',
      tone: 'calm',
      action: null,
    };
  });

  /* ── Attention lanes (compact right column) ── */
  let attentionLanes = $derived.by(() => {
    const lanes = [];
    if (downCount > 0 || !daemonRunning) {
      lanes.push({
        route: 'servers',
        label: 'Runtime',
        action: 'Investigate',
        value: downCount > 0 ? `${downCount} down` : 'Daemon offline',
        detail: daemonRunning
          ? `${healthyCount}/${serverCount} healthy · ${processCount} proc`
          : 'Daemon needs restart',
        severity: 'error',
      });
    }
    if (blockedTasks > 0 || coordinationSummary.cross_agent_blockers > 0) {
      lanes.push({
        route: 'dispatch',
        label: 'Blocked',
        action: 'Unblock',
        value: `${blockedTasks} task${blockedTasks === 1 ? '' : 's'}`,
        detail: activeBlockers.length > 0
          ? activeBlockers[0].task_title
          : `${coordinationSummary.cross_agent_blockers} cross-agent`,
        severity: 'warning',
      });
    }
    if (pendingApprovals > 0) {
      lanes.push({
        route: 'workflows',
        label: 'Approvals',
        action: 'Review',
        value: `${pendingApprovals} waiting`,
        detail: 'Workflow decisions ready',
        severity: 'warning',
      });
    }
    if (coordinationSummary.agents_needing_attention > 0) {
      const leadAgent = topAttentionAgents[0];
      lanes.push({
        route: 'fleet',
        label: 'Attention',
        action: leadAgent?.session_id ? 'Session' : 'Traces',
        value: `${coordinationSummary.agents_needing_attention} agent${coordinationSummary.agents_needing_attention === 1 ? '' : 's'}`,
        detail: leadAgent
          ? `${leadAgent.agent_id} · ${leadAgent.attention_reasons?.[0] || 'needs review'}`
          : 'Needs review',
        severity: 'info',
        kind: 'agent',
        agent: leadAgent,
      });
    }
    if (shuttleStore.hasRecommendations) {
      lanes.push({
        route: 'dispatch',
        label: 'Dispatch',
        action: 'Route',
        value: `${shuttleStore.recommendations.length} suggestion${shuttleStore.recommendations.length === 1 ? '' : 's'}`,
        detail: shuttleStore.recommendations[0]?.task_title || 'Ready',
        severity: 'info',
      });
    }
    return lanes.slice(0, 5);
  });

  function onLaneClick(lane) {
    if (lane.kind === 'agent' && lane.agent) {
      navigateToAgentSessionOrTraces(router, lane.agent, (id) => fleetStore.sessionForAgent(id));
      return;
    }
    router.navigate(lane.route);
  }

  /* ── Inbox cards ── */
  let inboxCards = $derived.by(() => selectInboxCards({
    router,
    coordination: coordinationStore,
    tasks: taskStore,
    workflows: workflowStore,
    health: healthStore,
    fleet: fleetStore,
    rbac: rbacStore,
    liveSessions: liveSessionsStore,
  }));

  /* ── Supporting surfaces ── */
  let supportSurfaces = $derived.by(() => [
    { route: 'memory',  label: 'Memory',  value: `${workingItems + shortItems + longItems}`, detail: `${totalTokens.toLocaleString()} tok` },
    { route: 'stream',  label: 'Stream',  value: `${streamCount}`,                          detail: lastStreamAge ?? 'idle' },
    { route: 'sandbox', label: 'Sandbox', value: `${sandboxStore.runningCount}`,            detail: sandboxStore.available ? 'online' : 'offline' },
    { route: 'graph',   label: 'Graph',   value: `${graphEntities}`,                        detail: graphTopTypes },
  ]);

  let todayLabel = $derived(`${kpis.sessions_today} sessions · ${completedTaskCount} tasks`);
  let refreshedLabel = $derived(lastRefreshed ? agoText(lastRefreshed.getTime()) : null);
</script>

<div class="panel overview">
  {#if initialLoad}
    <div class="skeleton-block" aria-hidden="true">
      <div class="skeleton skeleton-bar" style="height: 64px;"></div>
      <div class="skeleton skeleton-bar" style="height: 120px;"></div>
      <div class="skeleton skeleton-bar" style="height: 200px;"></div>
    </div>
  {:else}
    <InstrumentStrip
      instruments={instruments}
      todayLabel={todayLabel}
      refreshedLabel={refreshedLabel}
      onSelect={navigate}
    />

    <HeroSummary
      hero={heroSummary}
      lanes={attentionLanes}
      onAction={navigate}
      onLaneClick={onLaneClick}
    />

    <InboxDeck cards={inboxCards} />

    <section class="live-sessions-section">
      <LiveSessionsCard agentCount={agentCount} />
    </section>

    <MillsKPIRow />

    <SupportingStrip surfaces={supportSurfaces} onSelect={navigate} />
  {/if}
</div>

<style>
  .overview {
    display: flex;
    flex-direction: column;
    flex: 1;
    width: 100%;
    min-height: 0;
    min-width: 0;
    padding: 0 var(--space-5) var(--space-4);
    gap: var(--space-5);
    overflow-y: auto;
  }

  .skeleton-block {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-4) 0;
  }

  @media (max-width: 480px) {
    .overview {
      padding: var(--space-3);
      gap: var(--space-3);
    }
  }
</style>
