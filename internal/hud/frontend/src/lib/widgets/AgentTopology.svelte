<script>
  import { onDestroy, untrack } from 'svelte';
  import { forceSimulation, forceLink, forceManyBody, forceCenter, forceCollide } from 'd3-force';

  /** @type {{ nodes: any[], edges: any[], clusters: any[], width?: number, height?: number, selectedNode?: string | null, onselect?: (id: string | null) => void }} */
  let { nodes: inputNodes = [], edges: inputEdges = [], clusters = [], width = 800, height = 500, selectedNode = null, onselect } = $props();

  // Agent type colors — read from CSS tokens so the theme owns the palette.
  const AGENT_COLORS = {
    claude: 'var(--agent-claude)',
    codex: 'var(--agent-codex)',
    gemini: 'var(--agent-gemini)',
    copilot: 'var(--agent-copilot)',
  };

  function agentColor(agentType) {
    if (!agentType) return 'var(--fg-secondary)';
    const lower = agentType.toLowerCase();
    for (const [key, color] of Object.entries(AGENT_COLORS)) {
      if (lower.includes(key)) return color;
    }
    return 'var(--fg-secondary)';
  }

  function statusColor(status) {
    if (status === 'active') return 'var(--success)';
    if (status === 'idle') return 'var(--warning)';
    return 'var(--fg-muted)';
  }

  let svgEl = $state(null);
  let simulation = null;
  let simNodes = $state([]);
  let simLinks = $state([]);
  let transform = $state({ x: 0, y: 0, k: 1 });

  // Pan state
  let isPanning = false;
  let panStart = { x: 0, y: 0 };
  let dragNode = null;

  // Cluster hull paths
  let hullPaths = $state([]);

  function buildGraph() {
    if (!inputNodes || inputNodes.length === 0) {
      simNodes = [];
      simLinks = [];
      hullPaths = [];
      return;
    }

    const nodeIds = new Set(inputNodes.map((n) => n.agent_id));

    const newNodes = inputNodes.map((n) => ({
      ...n,
      id: n.agent_id,
      color: agentColor(n.agent_type),
      x: width / 2 + (Math.random() - 0.5) * 200,
      y: height / 2 + (Math.random() - 0.5) * 200,
    }));

    const validEdges = (inputEdges || []).filter(
      (e) => nodeIds.has(e.source) && nodeIds.has(e.target)
    );

    const newLinks = validEdges.map((e) => ({
      source: e.source,
      target: e.target,
      edge_type: e.edge_type,
      weight: e.weight || 1,
      label: e.label,
      status: e.status,
    }));

    simNodes = newNodes;
    simLinks = newLinks;

    if (simulation) simulation.stop();

    // Custom cluster force: nudge nodes toward their cluster centroid.
    function clusterForce(alpha) {
      const clusterCentroids = new Map();
      for (const c of clusters) {
        const members = simNodes.filter((n) => c.agent_ids.includes(n.id));
        if (members.length === 0) continue;
        const cx = members.reduce((s, n) => s + (n.x || 0), 0) / members.length;
        const cy = members.reduce((s, n) => s + (n.y || 0), 0) / members.length;
        clusterCentroids.set(c.project, { x: cx, y: cy });
      }
      for (const n of simNodes) {
        for (const c of clusters) {
          if (c.agent_ids.includes(n.id)) {
            const centroid = clusterCentroids.get(c.project);
            if (centroid) {
              n.vx += (centroid.x - n.x) * alpha * 0.3;
              n.vy += (centroid.y - n.y) * alpha * 0.3;
            }
            break;
          }
        }
      }
    }

    let lastTickUpdate = 0;
    simulation = forceSimulation(simNodes)
      .force(
        'link',
        forceLink(simLinks)
          .id((d) => d.id)
          .distance((d) => d.edge_type === 'shared_file' ? 60 : 100)
      )
      .force('charge', forceManyBody().strength(-200))
      .force('center', forceCenter(width / 2, height / 2))
      .force('collide', forceCollide(30))
      .force('cluster', () => clusterForce(simulation.alpha()))
      .on('tick', () => {
        const now = Date.now();
        if (now - lastTickUpdate < 50) return;
        lastTickUpdate = now;
        simNodes = [...simNodes];
        simLinks = [...simLinks];
        computeHulls();
      })
      .on('end', () => {
        simNodes = [...simNodes];
        simLinks = [...simLinks];
        computeHulls();
      });
  }

  function computeHulls() {
    const paths = [];
    for (const c of clusters) {
      const members = simNodes.filter((n) => c.agent_ids.includes(n.id));
      if (members.length < 2) continue;

      // Compute convex hull with padding.
      const points = members.map((n) => [n.x, n.y]);
      const hull = convexHull(points);
      if (hull.length < 3) continue;

      // Expand hull by padding.
      const pad = 25;
      const cx = hull.reduce((s, p) => s + p[0], 0) / hull.length;
      const cy = hull.reduce((s, p) => s + p[1], 0) / hull.length;
      const expanded = hull.map((p) => {
        const dx = p[0] - cx;
        const dy = p[1] - cy;
        const len = Math.sqrt(dx * dx + dy * dy) || 1;
        return [p[0] + (dx / len) * pad, p[1] + (dy / len) * pad];
      });

      const d = `M ${expanded.map((p) => `${p[0].toFixed(1)},${p[1].toFixed(1)}`).join(' L ')} Z`;
      paths.push({ project: c.project, d, cx, cy: cy - pad - 8 });
    }
    hullPaths = paths;
  }

  // Simple convex hull (Graham scan).
  function convexHull(points) {
    if (points.length < 3) return points;
    const sorted = [...points].sort((a, b) => a[0] - b[0] || a[1] - b[1]);
    const cross = (O, A, B) => (A[0] - O[0]) * (B[1] - O[1]) - (A[1] - O[1]) * (B[0] - O[0]);
    const lower = [];
    for (const p of sorted) {
      while (lower.length >= 2 && cross(lower[lower.length - 2], lower[lower.length - 1], p) <= 0)
        lower.pop();
      lower.push(p);
    }
    const upper = [];
    for (let i = sorted.length - 1; i >= 0; i--) {
      const p = sorted[i];
      while (upper.length >= 2 && cross(upper[upper.length - 2], upper[upper.length - 1], p) <= 0)
        upper.pop();
      upper.push(p);
    }
    return [...lower.slice(0, -1), ...upper.slice(0, -1)];
  }

  $effect(() => {
    const _n = inputNodes;
    const _e = inputEdges;
    const _c = clusters;
    untrack(() => buildGraph());
  });

  onDestroy(() => {
    if (simulation) simulation.stop();
  });

  let graphFocused = $state(false);

  function handleWheel(e) {
    if (!graphFocused && !e.ctrlKey && !e.metaKey) return;
    e.preventDefault();
    const delta = e.deltaY > 0 ? 0.9 : 1.1;
    const newK = Math.max(0.3, Math.min(3, transform.k * delta));
    transform = { ...transform, k: newK };
  }

  function handleMouseDown(e) {
    if (e.target.closest('.topo-node')) return;
    isPanning = true;
    panStart = { x: e.clientX - transform.x, y: e.clientY - transform.y };
  }

  function handleMouseMove(e) {
    if (isPanning) {
      transform = { ...transform, x: e.clientX - panStart.x, y: e.clientY - panStart.y };
    }
    if (dragNode && simulation) {
      const rect = svgEl.getBoundingClientRect();
      dragNode.fx = (e.clientX - rect.left - transform.x) / transform.k;
      dragNode.fy = (e.clientY - rect.top - transform.y) / transform.k;
      simulation.alpha(0.3).restart();
    }
  }

  function handleMouseUp() {
    isPanning = false;
    if (dragNode) {
      dragNode.fx = null;
      dragNode.fy = null;
      dragNode = null;
      if (simulation) simulation.alphaTarget(0);
    }
  }

  function handleNodeMouseDown(e, node) {
    e.stopPropagation();
    dragNode = node;
    node.fx = node.x;
    node.fy = node.y;
    if (simulation) simulation.alphaTarget(0.3).restart();
  }

  function handleNodeClick(e, node) {
    e.stopPropagation();
    if (onselect) onselect(selectedNode === node.id ? null : node.id);
  }

  function edgeStroke(edgeType) {
    if (edgeType === 'handoff') return 'var(--accent)';
    if (edgeType === 'shared_file') return 'var(--warning)';
    return 'var(--info)';
  }

  function edgeDashArray(edgeType) {
    if (edgeType === 'handoff') return '6,3';
    if (edgeType === 'shared_branch') return '2,3';
    return 'none';
  }

  function nodeShape(agentType) {
    if (!agentType) return 'circle';
    const lower = agentType.toLowerCase();
    if (lower.includes('claude')) return 'circle';
    if (lower.includes('codex')) return 'rect';
    if (lower.includes('gemini')) return 'hexagon';
    return 'diamond';
  }

  function truncate(s, len) {
    if (!s) return '';
    return s.length > len ? s.slice(0, len - 1) + '\u2026' : s;
  }
