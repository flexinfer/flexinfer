import { useStream, type StreamEntry } from "../hooks/useStream";

// EventTicker renders the last N entries from the loom HUD context
// stream as a compact list. Entry types get a coloured chip so users
// can scan agent activity quickly. Truncated to maxRows so the widget
// doesn't grow unbounded in the chat panel.
export function EventTicker({ maxRows = 8 }: { maxRows?: number }) {
  const { entries, error, loading } = useStream();

  if (error) {
    return (
      <div className="ticker">
        <div className="ticker-title">Recent activity</div>
        <div className="banner banner-error">{error}</div>
      </div>
    );
  }

  if (loading && entries.length === 0) {
    return (
      <div className="ticker">
        <div className="ticker-title">Recent activity</div>
        <div className="ticker-empty">loading…</div>
      </div>
    );
  }

  if (entries.length === 0) {
    return (
      <div className="ticker">
        <div className="ticker-title">Recent activity</div>
        <div className="ticker-empty">no recent entries</div>
      </div>
    );
  }

  const visible = entries.slice(0, maxRows);
  return (
    <div className="ticker">
      <div className="ticker-title">Recent activity</div>
      <ul className="ticker-list">
        {visible.map((entry) => (
          <TickerRow key={entry.id} entry={entry} />
        ))}
      </ul>
    </div>
  );
}

function TickerRow({ entry }: { entry: StreamEntry }) {
  return (
    <li className="ticker-row">
      <span className={`chip chip-${chipKind(entry.entry_type)}`}>
        {entry.entry_type}
      </span>
      <span className="ticker-agent">{entry.agent_id || entry.agent || "unknown"}</span>
      <span className="ticker-title-text">{entry.title || entry.content?.slice(0, 60) || "(no title)"}</span>
      <span className="ticker-time">{formatTime(entry.timestamp)}</span>
    </li>
  );
}

// chipKind maps the HUD entry_type to a CSS modifier so the chip
// colour conveys the kind at a glance. Unknown types fall back to
// neutral.
function chipKind(entryType: string): string {
  const k = entryType.toLowerCase();
  if (k === "decision") return "decision";
  if (k === "finding") return "finding";
  if (k === "task") return "task";
  if (k === "handoff") return "handoff";
  return "neutral";
}

// formatTime renders an absolute timestamp as "5m ago" when recent,
// HH:MM for same-day, and the full ISO date otherwise. Best-effort:
// if the string can't be parsed we fall back to the raw value.
function formatTime(ts: string): string {
  if (!ts) return "";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return ts;
  const now = Date.now();
  const deltaMs = now - date.getTime();
  if (deltaMs < 0) return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const sec = Math.floor(deltaMs / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return date.toLocaleDateString();
}
