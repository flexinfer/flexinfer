<script lang="ts">
  import type { CardSpec } from '../../utils/inbox.ts';
  import InboxCard from './InboxCard.svelte';

  interface Props {
    cards: CardSpec[];
  }
  let { cards }: Props = $props();
</script>

<section class="inbox" aria-label="Operator inbox">
  <header class="inbox-head">
    <span class="inbox-label">Inbox</span>
    <span class="inbox-count">
      {cards.length === 0 ? 'clear' : `${cards.length} pressure point${cards.length === 1 ? '' : 's'}`}
    </span>
  </header>

  {#if cards.length === 0}
    <div class="empty">
      <div class="empty-mark" aria-hidden="true">·</div>
      <div class="empty-title">System nominal</div>
      <div class="empty-detail">No conflicts, no blocks, no approvals waiting.</div>
    </div>
  {:else}
    <div class="deck">
      {#each cards as card (card.key)}
        <InboxCard {card} />
      {/each}
    </div>
  {/if}
</section>

<style>
  .inbox {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .inbox-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-3);
  }

  .inbox-label {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-secondary);
  }

  .inbox-count {
    font-size: 11px;
    color: var(--fg-muted);
    font-family: var(--font-mono);
  }

  .deck {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 320px), 1fr));
    gap: var(--space-3);
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--space-1);
    padding: var(--space-5) var(--space-4);
    border-radius: var(--radius-md);
    border: 1px dashed var(--border);
    background: var(--bg-secondary);
    color: var(--fg-muted);
    text-align: center;
  }

  .empty-mark {
    font-size: 28px;
    line-height: 1;
    color: var(--success);
    opacity: 0.6;
  }

  .empty-title {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-secondary);
  }

  .empty-detail {
    font-size: 11px;
    font-family: var(--font-mono);
  }
</style>
