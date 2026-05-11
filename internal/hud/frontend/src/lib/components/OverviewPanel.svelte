<script>
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
  import MillsKPIRow from './Mills/MillsKPIRow.svelte';
  import LiveSessionsCard from './LiveSessionsCard.svelte';
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
      if (res.ok) {
        kpis = await res.json();
      }
    } catch {
      // Non-critical: the landing view can still render from the live stores.
    } finally {
      initialLoad = false;
    }
  }

  $effect(() => {
    fetchKPIs();
    const t = setInterval(fetchKPIs, 15000);
    return () => clearInterval(t);
  });

  let sessionCount = $derived(fleetStore.activeSessions.length);
  let agentSummary = $derived(fleetStore.unifiedSummary);
  let agentCount = $derived(agentSummary.live_agents);
  let namespaceCount = $derived(fleetStore.namespaceGroups.length);

  let healthyCount = $derived(healthStore.healthyCount);
  let serverCount = $derived(healthStore.availableCount);
  let downCount = $derived(healthStore.downCount);

  let pendingTasks = $derived(taskStore.pendingCount);
  let activeTasks = $derived(taskStore.inProgressCount);
  let blockedTasks = $derived(taskStore.blockedCount);
  let coordinationSummary = $derived(coordinationStore.summary);
  let activeBlockers = $derived(coordinationStore.activeBlockers);
  let riskyNamespaces = $derived(coordinationStore.riskyNamespaces);
  let topAttentionAgents = $derived(coordinationStore.topAttentionAgents);
  let topRelations = $derived(coordinationStore.relations.slice(0, 4));

  let workingItems = $derived(memoryStore.stats.working_memory?.items ?? 0);
  let shortItems = $derived(memoryStore.stats.short_term_memory?.items ?? 0);
  let longItems = $derived(memoryStore.stats.long_term_memory?.items ?? 0);
  let totalTokens = $derived(memoryStore.stats.total_tokens ?? 0);
  let compressionRatio = $derived(memoryStore.stats.compression?.ratio ?? 0);

  let daemonRunning = $derived(fleetStore.status?.running ?? false);
  let processCount = $derived(fleetStore.status?.processes?.length ?? 0);

  let graphEntities = $derived(graphStore.stats?.total_entities ?? 0);
  let graphTopTypes = $derived.by(() => {
    const types = graphStore.stats?.entity_types ?? {};
    return Object.entries(types)
      .sort((a, b) => b[1] - a[1])
      .slice(0, 3)
      .map(([name, count]) => `${name}:${count}`)
      .join(' · ') || 'none';
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
    } catch {
      return null;
    }
  });

  let pendingApprovals = $derived(
    workflowStore.activeWorkflows.filter(w => w.status === 'waiting_approval').length
  );

  let costEnabled = $derived(costStore.enabled);
  let rbacEnabled = $derived(rbacStore.enabled);
  let rbacDeniedCount = $derived(rbacStore.deniedCount);
  let auditEnabled = $derived(rbacStore.auditEnabled);

  let otelConfigured = $derived(otelStore.data?.otlp_configured ?? false);

  $effect(() => {
    // Start core stores so the dashboard renders useful data even when
    // Overview is the first (or only) panel the user visits.
    fleetStore.startPolling(10000, fleetPollingOwner);
    healthStore.startPolling(15000);
    taskStore.startPolling(15000);
    memoryStore.startPolling(30000);
    streamStore.startPolling(15000);
    costStore.startPolling(30000);
    rbacStore.startPolling(30000);
    coordinationStore.startPolling(30000);
    mergeQueueStore.startPolling(30000);
    shuttleStore.startPolling(30000);
    millsStore.startPolling(30000);
    otelStore.startPolling(30000, otelPollingOwner);
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

  function navigate(panel) {
    router.navigate(panel);
  }

  function navigateToAttentionLane(lane) {
    if (lane.kind === 'agent' && lane.agent) {
      navigateToAgentSessionOrTraces(router, lane.agent, (agentId) => fleetStore.sessionForAgent(agentId));
      return;
    }
    router.navigate(lane.route);
  }

  /* ── Instrument readouts (signal strip) ── */
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
      color: (parseInt(shuttleStore.systemLoadPct) || 0) > 80 ? 'var(--error)' : (parseInt(shuttleStore.systemLoadPct) || 0) > 60 ? 'var(--warning)' : 'var(--info)',
      route: 'dispatch',
    },
    {
      // U1: Top counter labeled "Running" (running+OK) to avoid contradicting
      // the footer's broader "healthy" definition (which includes idle servers).
      label: 'Running',
      value: healthyCount,
      max: Math.max(serverCount, 1),
      suffix: `/${serverCount}`,
      color: downCount > 0 ? 'var(--error)' : 'var(--success)',
      route: 'servers',
    },
  ]);

  /* ── Hero / command summary ── */
  // Derive completed-task count from the task store so Overview does not depend
  // solely on the /api/kpis endpoint for that figure. Fall back to KPI value
  // when the task store has not loaded yet.
  let completedTaskCount = $derived.by(() => {
    const storeCount = taskStore.tasks.filter(t => t.status === 'completed').length;
    return storeCount > 0 ? storeCount : kpis.tasks_completed_today;
  });

  // Combined "last refreshed" timestamp: pick the most recent update across
  // the primary stores so the user knows how fresh the dashboard data is.
  let lastRefreshed = $derived.by(() => {
    const candidates = [
      fleetStore.lastUpdated,
      healthStore.lastUpdated,
      taskStore.lastUpdated,
    ].filter(Boolean);
    if (candidates.length === 0) return null;
    return new Date(Math.max(...candidates.map(d => d.getTime())));
  });

  let heroSummary = $derived.by(() => {
    // Prefer store-derived conflict data over KPI endpoint.
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

  /* ── Attention lanes ── */
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
          ? `${activeBlockers[0].task_title}`
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

    const mergeReady = coordinationSummary.merge_ready_branches ?? 0;
    if (mergeReady > 0) {
      lanes.push({
        route: 'dispatch',
        label: 'Merge',
        action: 'Land',
        value: `${mergeReady} branch${mergeReady === 1 ? '' : 'es'}`,
        detail: 'Passed checks, ready to land',
        severity: 'success',
      });
    }

    if (mergeQueueStore.hasConflicts) {
      lanes.push({
        route: 'dispatch',
        label: 'Conflicts',
        action: 'Resolve',
        value: `${mergeQueueStore.conflicts.length} pair${mergeQueueStore.conflicts.length === 1 ? '' : 's'}`,
        detail: 'Overlapping file claims',
        severity: 'error',
      });
    }

    return lanes.slice(0, 5);
  });

  let hasAttention = $derived(attentionLanes.length > 0);

  /* ── Primary surface cards ── */
  let primaryCards = $derived.by(() => {
    // U2: If no hotspots are surfaced in the body, zero out the Attention/Risk
    // chips so the card stops contradicting itself.
    const hotspotsPresent = activeBlockers.length > 0 || topRelations.length > 0;
    const attentionValue = hotspotsPresent ? coordinationSummary.agents_needing_attention : 0;
    const riskValue = hotspotsPresent ? coordinationSummary.namespaces_at_risk : 0;
    return [
    {
      route: 'fleet',
      label: 'Coordination',
      value: `${agentCount}`,
      unit: `live agent${agentCount === 1 ? '' : 's'}`,
      detail: `${sessionCount} active session${sessionCount === 1 ? '' : 's'} · ${namespaceCount} namespace${namespaceCount === 1 ? '' : 's'}`,
      foot: activeBlockers.length > 0
        ? `${activeBlockers[0].task_title} blocked`
        : topRelations.length > 0
          ? `${topRelations[0].source_label} → ${topRelations[0].target_label}`
          : 'No hotspots',
      alert: coordinationSummary.conflict_files > 0 || coordinationSummary.cross_agent_blockers > 0 || (hotspotsPresent && coordinationSummary.agents_needing_attention > 0),
      tags: [
        { label: 'Conflicts', value: coordinationSummary.conflict_files, active: coordinationSummary.conflict_files > 0 },
        { label: 'Attention', value: attentionValue, active: attentionValue > 0 },
        { label: 'Risk', value: riskValue, active: riskValue > 0 },
      ],
    },
    {
      route: 'servers',
      label: 'Runtime',
      value: serverCount > 0 ? `${healthyCount}` : '—',
      unit: serverCount > 0 ? `of ${serverCount} healthy` : 'no servers',
      detail: `${downCount > 0 ? `${downCount} down` : 'All reachable'} · ${daemonRunning ? 'daemon up' : 'daemon down'} · ${processCount} proc`,
      foot: `${otelConfigured ? 'OTel on' : 'OTel off'} · ${costEnabled ? 'cost on' : 'cost off'}`,
      alert: downCount > 0 || !daemonRunning,
      tags: [
        { label: 'RBAC', value: rbacEnabled && rbacDeniedCount > 0 ? rbacDeniedCount : (rbacEnabled ? 1 : 0), active: rbacEnabled },
        { label: 'Audit', value: auditEnabled ? 1 : 0, active: auditEnabled },
        { label: 'OTel', value: otelConfigured ? 1 : 0, active: otelConfigured },
      ],
    },
    {
      route: 'tasks',
      label: 'Work Queue',
      value: `${pendingTasks}`,
      unit: 'pending',
      detail: `${activeTasks} active · ${blockedTasks} blocked`,
      foot: pendingApprovals > 0
        ? `${pendingApprovals} approval${pendingApprovals === 1 ? '' : 's'} waiting`
        : `${coordinationSummary.cross_agent_blockers} cross-agent blocker${coordinationSummary.cross_agent_blockers === 1 ? '' : 's'}`,
      alert: blockedTasks > 0 || pendingApprovals > 0,
      tags: [
        { label: 'Approvals', value: pendingApprovals, active: pendingApprovals > 0 },
        { label: 'Blocked', value: blockedTasks, active: blockedTasks > 0 },
        { label: 'X-agent', value: coordinationSummary.cross_agent_blockers, active: coordinationSummary.cross_agent_blockers > 0 },
      ],
    },
    {
      route: 'dispatch',
      label: 'Shuttle',
      value: shuttleStore.systemLoadPct || '0%',
      unit: 'load',
      detail: `${shuttleStore.recommendations.length} suggested · ${(coordinationSummary.merge_ready_branches ?? 0)} merge-ready`,
      foot: shuttleStore.hasRecommendations
        ? `${shuttleStore.recommendations[0].task_title} → ${shuttleStore.recommendations[0].recommended_agent}`
        : 'No dispatch recommendations',
      alert: shuttleStore.hasRecommendations || mergeQueueStore.hasConflicts,
      tags: [
        { label: 'Dispatch', value: shuttleStore.recommendations.length, active: shuttleStore.hasRecommendations },
        { label: 'Merge', value: coordinationSummary.merge_ready_branches ?? 0, active: (coordinationSummary.merge_ready_branches ?? 0) > 0 },
        { label: 'Conflicts', value: mergeQueueStore.conflicts.length, active: mergeQueueStore.hasConflicts },
      ],
    },
  ];
  });

  let sortedCards = $derived.by(() => {
    const cards = primaryCards;
    return [...cards.filter(c => c.alert), ...cards.filter(c => !c.alert)];
  });

  /* ── Supporting surfaces ── */
  let supportExpanded = $state(false);

  let supportSurfaces = $derived.by(() => [
    {
      route: 'memory',
      label: 'Memory',
      value: `${workingItems + shortItems + longItems}`,
      detail: `${totalTokens.toLocaleString()} tok`,
    },
    {
      route: 'stream',
      label: 'Stream',
      value: `${streamCount}`,
      detail: lastStreamAge ? lastStreamAge : 'idle',
    },
    {
      route: 'sandbox',
      label: 'Sandbox',
      value: `${sandboxStore.runningCount}`,
      detail: sandboxStore.available ? 'online' : 'offline',
    },
    {
      route: 'graph',
      label: 'Graph',
      value: `${graphEntities}`,
      detail: graphTopTypes.split(' · ')[0] || 'empty',
    },
  ]);

  /* ── SVG ring gauge helper ── */
  function ringPath(pct) {
    const r = 18;
    const circumference = 2 * Math.PI * r;
    return circumference - (pct / 100) * circumference;
  }
