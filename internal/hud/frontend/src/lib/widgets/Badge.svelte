<script>
  /** @type {{ text: string, variant?: 'info' | 'success' | 'warning' | 'error' | 'accent' }} */
  let { text = '', variant = 'info' } = $props();

  const variantStyles = {
    info:    { bg: 'rgba(1, 135, 153, 0.15)',   fg: 'var(--info)',    border: 'rgba(1, 135, 153, 0.3)' },
    success: { bg: 'rgba(34, 178, 85, 0.15)',   fg: 'var(--success)', border: 'rgba(34, 178, 85, 0.3)' },
    warning: { bg: 'rgba(231, 179, 18, 0.15)',  fg: 'var(--warning)', border: 'rgba(231, 179, 18, 0.3)' },
    error:   { bg: 'rgba(230, 30, 63, 0.15)',   fg: 'var(--error)',   border: 'rgba(230, 30, 63, 0.3)' },
    accent:  { bg: 'rgba(233, 93, 116, 0.15)',  fg: 'var(--accent)',  border: 'rgba(233, 93, 116, 0.3)' },
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
    padding: 1px 7px;
    border-radius: 10px;
    font-size: 10px;
    font-weight: 500;
    letter-spacing: 0.3px;
    white-space: nowrap;
    border: 1px solid;
    line-height: 1.6;
  }
</style>
