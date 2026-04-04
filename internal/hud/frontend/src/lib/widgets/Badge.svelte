<script>
  /** @type {{ text: string, variant?: 'info' | 'success' | 'warning' | 'error' | 'accent' }} */
  let { text = '', variant = 'info' } = $props();

  const variantStyles = {
    info:    { bg: 'var(--info-dim)',    fg: 'var(--info)',    border: 'rgba(0, 200, 255, 0.2)' },
    success: { bg: 'var(--success-dim)', fg: 'var(--success)', border: 'rgba(34, 224, 118, 0.2)' },
    warning: { bg: 'var(--warning-dim)', fg: 'var(--warning)', border: 'rgba(255, 184, 48, 0.2)' },
    error:   { bg: 'var(--error-dim)',   fg: 'var(--error)',   border: 'rgba(255, 61, 113, 0.2)' },
    accent:  { bg: 'var(--accent-dim)',  fg: 'var(--accent)',  border: 'rgba(255, 107, 53, 0.2)' },
  };

  let style = $derived(variantStyles[variant] || variantStyles.info);
</script>

<span
  class="badge"
  style:background={style.bg}
  style:color={style.fg}
  style:border-color={style.border}
>
  {text}
</span>

<style>
  .badge {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: var(--radius-full);
    font-size: var(--text-xs);
    font-weight: 600;
    letter-spacing: var(--tracking-normal);
    white-space: nowrap;
    border: 1px solid;
    line-height: 1.3;
  }
</style>
