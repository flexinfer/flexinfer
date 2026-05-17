import { useEffect, useState } from "react";
import { callTool, unwrapEnvelope } from "../lib/mcpBridge";

// Dashboard mirrors the JSON shape the loom HUD returns for
// /api/mobile/v1/dashboard, narrowed to just the fields FleetOverview
// renders. New fields can be added incrementally; unknown fields are
// preserved in `extra` for forward compatibility but not typed.
export interface Dashboard {
  daemon_running: boolean;
  server_count: number;
  active_sessions: number;
  active_agents: number;
  idle_agents: number;
  offline_agents: number;
  updated_at?: string;
  health?: {
    total_servers?: number;
    healthy_servers?: number;
    degraded_servers?: number;
    down_servers?: number;
    idle_servers?: number;
  };
  spawns?: { active?: number; total?: number };
  last_heartbeat?: { agent_id?: string; timestamp?: string; count_1h?: number };
}

export interface FleetState {
  data: Dashboard | null;
  error: string | null;
  loading: boolean;
  lastUpdated: Date | null;
}

// useFleet polls loom_fleet_get_dashboard at refreshMs (default 5s)
// while the document is visible. Polling pauses when the host hides
// the iframe (visibilitychange=hidden) so an inactive widget does not
// thrash the HUD; the next visible tick fetches immediately.
export function useFleet(refreshMs = 5000): FleetState {
  const [state, setState] = useState<FleetState>({
    data: null,
    error: null,
    loading: true,
    lastUpdated: null,
  });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const fetchOnce = async () => {
      const result = await callTool("loom_fleet_get_dashboard");
      if (cancelled) return;

      if (result.isError) {
        setState((s) => ({
          ...s,
          error: result.content[0]?.text ?? "unknown relay error",
          loading: false,
        }));
        return;
      }
      const text = result.content[0]?.text ?? "{}";
      const { data, error } = unwrapEnvelope<Dashboard>(text);
      if (error) {
        setState((s) => ({ ...s, error, loading: false }));
        return;
      }
      setState({
        data: data ?? null,
        error: null,
        loading: false,
        lastUpdated: new Date(),
      });
    };

    const tick = async () => {
      if (document.visibilityState === "visible") {
        await fetchOnce();
      }
      if (!cancelled) {
        timer = setTimeout(tick, refreshMs);
      }
    };

    // Kick the first fetch synchronously so the UI does not flash an
    // empty loading state for a full refresh interval.
    tick();

    const onVis = () => {
      if (document.visibilityState === "visible" && !state.loading) {
        // Force an immediate refresh on return-to-visible so users
        // see fresh data the moment they look at the widget again.
        fetchOnce();
      }
    };
    document.addEventListener("visibilitychange", onVis);

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVis);
    };
    // refreshMs is captured intentionally; changing it re-subscribes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshMs]);

  return state;
}
