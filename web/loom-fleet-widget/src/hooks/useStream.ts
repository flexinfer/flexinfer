import { useEffect, useState } from "react";
import { callTool } from "../lib/mcpBridge";

// StreamEntry mirrors the streamEntryDTO shape returned by
// /api/mobile/v1/stream. The HUD packs decisions, findings, tasks
// (and other agent_context entry types) into the same envelope; the
// widget renders them as one chronological ticker.
export interface StreamEntry {
  id: string;
  entry_type: string;
  agent_id: string;
  agent?: string;
  namespace?: string;
  title?: string;
  content?: string;
  timestamp: string;
  score?: number;
}

export interface StreamState {
  entries: StreamEntry[];
  error: string | null;
  loading: boolean;
}

// useStream polls loom_fleet_get_stream at refreshMs (default 2s,
// faster than the dashboard's 5s because event tickers feel stale
// quickly). Same visibility-pause behavior as useFleet so an
// off-screen widget does not thrash the relay.
export function useStream(refreshMs = 2000): StreamState {
  const [state, setState] = useState<StreamState>({
    entries: [],
    error: null,
    loading: true,
  });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const fetchOnce = async () => {
      const result = await callTool("loom_fleet_get_stream");
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
      try {
        const parsed = JSON.parse(text) as { entries?: StreamEntry[] };
        setState({
          entries: parsed.entries ?? [],
          error: null,
          loading: false,
        });
      } catch (err) {
        setState((s) => ({
          ...s,
          error: `parse failed: ${(err as Error).message}`,
          loading: false,
        }));
      }
    };

    const tick = async () => {
      if (document.visibilityState === "visible") {
        await fetchOnce();
      }
      if (!cancelled) {
        timer = setTimeout(tick, refreshMs);
      }
    };

    tick();

    const onVis = () => {
      if (document.visibilityState === "visible") {
        fetchOnce();
      }
    };
    document.addEventListener("visibilitychange", onVis);

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVis);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshMs]);

  return state;
}
