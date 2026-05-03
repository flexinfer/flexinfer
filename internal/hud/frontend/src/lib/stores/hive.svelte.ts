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

// CouncilDebateRound mirrors the Go store.CouncilDebateRound shape.
// Used by the Council panel's "Debate Rounds" expander (Phase 5 slice
// 5.3) to render the per-round transcript persisted by slice 5.2.
export interface CouncilDebateRound {
  ID: number;
  CouncilRunID: string;
  RoundIndex: number;
  // editor_proposes | reviewer_critiques | moderator_decision | editor_revises
  Role: string;
  CostUSD: number;
  Summary?: string;
  ArtifactDeltas?: Array<{ path?: string; line_range?: string; action?: string }>;
  CreatedAt?: string;
}

// DebateLoadState tracks the lazy fetch lifecycle per council run.
// Stored in the hiveStore so the panel can render a spinner / error
// / cached transcript without re-fetching on every poll tick.
type DebateLoadState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'loaded'; rounds: CouncilDebateRound[] }
  | { status: 'error'; message: string };

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

// HiveKPISnapshot mirrors the operator's `kpi_snapshots.metrics_json`
// rollup. Fields are optional because the recorder only emits keys it has
// data for; missing keys render as "—" placeholders. Field names here are
// the contract the (future) snapshot recorder must honor.
export interface HiveKPISnapshot {
  snapshot_at?: string;
  window_seconds?: number;
  metrics?: {
    cost_per_merged_change_usd?: number;
    slice_to_merge_p50_seconds?: number;
    gate_pass_rate?: number;        // 0..1
    auto_merge_rate?: number;       // 0..1
    regression_rate?: number;       // 0..1
    council_roi?: number;           // merged-changes-per-council-USD
  };
}

// Operator returns snapshots with PascalCase fields (Go struct json tags
// follow Go naming). Accept both casings so a recorder that emits
// snake_case `metrics_json` payloads also works.
interface RawKPISnapshot {
  ID?: number;
  SnapshotAt?: string;
  WindowSeconds?: number;
  Metrics?: HiveKPISnapshot['metrics'];
  snapshot_at?: string;
  window_seconds?: number;
  metrics?: HiveKPISnapshot['metrics'];
}

const KPI_HISTORY_MAX = 24;

class HiveStore {
  // Per-panel data
  backlog = $state<BacklogItem[]>([]);
  pipelineRuns = $state<PipelineRun[]>([]);
  councilRuns = $state<CouncilRun[]>([]);
  evalScores = $state<EvalScore[]>([]);
  policy = $state<PolicyView | null>(null);

  // KPI snapshot for the rolling 1d window plus a small in-memory history
  // for sparkline trends. The history is only de-duped on snapshot_at so
  // repeated polls of the same snapshot don't pad the trend.
  kpis = $state<HiveKPISnapshot | null>(null);
  kpisHistory = $state<HiveKPISnapshot[]>([]);

  // Per-run debate transcripts, keyed by CouncilRun.ID. Populated
  // lazily by loadDebate() so the council list itself stays cheap.
  // Phase 5 slice 5.3 — feeds the CouncilPanel's "Debate Rounds"
  // expander.
  debateByRun = $state<Record<string, DebateLoadState>>({});

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
      const [policy, backlog, pipelines, council, scores, kpis] = await Promise.all([
        this.getJSON<PolicyView>('/api/hive/policy'),
        this.getJSON<BacklogItem[]>('/api/hive/backlog'),
        this.getJSON<PipelineRun[]>('/api/hive/pipeline/runs'),
        this.getJSON<CouncilRun[]>('/api/hive/council/runs'),
        this.getJSON<EvalScore[]>('/api/hive/eval/scores'),
        // KPI endpoint returns 404 until the snapshot recorder ships.
        // Tolerate that by passing { tolerate404: true }; null is fine.
        this.getJSON<RawKPISnapshot>('/api/hive/kpis?window=1d', { tolerate404: true }),
      ]);
      this.policy = policy;
      this.backlog = backlog ?? [];
      this.pipelineRuns = pipelines ?? [];
      this.councilRuns = council ?? [];
      this.evalScores = scores ?? [];
      this.applyKPISnapshot(kpis);
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

  private async getJSON<T>(path: string, opts: { tolerate404?: boolean } = {}): Promise<T | null> {
    const res = await globalThis.fetch(path);
    if (res.status === 503) {
      // Surface to fetchAll so it can flip the disabled flag.
      throw new Error(`hive proxy: 503 (operator not configured)`);
    }
    if (res.status === 404 && opts.tolerate404) {
      return null;
    }
    if (!res.ok) {
      throw new Error(`${path}: ${res.status}`);
    }
    // Some routes may return null body; tolerate it.
    const text = await res.text();
    if (!text) return null;
    return JSON.parse(text) as T;
  }

  private applyKPISnapshot(raw: RawKPISnapshot | null): void {
    if (!raw) {
      this.kpis = null;
      return;
    }
    const snap: HiveKPISnapshot = {
      snapshot_at: raw.SnapshotAt ?? raw.snapshot_at,
      window_seconds: raw.WindowSeconds ?? raw.window_seconds,
      metrics: raw.Metrics ?? raw.metrics ?? {},
    };
    this.kpis = snap;
    // Append to history only when snapshot_at advances; otherwise the
    // sparkline would just plot the same point N times.
    const last = this.kpisHistory[this.kpisHistory.length - 1];
    if (!last || last.snapshot_at !== snap.snapshot_at) {
      const next = [...this.kpisHistory, snap];
      this.kpisHistory = next.slice(-KPI_HISTORY_MAX);
    }
  }

  // Pull a single KPI metric series from the in-memory history. Missing
  // values are skipped (no zero-filling) so the sparkline reflects only
  // observed data points.
  metricSeries(key: keyof NonNullable<HiveKPISnapshot['metrics']>): number[] {
    const out: number[] = [];
    for (const snap of this.kpisHistory) {
      const v = snap.metrics?.[key];
      if (typeof v === 'number' && Number.isFinite(v)) out.push(v);
    }
    return out;
  }

  // loadDebate fetches the per-round transcript for one council run.
  // Cache-on-success: subsequent calls for the same id return without
  // network. Errors are surfaced via debateByRun[id].status === 'error'
  // so the panel can show a retry affordance instead of a silent fail.
  // The 'idle' / 'loading' transitions are explicit so the panel can
  // distinguish "never tried" from "in flight".
  async loadDebate(runID: string): Promise<void> {
    if (!runID) return;
    const cached = this.debateByRun[runID];
    if (cached && (cached.status === 'loaded' || cached.status === 'loading')) {
      return;
    }
    this.debateByRun = { ...this.debateByRun, [runID]: { status: 'loading' } };
    try {
      const rounds =
        (await this.getJSON<CouncilDebateRound[]>(
          `/api/hive/council/runs/${encodeURIComponent(runID)}/debate`,
        )) ?? [];
      this.debateByRun = {
        ...this.debateByRun,
        [runID]: { status: 'loaded', rounds },
      };
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      this.debateByRun = {
        ...this.debateByRun,
        [runID]: { status: 'error', message },
      };
    }
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
