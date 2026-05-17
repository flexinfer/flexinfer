import { EventTicker } from "./components/EventTicker";
import { useFleet } from "./hooks/useFleet";
import { hostKind } from "./lib/mcpBridge";

// FleetOverview renders the loom fleet dashboard inline. Data flows
// widget → host (Claude/ChatGPT) → mcp-loom-widget (Go) → loom HUD.
// The bearer token lives only in the Go process, per slice 1b-γ.
export function FleetOverview() {
  const { data, error, loading, lastUpdated } = useFleet();
  const isMock = hostKind() === "mock";

  return (
    <div className="card">
      <h1>
        <span
          className={data?.daemon_running ? "dot dot-ok" : "dot dot-warn"}
          aria-hidden="true"
        />
        Loom Fleet
        {isMock && <span className="badge">preview · mock data</span>}
      </h1>
      <p className="sub">{summary(data, loading, error)}</p>

      {error && <Banner kind="error">{error}</Banner>}

      {data && (
        <>
          <Row label="Daemon" value={data.daemon_running ? "running" : "down"} />
          <Row label="Active sessions" value={String(data.active_sessions)} />
          <Row
            label="Agents"
            value={`${data.active_agents} active · ${data.idle_agents} idle · ${data.offline_agents} offline`}
          />
          <Row label="MCP servers" value={String(data.server_count)} />
          {data.health && (
            <Row
              label="Server health"
              value={`${data.health.healthy_servers ?? 0} healthy · ${data.health.degraded_servers ?? 0} degraded · ${data.health.down_servers ?? 0} down`}
            />
          )}
          {data.spawns && (
            <Row
              label="Spawns"
              value={`${data.spawns.active ?? 0} active · ${data.spawns.total ?? 0} total`}
            />
          )}
          {data.last_heartbeat?.agent_id && (
            <Row
              label="Last heartbeat"
              value={`${data.last_heartbeat.agent_id} · ${data.last_heartbeat.count_1h ?? 0}/h`}
            />
          )}
        </>
      )}

      <EventTicker />

      <p className="footer">
        {lastUpdated ? `updated ${lastUpdated.toLocaleTimeString()}` : "polling…"}
        {" · "}
        <code>loom_fleet_get_dashboard</code>
      </p>
    </div>
  );
}

function summary(
  data: ReturnType<typeof useFleet>["data"],
  loading: boolean,
  error: string | null
): string {
  if (error) return "could not reach loom HUD";
  if (loading && !data) return "fetching loom fleet…";
  if (!data) return "no data";
  return `${data.active_agents + data.idle_agents + data.offline_agents} agents tracked, ${data.active_sessions} live sessions`;
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="row">
      <span className="label">{label}</span>
      <span className="value">{value}</span>
    </div>
  );
}

function Banner({ kind, children }: { kind: "error" | "info"; children: React.ReactNode }) {
  return <div className={`banner banner-${kind}`}>{children}</div>;
}
