/**
 * EventSource store — connects to /api/events SSE stream and dispatches
 * incoming daemon events to registered listeners. Stores can subscribe to
 * specific event types to trigger immediate data refreshes instead of
 * waiting for the next poll tick.
 */

type SSEEvent = {
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

  private source: EventSource | null = null;
  private listeners: Map<string, EventListener[]> = new Map();
  private anyListeners: EventListener[] = [];
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;

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
    for (const type of ['server.health', 'config.reload', 'process.start', 'process.stop', 'workflow.step']) {
      this.source.addEventListener(type, (e: MessageEvent) => {
        this.handleEvent(e.data, type);
      });
    }

    this.source.onerror = () => {
      this.connected = false;
      this.source?.close();
      this.source = null;

      // Auto-reconnect after 5 seconds.
      this.reconnectTimer = setTimeout(() => this.connect(), 5000);
    };
  }

  /** Disconnect from the SSE endpoint. */
  disconnect() {
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.source) {
      this.source.close();
      this.source = null;
    }
    this.connected = false;
  }

  private handleEvent(raw: string, fallbackType?: string) {
    try {
      const event: SSEEvent = JSON.parse(raw);
      if (!event.type && fallbackType) {
        event.type = fallbackType;
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
