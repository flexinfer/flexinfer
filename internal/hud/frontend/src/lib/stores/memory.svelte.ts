// Memory store - tiered memory management
// v2: SSE-first for stats with 30s fallback. Items still fetched on demand.
import { eventStore } from './events.svelte.ts';
import { arraysEqualById } from '../utils/diff.ts';

export interface TierStats {
  items: number;
  tokens: number;
}

export interface MemoryStats {
  working_memory: TierStats;
  short_term_memory: TierStats;
  long_term_memory: TierStats;
  total_items: number;
  total_tokens: number;
  compression?: {
    ratio: number;
    overall_ratio?: number;
    compressed_items: number;
    tokens_saved: number;
    added_24h?: number;
    compressed_24h?: number;
    expired_24h?: number;
  };
}

export interface MemoryItem {
  id: string;
  title: string;
  content: string;
  tier: 'working' | 'short_term' | 'long_term';
  importance: string | number;
  tokens: number;
  status: string;
  category: string;
  accessed_at: string;
  last_accessed: string;
}

export interface MemoryItemsResponse {
  items: MemoryItem[];
}

class MemoryStore {
  stats = $state<MemoryStats>({
    working_memory: { items: 0, tokens: 0 },
    short_term_memory: { items: 0, tokens: 0 },
    long_term_memory: { items: 0, tokens: 0 },
    total_items: 0,
    total_tokens: 0,
  });
  items = $state<MemoryItem[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  filterTier = $state<string>('all');
  searchQuery = $state<string>('');

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get filteredItems(): MemoryItem[] {
    let result = [...this.items];
    if (this.filterTier !== 'all') {
      result = result.filter((i) => i.tier === this.filterTier);
    }
    if (this.searchQuery) {
      const q = this.searchQuery.toLowerCase();
      result = result.filter(
        (i) => i.title.toLowerCase().includes(q) || i.category.toLowerCase().includes(q)
      );
    }
    return result;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const params = new URLSearchParams();
      if (this.filterTier !== 'all') params.set('tier', this.filterTier);
      if (this.searchQuery) params.set('query', this.searchQuery);
      params.set('limit', '100');

      const [statsRes, itemsRes] = await Promise.all([
        globalThis.fetch('/api/memory/stats'),
        globalThis.fetch(`/api/memory/items?${params.toString()}`),
      ]);

      if (!statsRes.ok) throw new Error(`Memory stats: ${statsRes.status}`);
      if (!itemsRes.ok) throw new Error(`Memory items: ${itemsRes.status}`);

      this.stats = await statsRes.json();
      const data: MemoryItemsResponse = await itemsRes.json();
      this.items = data.items || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  /** Apply stats directly from SSE hud.memory event. */
  applyStats(data: Record<string, unknown>): void {
    // The hud.memory event carries MemoryStatsResult from the monitor.
    // Bridge uses item_count/token_count, but frontend expects items/tokens.
    const mapTier = (raw: Record<string, unknown> | undefined): TierStats => ({
      items: (raw?.item_count as number) ?? (raw?.items as number) ?? 0,
      tokens: (raw?.token_count as number) ?? (raw?.tokens as number) ?? 0,
    });

    const prevTotal = this.stats.total_items;
    this.stats = {
      ...this.stats,
      working_memory: mapTier(data.working_memory as Record<string, unknown>),
      short_term_memory: mapTier(data.short_term_memory as Record<string, unknown>),
      long_term_memory: mapTier(data.long_term_memory as Record<string, unknown>),
      total_items: (data.total_items as number) ?? this.stats.total_items,
      total_tokens: (data.total_tokens as number) ?? this.stats.total_tokens,
    };
    this.lastUpdated = new Date();
    this.error = null;

    // If the item count changed, re-fetch the items list so it stays in sync.
    if (this.stats.total_items !== prevTotal) {
      this.fetchItems();
    }
  }

  /** Fetch only the items list (not stats) to keep items in sync after SSE stat changes. */
  private async fetchItems(): Promise<void> {
    try {
      const params = new URLSearchParams();
      if (this.filterTier !== 'all') params.set('tier', this.filterTier);
      if (this.searchQuery) params.set('query', this.searchQuery);
      params.set('limit', '100');
      const res = await globalThis.fetch(`/api/memory/items?${params.toString()}`);
      if (!res.ok) return;
      const data: MemoryItemsResponse = await res.json();
      const next = data.items || [];
      const hashItem = (i: MemoryItem) => `${i.id}|${i.status}|${i.importance}`;
      if (!arraysEqualById(this.items, next, hashItem)) {
        this.items = next;
      }
    } catch {
      // Non-critical: items will refresh on next poll cycle.
    }
  }

  async promote(itemId: string): Promise<void> {
    try {
      const res = await globalThis.fetch(`/api/memory/${itemId}/promote`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(`Promote: ${res.status}`);
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async demote(itemId: string): Promise<void> {
    try {
      const res = await globalThis.fetch(`/api/memory/${itemId}/demote`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(`Demote: ${res.status}`);
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async addItem(title: string, content: string, tier: string, importance: string, category?: string): Promise<boolean> {
    try {
      const body: Record<string, unknown> = { title, content, tier, importance };
      if (category) body.category = category;
      const res = await globalThis.fetch('/api/memory', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (!res.ok) throw new Error(`Add memory: ${res.status}`);
      await this.fetch();
      return true;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async deleteItem(id: string): Promise<boolean> {
    try {
      const res = await globalThis.fetch(`/api/memory/${id}`, {
        method: 'DELETE',
      });
      if (!res.ok) throw new Error(`Delete memory: ${res.status}`);
      await this.fetch();
      return true;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async fetchCompaction(): Promise<Record<string, unknown> | null> {
    try {
      const res = await globalThis.fetch('/api/memory/compaction');
      if (!res.ok) throw new Error(`Compaction status: ${res.status}`);
      return await res.json();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  async recall(tier?: string, query?: string, limit?: number): Promise<void> {
    if (tier) this.filterTier = tier;
    if (query !== undefined) this.searchQuery = query;
    const params = new URLSearchParams();
    if (tier) params.set('tier', tier);
    if (query) params.set('query', query);
    if (limit) params.set('limit', String(limit));

    this.loading = true;
    this.error = null;
    try {
      const [statsRes, itemsRes] = await Promise.all([
        globalThis.fetch('/api/memory/stats'),
        globalThis.fetch(`/api/memory/items?${params.toString()}`),
      ]);

      if (!statsRes.ok) throw new Error(`Memory stats: ${statsRes.status}`);
      if (!itemsRes.ok) throw new Error(`Memory items: ${itemsRes.status}`);

      this.stats = await statsRes.json();
      const data: MemoryItemsResponse = await itemsRes.json();
      this.items = data.items || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  startPolling(intervalMs = 30000): void {
    this.stopPolling();
    this.fetch();
    // 30s fallback poll (SSE is the primary data source for stats).
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);

    // Subscribe to SSE events: apply stats directly from hud.memory snapshots.
    this.eventUnsubs.push(
      eventStore.on('hud.memory', (e) => this.applyStats(e.data)),
      // Granular memory mutation events — trigger full refresh for items + stats.
      eventStore.on('hud.memory.add', () => this.fetch()),
      eventStore.on('hud.memory.delete', () => this.fetch()),
      eventStore.on('hud.memory.promote', () => this.fetch()),
      eventStore.on('hud.memory.demote', () => this.fetch()),
    );
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

export const memoryStore = new MemoryStore();
