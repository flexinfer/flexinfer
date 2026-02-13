<script>
  /** @type {{ lanes: import('../stores/lifecycle.svelte.ts').SwimLane[], timeRange: { start: number, end: number }, width?: number }} */
  let { lanes = [], timeRange, width = 900 } = $props();

  // Agent type colors (shared)
  const AGENT_COLORS = {
    claude: '#E95D74',
    codex: '#22B255',
    gemini: '#018799',
    copilot: '#E7B312',
  };

  function agentColor(agentType) {
    if (!agentType) return '#5EBDC9';
    const lower = agentType.toLowerCase();
    for (const [key, color] of Object.entries(AGENT_COLORS)) {
      if (lower.includes(key)) return color;
    }
    return '#5EBDC9';
  }

  const LABEL_WIDTH = 120;
  const ROW_HEIGHT = 32;
  const AXIS_HEIGHT = 24;
  const PADDING_TOP = 4;

  let svgWidth = $derived(width);
  let chartWidth = $derived(svgWidth - LABEL_WIDTH);
  let svgHeight = $derived(lanes.length * ROW_HEIGHT + AXIS_HEIGHT + PADDING_TOP);

  function timeX(ts) {
    const { start, end } = timeRange;
    const range = end - start;
    if (range <= 0) return 0;
    return LABEL_WIDTH + ((ts - start) / range) * chartWidth;
  }

  // Status icon for agent type.
  function agentIcon(agentType) {
    if (!agentType) return '\u25C9';
    const lower = agentType.toLowerCase();
    if (lower.includes('claude')) return '\u25CF';
    if (lower.includes('codex')) return '\u25A0';
    if (lower.includes('gemini')) return '\u2B22';
    return '\u25C6';
  }

  function statusDotColor(status) {
    if (status === 'active') return 'var(--success)';
    if (status === 'idle') return 'var(--warning)';
    return 'var(--fg-muted)';
  }

  // Event marker shapes and colors.
  function eventMarkerColor(eventType) {
    if (eventType === 'agent.session.start') return 'var(--success)';
    if (eventType === 'agent.session.end' || eventType === 'agent.session.reaped') return 'var(--error)';
    if (eventType.includes('task')) return 'var(--info)';
    if (eventType === 'hud.conflict') return 'var(--warning)';
    return 'var(--fg-muted)';
  }

  function eventMarkerShape(eventType) {
    if (eventType === 'agent.session.start') return 'triangle';
    if (eventType === 'agent.session.end' || eventType === 'agent.session.reaped') return 'square';
    if (eventType.includes('task')) return 'circle';
    if (eventType === 'hud.conflict') return 'diamond';
    return 'circle';
  }

  // Time axis tick marks.
  let ticks = $derived.by(() => {
    const { start, end } = timeRange;
    const range = end - start;
    // Aim for ~6-8 ticks.
    const hours = range / 3600_000;
    let stepHours = 1;
    if (hours > 12) stepHours = 3;
    if (hours > 24) stepHours = 6;
    if (hours > 36) stepHours = 8;
    const stepMs = stepHours * 3600_000;

    const result = [];
    // Start from the first even hour boundary.
    const firstTick = Math.ceil(start / stepMs) * stepMs;
    for (let t = firstTick; t <= end; t += stepMs) {
      const hoursAgo = Math.round((end - t) / 3600_000);
      let label;
      if (hoursAgo === 0) label = 'now';
      else if (hoursAgo === 1) label = '1h ago';
      else label = `${hoursAgo}h ago`;
      result.push({ ts: t, x: timeX(t), label });
    }
    return result;
  });

  // Session bar opacity.
  function sessionOpacity(status) {
    if (status === 'active') return 1.0;
    if (status === 'ended') return 0.6;
    return 0.3;
  }

  // Tooltip state.
  let tooltip = $state(null);

  function showTooltip(e, text) {
    const rect = e.currentTarget.closest('svg').getBoundingClientRect();
    tooltip = {
      text,
      x: e.clientX - rect.left + 10,
      y: e.clientY - rect.top - 20,
    };
  }

  function hideTooltip() {
    tooltip = null;
  }

  function formatDuration(ms) {
    const hours = Math.floor(ms / 3600_000);
    const mins = Math.floor((ms % 3600_000) / 60_000);
    if (hours > 0) return `${hours}h ${mins}m`;
    return `${mins}m`;
  }
</script>

