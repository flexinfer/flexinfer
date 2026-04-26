// Hive store — backlog / pipeline runs / council runs / eval scores from
// the in-cluster loom-hive-operator, proxied through /api/hive/* by the
// HUD's domain/hive package. Each panel owns a slice of this store and
// polls the corresponding read endpoint at 15s.
//
// Empty/disabled state: when the proxy returns 503 ("operator not
// configured") we surface that via the `disabled` flag so panels can
// render a clear "Hive disabled" empty-state instead of a fetch error.

export interface BacklogItem {
  ID: string;
  Title: string;
  State: string;
  Priority: string;
  Labels?: string[];
  CreatedBy?: string;
  CreatedAt?: string;
  UpdatedAt?: string;
}

export interface PipelineRun {
  ID: string;
  BacklogID: string;
  Template: string;
  State: string;
  Attempts: number;
  StartedAt?: string;
  EndedAt?: string;
}

export interface CouncilRun {
  ID: string;
  Trigger: string;
  Outcome: string;
  StartedAt?: string;
  EndedAt?: string;
  CostUSD?: number;
}

export interface EvalScore {
  ID: string;
  Loop: string;        // "A" | "B" | "C"
  Subject: string;     // run id, merge sha, etc.
  Score: number;       // 0..1
  Notes?: string;
  CreatedAt?: string;
}

export interface PolicyView {
  enabled: boolean;
  version: number;
  raw?: unknown;
}

class HiveStore {
  // Per-panel data
  backlog = $state<BacklogItem[]>([]);
  pipelineRuns = $state<PipelineRun[]>([]);
  councilRuns = $state<CouncilRun[]>([]);
  evalScores = $state<EvalScore[]>([]);
  policy = $state<PolicyView | null>(null);

  // Connection state
  loading = $state(false);
  error = $state<string | null>(null);
  disabled = $state(false); // operator URL unset → 503 from proxy
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  get pipelinesByState(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const r of this.pipelineRuns) {
      out[r.State] = (out[r.State] ?? 0) + 1;
    }
    return out;
  }

  get backlogByState(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const i of this.backlog) {
      out[i.State] = (out[i.State] ?? 0) + 1;
    }
    return out;
  }

  async fetchAll(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const [policy, backlog, pipelines, council, scores] = await Promise.all([
        this.getJSON<PolicyView>('/api/hive/policy'),
        this.getJSON<BacklogItem[]>('/api/hive/backlog'),
        this.getJSON<PipelineRun[]>('/api/hive/pipeline/runs'),
        this.getJSON<CouncilRun[]>('/api/hive/council/runs'),
        this.getJSON<EvalScore[]>('/api/hive/eval/scores'),
      ]);
      this.policy = policy;
      this.backlog = backlog ?? [];
      this.pipelineRuns = pipelines ?? [];
      this.councilRuns = council ?? [];
      this.evalScores = scores ?? [];
      this.lastUpdated = new Date();
      this.disabled = false;
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // Treat 503 from the proxy as "Hive disabled" rather than an error,
      // so the empty-state UX is calm, not red.
      if (msg.includes('503') || msg.toLowerCase().includes('not configured')) {
        this.disabled = true;
        this.error = null;
      } else {
        this.disabled = false;
        this.error = msg;
      }
    } finally {
      this.loading = false;
    }
  }

  private async getJSON<T>(path: string): Promise<T | null> {
    const res = await globalThis.fetch(path);
    if (res.status === 503) {
      // Surface to fetchAll so it can flip the disabled flag.
      throw new Error(`hive proxy: 503 (operator not configured)`);
    }
    if (!res.ok) {
      throw new Error(`${path}: ${res.status}`);
    }
    // Some routes may return null body; tolerate it.
    const text = await res.text();
    if (!text) return null;
    return JSON.parse(text) as T;
  }

  startPolling(intervalMs = 15000): void {
    this.stopPolling();
    void this.fetchAll();
    this.pollTimer = setInterval(() => void this.fetchAll(), intervalMs);
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }
}

export const hiveStore = new HiveStore();
