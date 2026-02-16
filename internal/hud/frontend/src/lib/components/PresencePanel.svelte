<script>
  import { presenceStore } from '../stores/presence.svelte.ts';
  import { timelineStore } from '../stores/timeline.svelte.ts';
  import { toastStore } from '../stores/toasts.svelte.ts';
  import { formatTime, relativeTime, agentColor, truncatePath } from '../utils/format.ts';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';
  import Modal from '../widgets/Modal.svelte';
  import AgentCard from '../widgets/AgentCard.svelte';
  import DataTable from './shared/DataTable.svelte';
  import BulkToolbar from './shared/BulkToolbar.svelte';
  import EmptyState from './shared/EmptyState.svelte';

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

  // --- Handoffs state ---
  let handoffs = $state([]);
  let templates = $state([]);
  let handoffLoading = $state(false);
  let handoffError = $state('');
  let showHandoffModal = $state(false);
  let newHandoffTo = $state('');
  let newHandoffSummary = $state('');
  let newHandoffContext = $state('');
  let creatingHandoff = $state(false);

  async function fetchHandoffs() {
    handoffLoading = true;
    handoffError = '';
    try {
      const [hRes, tRes] = await Promise.all([
        globalThis.fetch('/api/handoffs'),
        globalThis.fetch('/api/templates'),
      ]);
      if (hRes.ok) {
        const hData = await hRes.json();
        handoffs = hData.handoffs ?? [];
      }
      if (tRes.ok) {
        const tData = await tRes.json();
        templates = tData.templates ?? [];
      }
    } catch (e) {
      handoffError = e instanceof Error ? e.message : 'Failed to load';
    } finally {
      handoffLoading = false;
    }
  }

  // Fetch handoffs when tab is shown
  $effect(() => {
    if (activeTab === 'handoffs') {
      fetchHandoffs();
    }
  });

  async function submitHandoff() {
    if (!newHandoffSummary.trim()) return;
    creatingHandoff = true;
    try {
      const res = await globalThis.fetch('/api/handoffs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          to_agent: newHandoffTo.trim() || undefined,
          summary: newHandoffSummary.trim(),
          context: newHandoffContext.trim() || undefined,
        }),
      });
      if (!res.ok) throw new Error(`Create handoff: ${res.status}`);
      toastStore.success('Handoff created');
      showHandoffModal = false;
      newHandoffTo = '';
      newHandoffSummary = '';
      newHandoffContext = '';
      await fetchHandoffs();
    } catch (e) {
      toastStore.error('Failed to create handoff');
    } finally {
      creatingHandoff = false;
    }
  }

  async function acceptHandoff(id) {
    try {
      const res = await globalThis.fetch(`/api/handoffs/${id}/accept`, { method: 'POST' });
      if (!res.ok) throw new Error(`Accept: ${res.status}`);
      toastStore.success('Handoff accepted');
      await fetchHandoffs();
    } catch (e) {
      toastStore.error('Failed to accept handoff');
    }
  }

  // --- Dispatch task ---
  let showDispatchModal = $state(false);
  let dispatchTargetAgent = $state('');
  let dispatchTitle = $state('');
  let dispatchContext = $state('');
  let dispatchPriority = $state('medium');
  let dispatchSubmitting = $state(false);

  async function submitDispatch() {
    if (!dispatchTargetAgent || !dispatchTitle.trim()) return;
    dispatchSubmitting = true;
    try {
      const res = await globalThis.fetch('/api/agent/dispatch', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          target_agent_id: dispatchTargetAgent,
          title: dispatchTitle.trim(),
          context: dispatchContext.trim() || undefined,
          priority: dispatchPriority,
        }),
      });
      if (!res.ok) throw new Error(`Dispatch: ${res.status}`);
      toastStore.success(`Task dispatched to ${dispatchTargetAgent}`);
      showDispatchModal = false;
      dispatchTitle = '';
      dispatchContext = '';
      dispatchPriority = 'medium';
    } catch (e) {
      toastStore.error('Failed to dispatch task');
    } finally {
      dispatchSubmitting = false;
    }
  }

  function openDispatch(agentId) {
    dispatchTargetAgent = agentId;
    showDispatchModal = true;
  }

  // --- Nudge agent ---
  let showNudgeModal = $state(false);
  let nudgeTargetAgent = $state('');
  let nudgeType = $state('message');
  let nudgeContent = $state('');
  let nudgeSubmitting = $state(false);

  function openNudge(agentId) {
    nudgeTargetAgent = agentId;
    nudgeType = 'message';
    nudgeContent = '';
    showNudgeModal = true;
  }

  async function submitNudge() {
    if (!nudgeTargetAgent || !nudgeContent.trim()) return;
    nudgeSubmitting = true;
    try {
      const res = await globalThis.fetch('/api/agent/nudge', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          target_agent_id: nudgeTargetAgent,
          type: nudgeType,
          content: nudgeContent.trim(),
          from_agent: 'hud',
        }),
      });
      if (!res.ok) throw new Error(`Nudge: ${res.status}`);
      toastStore.success(`Nudge sent to ${nudgeTargetAgent}`);
      showNudgeModal = false;
    } catch (e) {
      toastStore.error('Failed to send nudge');
    } finally {
      nudgeSubmitting = false;
    }
  }

  // --- Release claim ---
  async function releaseClaim(agentId, filePath) {
    try {
      const res = await globalThis.fetch(`/api/claims/${encodeURIComponent(agentId)}/${encodeURIComponent(filePath)}`, {
        method: 'DELETE',
      });
      if (!res.ok) throw new Error(`Release: ${res.status}`);
      toastStore.success('Claim released');
      presenceStore.fetch();
    } catch (e) {
      toastStore.error('Failed to release claim');
    }
  }

  // --- Bulk claim selection ---
  let selectedClaimIds = $state(new Set());

  function handleClaimSelect(ids) {
    selectedClaimIds = ids;
  }

  async function bulkReleaseClaims() {
    for (const id of selectedClaimIds) {
      const claim = claims.find((c) => c.id === id);
      if (claim) {
        await releaseClaim(claim.agent_id, claim.file_path);
      }
    }
    toastStore.success(`${selectedClaimIds.size} claims released`);
    selectedClaimIds = new Set();
  }

  let claimBulkActions = $derived([
    { label: 'Release Selected', variant: 'danger', onclick: bulkReleaseClaims },
  ]);

  // --- Relative time tick (forces re-render) ---
  let _tick = $state(0);
  $effect(() => {
    const t = setInterval(() => { _tick++ }, 5000);
    return () => clearInterval(t);
  });
  // Force dependency on _tick for relativeTime reactivity
  function reactiveRelativeTime(ts) {
    void _tick;
    return relativeTime(ts);
  }

  // --- Branch collision detection ---
  let branchCollisions = $derived.by(() => {
    const branchAgents = {};
    agents.filter(a => a.status === 'active' && a.branch).forEach(a => {
      if (!branchAgents[a.branch]) branchAgents[a.branch] = [];
      branchAgents[a.branch].push(a.agent_id);
    });
    return Object.entries(branchAgents)
      .filter(([, agents]) => agents.length > 1)
      .map(([branch, agents]) => ({ branch, agents }));
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


  // --- View toggle: table vs cards ---
  let agentView = $state('cards');

  // Start timeline polling for heartbeat data in card view.
  $effect(() => {
    if (agentView === 'cards') {
      timelineStore.startPolling(30000);
      return () => timelineStore.stopPolling();
    }
  });

  // Compute agent file overlaps: Map<agent_id, string[]> of agents sharing files.
  let agentOverlaps = $derived.by(() => {
    const fileCounts = {};
    claims.forEach(c => {
      if (!fileCounts[c.file_path]) fileCounts[c.file_path] = [];
      fileCounts[c.file_path].push(c.agent_id);
    });
    const result = new Map();
    for (const [, claimAgents] of Object.entries(fileCounts)) {
      if (claimAgents.length < 2) continue;
      for (const a of claimAgents) {
        const others = claimAgents.filter(x => x !== a);
        const existing = result.get(a) ?? [];
        for (const o of others) {
          if (!existing.includes(o)) existing.push(o);
        }
        result.set(a, existing);
      }
    }
    return result;
  });

  // Compute heartbeat frequency data per agent from timeline entries (12 buckets of 5min = 60min).
  let heartbeatDataMap = $derived.by(() => {
    const now = Date.now();
    const bucketSize = 5 * 60_000;
    const bucketCount = 12;
    const result = new Map();
    const entries = timelineStore.entries ?? [];

    for (const agent of agents) {
      const buckets = new Array(bucketCount).fill(0);
      for (const e of entries) {
        if (e.agent_id !== agent.agent_id) continue;
        if (e.event_type !== 'agent.heartbeat') continue;
        const ts = new Date(e.timestamp).getTime();
        const age = now - ts;
        const idx = bucketCount - 1 - Math.floor(age / bucketSize);
        if (idx >= 0 && idx < bucketCount) buckets[idx]++;
      }
      result.set(agent.agent_id, buckets);
    }
    return result;
  });

  function presenceStatus(status) {
    const map = {
      active: 'healthy',
      idle: 'degraded',
      offline: 'down',
    };
    return map[status] ?? 'down';
  }

  function claimVariant(type) {
    const map = {
      edit: 'warning',
      review: 'info',
      reserve: 'accent',
    };
    return map[type] ?? 'info';
  }

  function worktreeVariant(status) {
    const map = {
      active: 'success',
      released: 'info',
      orphaned: 'error',
    };
    return map[status] ?? 'info';
  }

  function handoffStatusVariant(status) {
    const map = {
      pending: 'warning',
      accepted: 'success',
      expired: 'error',
    };
    return map[status] ?? 'info';
  }

  function shortPath(path) {
    return truncatePath(path, 50);
  }

  // --- Diagnostics (context + queue) ---
  let diagnosticsAgentId = $state('');
  let diagnosticsLoading = $state(false);
  let diagnosticsError = $state('');
  let contextInspect = $state(null);
  let contextInspectError = $state('');
  let nudgeQueueStatus = $state(null);
  let nudgeQueueStatusError = $state('');
  let nudgeQueuePolicy = $state(null);
  let nudgeQueuePolicyError = $state('');
  let nudgePolicyCapInput = $state('');
  let nudgePolicyDebounceInput = $state('');
  let nudgePolicyDropPolicy = $state('drop_old');
  let nudgePolicyLanePriorityInput = $state('');
  let nudgePolicyUpdatedBy = $state('hud-ui');
  let nudgePolicyAdminToken = $state('');
  let nudgePolicyUpdating = $state(false);
  let nudgePolicyMutationError = $state('');
  let nudgePolicyFormDirty = $state(false);
  const nudgeDropPolicyOptions = ['drop_old', 'drop_new', 'summarize'];

  function parseLanePriorityInput(raw) {
    return raw
      .split(',')
      .map((lane) => lane.trim())
      .filter((lane) => lane.length > 0);
  }

  function markNudgePolicyDirty() {
    nudgePolicyFormDirty = true;
    nudgePolicyMutationError = '';
  }

  function hydrateNudgePolicyForm(policy) {
    if (!policy || nudgePolicyFormDirty || nudgePolicyUpdating) return;
    nudgePolicyCapInput = String(policy.cap ?? '');
    nudgePolicyDebounceInput = String(policy.debounce_ms ?? 0);
    nudgePolicyDropPolicy = nudgeDropPolicyOptions.includes(policy.drop_policy) ? policy.drop_policy : 'drop_old';
    nudgePolicyLanePriorityInput = (policy.lane_priority ?? []).join(', ');
    nudgePolicyMutationError = '';
    nudgePolicyFormDirty = false;
  }

  function resetNudgePolicyForm() {
    const source = nudgeQueuePolicy ?? nudgeQueueStatus;
    if (!source) return;
    nudgePolicyFormDirty = false;
    hydrateNudgePolicyForm(source);
  }

  async function updateNudgePolicy() {
    if (nudgePolicyUpdating) return;

    const token = nudgePolicyAdminToken.trim();
    if (!token) {
      nudgePolicyMutationError = 'Admin token is required to update policy.';
      return;
    }

    const cap = Number.parseInt(nudgePolicyCapInput.trim(), 10);
    if (!Number.isInteger(cap) || cap <= 0) {
      nudgePolicyMutationError = 'Cap must be a positive integer.';
      return;
    }

    const debounceMs = Number.parseInt(nudgePolicyDebounceInput.trim(), 10);
    if (!Number.isInteger(debounceMs) || debounceMs < 0) {
      nudgePolicyMutationError = 'Debounce must be a non-negative integer (ms).';
      return;
    }

    const dropPolicy = nudgePolicyDropPolicy.trim();
    if (!nudgeDropPolicyOptions.includes(dropPolicy)) {
      nudgePolicyMutationError = 'Drop policy must be drop_old, drop_new, or summarize.';
      return;
    }

    const lanePriority = parseLanePriorityInput(nudgePolicyLanePriorityInput);
    if (lanePriority.length === 0) {
      nudgePolicyMutationError = 'Lane priority must include at least one lane.';
      return;
    }

    const currentPolicy = nudgeQueuePolicy ?? nudgeQueueStatus;
    if (
      currentPolicy &&
      currentPolicy.cap === cap &&
      currentPolicy.debounce_ms === debounceMs &&
      currentPolicy.drop_policy === dropPolicy &&
      JSON.stringify(currentPolicy.lane_priority ?? []) === JSON.stringify(lanePriority)
    ) {
      nudgePolicyMutationError = '';
      nudgePolicyFormDirty = false;
      toastStore.info('Nudge queue policy is already up to date');
      return;
    }

    nudgePolicyMutationError = '';
    nudgePolicyUpdating = true;
    try {
      const res = await globalThis.fetch('/api/agent/nudge-queue-policy', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Admin-Token': token,
        },
        body: JSON.stringify({
          cap,
          debounce_ms: debounceMs,
          drop_policy: dropPolicy,
          lane_priority: lanePriority,
          updated_by: nudgePolicyUpdatedBy.trim() || 'hud-ui',
        }),
      });
      let data = null;
      try {
        data = await res.json();
      } catch {
        data = null;
      }
      if (!res.ok) {
        throw new Error(data?.error || `${res.status} ${res.statusText}`);
      }
      nudgeQueuePolicy = data?.policy ?? null;
      nudgePolicyFormDirty = false;
      hydrateNudgePolicyForm(nudgeQueuePolicy);
      toastStore.success('Nudge queue policy updated');
      await fetchDiagnostics();
    } catch (e) {
      nudgePolicyMutationError = e instanceof Error ? e.message : 'Failed to update policy';
      toastStore.error(nudgePolicyMutationError);
    } finally {
      nudgePolicyUpdating = false;
    }
  }

  async function fetchJSON(url) {
    const res = await globalThis.fetch(url);
    let data = null;
    try {
      data = await res.json();
    } catch {
      data = null;
    }
    if (!res.ok) {
      const msg = data?.error || `${res.status} ${res.statusText}`;
      throw new Error(msg);
    }
    return data;
  }

  async function fetchDiagnostics() {
    const agentID = diagnosticsAgentId?.trim();
    if (!agentID) return;

    diagnosticsLoading = true;
    diagnosticsError = '';
    contextInspectError = '';
    nudgeQueueStatusError = '';
    nudgeQueuePolicyError = '';

    const [ctxResult, queueResult, policyResult] = await Promise.allSettled([
      fetchJSON(`/api/agent/context-inspect?agent_id=${encodeURIComponent(agentID)}&detail=true&limit=200`),
      fetchJSON(`/api/agent/nudge-queue?agent_id=${encodeURIComponent(agentID)}`),
      fetchJSON('/api/agent/nudge-queue-policy'),
    ]);

    if (ctxResult.status === 'fulfilled') {
      contextInspect = ctxResult.value ?? null;
    } else {
      contextInspect = null;
      contextInspectError = ctxResult.reason?.message ?? 'Failed to load context diagnostics';
    }

    if (queueResult.status === 'fulfilled') {
      nudgeQueueStatus = queueResult.value?.status ?? null;
    } else {
      nudgeQueueStatus = null;
      nudgeQueueStatusError = queueResult.reason?.message ?? 'Failed to load queue status';
    }

    if (policyResult.status === 'fulfilled') {
      nudgeQueuePolicy = policyResult.value?.policy ?? null;
    } else {
      nudgeQueuePolicy = null;
      nudgeQueuePolicyError = policyResult.reason?.message ?? 'Failed to load queue policy';
    }

    if (!nudgePolicyFormDirty && !nudgePolicyUpdating) {
      hydrateNudgePolicyForm(nudgeQueuePolicy ?? nudgeQueueStatus);
    }

    if (contextInspectError && nudgeQueueStatusError && nudgeQueuePolicyError) {
      diagnosticsError = 'Unable to load diagnostics from HUD API.';
    }

    diagnosticsLoading = false;
  }

  $effect(() => {
    if (activeTab !== 'diagnostics') return;
    if (!diagnosticsAgentId) {
      diagnosticsAgentId = agents.find((a) => a.status === 'active')?.agent_id || agents[0]?.agent_id || '';
    }
    if (!diagnosticsAgentId) return;
    fetchDiagnostics();
    const timer = setInterval(() => { fetchDiagnostics(); }, 10000);
    return () => clearInterval(timer);
  });

  // --- DataTable column definitions ---

  const agentColumns = [
    { key: 'agent_id', label: 'Agent' },
    { key: 'status', label: 'Status', width: '100px' },
    { key: 'agent_type', label: 'Type', width: '90px' },
    { key: 'current_task', label: 'Current Task' },
    { key: 'branch', label: 'Branch / PR', width: '120px' },
    { key: 'last_heartbeat', label: 'Heartbeat', width: '90px' },
    { key: 'actions', label: 'Actions', width: '120px' },
  ];

  const claimColumns = [
    { key: 'file_path', label: 'File' },
    { key: 'agent_id', label: 'Agent', width: '100px' },
    { key: 'claim_type', label: 'Type', width: '80px' },
    { key: 'reason', label: 'Reason' },
    { key: 'created_at', label: 'Since', width: '90px' },
    { key: 'actions', label: 'Actions', width: '80px' },
  ];

  const worktreeColumns = [
    { key: 'branch', label: 'Branch' },
    { key: 'agent_id', label: 'Agent', width: '100px' },
    { key: 'status', label: 'Status', width: '90px' },
    { key: 'purpose', label: 'Purpose' },
    { key: 'created_at', label: 'Created', width: '90px' },
  ];

  const handoffColumns = [
    { key: 'from_agent', label: 'From', width: '100px' },
    { key: 'to_agent', label: 'To', width: '100px' },
    { key: 'summary', label: 'Summary' },
    { key: 'status', label: 'Status', width: '90px' },
    { key: 'created_at', label: 'Created', width: '90px' },
    { key: 'actions', label: 'Actions', width: '80px' },
  ];
