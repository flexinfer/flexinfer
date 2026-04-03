// Catalog store - browsable registry with enable/disable toggles
import { arraysEqualByKey } from '../utils/diff.ts';

export interface CatalogServer {
  name: string;
  description: string;
  categories: string[];
  enabled: boolean;
  running: boolean;
}

export interface CatalogResponse {
  servers: CatalogServer[];
  count: number;
  registry_path: string;
}

class CatalogStore {
  servers = $state<CatalogServer[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);
  registryPath = $state('');

  searchQuery = $state('');
  categoryFilter = $state('all');
  statusFilter = $state<'all' | 'enabled' | 'disabled' | 'running'>('all');

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  get categories(): string[] {
    const set = new Set<string>();
    for (const srv of this.servers) {
      for (const cat of srv.categories ?? []) {
        set.add(cat);
      }
    }
    return [...set].sort();
  }

  get runningCount(): number {
    return this.servers.filter((s) => s.running).length;
  }

  get filteredServers(): CatalogServer[] {
    let result = [...this.servers];
    if (this.statusFilter === 'enabled') result = result.filter((s) => s.enabled);
    else if (this.statusFilter === 'disabled') result = result.filter((s) => !s.enabled);
    else if (this.statusFilter === 'running') result = result.filter((s) => s.running);
    if (this.categoryFilter !== 'all') {
      result = result.filter((s) =>
        s.categories?.some((c) => c.toLowerCase() === this.categoryFilter.toLowerCase())
      );
    }
    if (this.searchQuery) {
      const q = this.searchQuery.toLowerCase();
      result = result.filter((s) =>
        s.name.toLowerCase().includes(q) ||
        s.description?.toLowerCase().includes(q) ||
        s.categories?.some((c) => c.toLowerCase().includes(q))
      );
    }
    return result;
  }

  get enabledCount(): number {
    return this.servers.filter((s) => s.enabled).length;
  }

  get disabledCount(): number {
    return this.servers.filter((s) => !s.enabled).length;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const params = new URLSearchParams();
      if (this.categoryFilter !== 'all') {
        params.set('category', this.categoryFilter);
      }
      const url = params.toString() ? `/api/catalog?${params}` : '/api/catalog';
      const res = await globalThis.fetch(url);
      if (!res.ok) throw new Error(`Catalog API: ${res.status}`);

      const data: CatalogResponse = await res.json();
      const incoming = data.servers ?? [];

      const keyFn = (s: CatalogServer) => s.name;
      const hashFn = (s: CatalogServer) => `${s.enabled}|${s.running}`;
      if (!arraysEqualByKey(this.servers, incoming, keyFn, hashFn)) {
        this.servers = incoming;
      }
      this.registryPath = data.registry_path ?? '';
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  async toggleServer(name: string, enable: boolean): Promise<void> {
    const action = enable ? 'enable' : 'disable';
    try {
      const res = await globalThis.fetch(`/api/catalog/${encodeURIComponent(name)}/${action}`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(`Toggle ${action}: ${res.status}`);
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  search(query: string): void {
    this.searchQuery = query;
  }

  filterByCategory(category: string): void {
    this.categoryFilter = category;
    this.fetch();
  }

  filterByStatus(status: 'all' | 'enabled' | 'disabled' | 'running'): void {
    this.statusFilter = status;
  }

  startPolling(intervalMs = 30000): void {
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

export const catalogStore = new CatalogStore();
