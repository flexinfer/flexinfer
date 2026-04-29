<script lang="ts">
  // TranscriptTab fetches GET /api/agent/spawn/{id}/trace and renders the
  // persisted conversation transcript: assistant text, thinking blocks
  // (Claude), reasoning items + todo lists (Codex), terminal result.
  //
  // Companion to ActivityTab (live SSE events, ephemeral). This tab is the
  // durable view: surviving page reloads as long as the daemon process holds
  // telemetry in memory. Empty when the daemon was restarted or the spawn
  // has not emitted any text content yet.

  import type { Message, SpawnTraceResponse } from './types.ts';
  import { adminFetch } from '../../stores/labsAuth.svelte.ts';

  interface Props {
    spawnId: string;
  }

  let { spawnId }: Props = $props();

  let messages = $state<Message[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  let initialized = $state(false);

  async function load(): Promise<void> {
    if (!spawnId) return;
    loading = true;
    error = null;
    try {
      const res = await adminFetch(
        `/api/agent/spawn/${encodeURIComponent(spawnId)}/trace`,
        { requireToken: true, action: 'Loading spawn transcript' },
      );
      if (!res.ok) {
        // Surface daemon error body when present (e.g. admin-token messages),
        // mirroring EconomicsPanel.svelte pattern.
        let detail = `HTTP ${res.status}`;
        try {
          const body = await res.json();
          if (body && typeof body.error === 'string' && body.error.length > 0) {
            detail = body.error;
          }
        } catch {
          /* not JSON — keep status fallback */
        }
        throw new Error(detail);
      }
      const data = (await res.json()) as SpawnTraceResponse;
      messages = data.messages ?? [];
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      loading = false;
      initialized = true;
    }
  }

  $effect(() => {
    // Re-fetch whenever the active spawn changes.
    void spawnId;
    messages = [];
    initialized = false;
    void load();
  });

  // Best-effort poll while the panel is open so live spawns stream their
  // transcript without forcing the user to hit refresh. 5s feels right —
  // matches the existing spawn-detail polling cadence.
  $effect(() => {
    if (!spawnId) return;
    const id = setInterval(() => void load(), 5000);
    return () => clearInterval(id);
  });

  function kindLabel(kind: string): string {
    switch (kind) {
      case 'text': return 'Message';
      case 'thinking': return 'Thinking';
      case 'reasoning': return 'Reasoning';
      case 'todo': return 'Todo';
      case 'result': return 'Result';
      default: return kind;
    }
  }

  function kindIcon(kind: string): string {
    switch (kind) {
      case 'text': return '\u{1F4AC}';
      case 'thinking': return '\u{1F4AD}';
      case 'reasoning': return '\u{1F9E0}';
      case 'todo': return '\u{2611}';
      case 'result': return '\u{1F3C1}';
      default: return '\u{25CF}';
    }
  }

  function formatTime(iso: string): string {
    if (!iso) return '';
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleTimeString();
  }
</script>

<div class="tab-content">
  {#if error}
    <div class="tab-error">error: {error}</div>
  {:else if !initialized && loading}
    <div class="tab-loading">Loading transcript…</div>
  {:else if messages.length === 0}
    <div class="tab-empty">No transcript yet — agent has not emitted text.</div>
  {:else}
    <div class="messages-list" aria-label="Spawn transcript">
      {#each messages as m, i (i)}
        <article class="message" class:thinking={m.kind === 'thinking'} class:result={m.kind === 'result'}>
          <header class="message-head">
            <span class="kind-icon" aria-hidden="true">{kindIcon(m.kind)}</span>
            <span class="kind-label">{kindLabel(m.kind)}</span>
            {#if m.role && m.role !== 'assistant'}
              <span class="role-tag">{m.role}</span>
            {/if}
            <time class="message-time" datetime={m.time}>{formatTime(m.time)}</time>
          </header>
          <pre class="message-body">{m.text}</pre>
        </article>
      {/each}
    </div>
    <div class="tab-footer">
      {messages.length} message{messages.length === 1 ? '' : 's'}
      {#if loading}<span class="poll-indicator">· refreshing…</span>{/if}
    </div>
  {/if}
</div>

<style>
  .tab-content {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding: var(--space-3);
    background: var(--bg-secondary);
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    font-family: var(--font-mono);
  }

  .tab-loading,
  .tab-empty {
    padding: var(--space-2);
    color: var(--fg-secondary);
    font-size: var(--text-sm);
  }

  .tab-error {
    padding: var(--space-2);
    color: var(--error);
    font-size: var(--text-sm);
  }

  .messages-list {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    max-height: 28rem;
    overflow-y: auto;
  }

  .message {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-2);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    background: var(--bg-primary);
  }

  .message.thinking {
    background: var(--bg-tertiary, var(--bg-primary));
    opacity: 0.85;
    font-style: italic;
  }

  .message.result {
    border-color: var(--border-active, var(--border));
  }

  .message-head {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    font-size: var(--text-xs);
    color: var(--fg-secondary);
  }

  .kind-icon {
    font-style: normal;
  }

  .kind-label {
    font-weight: 500;
    color: var(--fg-primary);
  }

  .role-tag {
    text-transform: lowercase;
    color: var(--fg-dim);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-xs);
    padding: 0 var(--space-1);
  }

  .message-time {
    margin-left: auto;
    color: var(--fg-dim);
    font-variant-numeric: tabular-nums;
  }

  .message-body {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--fg-primary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.45;
  }

  .tab-footer {
    font-size: var(--text-xs);
    color: var(--fg-dim);
  }

  .poll-indicator {
    margin-left: var(--space-1);
    color: var(--fg-dim);
  }
</style>
