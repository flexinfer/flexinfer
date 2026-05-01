// OTel store — fetches OTel observability status from GET /api/otel.

export interface OTelStatus {
  otlp_endpoint: string;
  otlp_configured: boolean;
  log_format: string;
  json_logs_enabled: boolean;
  traced_servers: number;
  total_servers: number;
  trace_coverage: string;
  runtime_otlp_configured: boolean;
  runtime_otlp_enabled: boolean;
  runtime_otlp_endpoint: string;
  runtime_otlp_protocol: string;
  runtime_otlp_service_name: string;
  runtime_otlp_sample_rate: number;
  runtime_otlp_error: string;
  runtime_meter_enabled: boolean;
  runtime_trace_surfaces: Record<string, boolean>;
  runtime_trace_coverage: string;
}

type PollingOwner = string | symbol;
const DEFAULT_POLLING_OWNER: PollingOwner = 'default';

class OTelStore {
  data = $state<OTelStatus | null>(null);
  loading = $state(false);
  error = $state<string | null>(null);
  lastUpdated = $state<Date | null>(null);

  private pollTimer: ReturnType<typeof setInterval> | null = null;
  private pollingOwners = new Map<PollingOwner, number>();

  get configured(): boolean {
    return this.data?.runtime_otlp_configured ?? false;
  }

  get enabled(): boolean {
    return this.data?.runtime_otlp_enabled ?? false;
  }

  get meterEnabled(): boolean {
    return this.data?.runtime_meter_enabled ?? false;
  }

  get traceCoverage(): string {
    return this.data?.runtime_trace_coverage ?? '0%';
  }

  get endpoint(): string {
    return this.data?.runtime_otlp_endpoint ?? '';
  }

  get protocol(): string {
    return this.data?.runtime_otlp_protocol ?? '';
  }

  get sampleRate(): number {
    return this.data?.runtime_otlp_sample_rate ?? 1.0;
  }

  get surfaces(): Record<string, boolean> {
    return this.data?.runtime_trace_surfaces ?? {};
  }

  get surfaceCount(): number {
    const s = this.surfaces;
    return Object.values(s).filter(Boolean).length;
  }

  get totalSurfaces(): number {
    return Object.keys(this.surfaces).length;
  }

  async fetch(): Promise<void> {
    this.loading = true;
    this.error = null;
    try {
      const res = await globalThis.fetch('/api/otel');
      if (!res.ok) throw new Error(`OTel API: ${res.status}`);
      this.data = await res.json() as OTelStatus;
      this.lastUpdated = new Date();
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    } finally {
      this.loading = false;
    }
  }

  private refreshPollTimer(): void {
    if (this.pollTimer) {
      clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
    if (this.pollingOwners.size === 0) return;
    const intervalMs = Math.min(...this.pollingOwners.values());
    this.pollTimer = setInterval(() => this.fetch(), intervalMs);
  }

  startPolling(intervalMs = 60000, owner: PollingOwner = DEFAULT_POLLING_OWNER): void {
    const wasIdle = this.pollingOwners.size === 0;
    this.pollingOwners.set(owner, intervalMs);
    this.refreshPollTimer();
    if (wasIdle) this.fetch();
  }

  stopPolling(owner: PollingOwner = DEFAULT_POLLING_OWNER): void {
    this.pollingOwners.delete(owner);
    this.refreshPollTimer();
  }
}

export const otelStore = new OTelStore();
