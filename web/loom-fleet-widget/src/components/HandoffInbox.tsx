import { useHandoffs, type Handoff } from "../hooks/useHandoffs";

// HandoffInbox renders the pending handoff inbox as a stack of cards.
// Slice 2-α is read-only — the cards show the from/to + summary +
// age, and instruct the user how to accept/reject from outside the
// widget. Slice 2-β adds in-widget Accept/Reject buttons once the
// HUD exposes POST /handoffs/{id}/{accept,reject} endpoints.
export function HandoffInbox({ maxRows = 5 }: { maxRows?: number }) {
  const { handoffs, total, error, loading } = useHandoffs();

  // Only surface PENDING handoffs in the inbox; accepted/rejected
  // ones live in the timeline ticker instead.
  const pending = handoffs.filter((h) => isPending(h.status));

  if (error) {
    return (
      <div className="inbox">
        <div className="inbox-title">Pending handoffs</div>
        <div className="banner banner-error">{error}</div>
      </div>
    );
  }

  if (loading && handoffs.length === 0) {
    return (
      <div className="inbox">
        <div className="inbox-title">Pending handoffs</div>
        <div className="inbox-empty">loading…</div>
      </div>
    );
  }

  if (pending.length === 0) {
    return (
      <div className="inbox">
        <div className="inbox-title">Pending handoffs</div>
        <div className="inbox-empty">
          no pending handoffs
          {total > pending.length ? ` · ${total - pending.length} resolved` : ""}
        </div>
      </div>
    );
  }

  const visible = pending.slice(0, maxRows);
  return (
    <div className="inbox">
      <div className="inbox-title">
        Pending handoffs
        <span className="inbox-count">{pending.length}</span>
      </div>
      <ul className="inbox-list">
        {visible.map((h) => (
          <HandoffCard key={h.id} handoff={h} />
        ))}
      </ul>
      {pending.length > maxRows && (
        <div className="inbox-empty">
          +{pending.length - maxRows} more in queue
        </div>
      )}
    </div>
  );
}

function HandoffCard({ handoff }: { handoff: Handoff }) {
  const target = handoff.to_agent || handoff.target_agent_id || "any";
  return (
    <li className="card-inner">
      <div className="card-head">
        <span className="card-actor">{handoff.from_agent}</span>
        <span className="card-arrow">→</span>
        <span className="card-actor">{target}</span>
        <span className="card-age">{ageOf(handoff.created_at)}</span>
      </div>
      <div className="card-summary">{handoff.summary || "(no summary)"}</div>
      {handoff.context && (
        <div className="card-context">{truncate(handoff.context, 140)}</div>
      )}
      <div className="card-action-hint">
        Accept/reject via <code>loom agent</code> or the HUD; in-widget buttons
        ship in slice 2-β.
      </div>
    </li>
  );
}

function isPending(status: string): boolean {
  const s = status.toLowerCase();
  // Treat any non-terminal status as pending. The HUD currently uses
  // "pending" but we tolerate other terms (e.g. "queued", "new") so
  // the widget keeps working if the upstream vocabulary widens.
  return s === "" || s === "pending" || s === "queued" || s === "new" || s === "open";
}

function ageOf(ts: string): string {
  if (!ts) return "";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "";
  const sec = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  return `${Math.floor(hr / 24)}d`;
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) + "…" : s;
}
