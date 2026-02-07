<script>
  /** @type {{ text: string, variant?: 'info' | 'success' | 'warning' | 'error' | 'accent' }} */
  let { text = '', variant = 'info' } = $props();

  const variantStyles = {
    info:    { bg: 'rgba(88, 166, 255, 0.15)',  fg: 'var(--info)',    border: 'rgba(88, 166, 255, 0.3)' },
    success: { bg: 'rgba(63, 185, 80, 0.15)',   fg: 'var(--success)', border: 'rgba(63, 185, 80, 0.3)' },
    warning: { bg: 'rgba(210, 153, 34, 0.15)',  fg: 'var(--warning)', border: 'rgba(210, 153, 34, 0.3)' },
    error:   { bg: 'rgba(248, 81, 73, 0.15)',   fg: 'var(--error)',   border: 'rgba(248, 81, 73, 0.3)' },
    accent:  { bg: 'rgba(188, 140, 255, 0.15)', fg: 'var(--accent)',  border: 'rgba(188, 140, 255, 0.3)' },
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
