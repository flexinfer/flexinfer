<script>
  /** @type {{ status: 'healthy' | 'degraded' | 'down' | 'unknown' }} */
  let { status = 'unknown' } = $props();

  const colorMap = {
    healthy: 'var(--success)',
    degraded: 'var(--warning)',
    down: 'var(--error)',
    unknown: 'var(--fg-muted)',
  };

  let color = $derived(colorMap[status] || colorMap.unknown);
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
></span>

<style>
  .status-dot {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
  }

  .pulse {
    animation: dotPulse 1.5s ease-in-out infinite;
  }

  @keyframes dotPulse {
    0%, 100% { opacity: 1; transform: scale(1); }
    50%      { opacity: 0.5; transform: scale(0.85); }
  }
</style>
