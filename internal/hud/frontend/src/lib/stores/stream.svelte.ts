// Stream store - live context stream

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
      const newEntries = data.entries || [];

      if (newEntries.length > 0) {
        // Prepend new entries, keep max 500
        this.entries = [...newEntries, ...this.entries].slice(0, 500);
        // Track latest timestamp for incremental fetching
        this.lastTimestamp = newEntries[0].timestamp;
      }

      this.lastUpdated = new Date();
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
  }

  startPolling(intervalMs = 3000): void {
    this.stopPolling();
    this.fetch();
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }
}

export const streamStore = new StreamStore();
