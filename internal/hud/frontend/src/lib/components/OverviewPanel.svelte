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
  let agentCount = $derived(fleetStore.agents.filter(a => a.status === 'active').length);
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

  let otelStatus = $state({ otlp_configured: false, traced_servers: 0, total_servers: 0 });
  async function fetchOTelStatus() {
    try {
      const res = await globalThis.fetch('/api/otel');
      if (res.ok) otelStatus = await res.json();
    } catch {
      // Non-critical.
    }
  }
  $effect(() => {
    fetchOTelStatus();
    costStore.startPolling(30000);
    rbacStore.startPolling(30000);
    coordinationStore.startPolling(30000);
    mergeQueueStore.startPolling(30000);
    shuttleStore.startPolling(30000);
    const t = setInterval(fetchOTelStatus, 30000);
    return () => {
      clearInterval(t);
      costStore.stopPolling();
      rbacStore.stopPolling();
      coordinationStore.stopPolling();
      mergeQueueStore.stopPolling();
      shuttleStore.stopPolling();
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

  let heroSummary = $derived.by(() => {
    const conflict = kpis.conflict_details?.[0];

    if (kpis.file_conflicts > 0) {
      return {
        eyebrow: 'Coordination pressure',
        headline: 'File conflicts need attention',
        detail: conflict
          ? `${conflict.path} is shared by ${conflict.agents.join(', ')}`
          : `${kpis.file_conflicts} file conflict${kpis.file_conflicts === 1 ? '' : 's'} detected`,
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
      eyebrow: 'Landing surface',
      headline: 'No active pressure right now',
      detail: 'The overview is quiet. Open a lane below when you want deeper detail.',
      tone: 'calm',
      action: null,
    };
  });

  let heroSignals = $derived.by(() => [
    { label: 'active agents', value: agentCount },
    { label: 'tasks completed', value: kpis.tasks_completed_today },
    { label: 'system load', value: shuttleStore.systemLoadPct },
    { label: 'merge ready', value: coordinationSummary.merge_ready_branches ?? 0 },
  ]);

  let attentionLanes = $derived.by(() => {
    const lanes = [];

    if (downCount > 0 || !daemonRunning) {
      lanes.push({
        route: 'servers',
        label: 'Runtime lane',
        action: 'Investigate',
        value: downCount > 0 ? `${downCount} down` : 'Daemon offline',
        detail: daemonRunning
          ? `${healthyCount}/${serverCount} healthy · ${processCount} processes`
          : 'Bring the daemon and server health back into a safe state',
      });
    }

    if (blockedTasks > 0 || coordinationSummary.cross_agent_blockers > 0) {
      lanes.push({
        route: 'dispatch',
        label: 'Blocked work',
        action: 'Unblock',
        value: `${blockedTasks} blocked`,
        detail: activeBlockers.length > 0
          ? `${activeBlockers[0].task_title} blocked by ${activeBlockers[0].blocked_by_task_title || activeBlockers[0].blocked_by_task_id}`
          : `${coordinationSummary.cross_agent_blockers} cross-agent blocker${coordinationSummary.cross_agent_blockers === 1 ? '' : 's'}`,
      });
    }

    if (pendingApprovals > 0) {
      lanes.push({
        route: 'workflows',
        label: 'Approvals',
        action: 'Review',
        value: `${pendingApprovals} waiting`,
        detail: 'Review workflow decisions before the queue drifts further.',
      });
    }

    if (coordinationSummary.agents_needing_attention > 0) {
      lanes.push({
        route: 'fleet',
        label: 'Attention agents',
        action: 'Inspect',
        value: `${coordinationSummary.agents_needing_attention} flagged`,
        detail: topAttentionAgents.length > 0
          ? `${topAttentionAgents[0].agent_id} · ${(topAttentionAgents[0].attention_reasons || []).slice(0, 2).join(' · ') || 'needs coordination review'}`
          : 'Open Fleet to inspect the agents currently under pressure.',
      });
    }

    if (coordinationSummary.namespaces_at_risk > 0) {
      lanes.push({
        route: 'dispatch',
        label: 'Risky namespaces',
        action: 'Review',
        value: `${coordinationSummary.namespaces_at_risk} at risk`,
        detail: riskyNamespaces.length > 0
          ? `${riskyNamespaces[0].namespace} · ${(riskyNamespaces[0].attention_reasons || []).slice(0, 2).join(' · ')}`
          : 'Open Dispatch to review the namespaces with the most coordination pressure.',
      });
    }

    if (shuttleStore.hasRecommendations) {
      const top = shuttleStore.recommendations[0];
      lanes.push({
        route: 'dispatch',
        label: 'Smart dispatch',
        action: 'Dispatch',
        value: `${shuttleStore.recommendations.length} suggested`,
        detail: top
          ? `${top.task_title} → ${top.recommended_agent} (${Math.round(top.score * 100)}% match)`
          : 'AI-recommended agent-task pairings are ready for review.',
      });
    }

    const mergeReady = coordinationSummary.merge_ready_branches ?? 0;
    if (mergeReady > 0) {
      lanes.push({
        route: 'dispatch',
        label: 'Merge ready',
        action: 'Merge',
        value: `${mergeReady} branch${mergeReady === 1 ? '' : 'es'}`,
        detail: 'Branches passed merge checks and are ready to land.',
      });
    }

    if (mergeQueueStore.hasConflicts) {
      lanes.push({
        route: 'dispatch',
        label: 'File conflicts',
        action: 'Resolve',
        value: `${mergeQueueStore.conflicts.length} pair${mergeQueueStore.conflicts.length === 1 ? '' : 's'}`,
        detail: 'Agents are claiming overlapping files. Resolve before merging.',
      });
    }

    return lanes.slice(0, 5);
  });

  let priorityLinks = $derived.by(() => [
    {
      route: 'fleet',
      label: 'Fleet',
      value: `${sessionCount} sessions`,
      detail: coordinationSummary.conflict_files > 0
        ? `${coordinationSummary.conflict_files} conflict${coordinationSummary.conflict_files === 1 ? '' : 's'}`
        : `${coordinationSummary.namespaces_at_risk} namespace${coordinationSummary.namespaces_at_risk === 1 ? '' : 's'} at risk`,
    },
    {
      route: 'servers',
      label: 'Servers',
      value: serverCount > 0 ? `${healthyCount}/${serverCount} healthy` : 'No servers',
      detail: downCount > 0
        ? `${downCount} down`
        : `${daemonRunning ? 'daemon running' : 'daemon stopped'} · ${processCount} processes`,
    },
    {
      route: 'tasks',
      label: 'Work',
      value: `${pendingTasks} pending`,
      detail: `${activeTasks} active · ${blockedTasks} blocked`,
    },
    {
      route: 'workflows',
      label: 'Approvals',
      value: `${pendingApprovals} waiting`,
      detail: pendingApprovals > 0 ? 'Workflow decisions are waiting' : 'No approval pressure',
    },
  ]);

  let primaryCards = $derived.by(() => [
    {
      route: 'fleet',
      label: 'Coordination pressure',
      value: `${sessionCount} sessions`,
      detail: `${agentCount} active agents · ${namespaceCount} namespaces · ${coordinationSummary.namespaces_at_risk} at risk`,
      foot: activeBlockers.length > 0
        ? `${activeBlockers[0].task_title} is blocked by ${activeBlockers[0].blocked_by_task_title || activeBlockers[0].blocked_by_task_id}`
        : topRelations.length > 0
          ? `${topRelations[0].source_label} ↔ ${topRelations[0].target_label}`
          : 'No active relation hotspots',
      alert: coordinationSummary.conflict_files > 0 || coordinationSummary.cross_agent_blockers > 0 || coordinationSummary.agents_needing_attention > 0,
      tags: [
        {
          label: 'Conflicts',
          active: coordinationSummary.conflict_files > 0,
          note: coordinationSummary.conflict_files > 0 ? String(coordinationSummary.conflict_files) : 'clear',
        },
        {
          label: 'Attention',
          active: coordinationSummary.agents_needing_attention > 0,
          note: coordinationSummary.agents_needing_attention > 0 ? String(coordinationSummary.agents_needing_attention) : 'clear',
        },
        {
          label: 'Risk',
          active: coordinationSummary.namespaces_at_risk > 0,
          note: coordinationSummary.namespaces_at_risk > 0 ? String(coordinationSummary.namespaces_at_risk) : 'clear',
        },
      ],
    },
    {
      route: 'servers',
      label: 'Runtime health',
      value: serverCount > 0 ? `${healthyCount}/${serverCount} healthy` : 'No servers',
      detail: `${downCount > 0 ? `${downCount} down` : 'All servers reachable'} · ${daemonRunning ? 'daemon running' : 'daemon stopped'} · ${processCount} processes`,
      foot: `${otelStatus.otlp_configured ? 'OTel configured' : 'OTel off'} · ${costEnabled ? 'cost tracking on' : 'cost tracking off'}`,
      alert: downCount > 0 || !daemonRunning,
      tags: [
        {
          label: 'RBAC',
          active: rbacEnabled,
          note: rbacEnabled && rbacDeniedCount > 0 ? `${rbacDeniedCount} denied` : (rbacEnabled ? 'on' : 'off'),
        },
        {
          label: 'Audit',
          active: auditEnabled,
          note: auditEnabled ? 'on' : 'off',
        },
        {
          label: 'OTel',
          active: otelStatus.otlp_configured,
          note: otelStatus.otlp_configured ? 'on' : 'off',
        },
        {
          label: 'Cost',
          active: costEnabled,
          note: costEnabled ? 'on' : 'off',
        },
      ],
    },
    {
      route: 'tasks',
      label: 'Work queue',
      value: `${pendingTasks} pending`,
      detail: `${activeTasks} active · ${blockedTasks} blocked`,
      foot: pendingApprovals > 0
        ? `${pendingApprovals} approvals waiting`
        : `${coordinationSummary.cross_agent_blockers} cross-agent blockers`,
      alert: blockedTasks > 0 || pendingApprovals > 0 || coordinationSummary.cross_agent_blockers > 0,
      tags: [
        {
          label: 'Approvals',
          active: pendingApprovals > 0,
          note: pendingApprovals > 0 ? String(pendingApprovals) : 'clear',
        },
        {
          label: 'Blocked',
          active: blockedTasks > 0,
          note: blockedTasks > 0 ? String(blockedTasks) : 'clear',
        },
        {
          label: 'Cross-agent',
          active: coordinationSummary.cross_agent_blockers > 0,
          note: coordinationSummary.cross_agent_blockers > 0 ? String(coordinationSummary.cross_agent_blockers) : 'clear',
        },
      ],
    },
    {
      route: 'dispatch',
      label: 'Fleet shuttle',
      value: shuttleStore.systemLoadPct,
      detail: `${shuttleStore.recommendations.length} suggestion${shuttleStore.recommendations.length === 1 ? '' : 's'} · ${(coordinationSummary.merge_ready_branches ?? 0)} merge-ready · ${mergeQueueStore.conflicts.length} conflict${mergeQueueStore.conflicts.length === 1 ? '' : 's'}`,
      foot: shuttleStore.hasRecommendations
        ? `Top: ${shuttleStore.recommendations[0].task_title} → ${shuttleStore.recommendations[0].recommended_agent}`
        : 'No pending dispatch recommendations',
      alert: shuttleStore.hasRecommendations || mergeQueueStore.hasConflicts || (coordinationSummary.merge_ready_branches ?? 0) > 0,
      tags: [
        {
          label: 'Suggestions',
          active: shuttleStore.hasRecommendations,
          note: shuttleStore.hasRecommendations ? String(shuttleStore.recommendations.length) : 'none',
        },
        {
          label: 'Merge',
          active: (coordinationSummary.merge_ready_branches ?? 0) > 0,
          note: (coordinationSummary.merge_ready_branches ?? 0) > 0 ? String(coordinationSummary.merge_ready_branches) : 'none',
        },
        {
          label: 'Conflicts',
          active: mergeQueueStore.hasConflicts,
          note: mergeQueueStore.hasConflicts ? String(mergeQueueStore.conflicts.length) : 'clear',
        },
      ],
    },
  ]);

  let sortedPrimaryCards = $derived.by(() => {
    const cards = primaryCards;
    const alertCards = cards.filter((c) => c.alert);
    const calmCards = cards.filter((c) => !c.alert);
    return [...alertCards, ...calmCards];
  });

  let hasAttention = $derived(attentionLanes.length > 0);
  let supportExpanded = $state(false);

  let supportSurfaces = $derived.by(() => [
    {
      route: 'memory',
      label: 'Memory',
      value: `${workingItems + shortItems + longItems} items`,
      detail: `${totalTokens.toLocaleString()} tokens · ${compressionRatio > 0 ? `${Math.round(compressionRatio * 100)}% compressed` : 'no compression data'}`,
    },
    {
      route: 'stream',
      label: 'Stream',
      value: `${streamCount} entries`,
      detail: lastStreamAge ? `last update ${lastStreamAge}` : 'no stream data',
    },
    {
      route: 'sandbox',
      label: 'Sandbox',
      value: `${sandboxStore.runningCount} running`,
      detail: sandboxStore.available
        ? `${sandboxStore.totalExecs} exec${sandboxStore.totalExecs === 1 ? '' : 's'} · ${sandboxStore.totalBuilds} build${sandboxStore.totalBuilds === 1 ? '' : 's'}`
        : 'offline',
    },
    {
      route: 'graph',
      label: 'Graph',
      value: `${graphEntities} entities`,
      detail: graphTopTypes,
    },
  ]);
</script>

<div class="panel overview-panel">
  {#if initialLoad}
    <section class="overview-hero hero-skeleton">
      <div class="hero-copy">
        <div class="skeleton skeleton-text" style="width: 120px;"></div>
        <div class="skeleton skeleton-bar" style="width: min(540px, 78%); height: 30px; margin-top: 10px;"></div>
        <div class="skeleton skeleton-text" style="width: min(520px, 70%); margin-top: 10px;"></div>
        <div class="hero-signals">
          {#each Array(4) as _}
            <div class="signal-chip signal-chip-skeleton"></div>
          {/each}
        </div>
      </div>
      <div class="hero-rail">
        {#each Array(4) as _}
          <div class="rail-item rail-item-skeleton"></div>
        {/each}
      </div>
    </section>

    <div class="focus-grid">
      {#each Array(3) as _}
        <div class="focus-card focus-card-skeleton">
          <div class="skeleton skeleton-text" style="width: 45%;"></div>
          <div class="skeleton skeleton-bar" style="width: 65%; height: 24px; margin-top: 10px;"></div>
          <div class="skeleton skeleton-text" style="width: 80%; margin-top: 10px;"></div>
        </div>
      {/each}
    </div>
  {:else}
    <section class="overview-hero" class:hero-alert={heroSummary.tone === 'alert'}>
      <div class="hero-copy">
        <div class="hero-eyebrow">{heroSummary.eyebrow}</div>
        <h1 class="hero-title">{heroSummary.headline}</h1>
        <p class="hero-detail">{heroSummary.detail}</p>
        {#if heroSummary.action}
          <button class="hero-action" onclick={() => navigate(heroSummary.action.route)}>
            {heroSummary.action.label} &rarr;
          </button>
        {:else}
          <div class="hero-note">
            Today: {kpis.sessions_today} sessions · {kpis.tasks_completed_today} tasks completed
          </div>
        {/if}
        <div class="hero-signals">
          {#each heroSignals as signal (signal.label)}
            <div class="signal-chip">
              <span>{signal.label}</span>
              <strong>{signal.value}</strong>
            </div>
          {/each}
        </div>
      </div>

      <div class="hero-rail">
        <div class="rail-label">{attentionLanes.length > 0 ? 'Attention lanes' : 'Priority lanes'}</div>
        <div class="rail-title">{attentionLanes.length > 0 ? 'Start with the lane that needs action next.' : 'Open the lane that needs detail.'}</div>
        <div class="rail-stack">
          {#if attentionLanes.length > 0}
            {#each attentionLanes as lane (lane.route + lane.label)}
              <button class="rail-item" onclick={() => navigate(lane.route)}>
                <div class="rail-item-head">
                  <span class="rail-item-label">{lane.label}</span>
                  <span class="rail-item-action">{lane.action}</span>
                </div>
                <strong>{lane.value}</strong>
                <small>{lane.detail}</small>
              </button>
            {/each}
          {:else}
            {#each priorityLinks as link (link.route)}
              <button class="rail-item" onclick={() => navigate(link.route)}>
                <span class="rail-item-label">{link.label}</span>
                <strong>{link.value}</strong>
                <small>{link.detail}</small>
              </button>
            {/each}
          {/if}
        </div>
        {#if attentionLanes.length > 0}
          <div class="rail-quicklinks">
            {#each priorityLinks as link (link.route)}
              <button class="quicklink-chip" onclick={() => navigate(link.route)}>
                <span>{link.label}</span>
                <strong>{link.value}</strong>
              </button>
            {/each}
          </div>
        {/if}
      </div>
    </section>

    <section class="section">
      <div class="section-heading">
        <span class="section-label">Primary surfaces</span>
        <span class="section-note">{hasAttention ? 'Alert surfaces are promoted. Calm surfaces are compacted below.' : 'These are the first places to look when the HUD is asking for attention.'}</span>
      </div>
      <div class="focus-grid">
        {#each sortedPrimaryCards as card (card.route)}
          {#if card.alert}
            <button
              class="focus-card focus-card-alert"
              onclick={() => navigate(card.route)}
            >
              <div class="card-head">
                <span class="card-title">{card.label}</span>
                <span class="card-cta">Open</span>
              </div>
              <div class="card-value">{card.value}</div>
              <div class="card-detail">{card.detail}</div>
              <div class="card-tags">
                {#each card.tags as tag (tag.label)}
                  <span class="card-tag" class:tag-on={tag.active} class:tag-off={!tag.active}>
                    {tag.label} · {tag.note}
                  </span>
                {/each}
              </div>
              <div class="card-foot">{card.foot}</div>
            </button>
          {:else}
            <button
              class="focus-card focus-card-calm"
              onclick={() => navigate(card.route)}
            >
              <div class="card-head">
                <span class="card-title">{card.label}</span>
                <span class="card-value-inline">{card.value}</span>
                <span class="card-cta">Open</span>
              </div>
              {#if !hasAttention}
                <div class="card-detail">{card.detail}</div>
                <div class="card-tags">
                  {#each card.tags as tag (tag.label)}
                    <span class="card-tag" class:tag-on={tag.active} class:tag-off={!tag.active}>
                      {tag.label} · {tag.note}
                    </span>
                  {/each}
                </div>
              {/if}
            </button>
          {/if}
        {/each}
      </div>
    </section>

    <section class="section support-section">
      <button class="section-heading section-toggle" onclick={() => { supportExpanded = !supportExpanded; }}>
        <span class="section-label">Supporting surfaces</span>
        {#if !supportExpanded}
          <span class="section-collapsed-summary">
            {supportSurfaces.map((s) => `${s.label}: ${s.value}`).join(' · ')}
          </span>
        {/if}
        <span class="section-chevron" class:section-chevron-open={supportExpanded}>{'\u25B8'}</span>
      </button>
      {#if supportExpanded || !hasAttention}
        <div class="support-grid">
          {#each supportSurfaces as surface (surface.route)}
            <button class="support-card" onclick={() => navigate(surface.route)}>
              <span class="support-label">{surface.label}</span>
              <strong>{surface.value}</strong>
              <small>{surface.detail}</small>
            </button>
          {/each}
        </div>
      {/if}
    </section>
  {/if}
</div>

<style>
  .overview-panel {
    display: flex;
    flex-direction: column;
    padding: 16px;
    gap: 16px;
  }

  .overview-hero {
    display: grid;
    grid-template-columns: minmax(0, 1.6fr) minmax(280px, 0.9fr);
    gap: 14px;
    padding: 18px;
    border-radius: var(--radius-lg);
    border: 1px solid color-mix(in srgb, var(--border) 72%, var(--accent) 28%);
    background:
      radial-gradient(circle at top left, color-mix(in srgb, var(--accent) 18%, transparent), transparent 42%),
      linear-gradient(180deg, color-mix(in srgb, var(--bg-secondary) 88%, var(--accent) 12%), var(--bg-secondary));
    box-shadow: var(--shadow-md);
  }

  .hero-alert {
    border-color: rgba(231, 179, 18, 0.35);
  }

  .hero-copy {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .hero-eyebrow,
  .rail-label,
  .section-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-secondary);
    font-weight: 700;
  }

  .hero-title {
    margin: 0;
    font-size: clamp(24px, 2.6vw, 34px);
    line-height: 1.05;
    color: var(--fg-primary);
    letter-spacing: -0.02em;
  }

  .hero-detail {
    margin: 0;
    max-width: 62ch;
    font-size: 14px;
    line-height: 1.5;
    color: var(--fg-muted);
  }

  .hero-note {
    font-size: 11px;
    font-family: var(--font-mono);
    color: var(--fg-secondary);
  }

  .hero-action {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 8px 16px;
    border-radius: var(--radius-sm);
    border: 1px solid color-mix(in srgb, var(--accent) 60%, var(--border) 40%);
    background: color-mix(in srgb, var(--accent) 14%, transparent);
    color: var(--accent);
    font-size: 12px;
    font-weight: 600;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .hero-action:hover {
    background: color-mix(in srgb, var(--accent) 22%, transparent);
    border-color: var(--accent);
  }

  .hero-signals {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
    max-width: 560px;
  }

  .signal-chip {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    align-items: baseline;
    padding: 10px 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: color-mix(in srgb, var(--bg-primary) 55%, transparent);
    font-family: var(--font-mono);
  }

  .signal-chip span {
    font-size: 10px;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--fg-muted);
  }

  .signal-chip strong {
    font-size: 14px;
    color: var(--fg-primary);
  }

  .hero-rail {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 12px;
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--bg-primary) 42%, transparent);
    border: 1px solid color-mix(in srgb, var(--border) 72%, var(--accent) 28%);
  }

  .rail-title {
    font-size: 16px;
    font-weight: 700;
    color: var(--fg-primary);
    line-height: 1.2;
  }

  .rail-stack {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .rail-quicklinks {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    padding-top: 2px;
  }

  .rail-item {
    display: flex;
    flex-direction: column;
    gap: 3px;
    text-align: left;
    padding: 10px 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    cursor: pointer;
    transition: border-color var(--transition-normal),
                transform var(--transition-normal),
                box-shadow var(--transition-normal);
  }

  .rail-item:hover {
    border-color: rgba(233, 93, 116, 0.3);
    transform: translateY(-1px);
    box-shadow: 0 0 12px var(--glow-accent), var(--shadow-sm);
  }

  .rail-item strong {
    font-size: 14px;
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .rail-item small {
    font-size: 11px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .rail-item-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .rail-item-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-secondary);
    font-weight: 700;
  }

  .rail-item-action {
    font-size: 9px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--accent);
    padding: 1px 6px;
    border: 1px solid color-mix(in srgb, var(--accent) 40%, transparent);
    border-radius: 999px;
    background: color-mix(in srgb, var(--accent) 10%, transparent);
  }

  .quicklink-chip {
    display: inline-flex;
    align-items: baseline;
    gap: 8px;
    padding: 6px 10px;
    border-radius: 999px;
    border: 1px solid var(--border);
    background: color-mix(in srgb, var(--bg-primary) 50%, transparent);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    font-size: 10px;
    cursor: pointer;
  }

  .quicklink-chip strong {
    color: var(--fg-primary);
    font-size: 11px;
  }

  .section {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .section-heading {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 12px;
  }

  .section-note {
    font-size: 11px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .section-toggle {
    cursor: pointer;
    background: none;
    border: none;
    padding: 0;
    width: 100%;
    text-align: left;
  }

  .section-toggle:hover .section-label {
    color: var(--fg-primary);
  }

  .section-collapsed-summary {
    font-size: 10px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    margin-left: 8px;
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .section-chevron {
    font-size: 10px;
    color: var(--fg-muted);
    transition: transform var(--transition-fast);
    flex-shrink: 0;
  }

  .section-chevron-open {
    transform: rotate(90deg);
  }

  .focus-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
    gap: 12px;
  }

  .focus-card {
    display: flex;
    flex-direction: column;
    gap: 8px;
    text-align: left;
    padding: 14px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    cursor: pointer;
    transition: border-color var(--transition-normal),
                transform var(--transition-normal),
                box-shadow var(--transition-normal);
  }

  .focus-card:hover {
    border-color: rgba(233, 93, 116, 0.3);
    transform: translateY(-2px);
    box-shadow: 0 0 12px var(--glow-accent), var(--shadow-md);
  }

  .focus-card-alert {
    border-color: rgba(231, 179, 18, 0.35);
    background: linear-gradient(180deg, color-mix(in srgb, var(--bg-secondary) 88%, var(--warning) 12%), var(--bg-secondary));
  }

  .focus-card-calm {
    opacity: 0.82;
    padding: 10px 14px;
  }

  .focus-card-calm:hover {
    opacity: 1;
  }

  .card-value-inline {
    font-size: 13px;
    font-weight: 600;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    margin-left: auto;
    margin-right: 8px;
  }

  .card-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  .card-title {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-secondary);
    font-weight: 700;
  }

  .card-cta {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--accent);
    font-weight: 700;
  }

  .card-value {
    font-size: 22px;
    font-weight: 700;
    font-family: var(--font-mono);
    font-feature-settings: 'tnum';
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .card-detail,
  .card-foot,
  .support-card small {
    font-size: 11px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
    line-height: 1.4;
  }

  .card-tags {
    display: flex;
    gap: 6px;
    flex-wrap: wrap;
  }

  .card-tag {
    font-size: 9px;
    font-family: var(--font-mono);
    padding: 2px 6px;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--fg-muted);
    background: color-mix(in srgb, var(--bg-primary) 45%, transparent);
  }

  .tag-on {
    color: var(--fg-primary);
    border-color: color-mix(in srgb, var(--accent) 50%, var(--border) 50%);
    background: color-mix(in srgb, var(--accent) 14%, transparent);
  }

  .tag-off {
    opacity: 0.72;
  }

  .support-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
    gap: 10px;
  }

  .support-card {
    display: flex;
    flex-direction: column;
    gap: 4px;
    text-align: left;
    padding: 12px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    cursor: pointer;
    transition: border-color var(--transition-normal),
                transform var(--transition-normal),
                box-shadow var(--transition-normal);
  }

  .support-card:hover {
    border-color: rgba(233, 93, 116, 0.3);
    transform: translateY(-1px);
    box-shadow: 0 0 10px var(--glow-accent), var(--shadow-sm);
  }

  .support-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--fg-secondary);
    font-weight: 700;
  }

  .support-card strong {
    font-size: 14px;
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }

  .hero-skeleton,
  .focus-card-skeleton {
    cursor: default;
  }

  .signal-chip-skeleton,
  .rail-item-skeleton {
    min-height: 42px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
  }

  .focus-card-skeleton {
    min-height: 176px;
  }

  @media (max-width: 840px) {
    .overview-hero {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 600px) {
    .hero-signals {
      grid-template-columns: 1fr;
    }

    .section-heading {
      align-items: flex-start;
      flex-direction: column;
    }
  }
</style>
