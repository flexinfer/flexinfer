/**
 * Shared formatting utilities — replaces duplicated functions across panels.
 *
 * All functions are pure and side-effect-free.
 */

import { AGENT_COLORS, STATUS_VARIANTS, type BadgeVariant } from './tokens.ts';

// ---- Time formatting ----

/** Format timestamp to HH:MM:SS (24h). Returns '--:--:--' for falsy input. */
export function formatTime(ts: string | number | Date | null | undefined): string {
  if (!ts) return '--:--:--';
  const d = ts instanceof Date ? ts : new Date(ts);
  if (isNaN(d.getTime())) return '--:--:--';
  return d.toLocaleTimeString('en-US', { hour12: false });
}

/** Format timestamp to HH:MM only. */
export function formatTimeShort(ts: string | number | Date | null | undefined): string {
  if (!ts) return '--:--';
  const d = ts instanceof Date ? ts : new Date(ts);
  if (isNaN(d.getTime())) return '--:--';
  return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false });
}

/** Format timestamp to YYYY-MM-DD HH:MM:SS. */
export function formatDateTime(ts: string | number | Date | null | undefined): string {
  if (!ts) return '---';
  const d = ts instanceof Date ? ts : new Date(ts);
  if (isNaN(d.getTime())) return '---';
  return d.toLocaleDateString('en-CA') + ' ' + d.toLocaleTimeString('en-US', { hour12: false });
}

/** Relative time string: "3s ago", "5m ago", "2h ago", "1d ago". */
export function relativeTime(ts: string | number | Date | null | undefined): string {
  if (!ts) return '---';
  const then = ts instanceof Date ? ts.getTime() : new Date(ts).getTime();
  if (isNaN(then)) return '---';
  const diff = Date.now() - then;
  const secs = Math.floor(diff / 1_000);
  if (secs < 0) return 'just now';
  if (secs < 60) return secs + 's ago';
  const mins = Math.floor(secs / 60);
  if (mins < 60) return mins + 'm ago';
  const hours = Math.floor(mins / 60);
  if (hours < 24) return hours + 'h ago';
  const days = Math.floor(hours / 24);
  return days + 'd ago';
}

// ---- Number formatting ----

/** Abbreviate large numbers: 1234 -> "1.2K", 1234567 -> "1.2M". */
export function formatNumber(n: number | null | undefined): string {
  if (n == null) return '0';
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return String(n);
}

