<script>
  import { topologyStore } from '../stores/topology.svelte.ts';
  import { presenceStore } from '../stores/presence.svelte.ts';
  import { truncatePath } from '../utils/format.ts';
  import AgentTopology from '../widgets/AgentTopology.svelte';
  import StatusDot from '../widgets/StatusDot.svelte';
  import Badge from '../widgets/Badge.svelte';

  $effect(() => {
    topologyStore.startPolling(30000);
    return () => topologyStore.stopPolling();
  });

  let nodes = $derived(topologyStore.nodes);
  let edges = $derived(topologyStore.edges);
  let clusters = $derived(topologyStore.clusters);
  let selected = $derived(topologyStore.selectedNode);

  // Find the selected node details.
  let selectedAgent = $derived.by(() => {
    if (!selected) return null;
    return nodes.find((n) => n.agent_id === selected) ?? null;
  });

  // Find file claims for selected agent.
  let selectedClaims = $derived.by(() => {
    if (!selected) return [];
    return (presenceStore.claims ?? []).filter((c) => c.agent_id === selected);
  });

  // Find edges for selected agent.
  let selectedEdges = $derived.by(() => {
    if (!selected) return [];
    return edges.filter((e) => e.source === selected || e.target === selected);
  });

  function presenceStatus(status) {
    const map = { active: 'healthy', idle: 'degraded', offline: 'down' };
    return map[status] ?? 'down';
  }

  function edgeTypeLabel(type) {
    const map = { handoff: 'Handoff', shared_file: 'Shared File', shared_branch: 'Shared Branch' };
    return map[type] ?? type;
  }

  function edgeTypeVariant(type) {
    const map = { handoff: 'accent', shared_file: 'warning', shared_branch: 'info' };
    return map[type] ?? 'info';
  }

  function shortPath(path) {
    return truncatePath(path, 50);
  }
</script>

