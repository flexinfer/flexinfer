<script lang="ts">
  import { onDestroy, onMount } from 'svelte';

  // F9 — Live file-claim conflict overlay.
  //
  // Subscribes to /api/fleet/claims/stream (SSE). Each event:
  //   { file, holder, requester, ts }
  //
  // Displays a pulsing chip with the count of events received in the
  // trailing 60s. Clicking reveals the last 5 events inline.

  type Event = {
    file: string;
    holder: string;
    requester: string;
    ts: string; // RFC3339Nano
    receivedAt: number;
  };

  const WINDOW_MS = 60_000;
  const HISTORY_CAP = 5;

  let events = $state<Event[]>([]);
  let expanded = $state(false);
  let source: EventSource | null = null;
  let pruneHandle: number | null = null;

  const recentCount = $derived(
    events.filter((e) => Date.now() - e.receivedAt <= WINDOW_MS).length,
  );

  function prune() {
    const cutoff = Date.now() - WINDOW_MS;
    events = events.filter((e) => e.receivedAt >= cutoff).slice(-HISTORY_CAP);
  }

  function onEvent(ev: MessageEvent) {
    try {
      const data = JSON.parse(ev.data) as {
        file: string;
        holder: string;
        requester: string;
        ts: string;
      };
      events = [
        ...events,
        { ...data, receivedAt: Date.now() },
      ].slice(-HISTORY_CAP);
    } catch {
      // Ignore malformed frames.
    }
  }

  onMount(() => {
    if (typeof EventSource === 'undefined') {
      return;
    }
    try {
      source = new EventSource('/api/fleet/claims/stream');
      source.addEventListener('claim.conflict', onEvent);
    } catch {
      source = null;
    }
    pruneHandle = window.setInterval(prune, 5_000);
  });

  onDestroy(() => {
    if (source) {
      source.close();
      source = null;
    }
    if (pruneHandle !== null) {
      clearInterval(pruneHandle);
      pruneHandle = null;
    }
  });

  function shortFile(path: string) {
    const parts = path.split('/');
    return parts.length > 2 ? `…/${parts.slice(-2).join('/')}` : path;
  }

  function formatTimestamp(iso: string) {
    try {
      return new Date(iso).toLocaleTimeString();
    } catch {
      return iso;
    }
  }
</script>

<div class="claim-conflict-chip" class:active={recentCount > 0}>
  <button
    type="button"
    class="chip-button"
    onclick={() => (expanded = !expanded)}
    aria-expanded={expanded}
    aria-label="File claim conflicts in the last 60 seconds"
  >
    <span class="dot" class:pulse={recentCount > 0}></span>
    <span class="label">Claim conflicts</span>
    <span class="count">{recentCount}</span>
  </button>

  {#if expanded && events.length > 0}
    <ul class="event-list">
      {#each events.slice().reverse() as evt (evt.ts + evt.file)}
        <li class="event">
          <span class="event-file">{shortFile(evt.file)}</span>
          <span class="event-meta">
            <strong>{evt.requester}</strong> vs <strong>{evt.holder}</strong>
            <time>{formatTimestamp(evt.ts)}</time>
          </span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .claim-conflict-chip {
    display: inline-flex;
    flex-direction: column;
    gap: 0.25rem;
    font-size: 0.75rem;
  }
  .chip-button {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.25rem 0.6rem;
    border-radius: 999px;
    background: var(--surface-subtle, rgba(255, 255, 255, 0.04));
    border: 1px solid var(--border-subtle, rgba(255, 255, 255, 0.1));
    color: var(--text-muted, #b0b0b0);
    cursor: pointer;
    transition: background 120ms ease, border-color 120ms ease, color 120ms ease;
  }
  .chip-button:hover {
    background: var(--surface, rgba(255, 255, 255, 0.08));
    color: var(--text, #e6e6e6);
  }
  .claim-conflict-chip.active .chip-button {
    border-color: var(--danger, #d86868);
    color: var(--danger-text, #f2b8b8);
  }
  .dot {
    width: 0.5rem;
    height: 0.5rem;
    border-radius: 50%;
    background: var(--border-subtle, rgba(255, 255, 255, 0.15));
  }
  .dot.pulse {
    background: var(--danger, #d86868);
    animation: pulse 1.2s ease-in-out infinite;
  }
  .count {
    font-variant-numeric: tabular-nums;
    font-weight: 600;
  }
  .event-list {
    list-style: none;
    margin: 0;
    padding: 0.25rem 0;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .event {
    display: flex;
    flex-direction: column;
    padding: 0.25rem 0.5rem;
    background: var(--surface-subtle, rgba(0, 0, 0, 0.2));
    border-left: 2px solid var(--danger, #d86868);
    border-radius: 0 4px 4px 0;
  }
  .event-file {
    font-family: var(--font-mono, monospace);
    font-size: 0.7rem;
    color: var(--text, #e6e6e6);
  }
  .event-meta {
    display: flex;
    gap: 0.5rem;
    align-items: center;
    color: var(--text-muted, #b0b0b0);
  }
  .event-meta time {
    margin-left: auto;
    font-variant-numeric: tabular-nums;
  }
  @keyframes pulse {
    0%, 100% { transform: scale(1); opacity: 1; }
    50% { transform: scale(1.25); opacity: 0.6; }
  }
</style>
