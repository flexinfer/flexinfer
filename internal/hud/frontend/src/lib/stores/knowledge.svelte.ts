// Knowledge store - cross-agent context aggregation
import { arraysEqualById } from '../utils/diff.ts';

export interface KnowledgeEntry {
  id: string;
  agent_id: string;
  session_id: string;
  namespace: string;
  entry_type: string;
  title: string;
  content: string;
  file_path: string;
  tags: string[];
  timestamp: string;
  token_count: number;
  metadata: Record<string, unknown>;
}

export interface KnowledgeResponse {
  ok: boolean;
  entries: KnowledgeEntry[];
  grouped: Record<string, KnowledgeEntry[]>;
  count: number;
  total_tokens: number;
  token_budget: number;
}

class KnowledgeStore {
  entries = $state<KnowledgeEntry[]>([]);
  grouped = $state<Record<string, KnowledgeEntry[]>>({});
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  totalTokens = $state(0);
  tokenBudget = $state(0);

  searchQuery = $state('');
  filterCategory = $state('all');
  filterAgent = $state('all');

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  get categories(): string[] {
    return Object.keys(this.grouped).sort();
  }

  get agents(): string[] {
    const agents = new Set(this.entries.map((e) => e.agent_id).filter(Boolean));
    return Array.from(agents).sort();
  }

  get filteredEntries(): KnowledgeEntry[] {
    let result = [...this.entries];
    if (this.filterCategory !== 'all') {
      result = result.filter((e) => e.entry_type === this.filterCategory);
    }
    if (this.filterAgent !== 'all') {
      result = result.filter((e) => e.agent_id === this.filterAgent);
    }
    return result;
  }

  async fetch(query?: string): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const params = new URLSearchParams();
      if (query || this.searchQuery) {
        params.set('query', query || this.searchQuery);
      }
      if (this.filterCategory !== 'all') {
        params.set('category', this.filterCategory);
      }
      params.set('budget', '8000');

      const res = await globalThis.fetch(`/api/knowledge?${params.toString()}`);
      if (!res.ok) throw new Error(`Knowledge API: ${res.status}`);

      const data: KnowledgeResponse = await res.json();
      const newEntries = data.entries ?? [];
      const hashFn = (e: KnowledgeEntry) => `${e.entry_type}|${e.title}|${e.timestamp}`;
      if (!arraysEqualById(this.entries, newEntries, hashFn)) {
        this.entries = newEntries;
        this.grouped = data.grouped ?? {};
      }
      this.totalTokens = data.total_tokens ?? 0;
      this.tokenBudget = data.token_budget ?? 0;
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  async search(query: string): Promise<void> {
    this.searchQuery = query;
    return this.fetch(query);
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

export const knowledgeStore = new KnowledgeStore();
