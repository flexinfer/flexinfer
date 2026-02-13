// Timeline store - backed by initial HTTP load + SSE subscription.
import { eventStore } from './events.svelte.ts';

export interface TimelineEntry {
  timestamp: string;
  event_type: string;
  agent_id?: string;
  agent_type?: string;
  data?: Record<string, unknown>;
}

class TimelineStore {
  entries = $state<TimelineEntry[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);

  private eventUnsubs: Array<() => void> = [];
  private pollTimer: ReturnType<typeof setInterval> | null = null;

  async fetch(limit = 200): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch(`/api/timeline?limit=${limit}`);
      if (!res.ok) throw new Error(`Timeline API: ${res.status}`);
      const data = await res.json();
      this.entries = data.entries ?? [];
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();

    // 30s fallback poll.
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);

    // Subscribe to all agent.* SSE events for live updates.
    const agentEvents = [
      'agent.session.start', 'agent.session.end', 'agent.session.reaped',
      'agent.heartbeat', 'agent.task.update', 'agent.task.dispatched',
      'hud.conflict', 'hud.approval_needed', 'hud.claim.released',
    ];

    for (const eventType of agentEvents) {
      this.eventUnsubs.push(
        eventStore.on(eventType, (e) => {
          const entry: TimelineEntry = {
            timestamp: new Date().toISOString(),
            event_type: eventType,
            agent_id: (e.data as Record<string, unknown>)?.agent_id as string | undefined,
            agent_type: (e.data as Record<string, unknown>)?.agent_type as string | undefined,
            data: e.data as Record<string, unknown>,
          };
          // Prepend (newest first) and cap at 500.
          this.entries = [entry, ...this.entries].slice(0, 500);
        }),
      );
    }
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    for (const unsub of this.eventUnsubs) unsub();
    this.eventUnsubs = [];
  }
}

export const timelineStore = new TimelineStore();
