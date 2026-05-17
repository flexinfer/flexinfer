import { useCallback, useEffect, useRef, useState } from "react";
import { callTool, unwrapEnvelope } from "../lib/mcpBridge";

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
  // pendingAction is the handoff id currently being accepted/rejected
  // (or null when no mutation is in flight). The HandoffInbox uses
  // this to disable buttons + show a per-card spinner state.
  pendingAction: string | null;
  // actionError is the last accept/reject failure message; null on
  // success or before any action.
  actionError: string | null;
  // accept/reject return promises so callers can await the result and
  // surface success toasts; the local state is also updated.
  accept: (handoffID: string, opts?: { sessionID?: string; targetAgentID?: string; importEntries?: boolean }) => Promise<boolean>;
  reject: (handoffID: string, opts?: { reason?: string }) => Promise<boolean>;
}

// useHandoffs polls loom_fleet_get_handoffs at refreshMs (default
// 8s — slower than the stream ticker because handoffs change less
// frequently and a stale handoff is more recoverable than stale tool
// activity). Same visibility-pause behavior as the other hooks.
// Also exposes accept/reject mutation helpers that call the
// loom_fleet_handoff_{accept,reject} tools and trigger an immediate
// refresh on success.
export function useHandoffs(refreshMs = 8000): HandoffState {
  const [data, setData] = useState<{ handoffs: Handoff[]; total: number; loading: boolean; error: string | null }>({
    handoffs: [],
    total: 0,
    loading: true,
    error: null,
  });
  const [actionState, setActionState] = useState<{ pending: string | null; error: string | null }>({
    pending: null,
    error: null,
  });

  // fetchRef holds the latest fetchOnce so accept/reject can trigger
  // an immediate refresh after a successful mutation without taking
  // a hook dep on the function identity.
  const fetchRef = useRef<() => Promise<void>>(async () => {});

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const fetchOnce = async () => {
      const result = await callTool("loom_fleet_get_handoffs");
      if (cancelled) return;
      if (result.isError) {
        setData((s) => ({
          ...s,
          error: result.content[0]?.text ?? "unknown relay error",
          loading: false,
        }));
        return;
      }
      const text = result.content[0]?.text ?? "{}";
      const { data: payload, error } = unwrapEnvelope<{ handoffs?: Handoff[]; total?: number }>(text);
      if (error) {
        setData((s) => ({ ...s, error, loading: false }));
        return;
      }
      setData({
        handoffs: payload?.handoffs ?? [],
        total: payload?.total ?? (payload?.handoffs?.length ?? 0),
        error: null,
        loading: false,
      });
    };
    fetchRef.current = fetchOnce;

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

  const accept = useCallback(
    async (handoffID: string, opts?: { sessionID?: string; targetAgentID?: string; importEntries?: boolean }) => {
      setActionState({ pending: handoffID, error: null });
      const args: Record<string, unknown> = { handoff_id: handoffID };
      if (opts?.sessionID) args.session_id = opts.sessionID;
      if (opts?.targetAgentID) args.target_agent_id = opts.targetAgentID;
      if (opts?.importEntries) args.import_entries = true;
      const result = await callTool("loom_fleet_handoff_accept", args);
      if (result.isError) {
        setActionState({ pending: null, error: result.content[0]?.text ?? "accept failed" });
        return false;
      }
      setActionState({ pending: null, error: null });
      // Refresh immediately so the accepted handoff disappears from
      // the inbox without waiting for the next 8s poll tick.
      await fetchRef.current();
      return true;
    },
    []
  );

  const reject = useCallback(async (handoffID: string, opts?: { reason?: string }) => {
    setActionState({ pending: handoffID, error: null });
    const args: Record<string, unknown> = { handoff_id: handoffID };
    if (opts?.reason) args.reason = opts.reason;
    const result = await callTool("loom_fleet_handoff_reject", args);
    if (result.isError) {
      setActionState({ pending: null, error: result.content[0]?.text ?? "reject failed" });
      return false;
    }
    setActionState({ pending: null, error: null });
    await fetchRef.current();
    return true;
  }, []);

  return {
    handoffs: data.handoffs,
    total: data.total,
    loading: data.loading,
    error: data.error,
    pendingAction: actionState.pending,
    actionError: actionState.error,
    accept,
    reject,
  };
}
