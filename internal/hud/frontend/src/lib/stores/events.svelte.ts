/**
 * EventSource store — connects to /api/events SSE stream and dispatches
 * incoming daemon events to registered listeners. Stores can subscribe to
 * specific event types to trigger immediate data refreshes instead of
 * waiting for the next poll tick.
 *
 * v2: Supports HUD-specific events (hud.fleet, hud.health, hud.memory,
 * hud.workflows) that carry full data snapshots for SSE-first data flow.
 */

export type SSEEvent = {
  id: string;
  type: string;
  timestamp: string;
  data: Record<string, unknown>;
};

type EventListener = (event: SSEEvent) => void;

class EventStore {
  connected = $state(false);
  lastEvent: SSEEvent | null = $state(null);
  eventCount = $state(0);

  /** Connection state for the banner: 'connected' | 'reconnecting' | 'disconnected' | 'circuit-open' */
  connectionState = $state<'connected' | 'reconnecting' | 'disconnected' | 'circuit-open'>('disconnected');
  /** Seconds until next reconnect attempt (shown in banner). */
  retryCountdown = $state(0);

  private source: EventSource | null = null;
  private listeners: Map<string, EventListener[]> = new Map();
  private anyListeners: EventListener[] = [];
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private countdownTimer: ReturnType<typeof setInterval> | null = null;
  private consecutiveErrors = 0;
  private static readonly MAX_RECONNECT_ERRORS = 5;
  private static readonly BASE_RECONNECT_MS = 5000;

  /** Register a listener for a specific event type. Returns an unsubscribe function. */
  on(eventType: string, listener: EventListener): () => void {
    const list = this.listeners.get(eventType) ?? [];
    list.push(listener);
    this.listeners.set(eventType, list);
    return () => {
      const arr = this.listeners.get(eventType);
      if (arr) {
        const idx = arr.indexOf(listener);
        if (idx >= 0) arr.splice(idx, 1);
      }
    };
  }

  /** Register a listener that fires for every event. Returns an unsubscribe function. */
  onAny(listener: EventListener): () => void {
    this.anyListeners.push(listener);
    return () => {
      const idx = this.anyListeners.indexOf(listener);
      if (idx >= 0) this.anyListeners.splice(idx, 1);
    };
  }

  /** Connect to the SSE endpoint. Safe to call multiple times. */
  connect() {
    if (this.source) return;

    this.source = new EventSource('/api/events');

    this.source.addEventListener('connected', (e: MessageEvent) => {
      this.connected = true;
      this.connectionState = 'connected';
      this.consecutiveErrors = 0;
      this.clearCountdown();
      try {
        const data = JSON.parse(e.data);
        this.dispatch({ id: 'connected', type: 'connected', timestamp: new Date().toISOString(), data });
      } catch { /* ignore */ }
    });

    this.source.addEventListener('heartbeat', () => {
      // Heartbeats confirm connectivity — no dispatch needed.
    });

    // The default "message" event catches all named events sent with "event:" SSE field.
    // However, EventSource only fires "message" for events WITHOUT a named type.
    // For named events, we need to listen for them explicitly.
    // Since we don't know all event types upfront, use onmessage for unnamed + specific listeners.
    this.source.onmessage = (e: MessageEvent) => {
      this.handleEvent(e.data);
    };

    // Listen for known daemon event types.
    const knownTypes = [
      'server.health', 'config.reload', 'process.start', 'process.stop', 'workflow.step',
      // HUD-specific snapshot events (SSE-first data flow).
      'hud.fleet', 'hud.health', 'hud.memory', 'hud.workflows', 'hud.stream',
      'hud.sandbox', 'hud.sandbox.event',
      // Granular agent lifecycle events (real-time deltas, <100ms latency).
      'agent.session.start', 'agent.session.end', 'agent.session.reaped', 'agent.session.bootstrap',
      'agent.heartbeat', 'agent.task.update', 'agent.task.dispatched',
      'agent.spawn.building', 'agent.spawn.running', 'agent.spawn.completed', 'agent.spawn.failed', 'agent.spawn.stopped',
      'agent.spawn.telemetry.delta',
      // Spawn activity events (for live activity feed).
      'agent.spawn.message', 'agent.spawn.thinking', 'agent.spawn.reasoning', 'agent.spawn.todo',
      'agent.spawn.tool_start', 'agent.spawn.tool_complete', 'agent.spawn.file_change',
      'agent.spawn.result', 'agent.spawn.rate_limit',
      // Proactive notification events.
      'hud.conflict', 'hud.approval_needed', 'hud.claim.released',
      // Granular memory mutation events.
      'hud.memory.add', 'hud.memory.delete', 'hud.memory.promote', 'hud.memory.demote',
      // Granular workflow/task mutation events.
      'hud.workflow.approve', 'hud.workflow.reject', 'hud.task.create',
      // Handoff events.
      'hud.handoff.created',
    ];
    for (const type of knownTypes) {
      this.source.addEventListener(type, (e: MessageEvent) => {
        this.handleEvent(e.data, type);
      });
    }

    this.source.onerror = () => {
      this.connected = false;
      this.source?.close();
      this.source = null;
      this.consecutiveErrors++;

      // Circuit breaker: after repeated failures, open circuit with longer cooldown.
      const isCircuitOpen = this.consecutiveErrors >= EventStore.MAX_RECONNECT_ERRORS;
      const delayMs = isCircuitOpen
        ? EventStore.BASE_RECONNECT_MS * Math.min(this.consecutiveErrors, 12)
        : EventStore.BASE_RECONNECT_MS;

      this.connectionState = isCircuitOpen ? 'circuit-open' : 'reconnecting';
      this.startCountdown(delayMs);

      this.reconnectTimer = setTimeout(() => this.connect(), delayMs);
    };
  }

  /** Disconnect from the SSE endpoint. */
  disconnect() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.clearCountdown();
    if (this.source) {
      this.source.close();
      this.source = null;
    }
    this.connected = false;
    this.connectionState = 'disconnected';
  }

  /** Start a visual countdown timer for the banner. */
  private startCountdown(durationMs: number) {
    this.clearCountdown();
    this.retryCountdown = Math.ceil(durationMs / 1000);
    this.countdownTimer = setInterval(() => {
      this.retryCountdown = Math.max(0, this.retryCountdown - 1);
      if (this.retryCountdown <= 0) this.clearCountdown();
    }, 1000);
  }

  /** Clear the countdown timer. */
  private clearCountdown() {
    if (this.countdownTimer) {
      clearInterval(this.countdownTimer);
      this.countdownTimer = null;
    }
    this.retryCountdown = 0;
  }

  private handleEvent(raw: string, fallbackType?: string) {
    try {
      const event: SSEEvent = JSON.parse(raw);
      if (!event.type && fallbackType) {
        event.type = fallbackType;
      }
      // For HUD and agent events, the data payload is nested inside the event's
      // top-level "data" field. Parse it if it's a string (from json.RawMessage).
      if ((event.type?.startsWith('hud.') || event.type?.startsWith('agent.')) && typeof event.data === 'string') {
        try {
          event.data = JSON.parse(event.data as unknown as string);
        } catch { /* keep as-is */ }
      }
      this.eventCount++;
      this.lastEvent = event;
      this.dispatch(event);
    } catch {
      // Ignore unparseable events.
    }
  }

  private dispatch(event: SSEEvent) {
    // Type-specific listeners.
    const typed = this.listeners.get(event.type);
    if (typed) {
      for (const fn of typed) fn(event);
    }

    // Wildcard listeners.
    for (const fn of this.anyListeners) fn(event);
  }
}

export const eventStore = new EventStore();
