<script lang="ts">
  import { spawnStore, type SpawnActivityEvent } from '../../stores/spawn.svelte.ts';

  interface Props {
    spawnId: string;
  }

  let { spawnId }: Props = $props();

  let events = $derived(spawnStore.activityBySpawnId.get(spawnId) ?? []);
  let reversed = $derived([...events].reverse());

  function icon(type: string): string {
    switch (type) {
      case 'message': return '\u{1F4AC}';
      case 'thinking': return '\u{1F4AD}';
      case 'reasoning': return '\u{1F9E0}';
      case 'todo': return '\u{2611}';
      case 'tool_start': return '\u{1F527}';
      case 'tool_complete': return '\u{2705}';
      case 'file_change': return '\u{1F4C4}';
      case 'result': return '\u{1F3C1}';
      case 'rate_limit': return '\u{23F3}';
      default: return '\u{25CF}';
    }
  }

  function label(type: string): string {
    switch (type) {
      case 'message': return 'Message';
      case 'thinking': return 'Thinking';
      case 'reasoning': return 'Reasoning';
      case 'todo': return 'Todo';
      case 'tool_start': return 'Tool start';
      case 'tool_complete': return 'Tool done';
      case 'file_change': return 'File change';
      case 'result': return 'Result';
      case 'rate_limit': return 'Rate limit';
      default: return type;
    }
  }

  function formatTime(ts: string): string {
    try {
      const d = new Date(ts);
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    } catch { return ''; }
  }

  function summary(ev: SpawnActivityEvent): string {
    const d = ev.data;
    switch (ev.type) {
      case 'message':
        return truncate(String(d.text ?? ''), 200);
      case 'thinking':
        return truncate(String(d.thinking ?? ''), 120);
      case 'reasoning':
        return truncate(String(d.text ?? ''), 120);
      case 'todo':
        return truncate(String(d.text ?? ''), 120);
      case 'tool_start': {
        const name = String(d.name ?? 'unknown');
        const server = d.server_name ? ` (${d.server_name})` : '';
        return `${name}${server}`;
      }
      case 'tool_complete': {
        const name = d.tool ? String(d.tool) : d.command ? String(d.command) : d.id ? String(d.id) : '';
        const dur = typeof d.duration_ms === 'number' ? ` ${d.duration_ms}ms` : '';
        const err = d.is_error ? ' \u274C' : d.error ? ` \u274C ${d.error}` : '';
        const exit = typeof d.exit_code === 'number' ? ` exit=${d.exit_code}` : '';
        return `${name}${dur}${exit}${err}`;
      }
      case 'file_change': {
        const changes = Array.isArray(d.changes) ? d.changes : [];
        return changes.map((c: Record<string, unknown>) => `${c.kind} ${c.path}`).join(', ');
      }
      case 'result': {
        const reason = d.stop_reason ? String(d.stop_reason) : '';
        const cost = typeof d.cost_usd === 'number' ? ` $${(d.cost_usd as number).toFixed(4)}` : '';
        const turns = typeof d.turns === 'number' ? ` ${d.turns}t` : '';
        return `${reason}${turns}${cost}`;
      }
      case 'rate_limit':
        return `attempt ${d.attempt ?? '?'} status ${d.status ?? '?'}`;
      default:
        return JSON.stringify(d).slice(0, 100);
    }
  }

  function truncate(s: string, max: number): string {
    return s.length > max ? s.slice(0, max) + '\u2026' : s;
  }
</script>

<div class="activity-tab">
  {#if reversed.length === 0}
    <div class="empty">No activity events yet. Events stream in real-time while the spawn is running.</div>
  {:else}
    <div class="event-count">{events.length} event{events.length === 1 ? '' : 's'}</div>
    <div class="event-list">
      {#each reversed as ev, i (i)}
        <div class="event-row" class:is-error={ev.type === 'tool_complete' && (ev.data.is_error || ev.data.error)} class:is-result={ev.type === 'result'}>
          <span class="event-icon">{icon(ev.type)}</span>
          <span class="event-time">{formatTime(ev.timestamp)}</span>
          <span class="event-label">{label(ev.type)}</span>
          <span class="event-summary">{summary(ev)}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .activity-tab {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .empty {
    padding: var(--space-3);
    color: var(--fg-dim);
    font-size: var(--text-sm);
    font-family: var(--font-mono);
  }

  .event-count {
    font-size: var(--text-xs);
    color: var(--fg-dim);
    font-family: var(--font-mono);
    padding: 0 var(--space-1);
  }

  .event-list {
    display: flex;
    flex-direction: column;
    gap: 1px;
    max-height: 28rem;
    overflow-y: auto;
  }

  .event-row {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
    padding: 3px var(--space-2);
    font-size: var(--text-xs);
    font-family: var(--font-mono);
    border-radius: var(--radius-xs);
    transition: background var(--transition-fast);
  }

  .event-row:hover {
    background: var(--bg-secondary);
  }

  .event-row.is-error {
    background: rgba(255, 61, 113, 0.06);
  }

  .event-row.is-result {
    background: rgba(129, 240, 254, 0.06);
  }

  .event-icon {
    flex-shrink: 0;
    width: 1.2em;
    text-align: center;
  }

  .event-time {
    flex-shrink: 0;
    color: var(--fg-dim);
    min-width: 5.5em;
  }

  .event-label {
    flex-shrink: 0;
    color: var(--fg-secondary);
    min-width: 6em;
    font-weight: 500;
  }

  .event-summary {
    color: var(--fg-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }
</style>
