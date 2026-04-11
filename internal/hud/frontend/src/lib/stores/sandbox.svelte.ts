// Sandbox store — fetches devbox sandbox data from GET /api/sandbox
// and subscribes to SSE events for real-time exec/build activity.
// Follows the health.svelte.ts SSE-first pattern with fallback polling.
import { eventStore } from './events.svelte.ts';
import { adminFetch, labsAuthStore } from './labsAuth.svelte.ts';

export interface SandboxSummary {
  available: boolean;
  status?: string;
  reason?: string;
  hint?: string;
  start_command?: string;
  backend?: string;
  total_sandboxes: number;
  running: number;
  paused: number;
  stopped?: number;
  total_execs: number;
  total_builds: number;
  uptime_seconds: number;
  projects: string[];
  agent_labels?: Record<string, string>;
}

export interface SandboxEvent {
  type: string;       // "exec", "build", "start", "stop"
  project: string;
  detail: string;
  timestamp: Date;
}

export interface SandboxPolicy {
  configured: boolean;
  require_sandbox?: string[];
  recommend_sandbox?: string[];
  auto_provision?: boolean;
  default_backend?: string;
}

export interface SandboxCapabilities {
  available: boolean;
  backend?: string;
  auth_required: boolean;
  supported_actions: string[];
  project_count?: number;
  projects?: string[];
  notes?: {
    async_exec?: boolean;
    polling_required?: boolean;
    streaming_output?: boolean;
    telemetry_source?: string;
    sandbox_event_source?: string;
  };
}

export interface SandboxExecRun {
  exec_id: string;
  status: string;
  project: string;
  command: string;
  started_at?: string;
  completed_at?: string;
  elapsed_ms?: number;
  duration_ms?: number;
  exit_code?: number;
  stdout_tail?: string;
  stderr_tail?: string;
  error?: string;
}

export interface SandboxProjectEntry {
  project: string;
  status: string;
  image?: string;
  backend?: string;
  agent_id?: string;
  running?: boolean;
  uptime?: string;
  last_used?: string;
  error?: string;
}

const MAX_EVENTS = 20;
const MAX_EXEC_RUNS = 8;

class SandboxStore {
  summary = $state<SandboxSummary | null>(null);
  available = $state(false);
  loading = $state(false);
  error = $state<string | null>(null);
  lastAction = $state<{ kind: 'start' | 'stop' | 'exec'; project: string; message: string; buildId?: string; execId?: string } | null>(null);
  recentEvents = $state<SandboxEvent[]>([]);
  lastUpdated = $state<Date | null>(null);
  policy = $state<SandboxPolicy | null>(null);
  capabilities = $state<SandboxCapabilities | null>(null);
  capabilitiesLoading = $state(false);
  capabilitiesError = $state<string | null>(null);
  execRuns = $state<SandboxExecRun[]>([]);
  projectStatus = $state(new Map<string, SandboxProjectEntry[]>());
  projectStatusLoading = $state(new Set<string>());

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private execPollTimer: ReturnType<typeof setInterval> | null = null;
  private eventUnsubs: Array<() => void> = [];

  get runningCount(): number {
    return this.summary?.running ?? 0;
  }

  get pausedCount(): number {
    return this.summary?.paused ?? 0;
  }

  get totalExecs(): number {
    return this.summary?.total_execs ?? 0;
  }

  get totalBuilds(): number {
    return this.summary?.total_builds ?? 0;
  }

  get totalSandboxes(): number {
    return this.summary?.total_sandboxes ?? 0;
  }

  get projects(): string[] {
    return this.summary?.projects ?? [];
  }

  clearError(): void {
    this.error = null;
  }

