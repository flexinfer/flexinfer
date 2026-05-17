import { useEffect, useState } from "react";
import { callTool } from "../lib/mcpBridge";

// Handoff mirrors the HandoffInfo Go DTO returned by
// /api/mobile/v1/handoffs. Fields are kept lowercase JSON to match
// the wire shape; optional fields use ? so widget code that doesn't
// need them stays simple.
export interface Handoff {
  id: string;
  from_agent: string;
  to_agent?: string;
  target_agent_id?: string;
  status: string;
  summary: string;
  context?: string;
  created_at: string;
  accepted_at?: string;
}

export interface HandoffState {
  handoffs: Handoff[];
  total: number;
  error: string | null;
  loading: boolean;
}

// useHandoffs polls loom_fleet_get_handoffs at refreshMs (default
// 8s — slower than the stream ticker because handoffs change less
// frequently and a stale handoff is more recoverable than stale tool
// activity). Same visibility-pause behavior as the other hooks.
export function useHandoffs(refreshMs = 8000): HandoffState {
  const [state, setState] = useState<HandoffState>({
    handoffs: [],
    total: 0,
    error: null,
    loading: true,
  });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const fetchOnce = async () => {
      const result = await callTool("loom_fleet_get_handoffs");
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
        const parsed = JSON.parse(text) as { handoffs?: Handoff[]; total?: number };
        setState({
          handoffs: parsed.handoffs ?? [],
          total: parsed.total ?? (parsed.handoffs?.length ?? 0),
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
