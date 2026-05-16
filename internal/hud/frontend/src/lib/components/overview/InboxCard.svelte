<script lang="ts">
  import ConfirmDialog from '../shared/ConfirmDialog.svelte';
  import type { CardSpec } from '../../utils/inbox.ts';
  import { useAction } from '../../utils/useAction.svelte.ts';

  interface Props {
    card: CardSpec;
  }
  let { card }: Props = $props();

  // Wrap async primary in useAction so audit + toast + retry come for free.
  // Re-derived when `card` changes so label/source reflect the current card
  // (Svelte 5 warns when prop values are captured into closures only at init).
  let primaryAction = $derived(
    useAction({
      label: card.primary.label,
      source: `OverviewPanel:inbox/${card.kind}`,
      run: async () => {
        const r = card.primary.run();
        return r instanceof Promise ? await r : r;
      },
      silentSuccess: true,
    })
  );

  let confirmOpen = $state(false);

  function onPrimaryClick(): void {
    if (card.primary.confirm) {
      confirmOpen = true;
      return;
    }
    void primaryAction.run();
  }

  function onConfirm(): void {
    confirmOpen = false;
    void primaryAction.run();
  }

  function onCancel(): void {
    confirmOpen = false;
  }
</script>

<article class="inbox-card severity-{card.severity}" aria-label="{card.kind}">
  <header class="card-head">
    <span class="dot" aria-hidden="true"></span>
    <span class="kind">{card.kind.replace(/_/g, ' ')}</span>
  </header>

  <h3 class="headline">{card.headline}</h3>
  <p class="detail">{card.detail}</p>

  <div class="actions">
    <button
      type="button"
      class="btn-primary"
      class:btn-danger={card.primary.destructive}
      onclick={onPrimaryClick}
      disabled={primaryAction.pending}
    >
      {primaryAction.pending ? '...' : card.primary.label}
    </button>

    {#if card.secondary}
      <button
        type="button"
        class="btn-secondary"
        onclick={() => card.secondary?.run()}
      >
        {card.secondary.label}
      </button>
    {/if}
  </div>

  {#if primaryAction.error}
    <div class="err" role="alert">{primaryAction.error}</div>
  {/if}
</article>

{#if card.primary.confirm}
  <ConfirmDialog
    open={confirmOpen}
    title={card.primary.confirm.title}
    message={card.primary.confirm.message}
    confirmLabel={card.primary.confirm.confirmLabel}
    variant={card.primary.confirm.variant ?? 'default'}
    onConfirm={onConfirm}
    onCancel={onCancel}
  />
{/if}

<style>
  .inbox-card {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3) var(--space-4);
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-secondary);
    position: relative;
  }

  /* Left-edge severity stripe carries meaning without leaning on glow. */
  .inbox-card::before {
    content: '';
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 3px;
    border-radius: var(--radius-md) 0 0 var(--radius-md);
  }
  .severity-alert::before { background: var(--error); }
  .severity-warn::before  { background: var(--warning); }
  .severity-info::before  { background: var(--info); }

  .card-head {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .dot {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .severity-alert .dot { background: var(--error); box-shadow: 0 0 6px var(--error); }
  .severity-warn  .dot { background: var(--warning); }
  .severity-info  .dot { background: var(--info); }

  .kind {
    font-size: 9px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wider);
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .headline {
    margin: 0;
    font-size: var(--text-base);
    font-weight: 600;
    color: var(--fg-primary);
    line-height: var(--leading-tight);
  }

  .detail {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.5;
    font-family: var(--font-mono);
  }

  .actions {
    display: flex;
    gap: var(--space-2);
    flex-wrap: wrap;
    margin-top: var(--space-1);
  }

  .btn-primary,
  .btn-secondary {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 6px 14px;
    border-radius: var(--radius-sm);
    font-size: 12px;
    font-weight: 600;
    font-family: var(--font-mono);
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .btn-primary {
    background: var(--accent-dim);
    border: 1px solid rgba(255, 107, 53, 0.35);
    color: var(--accent);
  }
  .btn-primary:hover:not(:disabled) {
    background: rgba(255, 107, 53, 0.18);
    border-color: rgba(255, 107, 53, 0.5);
  }
  .btn-primary:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .btn-danger {
    background: var(--error-dim);
    border-color: rgba(255, 61, 113, 0.4);
    color: var(--error);
  }
  .btn-danger:hover:not(:disabled) {
    background: rgba(255, 61, 113, 0.18);
    border-color: var(--error);
  }

  .btn-secondary {
    background: transparent;
    border: 1px solid var(--border);
    color: var(--fg-secondary);
  }
  .btn-secondary:hover {
    border-color: var(--border-focus);
    color: var(--fg-primary);
  }

  .err {
    font-size: 11px;
    color: var(--error);
    font-family: var(--font-mono);
    padding: var(--space-1) 0;
  }
</style>