</script>

<div class="panel overview">
  {#if initialLoad}
    <!-- Skeleton -->
    <div class="signal-strip">
      {#each Array(4) as _}
        <div class="instrument instrument-skeleton">
          <div class="skeleton skeleton-bar" style="width: 42px; height: 42px; border-radius: 50%;"></div>
          <div class="inst-data">
            <div class="skeleton skeleton-text" style="width: 48px;"></div>
            <div class="skeleton skeleton-bar" style="width: 64px; height: 18px;"></div>
          </div>
        </div>
      {/each}
    </div>
    <div class="command-section command-skeleton">
      <div class="skeleton skeleton-bar" style="width: min(400px, 60%); height: 28px;"></div>
      <div class="skeleton skeleton-text" style="width: min(320px, 50%); margin-top: 8px;"></div>
    </div>
    <div class="surface-grid">
      {#each Array(4) as _}
        <div class="surface-card surface-card-skeleton">
          <div class="skeleton skeleton-text" style="width: 40%;"></div>
          <div class="skeleton skeleton-bar" style="width: 50%; height: 22px; margin-top: 8px;"></div>
          <div class="skeleton skeleton-text" style="width: 70%; margin-top: 8px;"></div>
        </div>
      {/each}
    </div>
  {:else}

    <!-- ═══ Signal Strip ═══ -->
    <div class="signal-strip">
      {#each instruments as inst, i (inst.label)}
        <button
          class="instrument"
          onclick={() => navigate(inst.route)}
          style="animation-delay: {i * 60}ms;"
        >
          <div class="inst-ring">
            <svg viewBox="0 0 44 44" class="ring-svg">
              <circle cx="22" cy="22" r="18" class="ring-track" />
              <circle
                cx="22" cy="22" r="18"
                class="ring-fill"
                style="
                  stroke: {inst.color};
                  stroke-dasharray: {2 * Math.PI * 18};
                  stroke-dashoffset: {ringPath(inst.max > 0 ? (inst.value / inst.max) * 100 : 0)};
                  filter: drop-shadow(0 0 4px {inst.color});
                "
              />
            </svg>
            <span class="inst-ring-value">{inst.value}{inst.suffix || ''}</span>
          </div>
          <div class="inst-data">
            <span class="inst-label">{inst.label}</span>
          </div>
        </button>
      {/each}

      <div class="strip-divider"></div>

      <!-- Today summary chip -->
      <div class="strip-summary">
        <span class="strip-summary-label">Today</span>
        <span class="strip-summary-value">{kpis.sessions_today} sessions · {completedTaskCount} tasks</span>
        {#if lastRefreshed}
          <span class="strip-refreshed">Last refreshed: {agoText(lastRefreshed.getTime())}</span>
        {/if}
      </div>
    </div>

    <!-- ═══ Command Section ═══ -->
    <section class="command-section" class:command-alert={heroSummary.tone === 'alert'}>
      <div class="command-main">
        <div class="command-eyebrow">{heroSummary.eyebrow}</div>
        <h1 class="command-title">{heroSummary.headline}</h1>
        <p class="command-detail">{heroSummary.detail}</p>
        {#if heroSummary.action}
          <button class="command-action" onclick={() => navigate(heroSummary.action.route)}>
            {heroSummary.action.label}
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M5 3l4 4-4 4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
          </button>
        {/if}
      </div>

      {#if attentionLanes.length > 0}
        <div class="lane-list">
          <div class="lane-list-label">Attention lanes</div>
          {#each attentionLanes as lane (lane.route + lane.label)}
            <button class="lane-item lane-{lane.severity}" onclick={() => navigateToAttentionLane(lane)}>
              <div class="lane-head">
                <span class="lane-dot" style="background: var(--{lane.severity});"></span>
                <span class="lane-name">{lane.label}</span>
                <span class="lane-badge">{lane.action}</span>
              </div>
              <div class="lane-value">{lane.value}</div>
              <div class="lane-detail">{lane.detail}</div>
            </button>
          {/each}
        </div>
      {/if}
    </section>

    <!-- ═══ Primary Surfaces ═══ -->
    <section class="surfaces-section">
      <div class="surfaces-header">
        <span class="surfaces-label">Surfaces</span>
        <span class="surfaces-note">{hasAttention ? 'Alert surfaces promoted' : 'Primary monitoring surfaces'}</span>
      </div>

      <div class="surface-grid">
        {#each sortedCards as card, i (card.route)}
          <button
            class="surface-card"
            class:surface-alert={card.alert}
            onclick={() => navigate(card.route)}
            style="animation-delay: {i * 50}ms;"
          >
            <div class="sc-top">
              <span class="sc-label">{card.label}</span>
              <span class="sc-cta">
                <svg width="12" height="12" viewBox="0 0 12 12" fill="none"><path d="M4 2l4 4-4 4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/></svg>
              </span>
            </div>
            <div class="sc-value-row">
              <span class="sc-value">{card.value}</span>
              <span class="sc-unit">{card.unit}</span>
            </div>
            <div class="sc-detail">{card.detail}</div>
            <div class="sc-tags">
              {#each card.tags as tag (tag.label)}
                <span class="sc-tag" class:tag-active={tag.active} class:tag-idle={!tag.active}>
                  {tag.label}
                  {#if tag.active}
                    <strong>{tag.value}</strong>
                  {/if}
                </span>
              {/each}
            </div>
            <div class="sc-foot">{card.foot}</div>
          </button>
        {/each}
      </div>
    </section>

    <!-- ═══ Live Sessions (Phase 3 spectator) ═══ -->
    <section class="live-sessions-section">
      <LiveSessionsCard agentCount={agentCount} />
    </section>

    <!-- ═══ Mills KPIs ═══ -->
    <MillsKPIRow />

    <!-- ═══ Supporting Surfaces ═══ -->
    <section class="support-section">
      <button class="support-toggle" onclick={() => { supportExpanded = !supportExpanded; }}>
        <span class="support-toggle-label">Supporting</span>
        {#if !supportExpanded}
          <span class="support-inline">
            {#each supportSurfaces as s (s.route)}
              <span class="support-chip-inline" onclick={(e) => { e.stopPropagation(); navigate(s.route); }}>
                <span class="sci-label">{s.label}</span>
                <strong>{s.value}</strong>
              </span>
            {/each}
          </span>
        {/if}
        <span class="support-chevron" class:chevron-open={supportExpanded}>
          <svg width="10" height="10" viewBox="0 0 10 10"><path d="M3 2l4 3-4 3" fill="currentColor"/></svg>
        </span>
      </button>

      {#if supportExpanded || !hasAttention}
        <div class="support-grid">
          {#each supportSurfaces as surface (surface.route)}
            <button class="support-chip" onclick={() => navigate(surface.route)}>
              <span class="chip-label">{surface.label}</span>
              <span class="chip-value">{surface.value}</span>
              <span class="chip-detail">{surface.detail}</span>
            </button>
          {/each}
        </div>
      {/if}
    </section>

  {/if}
</div>

<style>
  /* ═══ Overview Layout ═══════════════════════════════════════ */

  .overview {
    display: flex;
    flex-direction: column;
    width: 100%;
    min-width: 0;
    padding: var(--space-4) var(--space-5);
    gap: var(--space-5);
    overflow-y: auto;
  }


  /* ═══ Signal Strip ══════════════════════════════════════════ */

  .signal-strip {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    width: 100%;
    max-width: 100%;
    min-width: 0;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border-radius: var(--radius-lg);
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    position: relative;
    overflow: hidden;
  }

  /* Subtle grid pattern */
  .signal-strip::before {
    content: '';
    position: absolute;
    inset: 0;
    opacity: 0.03;
    background-image:
      linear-gradient(var(--fg-muted) 1px, transparent 1px),
      linear-gradient(90deg, var(--fg-muted) 1px, transparent 1px);
    background-size: 24px 24px;
    pointer-events: none;
  }

  /* Top-edge light */
  .signal-strip::after {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: linear-gradient(
      90deg,
      transparent 5%,
      rgba(0, 200, 255, 0.15) 30%,
      rgba(0, 200, 255, 0.25) 50%,
      rgba(0, 200, 255, 0.15) 70%,
      transparent 95%
    );
    pointer-events: none;
  }

  .instrument {
    display: flex;
    align-items: center;
    gap: var(--space-3);
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-md);
    cursor: pointer;
    transition: background var(--transition-fast), transform var(--transition-fast);
    position: relative;
    z-index: 1;
    animation: fadeIn 0.25s ease-out both;
  }

  .instrument:hover {
    background: rgba(0, 200, 255, 0.05);
    transform: translateY(-1px);
  }

  .inst-ring {
    position: relative;
    width: 44px;
    height: 44px;
    flex-shrink: 0;
  }

  .ring-svg {
    width: 100%;
    height: 100%;
    transform: rotate(-90deg);
    /* The .ring-fill has a drop-shadow filter that extends ~4px past the
       circle bounds. Browsers default to overflow:hidden on <svg> so we opt
       into visible explicitly without padding the viewBox, which would shrink
       the rendered geometry inside the fixed 44x44 box. */
    overflow: visible;
  }

  .ring-track {
    fill: none;
    stroke: var(--border);
    stroke-width: 2.5;
  }

  .ring-fill {
    fill: none;
    stroke-width: 2.5;
    stroke-linecap: round;
    transition: stroke-dashoffset 0.6s cubic-bezier(0.4, 0, 0.2, 1);
  }

  .inst-ring-value {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 700;
    color: var(--fg-primary);
    font-feature-settings: 'tnum';
  }

  .inst-data {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .inst-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
    white-space: nowrap;
  }

  .strip-divider {
    width: 1px;
    height: 32px;
    background: var(--border);
    flex-shrink: 0;
    margin: 0 var(--space-1);
  }

  .strip-summary {
    display: flex;
    flex-direction: column;
    gap: 2px;
    margin-left: auto;
    text-align: right;
    flex-shrink: 0;
    z-index: 1;
  }

  .strip-summary-label {
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wider);
    color: var(--fg-muted);
  }

  .strip-summary-value {
    font-size: 11px;
    font-family: var(--font-mono);
    color: var(--fg-secondary);
    white-space: nowrap;
  }

  .strip-refreshed {
    font-size: var(--text-2xs, 10px);
    font-family: var(--font-mono);
    color: var(--fg-muted);
    opacity: 0.7;
    letter-spacing: var(--tracking-normal);
  }

  .instrument-skeleton {
    cursor: default;
    min-width: 120px;
    padding: var(--space-2) var(--space-3);
  }


  /* ═══ Command Section ═══════════════════════════════════════ */

  .command-section {
    display: grid;
    grid-template-columns: 1fr minmax(260px, 0.5fr);
    width: 100%;
    max-width: 100%;
    min-width: 0;
    gap: var(--space-5);
    padding: var(--space-5);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border);
    background:
      radial-gradient(ellipse at 15% 20%, rgba(0, 200, 255, 0.04), transparent 50%),
      var(--bg-secondary);
    position: relative;
    /* overflow:hidden clipped attention lanes when the right column grew
       taller than the left; the ::after edge-glow stays inside the box. */
  }

  /* Top-edge glow */
  .command-section::after {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: linear-gradient(
      90deg,
      transparent 10%,
      rgba(0, 200, 255, 0.12) 40%,
      rgba(0, 200, 255, 0.08) 60%,
      transparent 90%
    );
  }

  .command-alert {
    border-color: rgba(255, 107, 53, 0.2);
    background:
      radial-gradient(ellipse at 15% 20%, rgba(255, 107, 53, 0.05), transparent 50%),
      var(--bg-secondary);
  }

  .command-alert::after {
    background: linear-gradient(
      90deg,
      transparent 10%,
      rgba(255, 107, 53, 0.15) 40%,
      rgba(255, 107, 53, 0.1) 60%,
      transparent 90%
    );
  }

  .command-main {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .command-eyebrow {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .command-title {
    margin: 0;
    font-size: clamp(22px, 2.4vw, 30px);
    font-weight: 700;
    line-height: var(--leading-tight);
    color: var(--fg-primary);
    letter-spacing: var(--tracking-tight);
  }

  .command-detail {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.5;
    max-width: 52ch;
    font-family: var(--font-mono);
  }

  .command-action {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 8px 16px;
    margin-top: var(--space-2);
    border-radius: var(--radius-sm);
    border: 1px solid rgba(255, 107, 53, 0.35);
    background: var(--accent-dim);
    color: var(--accent);
    font-size: 12px;
    font-weight: 600;
    font-family: var(--font-mono);
    letter-spacing: 0.02em;
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast), box-shadow var(--transition-fast);
    width: fit-content;
  }

  .command-action:hover {
    background: rgba(255, 107, 53, 0.18);
    border-color: rgba(255, 107, 53, 0.5);
    box-shadow: 0 0 16px rgba(255, 107, 53, 0.12);
  }

  .command-action svg {
    opacity: 0.8;
  }

  .command-skeleton {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-5);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    min-height: 100px;
  }


  /* ═══ Attention Lanes ═══════════════════════════════════════ */

  .lane-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .lane-list-label {
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wider);
    color: var(--fg-muted);
  }

  .lane-item {
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
    padding: var(--space-2) var(--space-3);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-surface);
    cursor: pointer;
    transition: border-color var(--transition-normal),
                transform var(--transition-fast),
                box-shadow var(--transition-normal);
  }

  .lane-item:hover {
    transform: translateY(-1px);
    border-color: var(--border-focus);
    box-shadow: var(--shadow-sm);
  }

  .lane-head {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .lane-dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
    box-shadow: 0 0 4px currentColor;
  }

  .lane-name {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
  }

  .lane-badge {
    margin-left: auto;
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent);
    padding: 1px 6px;
    border-radius: var(--radius-full);
    border: 1px solid rgba(255, 107, 53, 0.25);
    background: var(--accent-dim);
  }

  .lane-value {
    font-size: 13px;
    font-weight: 600;
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .lane-detail {
    font-size: 10px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }


  /* ═══ Surface Grid ══════════════════════════════════════════ */

  .surfaces-section {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .surfaces-header {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-3);
  }

  .surfaces-label {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
  }

  .surfaces-note {
    font-size: 11px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .surface-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 420px), 1fr));
    width: 100%;
    max-width: 100%;
    min-width: 0;
    gap: var(--space-3);
  }

  .surface-card {
    display: flex;
    flex-direction: column;
    min-width: 0;
    gap: var(--space-2);
    text-align: left;
    padding: var(--space-4);
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    cursor: pointer;
    position: relative;
    overflow: hidden;
    transition: border-color var(--transition-normal),
                transform var(--transition-normal),
                box-shadow var(--transition-normal);
    animation: panelSlideIn 0.25s ease-out both;
  }

  /* Top-edge highlight */
  .surface-card::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 1px;
    background: linear-gradient(
      90deg,
      transparent 10%,
      rgba(0, 200, 255, 0.1) 50%,
      transparent 90%
    );
  }

  .surface-card:hover {
    border-color: rgba(0, 200, 255, 0.2);
    transform: translateY(-2px);
    box-shadow: 0 0 20px var(--glow-info), var(--shadow-md);
  }

  .surface-alert {
    border-color: rgba(255, 107, 53, 0.18);
  }

  .surface-alert::before {
    background: linear-gradient(
      90deg,
      transparent 10%,
      rgba(255, 107, 53, 0.15) 50%,
      transparent 90%
    );
  }

  .surface-alert:hover {
    border-color: rgba(255, 107, 53, 0.3);
    box-shadow: 0 0 20px var(--glow-accent), var(--shadow-md);
  }

  .sc-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .sc-label {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
  }

  .sc-cta {
    color: var(--fg-muted);
    opacity: 0;
    transition: opacity var(--transition-fast), color var(--transition-fast);
  }

  .surface-card:hover .sc-cta {
    opacity: 1;
    color: var(--accent);
  }

  .sc-value-row {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
  }

  .sc-value {
    font-size: 28px;
    font-weight: 700;
    font-family: var(--font-mono);
    font-feature-settings: 'tnum';
    color: var(--fg-primary);
    line-height: 1;
    letter-spacing: -0.02em;
  }

  .sc-unit {
    font-size: 11px;
    font-weight: 500;
    color: var(--fg-muted);
  }

  .sc-detail {
    font-size: 11px;
    color: var(--fg-secondary);
    font-family: var(--font-mono);
  }

  .sc-tags {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
    margin-top: var(--space-1);
  }

  .sc-tag {
    font-size: 9px;
    font-family: var(--font-mono);
    padding: 2px 7px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    color: var(--fg-muted);
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }

  .tag-active {
    color: var(--fg-primary);
    border-color: rgba(255, 107, 53, 0.3);
    background: var(--accent-dim);
  }

  .tag-active strong {
    color: var(--accent);
  }

  .tag-idle {
    opacity: 0.55;
  }

  .sc-foot {
    font-size: 10px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    margin-top: auto;
    padding-top: var(--space-2);
    border-top: 1px solid var(--border-subtle);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .surface-card-skeleton {
    min-height: 160px;
    cursor: default;
  }


  /* ═══ Supporting Surfaces ═══════════════════════════════════ */

  .support-section {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .support-toggle {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    cursor: pointer;
    background: none;
    border: none;
    padding: 0;
    width: 100%;
    text-align: left;
    color: var(--fg-secondary);
  }

  .support-toggle:hover .support-toggle-label {
    color: var(--fg-primary);
  }

  .support-toggle-label {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    flex-shrink: 0;
    transition: color var(--transition-fast);
  }

  .support-inline {
    display: flex;
    gap: var(--space-2);
    flex: 1;
    overflow: hidden;
    margin-left: var(--space-2);
  }

  .support-chip-inline {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 2px 8px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    white-space: nowrap;
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast);
  }

  .support-chip-inline:hover {
    border-color: var(--border-focus);
    color: var(--fg-secondary);
  }

  .sci-label {
    color: var(--fg-muted);
  }

  .support-chip-inline strong {
    color: var(--fg-secondary);
    font-weight: 600;
  }

  .support-chevron {
    color: var(--fg-muted);
    transition: transform var(--transition-fast);
    flex-shrink: 0;
    display: flex;
    align-items: center;
    margin-left: auto;
  }

  .chevron-open {
    transform: rotate(90deg);
  }

  .support-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr));
    gap: var(--space-2);
  }

  .support-chip {
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
    padding: var(--space-3);
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    cursor: pointer;
    transition: border-color var(--transition-normal),
                transform var(--transition-fast),
                box-shadow var(--transition-normal);
  }

  .support-chip:hover {
    border-color: rgba(0, 200, 255, 0.2);
    transform: translateY(-1px);
    box-shadow: 0 0 12px var(--glow-info), var(--shadow-xs);
  }

  .chip-label {
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .chip-value {
    font-size: 16px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
  }

  .chip-detail {
    font-size: 10px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }


  /* ═══ Responsive ════════════════════════════════════════════ */

  @media (max-width: 900px) {
    .command-section {
      grid-template-columns: 1fr;
    }

    .surface-grid {
      grid-template-columns: 1fr;
    }

    .support-grid {
      grid-template-columns: repeat(2, 1fr);
    }
  }

  @media (max-width: 768px) {
    .signal-strip {
      overflow-x: auto;
      scrollbar-width: none;
      -webkit-overflow-scrolling: touch;
      gap: var(--space-2);
      padding: var(--space-2) var(--space-3);
    }

    .signal-strip::-webkit-scrollbar {
      display: none;
    }

    .instrument {
      flex-shrink: 0;
    }

    .strip-summary {
      display: none;
    }

    .strip-divider {
      display: none;
    }
  }

  @media (max-width: 480px) {
    .overview {
      padding: var(--space-3);
      gap: var(--space-3);
    }

    .command-title {
      font-size: clamp(18px, 5vw, 22px);
    }

    .command-action {
      width: 100%;
      justify-content: center;
    }

    .support-grid {
      grid-template-columns: 1fr 1fr;
    }

    .support-inline {
      display: none;
    }

    .sc-value {
      font-size: 22px;
    }
  }
</style>
