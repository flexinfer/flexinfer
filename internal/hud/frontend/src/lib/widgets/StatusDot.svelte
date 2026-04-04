<script>
  /** @type {{ status: 'healthy' | 'idle' | 'degraded' | 'down' | 'unknown' }} */
  let { status = 'unknown' } = $props();

  const colorMap = {
    healthy: 'var(--success)',
    idle: 'var(--fg-muted)',
    degraded: 'var(--warning)',
    down: 'var(--error)',
    unknown: 'var(--fg-muted)',
  };

  // Color-blind safe: distinct shapes per status
  const shapeMap = {
    healthy:  '\u25CF', // filled circle
    idle:     '\u25CB', // open circle
    degraded: '\u25B2', // filled triangle
    down:     '\u25A0', // filled square
    unknown:  '\u25C6', // diamond
  };

  let color = $derived(colorMap[status] || colorMap.unknown);
  let shape = $derived(shapeMap[status] || shapeMap.unknown);
  let shouldPulse = $derived(status === 'degraded' || status === 'down');
</script>

<span
  class="status-dot"
  class:pulse={shouldPulse}
  style:background={color}
  style:box-shadow="0 0 4px {color}"
  title={status}
  role="status"
  aria-label={status}
>{shape}</span>

<style>
  .status-dot {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
    font-size: 6px;
    line-height: 8px;
    text-align: center;
    color: var(--bg-primary);
    transition: box-shadow var(--transition-normal);
  }

  .pulse {
    animation: dotPulse 2s ease-in-out infinite;
  }

  @keyframes dotPulse {
    0%, 100% { opacity: 1; transform: scale(1); box-shadow: 0 0 4px currentColor; }
    50%      { opacity: 0.6; transform: scale(0.9); box-shadow: 0 0 8px currentColor; }
  }
</style>
