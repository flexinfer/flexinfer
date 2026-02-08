<script>
  import { onDestroy, untrack } from 'svelte';
  import { forceSimulation, forceLink, forceManyBody, forceCenter, forceCollide } from 'd3-force';

  /**
   * @typedef {{ id: string, name: string, entity_type: string, properties: Record<string, unknown> }} Entity
   * @typedef {{ source: string, target: string, relation_type: string }} Relation
   * @type {{ entities: Entity[], relations: Relation[], width?: number, height?: number }}
   */
  let { entities = [], relations = [], width = 600, height = 400 } = $props();

  const typeColors = [
    '#018799', '#22B255', '#E95D74', '#E7B312', '#E61E3F',
    '#4EEAFE', '#9B5CD0', '#81F0FE', '#5EBDC9', '#2A7A87',
  ];

  function getTypeColor(type, types) {
    const idx = types.indexOf(type);
    return typeColors[idx % typeColors.length];
  }

  let svgEl = $state(null);
  let simulation = null;
  let nodes = $state([]);
  let links = $state([]);
  let transform = $state({ x: 0, y: 0, k: 1 });

  // Pan state
  let isPanning = false;
  let panStart = { x: 0, y: 0 };

  // Drag state
  let dragNode = null;

  function buildGraph() {
    if (!entities || entities.length === 0) {
      nodes = [];
      links = [];
      return;
    }

    const types = [...new Set(entities.map((e) => e.entity_type))];
    const entityIds = new Set(entities.map((e) => e.id));

    const newNodes = entities.map((e) => ({
      id: e.id,
      name: e.name,
      entity_type: e.entity_type,
      color: getTypeColor(e.entity_type, types),
      x: width / 2 + (Math.random() - 0.5) * 200,
      y: height / 2 + (Math.random() - 0.5) * 200,
    }));

    const validRelations = (relations || []).filter(
      (r) => entityIds.has(r.source) && entityIds.has(r.target)
    );

    const newLinks = validRelations.map((r) => ({
      source: r.source,
      target: r.target,
      relation_type: r.relation_type,
    }));

    nodes = newNodes;
    links = newLinks;

    if (simulation) simulation.stop();

    let lastTickUpdate = 0;
    simulation = forceSimulation(nodes)
      .force(
        'link',
        forceLink(links)
          .id((d) => d.id)
          .distance(80)
      )
      .force('charge', forceManyBody().strength(-150))
      .force('center', forceCenter(width / 2, height / 2))
      .force('collide', forceCollide(24))
      .on('tick', () => {
        const now = Date.now();
        if (now - lastTickUpdate < 50) return;
        lastTickUpdate = now;
        nodes = [...nodes];
        links = [...links];
      })
      .on('end', () => {
        nodes = [...nodes];
        links = [...links];
      });
  }

  $effect(() => {
    // Read reactive props to establish tracking
    const e = entities;
    const r = relations;
    // Write without triggering re-tracking
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
    if (e.target.closest('.graph-node')) return;
    isPanning = true;
    panStart = { x: e.clientX - transform.x, y: e.clientY - transform.y };
  }

  function handleMouseMove(e) {
    if (isPanning) {
      transform = {
        ...transform,
        x: e.clientX - panStart.x,
        y: e.clientY - panStart.y,
      };
    }
    if (dragNode && simulation) {
      const rect = svgEl.getBoundingClientRect();
      const x = (e.clientX - rect.left - transform.x) / transform.k;
      const y = (e.clientY - rect.top - transform.y) / transform.k;
      dragNode.fx = x;
      dragNode.fy = y;
      simulation.alpha(0.3).restart();
    }
  }

  function handleMouseUp() {
    isPanning = false;
    if (dragNode) {
      dragNode.fx = null;
      dragNode.fy = null;
      dragNode = null;
      if (simulation) {
        simulation.alphaTarget(0);
      }
    }
  }

  function handleNodeMouseDown(e, node) {
    e.stopPropagation();
    dragNode = node;
    node.fx = node.x;
    node.fy = node.y;
    if (simulation) simulation.alphaTarget(0.3).restart();
  }
</script>

<div class="graph-container">
  {#if entities.length === 0}
    <div class="empty-state">
      <span class="text-muted text-sm">No entities to display</span>
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
      class="graph-svg"
      role="application"
      aria-label="Knowledge graph visualization"
      tabindex="0"
      onwheel={handleWheel}
      onmousedown={handleMouseDown}
      onmousemove={handleMouseMove}
      onmouseup={handleMouseUp}
      onmouseleave={handleMouseUp}
      onfocus={() => graphFocused = true}
      onblur={() => graphFocused = false}
    >
      <g transform="translate({transform.x},{transform.y}) scale({transform.k})">
        <!-- Edges -->
        {#each links as link}
          {#if typeof link.source === 'object' && typeof link.target === 'object'}
            <line
              x1={link.source.x}
              y1={link.source.y}
              x2={link.target.x}
              y2={link.target.y}
              stroke="var(--border)"
              stroke-width="1"
              opacity="0.6"
            />
          {/if}
        {/each}

        <!-- Nodes -->
        {#each nodes as node}
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <g
            class="graph-node"
            transform="translate({node.x},{node.y})"
            onmousedown={(e) => handleNodeMouseDown(e, node)}
          >
            <circle
              r="10"
              fill={node.color}
              opacity="0.85"
              stroke={node.color}
              stroke-width="2"
              stroke-opacity="0.3"
            />
            <text
              x="14"
              y="4"
              fill="var(--fg-secondary)"
              font-size="10"
              font-family="var(--font-sans)"
            >
              {node.name.length > 20 ? node.name.slice(0, 19) + '\u2026' : node.name}
            </text>
          </g>
        {/each}
      </g>
    </svg>
  {/if}
</div>

<style>
  .graph-container {
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    background: var(--bg-primary);
    overflow: hidden;
    position: relative;
    width: 100%;
  }

  .graph-svg {
    display: block;
    cursor: grab;
    width: 100%;
    height: 100%;
  }

  .graph-svg:active {
    cursor: grabbing;
  }

  .graph-node {
    cursor: pointer;
  }

  .graph-node:hover circle {
    opacity: 1;
    stroke-width: 3;
  }
</style>