</script>

<div class="topo-container">
  {#if inputNodes.length === 0}
    <div class="empty-state">
      <span class="text-muted text-sm">No agents to display</span>
    </div>
  {:else}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <svg
      bind:this={svgEl}
      {width}
      {height}
      viewBox="0 0 {width} {height}"
      class="topo-svg"
      role="application"
      aria-label="Agent topology graph"
      tabindex="0"
      onwheel={handleWheel}
      onmousedown={handleMouseDown}
      onmousemove={handleMouseMove}
      onmouseup={handleMouseUp}
      onmouseleave={handleMouseUp}
      onfocus={() => graphFocused = true}
      onblur={() => graphFocused = false}
    >
      <defs>
        <filter id="topo-glow" x="-50%" y="-50%" width="200%" height="200%">
          <feGaussianBlur in="SourceGraphic" stdDeviation="2" result="blur" />
          <feMerge>
            <feMergeNode in="blur" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>

      <g transform="translate({transform.x},{transform.y}) scale({transform.k})">
        <!-- Cluster hulls -->
        {#each hullPaths as hull}
          <path
            d={hull.d}
            fill="var(--bg-tertiary)"
            fill-opacity="0.3"
            stroke="var(--border)"
            stroke-width="1"
            stroke-dasharray="4,2"
          />
          <text
            x={hull.cx}
            y={hull.cy}
            text-anchor="middle"
            fill="var(--fg-muted)"
            font-size="9"
            font-family="var(--font-mono)"
          >
            {hull.project}
          </text>
        {/each}

        <!-- Edges -->
        {#each simLinks as link}
          {#if typeof link.source === 'object' && typeof link.target === 'object'}
            <line
              x1={link.source.x}
              y1={link.source.y}
              x2={link.target.x}
              y2={link.target.y}
              stroke={edgeStroke(link.edge_type)}
              stroke-width={Math.min(link.weight + 1, 4)}
              stroke-dasharray={edgeDashArray(link.edge_type)}
              opacity="0.6"
              class="topo-edge topo-edge--{link.edge_type}"
            />
          {/if}
        {/each}

        <!-- Nodes -->
        {#each simNodes as node}
          <!-- svelte-ignore a11y_no_static_element_interactions a11y_click_events_have_key_events -->
          <g
            class="topo-node"
            class:selected={selectedNode === node.id}
            transform="translate({node.x},{node.y})"
            onmousedown={(e) => handleNodeMouseDown(e, node)}
            onclick={(e) => handleNodeClick(e, node)}
          >
            <!-- Status ring -->
            <circle
              r="18"
              fill="none"
              stroke={statusColor(node.status)}
              stroke-width="2"
              class="status-ring status-ring--{node.status}"
              opacity="0.5"
            />

            <!-- Node shape -->
            {#if nodeShape(node.agent_type) === 'rect'}
              <rect
                x="-11" y="-11" width="22" height="22" rx="4"
                fill={node.color}
                opacity="0.85"
              />
            {:else if nodeShape(node.agent_type) === 'hexagon'}
              <polygon
                points="0,-13 11.3,-6.5 11.3,6.5 0,13 -11.3,6.5 -11.3,-6.5"
                fill={node.color}
                opacity="0.85"
              />
            {:else if nodeShape(node.agent_type) === 'diamond'}
              <polygon
                points="0,-13 13,0 0,13 -13,0"
                fill={node.color}
                opacity="0.85"
              />
            {:else}
              <circle
                r="11"
                fill={node.color}
                opacity="0.85"
              />
            {/if}

            <!-- Label -->
            <text
              x="0"
              y="28"
              text-anchor="middle"
              fill="var(--fg-secondary)"
              font-size="9"
              font-family="var(--font-mono)"
            >
              {truncate(node.agent_id, 12)}
            </text>
          </g>
        {/each}
      </g>
    </svg>
  {/if}
</div>

<style>
  .topo-container {
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    background: var(--bg-primary);
    overflow: hidden;
    position: relative;
    width: 100%;
    height: 100%;
  }

  .topo-svg {
    display: block;
    cursor: grab;
    width: 100%;
    height: 100%;
  }

  .topo-svg:active {
    cursor: grabbing;
  }

  .topo-node {
    cursor: pointer;
    transition: opacity 0.15s;
  }

  .topo-node:hover circle,
  .topo-node:hover rect,
  .topo-node:hover polygon {
    opacity: 1;
    filter: url(#topo-glow);
  }

  .topo-node.selected .status-ring {
    opacity: 1;
    stroke-width: 3;
  }

  .status-ring--active {
    animation: pulseRing 2s ease-in-out infinite;
  }

  .status-ring--offline {
    stroke-dasharray: 4 3;
  }

  @keyframes pulseRing {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 0.8; }
  }

  .topo-edge--handoff {
    animation: dashFlow 1.5s linear infinite;
  }

  @keyframes dashFlow {
    to { stroke-dashoffset: -18; }
  }

  .empty-state {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    min-height: 200px;
  }
</style>