  get activeExecs(): SandboxExecRun[] {
    return this.execRuns.filter((run) => run.status === 'running');
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/sandbox');
      if (!res.ok) throw new Error(`Sandbox API: ${res.status}`);
      const data: SandboxSummary = await res.json();
      this.summary = data;
      this.available = data.available ?? false;
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
      this.available = false;
    } finally {
      this.loading = false;
    }
  }

  async fetchCapabilities(): Promise<void> {
    this.capabilitiesLoading = true;
    this.capabilitiesError = null;
    try {
      const res = await globalThis.fetch('/api/sandbox/capabilities');
      if (!res.ok) throw new Error(`Sandbox capabilities API: ${res.status}`);
      this.capabilities = await res.json();
    } catch (e) {
      this.capabilitiesError = e instanceof Error ? e.message : String(e);
    } finally {
      this.capabilitiesLoading = false;
    }
  }

  /** Apply full sandbox snapshot from SSE hud.sandbox event. */
  applySnapshot(data: Record<string, unknown>): void {
    this.summary = data as unknown as SandboxSummary;
    this.available = (data.available as boolean) ?? false;
    this.lastUpdated = new Date();
    this.error = null;
  }

  /** Push a sandbox activity event from SSE hud.sandbox.event. */
  pushEvent(data: Record<string, unknown>): void {
    const evt: SandboxEvent = {
      type: (data.type as string) ?? 'unknown',
      project: (data.project as string) ?? '',
      detail: (data.detail as string) ?? '',
      timestamp: new Date((data.timestamp as string) ?? Date.now()),
    };
    this.recentEvents = [evt, ...this.recentEvents].slice(0, MAX_EVENTS);
  }

  private normalizeExecRun(data: Record<string, unknown>): SandboxExecRun {
    return {
      exec_id: String(data.exec_id ?? ''),
      status: String(data.status ?? 'unknown'),
      project: String(data.project ?? ''),
      command: String(data.command ?? ''),
      started_at: typeof data.started_at === 'string' ? data.started_at : undefined,
      completed_at: typeof data.completed_at === 'string' ? data.completed_at : undefined,
      elapsed_ms: typeof data.elapsed_ms === 'number' ? data.elapsed_ms : Number(data.elapsed_ms ?? 0),
      duration_ms: typeof data.duration_ms === 'number' ? data.duration_ms : Number(data.duration_ms ?? 0),
      exit_code: typeof data.exit_code === 'number' ? data.exit_code : (data.exit_code == null ? undefined : Number(data.exit_code)),
      stdout_tail: typeof data.stdout_tail === 'string' ? data.stdout_tail : undefined,
      stderr_tail: typeof data.stderr_tail === 'string' ? data.stderr_tail : undefined,
      error: typeof data.error === 'string' ? data.error : undefined,
    };
  }

  private upsertExecRun(run: SandboxExecRun): void {
    const next = [run, ...this.execRuns.filter((existing) => existing.exec_id !== run.exec_id)];
    this.execRuns = next.slice(0, MAX_EXEC_RUNS);
  }

  async startSandbox(project: string): Promise<void> {
    this.error = null;
    try {
      const res = await adminFetch('/api/sandbox/start', {
        method: 'POST',
        requireToken: true,
        action: 'Starting a sandbox',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error((data as { error?: string }).error || `Start sandbox: ${res.status}`);
      this.lastAction = {
        kind: 'start',
        project,
        message: typeof data.message === 'string' ? data.message : `Sandbox start requested for ${project}`,
        buildId: typeof data.build_id === 'string' ? data.build_id : undefined,
      };
      // Refresh after starting.
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async stopSandbox(project: string): Promise<void> {
    this.error = null;
    try {
      const res = await adminFetch('/api/sandbox/stop', {
        method: 'POST',
        requireToken: true,
        action: 'Stopping a sandbox',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error((data as { error?: string }).error || `Stop sandbox: ${res.status}`);
      this.lastAction = {
        kind: 'stop',
        project,
        message: typeof data.message === 'string' ? data.message : `Sandbox stop requested for ${project}`,
      };
      // Refresh after stopping.
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async startExec(project: string, command: string, timeout = '10m'): Promise<void> {
    this.error = null;
    try {
      const res = await adminFetch('/api/sandbox/exec', {
        method: 'POST',
        requireToken: true,
        action: 'Running a sandbox command',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ project, command, timeout }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error((data as { error?: string }).error || `Sandbox exec: ${res.status}`);

      const run = this.normalizeExecRun(data as Record<string, unknown>);
      this.upsertExecRun(run);
      this.lastAction = {
        kind: 'exec',
        project,
        message: `Queued ${command}`,
        execId: run.exec_id || undefined,
      };
      await this.fetch();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async pollExec(execId: string): Promise<void> {
    const res = await adminFetch(`/api/sandbox/exec/${encodeURIComponent(execId)}`, {
      requireToken: true,
      action: 'Polling a sandbox command',
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error((data as { error?: string }).error || `Sandbox exec poll: ${res.status}`);
    }
    this.upsertExecRun(this.normalizeExecRun(data as Record<string, unknown>));
  }

  async pollActiveExecs(): Promise<void> {
    if (!labsAuthStore.hasToken || this.activeExecs.length === 0) {
      return;
    }
    const results = await Promise.allSettled(this.activeExecs.map((run) => this.pollExec(run.exec_id)));
    for (const result of results) {
      if (result.status === 'rejected') {
        this.error = result.reason instanceof Error ? result.reason.message : String(result.reason);
      }
    }
  }

  async fetchPolicy(): Promise<void> {
    try {
      const res = await globalThis.fetch('/api/sandbox/policy');
      if (!res.ok) return;
      const data = await res.json();
      // If it has require_sandbox or recommend_sandbox, it's configured.
      this.policy = { configured: !!(data.require_sandbox || data.recommend_sandbox), ...data };
    } catch {
      // Policy is optional — silently ignore errors.
    }
  }

  async fetchProjectStatus(project: string): Promise<void> {
    if (!labsAuthStore.hasToken) return;
    const next = new Set(this.projectStatusLoading);
    next.add(project);
    this.projectStatusLoading = next;
    try {
      const res = await adminFetch(`/api/sandbox/project/${encodeURIComponent(project)}`, {
        requireToken: true,
        action: 'Loading sandbox project status',
      });
      if (!res.ok) return;
      const data = await res.json();
      const entries: SandboxProjectEntry[] = Array.isArray(data.sandboxes)
        ? data.sandboxes.map((s: Record<string, unknown>) => ({
            project: String(s.project ?? project),
            status: String(s.status ?? 'unknown'),
            image: typeof s.image === 'string' ? s.image : undefined,
            backend: typeof s.backend === 'string' ? s.backend : undefined,
            agent_id: typeof s.agent_id === 'string' ? s.agent_id : undefined,
            running: typeof s.running === 'boolean' ? s.running : undefined,
            uptime: typeof s.uptime === 'string' ? s.uptime : undefined,
            last_used: typeof s.last_used === 'string' ? s.last_used : undefined,
            error: typeof s.error === 'string' ? s.error : undefined,
          }))
        : [];
      const nextMap = new Map(this.projectStatus);
      nextMap.set(project, entries);
      this.projectStatus = nextMap;
    } catch {
      // best-effort
    } finally {
      const done = new Set(this.projectStatusLoading);
      done.delete(project);
      this.projectStatusLoading = done;
    }
  }

  async fetchAllProjectStatuses(): Promise<void> {
    const projects = this.projects;
    if (projects.length === 0) return;
    await Promise.allSettled(projects.map(p => this.fetchProjectStatus(p)));
  }

  startPolling(intervalMs = 15000): void {
    this.stopPolling();
    this.fetch();
    this.fetchCapabilities();
    this.fetchPolicy();
    this.pollTimer = setInterval(() => { if (!eventStore.connected) this.fetch(); }, intervalMs);
    this.execPollTimer = setInterval(() => {
      this.pollActiveExecs().catch(() => { /* best-effort */ });
    }, 3000);

    // Subscribe to SSE events.
    this.eventUnsubs.push(
      eventStore.on('hud.sandbox', (e) => this.applySnapshot(e.data)),
      eventStore.on('hud.sandbox.event', (e) => this.pushEvent(e.data)),
    );
  }

  stopPolling(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    if (this.execPollTimer) {
      clearInterval(this.execPollTimer);
      this.execPollTimer = null;
    }
    for (const unsub of this.eventUnsubs) unsub();
    this.eventUnsubs = [];
  }
}

export const sandboxStore = new SandboxStore();