<div class="panel topology-panel">
  <div class="topology-layout" class:has-sidebar={!!selected}>
    <!-- Graph area -->
    <div class="topology-graph">
      <AgentTopology
        {nodes}
        {edges}
        {clusters}
        selectedNode={selected}
        onselect={(id) => topologyStore.selectNode(id)}
      />
    </div>

    <!-- Detail sidebar (shown when node selected) -->
    {#if selectedAgent}
      <aside class="topology-sidebar">
        <div class="sidebar-header">
          <h3 class="sidebar-title">{selectedAgent.agent_id}</h3>
          <button class="btn btn-ghost btn-xs" onclick={() => topologyStore.selectNode(null)} title="Close">
            {'\u2715'}
          </button>
        </div>

        <div class="sidebar-section">
          <div class="detail-row">
            <span class="detail-label">Status</span>
            <span class="detail-value">
              <StatusDot status={presenceStatus(selectedAgent.status)} />
              <span class="status-text">{selectedAgent.status}</span>
            </span>
          </div>
          <div class="detail-row">
            <span class="detail-label">Type</span>
            <span class="detail-value text-mono">{selectedAgent.agent_type || '---'}</span>
          </div>
          {#if selectedAgent.current_task}
            <div class="detail-row">
              <span class="detail-label">Task</span>
              <span class="detail-value truncate" title={selectedAgent.current_task}>{selectedAgent.current_task}</span>
            </div>
          {/if}
          {#if selectedAgent.branch}
            <div class="detail-row">
              <span class="detail-label">Branch</span>
              <span class="detail-value text-mono">{selectedAgent.branch}</span>
            </div>
          {/if}
          {#if selectedAgent.pr_url}
            <div class="detail-row">
              <span class="detail-label">PR</span>
              <span class="detail-value">
                <a href={selectedAgent.pr_url} target="_blank" rel="noopener" class="link">{selectedAgent.pr_url}</a>
              </span>
            </div>
          {/if}
          {#if selectedAgent.namespace}
            <div class="detail-row">
              <span class="detail-label">Namespace</span>
              <span class="detail-value text-mono">{selectedAgent.namespace}</span>
            </div>
          {/if}
        </div>

        <!-- Connections -->
        {#if selectedEdges.length > 0}
          <div class="sidebar-section">
            <h4 class="section-title">Connections</h4>
            {#each selectedEdges as edge}
              <div class="connection-row">
                <Badge text={edgeTypeLabel(edge.edge_type)} variant={edgeTypeVariant(edge.edge_type)} />
                <span class="text-mono text-xs">
                  {edge.source === selected ? edge.target : edge.source}
                </span>
                {#if edge.weight > 1}
                  <span class="text-muted text-xs">({edge.weight})</span>
                {/if}
              </div>
            {/each}
          </div>
        {/if}

        <!-- File Claims -->
        {#if selectedClaims.length > 0}
          <div class="sidebar-section">
            <h4 class="section-title">File Claims ({selectedClaims.length})</h4>
            {#each selectedClaims as claim}
              <div class="claim-row text-mono text-xs" title={claim.file_path}>
                {shortPath(claim.file_path)}
              </div>
            {/each}
          </div>
        {/if}
      </aside>
    {/if}
  </div>

  <!-- Legend -->
  <footer class="topology-legend">
    <div class="legend-section">
      <span class="legend-label">Edges:</span>
      <span class="legend-item"><span class="legend-line legend-line--handoff"></span> Handoff</span>
      <span class="legend-item"><span class="legend-line legend-line--shared-file"></span> Shared File</span>
      <span class="legend-item"><span class="legend-line legend-line--shared-branch"></span> Shared Branch</span>
    </div>
    <div class="legend-section">
      <span class="legend-label">Status:</span>
      <span class="legend-item"><span class="legend-dot" style="background: var(--success)"></span> Active</span>
      <span class="legend-item"><span class="legend-dot" style="background: var(--warning)"></span> Idle</span>
      <span class="legend-item"><span class="legend-dot" style="background: var(--fg-muted)"></span> Offline</span>
    </div>
  </footer>
</div>

<style>
  .topology-panel {
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .topology-layout {
    flex: 1;
    display: flex;
    overflow: hidden;
  }

  .topology-graph {
    flex: 1;
    min-width: 0;
  }

  .topology-sidebar {
    width: 280px;
    flex-shrink: 0;
    border-left: 1px solid var(--border);
    background: var(--bg-secondary);
    overflow-y: auto;
    padding: var(--space-3);
    position: relative;
  }

  .topology-sidebar::before {
    content: '';
    position: absolute;
    inset: 0;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .sidebar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: var(--space-3);
    position: relative;
  }

  .sidebar-header::after {
    content: '';
    position: absolute;
    bottom: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.06) 50%, transparent);
    pointer-events: none;
  }

  .sidebar-title {
    font-size: var(--text-base);
    font-weight: 600;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    margin: 0;
  }

  .sidebar-section {
    margin-bottom: var(--space-4);
    padding-bottom: var(--space-3);
    border-bottom: 1px solid var(--border-subtle);
  }

  .sidebar-section:last-child {
    border-bottom: none;
  }

  .section-title {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
    margin: 0 0 var(--space-2);
  }

  .detail-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--space-1) 0;
    font-size: var(--text-sm);
  }

  .detail-label {
    color: var(--fg-muted);
    font-size: var(--text-xs);
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
  }

  .detail-value {
    color: var(--fg-primary);
    display: flex;
    align-items: center;
    gap: var(--space-1);
    max-width: 160px;
  }

  .status-text {
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
  }

  .connection-row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 3px 0;
  }

  .claim-row {
    padding: 2px 0;
    color: var(--fg-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .link {
    color: var(--accent);
    text-decoration: none;
    font-size: var(--text-xs);
    overflow: hidden;
    text-overflow: ellipsis;
    transition: color var(--transition-fast);
  }

  .link:hover {
    text-decoration: underline;
    color: var(--fg-primary);
  }

  /* Legend */
  .topology-legend {
    display: flex;
    align-items: center;
    gap: var(--space-5);
    padding: 6px var(--space-3);
    border-top: 1px solid var(--border);
    background: var(--bg-secondary);
    flex-shrink: 0;
  }

  .legend-section {
    display: flex;
    align-items: center;
    gap: var(--space-3);
  }

  .legend-label {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .legend-item {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    letter-spacing: var(--tracking-normal);
  }

  .legend-line {
    width: 16px;
    height: 2px;
    display: inline-block;
  }

  .legend-line--handoff {
    background: var(--accent);
    border-top: 2px dashed var(--accent);
    height: 0;
  }

  .legend-line--shared-file {
    background: var(--warning);
  }

  .legend-line--shared-branch {
    background: var(--info);
    border-top: 2px dotted var(--info);
    height: 0;
  }

  .legend-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    display: inline-block;
  }

  .btn-xs {
    padding: 2px 6px;
    font-size: var(--text-xs);
    transition: background var(--transition-fast);
  }

  .btn-xs:hover {
    background: var(--bg-elevated);
  }
</style>
