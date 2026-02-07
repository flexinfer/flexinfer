// Graph store - knowledge graph visualization

export interface Relation {
  source: string;
  source_name?: string;
  target: string;
  target_name?: string;
  relation_type: string;
  type?: string;
}

export interface GraphStats {
  entity_count: number;
  relation_count: number;
  total_entities: number;
  total_relations: number;
  entity_types: Record<string, number>;
  relation_types: Record<string, number>;
  namespaces: string[];
  all_relations: Relation[];
}

export interface Entity {
  id: string;
  name: string;
  entity_type: string;
  type: string;
  properties: Record<string, unknown>;
  relations: Relation[];
  inbound_relations: Relation[];
  outbound_relations: Relation[];
}

export interface EntitiesResponse {
  entities: Entity[];
}

class GraphStore {
  stats = $state<GraphStats>({
    entity_count: 0,
    relation_count: 0,
    total_entities: 0,
    total_relations: 0,
    entity_types: {},
    relation_types: {},
    namespaces: [],
    all_relations: [],
  });
  entities = $state<Entity[]>([]);
  relations = $state<Relation[]>([]);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  searchQuery = $state<string>('');
  filterType = $state<string>('all');

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  get entityTypeList(): string[] {
    return Object.keys(this.stats.entity_types);
  }

  get relationTypeList(): string[] {
    return Object.keys(this.stats.relation_types);
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const params = new URLSearchParams();
      if (this.searchQuery) params.set('q', this.searchQuery);
      if (this.filterType !== 'all') params.set('type', this.filterType);
      params.set('limit', '100');

      const [statsRes, entitiesRes] = await Promise.all([
        globalThis.fetch('/api/graph/stats'),
        globalThis.fetch(`/api/graph/entities?${params.toString()}`),
      ]);

      if (!statsRes.ok) throw new Error(`Graph stats: ${statsRes.status}`);
      if (!entitiesRes.ok) throw new Error(`Graph entities: ${entitiesRes.status}`);

      this.stats = await statsRes.json();
      const data: EntitiesResponse = await entitiesRes.json();
      this.entities = data.entities || [];
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  async search(query: string, type?: string, limit?: number): Promise<void> {
    this.searchQuery = query;
    if (type) this.filterType = type;
    if (limit) {
      const params = new URLSearchParams();
      if (query) params.set('q', query);
      if (type && type !== 'all') params.set('type', type);
      params.set('limit', String(limit));

      this.loading = true;
      this.error = null;
      try {
        const [statsRes, entitiesRes] = await Promise.all([
          globalThis.fetch('/api/graph/stats'),
          globalThis.fetch(`/api/graph/entities?${params.toString()}`),
        ]);

        if (!statsRes.ok) throw new Error(`Graph stats: ${statsRes.status}`);
        if (!entitiesRes.ok) throw new Error(`Graph entities: ${entitiesRes.status}`);

        this.stats = await statsRes.json();
        const data: EntitiesResponse = await entitiesRes.json();
        this.entities = data.entities || [];
        this.lastUpdated = new Date();
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e);
      } finally {
        this.loading = false;
      }
    } else {
      await this.fetch();
    }
  }

  startPolling(intervalMs = 15000): void {
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

export const graphStore = new GraphStore();