</script>

<div class="panel presence-panel">
  <!-- Tab bar -->
  <div class="tab-bar">
    <button class="tab-btn" class:active={activeTab === 'agents'} onclick={() => { activeTab = 'agents'; }}>
      Agents <span class="tab-count">{agents.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'claims'} onclick={() => { activeTab = 'claims'; }}>
      Claims <span class="tab-count">{claims.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'worktrees'} onclick={() => { activeTab = 'worktrees'; }}>
      Worktrees <span class="tab-count">{worktrees.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'handoffs'} onclick={() => { activeTab = 'handoffs'; }}>
      Handoffs <span class="tab-count">{handoffs.length}</span>
    </button>
    <button class="tab-btn" class:active={activeTab === 'diagnostics'} onclick={() => { activeTab = 'diagnostics'; }}>
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
      <!-- Agent Presence -->
      {#if agentView === 'cards'}
        <!-- Card View -->
        {#if branchCollisions.length > 0}
          <div class="conflict-banner">
            <span class="conflict-icon">⚠</span>
            <span>Branch collision: multiple agents on same branch</span>
            {#each branchCollisions as col}
              <div class="conflict-detail">
                <span class="text-mono text-xs">{col.branch}</span>
                <span class="text-muted text-xs">→ {col.agents.join(', ')}</span>
              </div>
            {/each}
          </div>
        {/if}
        <div class="cards-grid">
          {#each agents as agent (agent.agent_id)}
            <AgentCard
              {agent}
              heartbeatData={heartbeatDataMap.get(agent.agent_id) ?? []}
              sharedFileAgents={agentOverlaps.get(agent.agent_id) ?? []}
              ondispatch={openDispatch}
              onnudge={openNudge}
            />
          {:else}
            <EmptyState icon={'\u25A3'} heading="No registered agents" compact />
          {/each}
        </div>
      {:else}
      <!-- Table View -->
      <div class="presence-grid">
        <div class="card agents-card">
          <div class="card-header">
            <span class="card-title">Agent Presence</span>
            <span class="count-badge">{presenceStore.activeCount + presenceStore.idleCount}</span>
          </div>
          {#if branchCollisions.length > 0}
            <div class="conflict-banner">
              <span class="conflict-icon">⚠</span>
              <span>Branch collision: multiple agents on same branch</span>
              {#each branchCollisions as col}
                <div class="conflict-detail">
                  <span class="text-mono text-xs">{col.branch}</span>
                  <span class="text-muted text-xs">→ {col.agents.join(', ')}</span>
                </div>
              {/each}
            </div>
          {/if}
          {#if agents.length === 0}
            <EmptyState icon={'\u25A3'} heading="No registered agents" compact />
          {:else}
            <DataTable
              columns={agentColumns}
              rows={agents}
              idKey="agent_id"
            >
              {#snippet row({ row: agent })}
                <td class="text-mono">{agent.agent_id}</td>
                <td>
                  <StatusDot status={presenceStatus(agent.status)} />
                  <span class="status-label">{agent.status}</span>
                </td>
                <td>
                  <span class="agent-type-chip" style:color={agentColor(agent.agent_type)}>
                    {agent.agent_type || '---'}
                  </span>
                </td>
                <td class="truncate" title={agent.current_task}>{agent.current_task || '---'}</td>
                <td class="text-mono text-muted">
                  {#if agent.pr_url}
                    <a href={agent.pr_url} target="_blank" rel="noopener" class="pr-link" title={agent.pr_url}>
                      PR
                    </a>
                  {/if}
                  {agent.branch || '---'}
                </td>
                <td class="text-mono text-muted" title={formatTime(agent.last_heartbeat)}>{reactiveRelativeTime(agent.last_heartbeat)}</td>
                <td class="actions-cell">
                  {#if agent.status === 'active'}
                    <button class="btn btn-xs btn-nudge" onclick={() => openNudge(agent.agent_id)} title="Send nudge to agent">
                      Nudge
                    </button>
                    <button class="btn btn-xs btn-dispatch" onclick={() => openDispatch(agent.agent_id)} title="Dispatch task to agent">
                      Dispatch
                    </button>
                  {/if}
                </td>
              {/snippet}
            </DataTable>
          {/if}
        </div>

        <!-- Quick Stats -->
        <div class="stats-grid">
          <div class="stat-card" style="--accent-color: var(--success)">
            <div class="metric-value">{presenceStore.activeCount}</div>
            <div class="metric-label">Active</div>
          </div>
          <div class="stat-card" style="--accent-color: var(--warning)">
            <div class="metric-value">{presenceStore.idleCount}</div>
            <div class="metric-label">Idle</div>
          </div>
          <div class="stat-card" style="--accent-color: var(--error)">
            <div class="metric-value">{presenceStore.offlineCount}</div>
            <div class="metric-label">Offline</div>
          </div>
          <div class="stat-card" style="--accent-color: var(--info)">
            <div class="metric-value">{claims.length}</div>
            <div class="metric-label">File Claims</div>
          </div>
          <div class="stat-card" style="--accent-color: var(--accent)">
            <div class="metric-value">{worktrees.length}</div>
            <div class="metric-label">Worktrees</div>
          </div>
          <div class="stat-card" style="--accent-color: var(--tier-short)">
            <div class="metric-value">{presenceStore.claimedFiles.length}</div>
            <div class="metric-label">Claimed Files</div>
          </div>
        </div>
      </div>
      {/if}

    {:else if activeTab === 'claims'}
      <!-- File Claims -->
      <div class="card">
        <div class="card-header">
          <span class="card-title">File Claims</span>
          <span class="count-badge">{claims.length}</span>
        </div>

        {#if fileConflicts.length > 0}
          <div class="conflict-banner">
            <span class="conflict-icon">⚠</span>
            <span>{fileConflicts.length} file(s) claimed by multiple agents:</span>
            {#each fileConflicts as conflict}
              <div class="conflict-detail">
                <span class="text-mono text-xs">{shortPath(conflict.path)}</span>
                <span class="text-muted text-xs">→ {conflict.agents.join(', ')}</span>
              </div>
            {/each}
          </div>
        {/if}

        {#if claims.length === 0}
          <EmptyState icon={'\u{1F4C1}'} heading="No active file claims" compact />
        {:else}
          <DataTable
            columns={claimColumns}
            rows={claims}
            selectable={true}
            selectedIds={selectedClaimIds}
            onSelect={handleClaimSelect}
          >
            {#snippet row({ row: claim })}
              <td class="text-mono" title={claim.file_path}>{shortPath(claim.file_path)}</td>
              <td class="text-mono">{claim.agent_id}</td>
              <td><Badge text={claim.claim_type} variant={claimVariant(claim.claim_type)} /></td>
              <td class="truncate text-muted" title={claim.reason}>{claim.reason || '---'}</td>
              <td class="text-mono text-muted">{formatTime(claim.created_at)}</td>
              <td>
                <button class="btn btn-xs btn-danger" onclick={() => releaseClaim(claim.agent_id, claim.file_path)} title="Force-release this claim">
                  Release
                </button>
              </td>
            {/snippet}
          </DataTable>
          <BulkToolbar
            count={selectedClaimIds.size}
            actions={claimBulkActions}
            onClearSelection={() => { selectedClaimIds = new Set(); }}
          />
        {/if}
      </div>

    {:else if activeTab === 'worktrees'}
      <!-- Worktrees -->
      <div class="card">
        <div class="card-header">
          <span class="card-title">Git Worktrees</span>
          <span class="count-badge">{worktrees.length}</span>
        </div>
        {#if worktrees.length === 0}
          <EmptyState icon={'\u{1F333}'} heading="No active worktrees" compact />
        {:else}
          <DataTable
            columns={worktreeColumns}
            rows={worktrees}
            idKey="assignment_id"
          >
            {#snippet row({ row: wt })}
              <td class="text-mono">{wt.branch}</td>
              <td class="text-mono">{wt.agent_id}</td>
              <td><Badge text={wt.status} variant={worktreeVariant(wt.status)} /></td>
              <td class="truncate text-muted" title={wt.purpose}>{wt.purpose || '---'}</td>
              <td class="text-mono text-muted">{formatTime(wt.created_at)}</td>
            {/snippet}
          </DataTable>
        {/if}
      </div>

    {:else if activeTab === 'handoffs'}
      <!-- Handoffs -->
      <div class="card">
        <div class="card-header">
          <span class="card-title">Agent Handoffs</span>
          <span class="count-badge">{handoffs.length}</span>
          <div class="card-actions">
            <button class="btn btn-sm" onclick={() => { showHandoffModal = true; }}>+ Handoff</button>
          </div>
        </div>

        {#if handoffLoading}
          <div class="loading-bar"><div class="loading-bar-inner"></div></div>
        {/if}

        {#if handoffError}
          <div class="text-xs text-muted" style="padding: 4px 12px;">Failed to load handoffs</div>
        {/if}

        {#if handoffs.length === 0 && !handoffLoading}
          <EmptyState icon={'\u{1F91D}'} heading="No handoffs" compact />
        {:else if handoffs.length > 0}
          <DataTable
            columns={handoffColumns}
            rows={handoffs}
          >
            {#snippet row({ row: handoff })}
              <td class="text-mono">{handoff.from_agent || '---'}</td>
              <td class="text-mono">{handoff.to_agent || 'any'}</td>
              <td class="truncate" title={handoff.summary}>{handoff.summary}</td>
              <td><Badge text={handoff.status} variant={handoffStatusVariant(handoff.status)} /></td>
              <td class="text-mono text-muted">{formatTime(handoff.created_at)}</td>
              <td>
                {#if handoff.status === 'pending'}
                  <button class="btn btn-xs btn-success" onclick={() => acceptHandoff(handoff.id)}>
                    Accept
                  </button>
                {:else}
                  <span class="text-muted text-xs">{handoff.accepted_at ? formatTime(handoff.accepted_at) : '---'}</span>
                {/if}
              </td>
            {/snippet}
          </DataTable>
        {/if}

        {#if templates.length > 0}
          <div class="templates-section">
            <div class="section-header">
              <span class="section-title">Session Templates</span>
            </div>
            <div class="template-list">
              {#each templates as tpl (tpl.id)}
                <div class="template-chip">
                  <span class="template-name text-mono">{tpl.name}</span>
                  <span class="text-muted text-xs">{tpl.description}</span>
                </div>
              {/each}
            </div>
          </div>
        {/if}
      </div>

    {:else if activeTab === 'diagnostics'}
      <!-- Diagnostics -->
      <div class="card diagnostics-card">
        <div class="card-header diagnostics-header">
          <span class="card-title">Agent Diagnostics</span>
          <div class="diagnostics-controls">
            <label class="sr-only" for="diag-agent">Agent</label>
            <select
              id="diag-agent"
              class="form-input diagnostics-select"
              bind:value={diagnosticsAgentId}
              onchange={() => { fetchDiagnostics(); }}
            >
              <option value="">Select agent...</option>
              {#each agents as agent (agent.agent_id)}
                <option value={agent.agent_id}>{agent.agent_id}</option>
              {/each}
            </select>
            <button class="btn btn-sm" onclick={() => { fetchDiagnostics(); }} disabled={diagnosticsLoading || !diagnosticsAgentId}>
              {diagnosticsLoading ? 'Refreshing...' : 'Refresh'}
            </button>
          </div>
        </div>

        {#if diagnosticsError}
          <div class="text-xs text-muted diagnostics-error">{diagnosticsError}</div>
        {/if}

        {#if !diagnosticsAgentId}
          <EmptyState icon={'\u2699'} heading="Select an agent to inspect diagnostics" compact />
        {:else}
          <div class="diagnostics-metrics">
            <div class="stat-card" style="--accent-color: var(--accent)">
              <div class="metric-value">{contextInspect?.estimated_tokens ?? '---'}</div>
              <div class="metric-label">Prompt Est. Tokens</div>
            </div>
            <div class="stat-card" style="--accent-color: var(--info)">
              <div class="metric-value">{contextInspect?.entry_count ?? '---'}</div>
              <div class="metric-label">Context Entries</div>
            </div>
            <div class="stat-card" style="--accent-color: var(--warning)">
              <div class="metric-value">{nudgeQueueStatus?.pending ?? '---'}</div>
              <div class="metric-label">Queue Pending</div>
            </div>
            <div class="stat-card" style="--accent-color: var(--error)">
              <div class="metric-value">{nudgeQueueStatus?.dropped ?? '---'}</div>
              <div class="metric-label">Queue Dropped</div>
            </div>
          </div>

          <div class="diagnostics-grid">
            <div class="diag-section">
              <div class="section-header">
                <span class="section-title">Context Breakdown</span>
                {#if contextInspect?.session_id}
                  <span class="text-mono text-xs text-muted">{contextInspect.session_id}</span>
                {/if}
              </div>
              {#if contextInspectError}
                <div class="text-xs text-muted">{contextInspectError}</div>
              {:else if !contextInspect}
                <div class="text-xs text-muted">No context diagnostics available.</div>
              {:else}
                <div class="diag-list">
                  {#each (contextInspect.by_entry_type ?? []) as bucket}
                    <div class="diag-row">
                      <span class="text-mono">{bucket.entry_type}</span>
                      <span class="text-xs text-muted">{bucket.count} entries</span>
                      <span class="text-xs text-mono">{bucket.estimated_tokens} tok</span>
                    </div>
                  {/each}
                </div>
                {#if (contextInspect.sections ?? []).length > 0}
                  <div class="diag-subtitle">Prompt Sections</div>
                  <div class="diag-list">
                    {#each (contextInspect.sections ?? []) as section}
                      <div class="diag-row">
                        <span class="text-mono">{section.section}</span>
                        <span class="text-xs text-muted">{section.source}</span>
                        <span class="text-xs text-mono">{section.estimated_tokens} tok</span>
                      </div>
                    {/each}
                  </div>
                {/if}
                {#if (contextInspect.top_entries ?? []).length > 0}
                  <div class="diag-subtitle">Top Entries</div>
                  <div class="diag-list">
                    {#each (contextInspect.top_entries ?? []).slice(0, 5) as entry}
                      <div class="diag-row">
                        <span class="truncate" title={entry.title || entry.id}>{entry.title || entry.id}</span>
                        <span class="text-xs text-muted">{entry.entry_type}</span>
                        <span class="text-xs text-mono">{entry.estimated_tokens} tok</span>
                      </div>
                    {/each}
                  </div>
                {/if}
              {/if}
            </div>

            <div class="diag-section">
              <div class="section-header">
                <span class="section-title">Nudge Queue Policy</span>
              </div>
              {#if nudgeQueuePolicyError && nudgeQueueStatusError}
                <div class="text-xs text-muted">{nudgeQueuePolicyError}</div>
              {:else}
                <div class="diag-kv">
                  <div><span class="text-muted text-xs">Cap</span><span class="text-mono">{nudgeQueuePolicy?.cap ?? nudgeQueueStatus?.cap ?? '---'}</span></div>
                  <div><span class="text-muted text-xs">Drop</span><span class="text-mono">{nudgeQueuePolicy?.drop_policy ?? nudgeQueueStatus?.drop_policy ?? '---'}</span></div>
                  <div><span class="text-muted text-xs">Debounce</span><span class="text-mono">{nudgeQueuePolicy?.debounce_ms ?? nudgeQueueStatus?.debounce_ms ?? '---'}ms</span></div>
                </div>
                {#if (nudgeQueueStatus?.by_lane && Object.keys(nudgeQueueStatus.by_lane).length > 0)}
                  <div class="diag-subtitle">Pending by Lane</div>
                  <div class="diag-lanes">
                    {#each Object.entries(nudgeQueueStatus.by_lane) as [lane, count]}
                      <span class="lane-chip">
                        <span class="text-mono">{lane}</span>
                        <span class="text-mono">{count}</span>
                      </span>
                    {/each}
                  </div>
                {:else}
                  <div class="text-xs text-muted">No queued nudges for this agent.</div>
                {/if}
                {#if (nudgeQueuePolicy?.lane_priority ?? []).length > 0}
                  <div class="diag-subtitle">Lane Priority</div>
                  <div class="diag-lanes">
                    {#each nudgeQueuePolicy.lane_priority as lane}
                      <span class="lane-chip lane-priority-chip"><span class="text-mono">{lane}</span></span>
                    {/each}
                  </div>
                {/if}

                <div class="diag-subtitle">Runtime Controls</div>
                <form
                  class="diag-policy-form"
                  onsubmit={(e) => {
                    e.preventDefault();
                    updateNudgePolicy();
                  }}
                >
                  <div class="diag-policy-grid">
                    <div class="form-group diag-form-group">
                      <label class="form-label" for="diag-policy-cap">Cap</label>
                      <input
                        id="diag-policy-cap"
                        class="form-input"
                        type="number"
                        min="1"
                        step="1"
                        bind:value={nudgePolicyCapInput}
                        oninput={markNudgePolicyDirty}
                      />
                    </div>
                    <div class="form-group diag-form-group">
                      <label class="form-label" for="diag-policy-debounce">Debounce (ms)</label>
                      <input
                        id="diag-policy-debounce"
                        class="form-input"
                        type="number"
                        min="0"
                        step="1"
                        bind:value={nudgePolicyDebounceInput}
                        oninput={markNudgePolicyDirty}
                      />
                    </div>
                    <div class="form-group diag-form-group">
                      <label class="form-label" for="diag-policy-drop">Drop Policy</label>
                      <select
                        id="diag-policy-drop"
                        class="form-input"
                        bind:value={nudgePolicyDropPolicy}
                        onchange={markNudgePolicyDirty}
                      >
                        <option value="drop_old">drop_old</option>
                        <option value="drop_new">drop_new</option>
                        <option value="summarize">summarize</option>
                      </select>
                    </div>
                    <div class="form-group diag-form-group">
                      <label class="form-label" for="diag-policy-updated-by">Updated By</label>
                      <input
                        id="diag-policy-updated-by"
                        class="form-input"
                        type="text"
                        placeholder="hud-ui"
                        bind:value={nudgePolicyUpdatedBy}
                        oninput={markNudgePolicyDirty}
                      />
                    </div>
                  </div>

                  <div class="form-group diag-form-group">
                    <label class="form-label" for="diag-policy-lanes">Lane Priority (comma-separated)</label>
                    <input
                      id="diag-policy-lanes"
                      class="form-input text-mono"
                      type="text"
                      placeholder="control, handoff, advice, default"
                      bind:value={nudgePolicyLanePriorityInput}
                      oninput={markNudgePolicyDirty}
                    />
                  </div>

                  <div class="form-group diag-form-group">
                    <label class="form-label" for="diag-policy-token">Admin Token</label>
                    <input
                      id="diag-policy-token"
                      class="form-input text-mono"
                      type="password"
                      autocomplete="off"
                      placeholder="Required for policy updates"
                      bind:value={nudgePolicyAdminToken}
                    />
                  </div>

                  <div class="diag-policy-actions">
                    <button class="btn btn-sm btn-ghost" type="button" onclick={resetNudgePolicyForm} disabled={nudgePolicyUpdating}>
                      Reset
                    </button>
                    <button class="btn btn-sm btn-primary" type="submit" disabled={nudgePolicyUpdating}>
                      {nudgePolicyUpdating ? 'Updating...' : 'Apply Policy'}
                    </button>
                  </div>

                  {#if nudgePolicyMutationError}
                    <div class="text-xs diagnostics-error">{nudgePolicyMutationError}</div>
                  {/if}
                </form>
              {/if}
            </div>
          </div>
        {/if}
      </div>
    {/if}
  </div>
</div>

<!-- Dispatch Task Modal -->
<Modal title="Dispatch Task" open={showDispatchModal} onclose={() => { showDispatchModal = false; }}>
  <div class="form-group">
    <label class="form-label" for="dispatch-target">Target Agent</label>
    <input id="dispatch-target" type="text" bind:value={dispatchTargetAgent} class="form-input" readonly />
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-title">Title *</label>
    <input id="dispatch-title" type="text" bind:value={dispatchTitle} placeholder="Task title..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-context">Context</label>
    <textarea id="dispatch-context" bind:value={dispatchContext} placeholder="Additional instructions..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-priority">Priority</label>
    <select id="dispatch-priority" bind:value={dispatchPriority} class="form-input">
      <option value="low">Low</option>
      <option value="medium">Medium</option>
      <option value="high">High</option>
      <option value="critical">Critical</option>
    </select>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => { showDispatchModal = false; }}>Cancel</button>
    <button class="btn btn-primary" onclick={submitDispatch} disabled={dispatchSubmitting || !dispatchTitle.trim()}>
      {dispatchSubmitting ? 'Dispatching...' : 'Dispatch'}
    </button>
  </div>
</Modal>

<!-- Nudge Agent Modal -->
<Modal title="Nudge Agent" open={showNudgeModal} onclose={() => { showNudgeModal = false; }}>
  <div class="form-group">
    <label class="form-label" for="nudge-target">Target Agent</label>
    <input id="nudge-target" type="text" bind:value={nudgeTargetAgent} class="form-input" readonly />
  </div>
  <div class="form-group">
    <label class="form-label" for="nudge-type">Type</label>
    <select id="nudge-type" bind:value={nudgeType} class="form-input">
      <option value="message">Message</option>
      <option value="context_inject">Context Inject</option>
      <option value="task_redirect">Task Redirect</option>
      <option value="pause_request">Pause Request</option>
    </select>
  </div>
  <div class="form-group">
    <label class="form-label" for="nudge-content">Content *</label>
    <textarea id="nudge-content" bind:value={nudgeContent} placeholder="Message or context to send to the agent..." class="form-input" rows="4"></textarea>
  </div>
  <div class="nudge-hint">
    Nudge delivered on the agent's next heartbeat (5-15s latency).
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => { showNudgeModal = false; }}>Cancel</button>
    <button class="btn btn-primary" onclick={submitNudge} disabled={nudgeSubmitting || !nudgeContent.trim()}>
      {nudgeSubmitting ? 'Sending...' : 'Send Nudge'}
    </button>
  </div>
</Modal>

<!-- Create Handoff Modal -->
<Modal title="Create Handoff" open={showHandoffModal} onclose={() => { showHandoffModal = false; }}>
  <div class="form-group">
    <label class="form-label" for="handoff-to-agent">To Agent (optional)</label>
    <input id="handoff-to-agent" type="text" bind:value={newHandoffTo} placeholder="Agent ID or leave blank for any..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-summary">Summary *</label>
    <input id="handoff-summary" type="text" bind:value={newHandoffSummary} placeholder="What needs to be done..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-context">Context</label>
    <textarea id="handoff-context" bind:value={newHandoffContext} placeholder="Additional context, findings, decisions..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => { showHandoffModal = false; }}>Cancel</button>
    <button class="btn btn-primary" onclick={submitHandoff} disabled={creatingHandoff || !newHandoffSummary.trim()}>
      {creatingHandoff ? 'Creating...' : 'Create Handoff'}
    </button>
  </div>
</Modal>

<style>
  .presence-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  /* Tab bar */
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

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }

  /* View toggle */
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

  /* Cards grid */
  .cards-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: 12px;
    padding: 4px 0;
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

  .diagnostics-card {
    display: flex;
    flex-direction: column;
    gap: 12px;
  }

  .diagnostics-header {
    align-items: center;
    gap: 8px;
  }

  .diagnostics-controls {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .diagnostics-select {
    min-width: 220px;
    max-width: 320px;
  }

  .diagnostics-error {
    padding: 0 4px;
    color: var(--error);
  }

  .diagnostics-metrics {
    display: grid;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: 10px;
  }

  .diagnostics-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  .diag-section {
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    background: var(--bg-secondary);
    padding: 10px 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-height: 220px;
  }

  .diag-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .diag-row {
    display: grid;
    grid-template-columns: 1fr auto auto;
    gap: 8px;
    align-items: center;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    font-size: 12px;
  }

  .diag-subtitle {
    font-size: 11px;
    color: var(--fg-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
    margin-top: 4px;
  }

  .diag-kv {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 8px;
  }

  .diag-kv > div {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 8px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    font-size: 12px;
  }

  .diag-lanes {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }

  .diag-policy-form {
    margin-top: 2px;
    padding-top: 8px;
    border-top: 1px dashed var(--border);
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .diag-policy-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
  }

  .diag-form-group {
    margin-bottom: 0;
  }

  .diag-policy-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 2px;
  }

  .lane-chip {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 3px 7px;
    border-radius: var(--radius-md);
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    font-size: 11px;
  }

  .lane-priority-chip {
    border-color: rgba(129, 240, 254, 0.3);
  }

  /* Presence grid (agents tab) */
  .presence-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 16px;
    height: 100%;
  }

  .agents-card {
    min-height: 200px;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }

  .agent-type-chip {
    font-family: var(--font-mono);
    font-size: 12px;
    font-weight: 500;
  }

  .count-badge {
    font-family: var(--font-mono);
    font-size: 11px;
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    padding: 1px 6px;
    border-radius: var(--radius-lg);
  }

  /* Stats grid */
  .stats-grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    grid-template-rows: 1fr 1fr 1fr;
    gap: 12px;
  }

  .stat-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    padding: 14px 16px;
    border-left: 3px solid var(--accent-color, var(--info));
    display: flex;
    flex-direction: column;
    justify-content: center;
  }

  .stat-card .metric-value {
    font-size: 22px;
    font-weight: 700;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .stat-card .metric-label {
    font-size: 10px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
    margin-top: 4px;
  }

  /* Conflict banner */
  .conflict-banner {
    background: rgba(231, 179, 18, 0.08);
    border: 1px solid rgba(231, 179, 18, 0.2);
    border-radius: var(--border-radius);
    padding: 10px 14px;
    margin: 8px 0;
    font-size: 12px;
    color: var(--warning);
  }

  .conflict-icon {
    font-size: 14px;
    margin-right: 4px;
  }

  .conflict-detail {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 3px 0 3px 20px;
  }

  /* Card actions */
  .card-actions {
    margin-left: auto;
  }

  /* Handoff button */
  .btn-xs {
    padding: 2px 8px;
    font-size: 11px;
  }

  .btn-success {
    background: rgba(34, 178, 85, 0.15);
    color: var(--success);
    border: 1px solid rgba(34, 178, 85, 0.3);
  }

  .btn-success:hover {
    background: rgba(34, 178, 85, 0.25);
  }

  /* Templates section */
  .templates-section {
    border-top: 1px solid var(--border);
    padding: 12px 0 0;
    margin-top: 12px;
  }

  .section-header {
    margin-bottom: 8px;
  }

  .section-title {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--fg-muted);
  }

  .template-list {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .template-chip {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 6px 10px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
  }

  .template-name {
    font-size: 12px;
    font-weight: 500;
    color: var(--fg-primary);
  }

  /* Loading bar */
  .loading-bar {
    height: 2px;
    background: var(--bg-tertiary);
    border-radius: 1px;
    overflow: hidden;
    margin-bottom: 4px;
  }

  .loading-bar-inner {
    width: 40%;
    height: 100%;
    background: var(--accent);
    border-radius: 1px;
    animation: loadingSlide 1s ease-in-out infinite;
  }

  @keyframes loadingSlide {
    0% { transform: translateX(-100%); }
    100% { transform: translateX(300%); }
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

  /* Agent row styling (via DataTable row snippet) */
  .status-label {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--fg-muted);
    margin-left: 4px;
    text-transform: uppercase;
  }

  .pr-link {
    display: inline-block;
    font-size: 10px;
    padding: 1px 4px;
    border-radius: var(--radius-sm);
    background: rgba(129, 240, 254, 0.1);
    color: var(--accent);
    text-decoration: none;
    margin-right: 4px;
    border: 1px solid rgba(129, 240, 254, 0.2);
  }

  .pr-link:hover {
    background: rgba(129, 240, 254, 0.2);
    text-decoration: none;
  }

  .actions-cell {
    display: flex;
    gap: 4px;
  }

  /* Nudge button */
  .btn-nudge {
    background: rgba(231, 179, 18, 0.1);
    color: var(--warning);
    border: 1px solid rgba(231, 179, 18, 0.25);
  }

  .btn-nudge:hover {
    background: rgba(231, 179, 18, 0.2);
  }

  .nudge-hint {
    font-size: 11px;
    color: var(--fg-muted);
    font-style: italic;
    margin-top: 8px;
  }

  /* Dispatch button */
  .btn-dispatch {
    background: rgba(129, 240, 254, 0.1);
    color: var(--accent);
    border: 1px solid rgba(129, 240, 254, 0.25);
  }

  .btn-dispatch:hover {
    background: rgba(129, 240, 254, 0.2);
  }

  /* Release/danger button */
  .btn-danger {
    background: rgba(233, 93, 116, 0.12);
    color: var(--error);
    border: 1px solid rgba(233, 93, 116, 0.3);
  }

  .btn-danger:hover {
    background: rgba(233, 93, 116, 0.22);
  }

  @media (max-width: 1180px) {
    .diagnostics-metrics {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }

    .diagnostics-grid {
      grid-template-columns: 1fr;
    }
  }

  @media (max-width: 760px) {
    .diagnostics-controls {
      width: 100%;
      margin-left: 0;
      flex-direction: column;
      align-items: stretch;
    }

    .diagnostics-select {
      min-width: 0;
      max-width: none;
    }

    .diag-policy-grid {
      grid-template-columns: 1fr;
    }
  }
</style>
