<script>
  /**
   * @typedef {{ id: string, name: string, status: string, depends_on: string[] }} Step
   * @type {{ steps: Step[] }}
   */
  let { steps = [] } = $props();

  // Unique ID per instance to avoid SVG marker collisions when multiple DagViews exist.
  const markerId = `arrowhead-${Math.random().toString(36).slice(2, 8)}`;

  const NODE_W = 120;
  const NODE_H = 36;
  const LAYER_GAP = 60;
  const ROW_GAP = 16;
  const PADDING = 20;

  const statusColors = {
    completed: 'var(--status-completed)',
    running: 'var(--status-running)',
    pending: 'var(--status-pending)',
    failed: 'var(--status-failed)',
    waiting_approval: 'var(--status-waiting)',
    cancelled: 'var(--fg-muted)',
  };

  // Topological layering
  let layout = $derived.by(() => {
    if (!steps || steps.length === 0) return { nodes: [], edges: [], width: 0, height: 0 };

    const stepMap = new Map(steps.map((s) => [s.id, s]));
    const layers = [];
    const assigned = new Set();

    // BFS layering
    const inDegree = new Map();
    for (const s of steps) {
      if (!inDegree.has(s.id)) inDegree.set(s.id, 0);
      for (const dep of s.depends_on || []) {
        inDegree.set(s.id, (inDegree.get(s.id) || 0) + 1);
      }
    }

    let queue = steps.filter((s) => (inDegree.get(s.id) || 0) === 0).map((s) => s.id);
    while (queue.length > 0) {
      layers.push([...queue]);
      queue.forEach((id) => assigned.add(id));
      const next = [];
      for (const s of steps) {
        if (assigned.has(s.id)) continue;
        const deps = s.depends_on || [];
        if (deps.every((d) => assigned.has(d))) {
          next.push(s.id);
        }
      }
      queue = next;
    }

    // Catch any unassigned (cycles or orphans)
    for (const s of steps) {
      if (!assigned.has(s.id)) {
        if (layers.length === 0) layers.push([]);
        layers[layers.length - 1].push(s.id);
      }
    }

    // Position nodes
    const nodePositions = new Map();
    let maxRowCount = 0;

    for (let li = 0; li < layers.length; li++) {
      const layer = layers[li];
      maxRowCount = Math.max(maxRowCount, layer.length);
      for (let ri = 0; ri < layer.length; ri++) {
        const x = PADDING + li * (NODE_W + LAYER_GAP);
        const y = PADDING + ri * (NODE_H + ROW_GAP);
        nodePositions.set(layer[ri], { x, y });
      }
    }

    const nodes = steps.map((s) => {
      const pos = nodePositions.get(s.id) || { x: 0, y: 0 };
      return {
        ...s,
        x: pos.x,
        y: pos.y,
        color: statusColors[s.status] || 'var(--fg-muted)',
      };
    });

    // Build edges
    const edges = [];
    for (const s of steps) {
      for (const dep of s.depends_on || []) {
        const from = nodePositions.get(dep);
        const to = nodePositions.get(s.id);
        if (from && to) {
          edges.push({
            x1: from.x + NODE_W,
            y1: from.y + NODE_H / 2,
            x2: to.x,
            y2: to.y + NODE_H / 2,
          });
        }
      }
    }

    const totalWidth = PADDING * 2 + layers.length * (NODE_W + LAYER_GAP) - LAYER_GAP;
    const totalHeight = PADDING * 2 + maxRowCount * (NODE_H + ROW_GAP) - ROW_GAP;

    return { nodes, edges, width: totalWidth, height: totalHeight };
  });
</script>

{#if steps.length === 0}
  <div class="empty-state">
    <span class="text-muted text-sm">No workflow steps</span>
  </div>
{:else}
  <div class="dag-container">
    <svg
      width={layout.width}
      height={layout.height}
      viewBox="0 0 {layout.width} {layout.height}"
      class="dag-svg"
    >
      <!-- Arrow marker (scoped ID per instance to avoid collisions) -->
      <defs>
        <marker id={markerId} markerWidth="8" markerHeight="6" refX="8" refY="3" orient="auto">
          <polygon points="0 0, 8 3, 0 6" fill="var(--border)" />
        </marker>
      </defs>

      <!-- Edges -->
      {#each layout.edges as edge}
        <line
          x1={edge.x1}
          y1={edge.y1}
          x2={edge.x2}
          y2={edge.y2}
          stroke="var(--border)"
          stroke-width="1.5"
          marker-end="url(#{markerId})"
        />
      {/each}

      <!-- Nodes -->
      {#each layout.nodes as node}
        <g transform="translate({node.x}, {node.y})">
          <rect
            width={NODE_W}
            height={NODE_H}
            rx="4"
            ry="4"
            fill="var(--bg-tertiary)"
            stroke={node.color}
            stroke-width="1.5"
          />
          <text
            x={NODE_W / 2}
            y={NODE_H / 2 + 1}
            text-anchor="middle"
            dominant-baseline="central"
            fill="var(--fg-primary)"
            font-size="11"
            font-family="var(--font-sans)"
          >
            {node.name.length > 14 ? node.name.slice(0, 13) + '\u2026' : node.name}
          </text>
          <!-- Status indicator bar at bottom -->
          <rect
            x="2"
            y={NODE_H - 3}
            width={NODE_W - 4}
            height="2"
            rx="1"
            fill={node.color}
            opacity="0.7"
          />
        </g>
      {/each}
    </svg>
  </div>
{/if}

<style>
  .dag-container {
    overflow-x: auto;
    overflow-y: auto;
    max-height: 400px;
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    background: var(--bg-primary);
  }

  .dag-svg {
    display: block;
    min-width: 100%;
  }
</style>
