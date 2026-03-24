// Context Health store - context budget monitoring and compaction triggers
// Polls /api/context/health with fallback, provides per-agent health data.

export interface AgentContextHealth {
  agent_id: string;
  session_id: string;
  namespace: string;
  token_budget: number;
  tokens_used: number;
  budget_utilization: number;
  health_score: number;
  compaction_needed: boolean;
  stale_entries: number;
  last_entry_age: string;
  recall_hit_rate: number;
  recommendation?: string;
}

export interface ContextHealthSnapshot {
  agents: AgentContextHealth[];
  system_health: number;
  total_budget: number;
  total_used: number;
  compaction_queue: number;
  updated_at: string;
}

export interface BudgetSummary {
  agents: Array<{
    agent_id: string;
    token_budget: number;
    tokens_used: number;
    budget_utilization: number;
    compaction_needed: boolean;
  }>;
  total_budget: number;
  total_used: number;
}

class ContextHealthStore {
  snapshot = $state<ContextHealthSnapshot>({
    agents: [],
    system_health: 100,
    total_budget: 0,
    total_used: 0,
    compaction_queue: 0,
    updated_at: '',
  });
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);
  compacting = $state<Set<string>>(new Set());

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  get agents(): AgentContextHealth[] {
    return this.snapshot.agents;
  }

  get systemHealth(): number {
    return this.snapshot.system_health;
  }

  get totalBudget(): number {
    return this.snapshot.total_budget;
  }

  get totalUsed(): number {
    return this.snapshot.total_used;
  }

  get compactionQueue(): number {
    return this.snapshot.compaction_queue;
  }

  get overallUtilization(): number {
    if (this.snapshot.total_budget <= 0) return 0;
    return Math.min(this.snapshot.total_used / this.snapshot.total_budget, 1);
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/context/health');
      if (!res.ok) throw new Error(`Context health: ${res.status}`);
      this.snapshot = await res.json();
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  async fetchAgentHealth(agentId: string): Promise<AgentContextHealth | null> {
    try {
      const res = await globalThis.fetch(`/api/context/health/${encodeURIComponent(agentId)}`);
      if (!res.ok) return null;
      return await res.json();
    } catch {
      return null;
    }
  }

  async compact(sessionId: string): Promise<boolean> {
    const next = new Set(this.compacting);
    next.add(sessionId);
    this.compacting = next;
    try {
      const res = await globalThis.fetch(`/api/context/compact/${encodeURIComponent(sessionId)}`, {
        method: 'POST',
      });
      if (!res.ok) throw new Error(`Compact: ${res.status}`);
      // Refresh after compaction to reflect changes.
      await this.fetch();
      return true;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return false;
    } finally {
      const updated = new Set(this.compacting);
      updated.delete(sessionId);
      this.compacting = updated;
    }
  }

  async setBudget(agentId: string, tokenBudget: number): Promise<boolean> {
    try {
      const res = await globalThis.fetch(`/api/context/budget/${encodeURIComponent(agentId)}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token_budget: tokenBudget }),
      });
      if (!res.ok) throw new Error(`Set budget: ${res.status}`);
      await this.fetch();
      return true;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      return false;
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

export const contextHealthStore = new ContextHealthStore();
