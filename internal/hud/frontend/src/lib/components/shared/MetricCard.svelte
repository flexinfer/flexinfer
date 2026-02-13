<script>
  /**
   * MetricCard — stat display with label, value, optional trend sparkline and badge.
   *
   * @type {{
   *   label: string,
   *   value: string | number,
   *   color?: string,
   *   badge?: string,
   *   badgeVariant?: 'info' | 'success' | 'warning' | 'error' | 'accent' | 'muted',
   *   trend?: number[],
   *   trendColor?: string,
   *   compact?: boolean,
   *   onclick?: () => void,
   * }}
   */
  import SparkLine from '../../widgets/SparkLine.svelte';

  let {
    label,
    value,
    color = 'var(--fg-primary)',
    badge = '',
    badgeVariant = 'info',
    trend,
    trendColor = 'var(--info)',
    compact = false,
    onclick,
  } = $props();
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="metric-card"
  class:compact
  class:clickable={!!onclick}
  onclick={onclick}
  onkeydown={(e) => { if (onclick && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); onclick(); }}}
  tabindex={onclick ? 0 : undefined}
  role={onclick ? 'button' : undefined}
>
  <div class="metric-card-top">
    <span class="metric-card-label">{label}</span>
    {#if badge}
      <span class="metric-card-badge badge-{badgeVariant}">{badge}</span>
    {/if}
  </div>
  <div class="metric-card-value" style:color={color}>{value}</div>
  {#if trend && trend.length > 1}
    <div class="metric-card-trend">
      <SparkLine data={trend} width={compact ? 48 : 80} height={compact ? 14 : 18} color={trendColor} />
    </div>
  {/if}
</div>

<style>
  .metric-card {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-2) var(--space-3);
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    transition: border-color var(--transition-fast), box-shadow var(--transition-fast);
  }

  .metric-card.compact {
    padding: var(--space-1) var(--space-2);
  }

  .metric-card.clickable {
    cursor: pointer;
  }

  .metric-card.clickable:hover {
    border-color: var(--border-focus);
    box-shadow: 0 0 8px var(--glow-accent);
  }

  .metric-card.clickable:focus-visible {
    outline: 2px solid var(--border-focus);
    outline-offset: -2px;
  }

  .metric-card-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .metric-card-label {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .metric-card-badge {
    font-size: 9px;
    padding: 1px 5px;
    border-radius: var(--radius-lg);
    font-weight: 500;
  }

  .badge-info { background: rgba(1, 135, 153, 0.15); color: var(--info); }
  .badge-success { background: rgba(34, 178, 85, 0.15); color: var(--success); }
  .badge-warning { background: rgba(231, 179, 18, 0.15); color: var(--warning); }
  .badge-error { background: rgba(230, 30, 63, 0.15); color: var(--error); }
  .badge-accent { background: rgba(233, 93, 116, 0.15); color: var(--accent); }
  .badge-muted { background: var(--bg-tertiary); color: var(--fg-muted); }

  .metric-card-value {
    font-size: var(--text-xl);
    font-weight: 700;
    font-family: var(--font-mono);
    line-height: 1;
    font-feature-settings: 'tnum';
  }

  .metric-card.compact .metric-card-value {
    font-size: var(--text-lg);
  }

  .metric-card-trend {
    margin-top: var(--space-1);
  }
</style>