<div class="swimlane-container">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <svg
    width={svgWidth}
    height={svgHeight}
    viewBox="0 0 {svgWidth} {svgHeight}"
    class="swimlane-svg"
    role="img"
    aria-label="Agent lifecycle swimlane timeline"
  >
    <!-- Background grid lines -->
    {#each ticks as tick}
      <line
        x1={tick.x}
        y1={PADDING_TOP}
        x2={tick.x}
        y2={svgHeight - AXIS_HEIGHT}
        stroke="var(--border)"
        stroke-width="0.5"
        opacity="0.4"
      />
    {/each}

    <!-- Lanes -->
    {#each lanes as lane, i}
      {@const y = PADDING_TOP + i * ROW_HEIGHT}

      <!-- Row background (alternating) -->
      {#if i % 2 === 0}
        <rect
          x={0}
          {y}
          width={svgWidth}
          height={ROW_HEIGHT}
          fill="var(--bg-tertiary)"
          opacity="0.3"
        />
      {/if}

      <!-- Agent label -->
      <g transform="translate(8, {y + ROW_HEIGHT / 2})">
        <text
          x="0"
          y="0"
          dominant-baseline="middle"
          fill={agentColor(lane.agent_type)}
          font-size="11"
          font-family="var(--font-mono)"
        >
          {agentIcon(lane.agent_type)}
        </text>
        <circle
          cx="16"
          cy="0"
          r="3"
          fill={statusDotColor(lane.current_status)}
        />
        <text
          x="24"
          y="0"
          dominant-baseline="middle"
          fill="var(--fg-secondary)"
          font-size="10"
          font-family="var(--font-mono)"
        >
          {lane.agent_id.length > 12 ? lane.agent_id.slice(0, 11) + '\u2026' : lane.agent_id}
        </text>
      </g>

      <!-- Session bars -->
      {#each lane.sessions as session}
        {@const sx = timeX(session.start)}
        {@const ex = timeX(session.end)}
        {@const barWidth = Math.max(ex - sx, 3)}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <rect
          x={sx}
          y={y + 6}
          width={barWidth}
          height={ROW_HEIGHT - 12}
          rx="3"
          fill={agentColor(lane.agent_type)}
          opacity={sessionOpacity(session.status)}
          class="session-bar"
          onmouseenter={(e) => showTooltip(e, `${session.namespace || 'no namespace'} | ${formatDuration(session.end - session.start)}`)}
          onmouseleave={hideTooltip}
        />
        <!-- Active session pulsing right edge -->
        {#if session.status === 'active'}
          <rect
            x={ex - 3}
            y={y + 6}
            width="3"
            height={ROW_HEIGHT - 12}
            rx="1"
            fill={agentColor(lane.agent_type)}
            class="active-edge"
          />
        {/if}
      {/each}

      <!-- Event markers (skip heartbeats to reduce clutter) -->
      {#each lane.events.filter(e => e.event_type !== 'agent.heartbeat') as event}
        {@const ex = timeX(event.timestamp)}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <g
          transform="translate({ex}, {y + ROW_HEIGHT / 2})"
          class="event-marker"
          onmouseenter={(e) => showTooltip(e, event.label)}
          onmouseleave={hideTooltip}
        >
          {#if eventMarkerShape(event.event_type) === 'triangle'}
            <polygon points="0,-4 3.5,3 -3.5,3" fill={eventMarkerColor(event.event_type)} />
          {:else if eventMarkerShape(event.event_type) === 'square'}
            <rect x="-3" y="-3" width="6" height="6" fill={eventMarkerColor(event.event_type)} />
          {:else if eventMarkerShape(event.event_type) === 'diamond'}
            <polygon points="0,-4 4,0 0,4 -4,0" fill={eventMarkerColor(event.event_type)} />
          {:else}
            <circle r="3" fill={eventMarkerColor(event.event_type)} />
          {/if}
        </g>
      {/each}
    {/each}

    <!-- Time axis -->
    <line
      x1={LABEL_WIDTH}
      y1={svgHeight - AXIS_HEIGHT}
      x2={svgWidth}
      y2={svgHeight - AXIS_HEIGHT}
      stroke="var(--border)"
      stroke-width="1"
    />
    {#each ticks as tick}
      <line
        x1={tick.x}
        y1={svgHeight - AXIS_HEIGHT}
        x2={tick.x}
        y2={svgHeight - AXIS_HEIGHT + 4}
        stroke="var(--fg-muted)"
        stroke-width="1"
      />
      <text
        x={tick.x}
        y={svgHeight - 4}
        text-anchor="middle"
        fill="var(--fg-muted)"
        font-size="9"
        font-family="var(--font-mono)"
      >
        {tick.label}
      </text>
    {/each}
  </svg>

  <!-- Tooltip overlay -->
  {#if tooltip}
    <div class="tooltip" style="left: {tooltip.x}px; top: {tooltip.y}px;">
      {tooltip.text}
    </div>
  {/if}
</div>

<style>
  .swimlane-container {
    position: relative;
    overflow-x: auto;
    overflow-y: auto;
  }

  .swimlane-svg {
    display: block;
    min-width: 100%;
  }

  .session-bar {
    cursor: pointer;
    transition: opacity 0.1s;
  }

  .session-bar:hover {
    opacity: 1 !important;
    filter: brightness(1.2);
  }

  .active-edge {
    animation: pulseEdge 1.5s ease-in-out infinite;
  }

  @keyframes pulseEdge {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .event-marker {
    cursor: pointer;
    opacity: 0.8;
  }

  .event-marker:hover {
    opacity: 1;
    transform-origin: center;
  }

  .tooltip {
    position: absolute;
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    padding: 4px 8px;
    font-size: 11px;
    font-family: var(--font-mono);
    color: var(--fg-primary);
    pointer-events: none;
    white-space: nowrap;
    z-index: 100;
    box-shadow: var(--shadow-md);
  }
</style>