/** Format bytes: 1024 -> "1.0 KB", 1048576 -> "1.0 MB". */
export function formatBytes(bytes: number | null | undefined): string {
  if (bytes == null || bytes === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let i = 0;
  let val = bytes;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return val.toFixed(i === 0 ? 0 : 1) + ' ' + units[i];
}

// ---- String truncation ----

/** Truncate an ID/hash to first N characters with ellipsis. */
export function truncateId(id: string | null | undefined, len = 8): string {
  if (!id) return '---';
  if (id.length <= len) return id;
  return id.slice(0, len) + '\u2026';
}

/** Smart path truncation: keeps filename, shortens directory prefix. */
export function truncatePath(path: string | null | undefined, maxLen = 40): string {
  if (!path) return '---';
  if (path.length <= maxLen) return path;
  const parts = path.split('/');
  const filename = parts.pop() || '';
  if (filename.length >= maxLen - 4) return '\u2026' + filename.slice(-(maxLen - 1));
  const budget = maxLen - filename.length - 4; // 4 = ".../".length
  const dirPath = parts.join('/');
  return dirPath.slice(0, budget) + '\u2026/' + filename;
}

const ANSI_CSI_RE = /\u001b\[[0-9;?]*[ -/]*[@-~]/g;
const ANSI_OSC_RE = /\u001b\][^\u0007]*(?:\u0007|\u001b\\)/g;
const CONTROL_CHARS_RE = /[\u0000-\u001f\u007f-\u009f]/g;
const ZERO_WIDTH_BIDI_RE = /[\u200b-\u200f\u202a-\u202e\u2060-\u2069\ufeff]/g;
const MULTISPACE_RE = /\s+/g;

/** Strip ANSI/control/bidi characters that can destabilize table rendering. */
export function sanitizeText(value: string | null | undefined): string {
  if (!value) return '';
  return value
    .replace(ANSI_CSI_RE, '')
    .replace(ANSI_OSC_RE, '')
    .replace(CONTROL_CHARS_RE, ' ')
    .replace(ZERO_WIDTH_BIDI_RE, '')
    .replace(MULTISPACE_RE, ' ')
    .trim();
}

// ---- Status / variant mapping ----

/** Map a status string to a badge variant for consistent coloring. */
export function statusVariant(status: string | null | undefined): BadgeVariant {
  if (!status) return 'muted';
  const lower = status.toLowerCase().replace(/\s+/g, '_');
  return STATUS_VARIANTS[lower] ?? 'info';
}

/** Map a status string to a CSS color variable. */
export function statusColor(status: string | null | undefined): string {
  const variant = statusVariant(status);
  const map: Record<BadgeVariant, string> = {
    info: 'var(--info)',
    success: 'var(--success)',
    warning: 'var(--warning)',
    error: 'var(--error)',
    accent: 'var(--accent)',
    muted: 'var(--fg-muted)',
  };
  return map[variant];
}

// ---- Agent helpers ----

/** Get the CSS color variable for an agent type string. */
export function agentColor(agentType: string | null | undefined): string {
  if (!agentType) return 'var(--fg-secondary)';
  const lower = agentType.toLowerCase();
  for (const [key, color] of Object.entries(AGENT_COLORS)) {
    if (lower.includes(key)) return color;
  }
  return 'var(--fg-secondary)';
}

/** Get a display label for an agent type. */
export function agentLabel(agentType: string | null | undefined): string {
  if (!agentType) return 'unknown';
  const lower = agentType.toLowerCase();
  if (lower.includes('claude')) return 'Claude';
  if (lower.includes('codex')) return 'Codex';
  if (lower.includes('gemini')) return 'Gemini';
  if (lower.includes('copilot')) return 'Copilot';
  return agentType;
}

// ---- Event / entry icons ----

/** Map an event type string to a unicode icon character. */
export function eventIcon(type: string | null | undefined): string {
  if (!type) return '\u25CF'; // ●
  if (type.includes('session.start')) return '\u25B6'; // ▶
  if (type.includes('session.end') || type.includes('session.reaped')) return '\u25A0'; // ■
  if (type.includes('heartbeat')) return '\u2665'; // ♥
  if (type.includes('task.create')) return '\u2795'; // +
  if (type.includes('task.update') || type.includes('task.complete')) return '\u2611'; // ☑
  if (type.includes('workflow.approve')) return '\u2714'; // ✔
  if (type.includes('workflow.reject')) return '\u2718'; // ✘
  if (type.includes('handoff')) return '\u21C6'; // ⇆
  if (type.includes('claim')) return '\u{1F512}'; // 🔒
  if (type.includes('memory')) return '\u29BE'; // ⦾
  if (type.includes('error') || type.includes('fail')) return '\u26A0'; // ⚠
  return '\u25CF'; // ●
}

/** Map a context entry type to a badge variant. */
export function entryVariant(type: string | null | undefined): BadgeVariant {
  if (!type) return 'info';
  const lower = type.toLowerCase();
  if (lower === 'decision') return 'accent';
  if (lower === 'finding' || lower === 'discovery') return 'success';
  if (lower === 'error' || lower === 'issue') return 'error';
  if (lower === 'question' || lower === 'todo') return 'warning';
  return 'info';
}

// ---- Priority helpers ----

/** Map a priority level to a badge variant. */
export function priorityVariant(priority: string | number | null | undefined): BadgeVariant {
  if (priority == null) return 'muted';
  const p = typeof priority === 'string' ? priority.toLowerCase() : '';
  if (p === 'critical' || p === 'urgent' || priority === 1) return 'error';
  if (p === 'high' || priority === 2) return 'warning';
  if (p === 'medium' || p === 'normal' || priority === 3) return 'info';
  if (p === 'low' || priority === 4) return 'accent';
  return 'muted';
}

// ---- Confidence / threshold helpers ----

/** Map a 0-1 confidence score to a CSS color. */
export function confidenceColor(c: number | null | undefined): string {
  if (c == null) return 'var(--fg-muted)';
  if (c >= 0.8) return 'var(--success)';
  if (c >= 0.5) return 'var(--warning)';
  return 'var(--error)';
}

/** Map a 0-100 percentage to a threshold color (normal/warning/error). */
export function thresholdColor(pct: number): string {
  if (pct >= 80) return 'var(--error)';
  if (pct >= 60) return 'var(--warning)';
  return 'var(--success)';
}
