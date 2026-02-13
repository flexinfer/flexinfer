// Stream store - live context stream with SSE-first data flow

import { eventStore } from './events.svelte.ts';

export interface StreamEntry {
  id: string;
  entry_type: string;
  agent_id: string;
  agent: string;
  namespace: string;
  title: string;
  timestamp: string;
  content: string;
}

export interface StreamResponse {
  entries: StreamEntry[];
}

class StreamStore {
  entries = $state<StreamEntry[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  paused = $state(false);
  filterType = $state<string>('all');
  filterAgent = $state<string>('all');

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private lastTimestamp: string | null = null;
  private eventUnsubs: Array<() => void> = [];
  private seenIds = new Set<string>();

  get filteredEntries(): StreamEntry[] {
    let result = [...this.entries];
    if (this.filterType !== 'all') {
      result = result.filter((e) => e.entry_type === this.filterType);
    }
    if (this.filterAgent !== 'all') {
      result = result.filter((e) => e.agent === this.filterAgent);
    }
    return result;
  }

  get entryTypes(): string[] {
    const types = new Set(this.entries.map((e) => e.entry_type));
    return Array.from(types).sort();
  }

  get agents(): string[] {
    const agents = new Set(this.entries.map((e) => e.agent).filter(Boolean));
    return Array.from(agents).sort();
  }

  /** Apply entries from either SSE or HTTP, deduplicating against existing IDs. */
  private applyEntries(newEntries: StreamEntry[]): void {
    if (!newEntries || newEntries.length === 0) return;

    const unique = newEntries.filter((e) => {
      if (!e.id || this.seenIds.has(e.id)) return false;
      this.seenIds.add(e.id);
      return true;
    });

    if (unique.length === 0) return;

    // Prepend new entries, keep max 500.
    this.entries = [...unique, ...this.entries].slice(0, 500);

    // Trim seenIds to match entries.
    if (this.seenIds.size > 600) {
      this.seenIds = new Set(this.entries.map((e) => e.id));
    }

    // Track latest timestamp for incremental HTTP fetching.
    if (unique[0].timestamp) {
      this.lastTimestamp = unique[0].timestamp;
    }

    this.lastUpdated = new Date();
  }

  async fetch(): Promise<void> {
    if (this.paused) return;

    this.loading = true;
    this.error = null;
    try {
      const params = new URLSearchParams();
      if (this.lastTimestamp) {
        params.set('since', this.lastTimestamp);
      }
      params.set('limit', '100');

      const res = await globalThis.fetch(`/api/stream?${params.toString()}`);
      if (!res.ok) throw new Error(`Stream API: ${res.status}`);

      const data: StreamResponse = await res.json();
      this.applyEntries(data.entries || []);
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  togglePause(): void {
    this.paused = !this.paused;
  }

  clear(): void {
    this.entries = [];
    this.lastTimestamp = null;
    this.seenIds.clear();
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();

    // Subscribe to SSE events for real-time delivery.
    const unsub = eventStore.on('hud.stream', (event) => {
      if (this.paused) return;
      const data = event.data as Record<string, unknown>;
      if (data && Array.isArray(data.entries)) {
        this.applyEntries(data.entries as StreamEntry[]);
      }
    });
    this.eventUnsubs.push(unsub);

    // Initial HTTP fetch + slow fallback polling.
    this.fetch();
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    for (const unsub of this.eventUnsubs) {
      unsub();
    }
    this.eventUnsubs = [];
  }
}

export const streamStore = new StreamStore();
