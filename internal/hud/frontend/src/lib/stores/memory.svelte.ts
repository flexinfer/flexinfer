// Memory store - tiered memory management

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
  tiers?: {
    working?: TierStats;
    short_term?: TierStats;
    long_term?: TierStats;
  };
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

  startPolling(intervalMs = 10000): void {
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

export const memoryStore = new MemoryStore();
