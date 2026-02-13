/**
 * Design system tokens — JS-side constants that mirror CSS custom properties.
 * Use these for values needed in JS (animation durations, breakpoints, polling intervals).
 * CSS values should be consumed via var(--token-name) in stylesheets.
 */

// ---- Timing (ms) ----

export const DURATION_FAST = 100;
export const DURATION_NORMAL = 200;
export const DURATION_SLOW = 350;

// ---- Polling & SSE (ms) ----

export const POLL_INTERVAL_HEALTH = 5_000;
export const POLL_INTERVAL_FLEET = 15_000;
export const POLL_INTERVAL_MEMORY = 10_000;
export const POLL_INTERVAL_WORKFLOWS = 5_000;
export const POLL_INTERVAL_STREAM = 5_000;
export const POLL_FALLBACK = 30_000;

export const SSE_RETRY_INITIAL = 1_000;
export const SSE_RETRY_MAX = 30_000;
export const SSE_CIRCUIT_THRESHOLD = 5;

// ---- Layout (px) ----

export const HEADER_HEIGHT = 40;
export const STATUSBAR_HEIGHT = 28;
export const DRAWER_WIDTH = 400;
export const DRAWER_MIN_WIDTH = 320;
export const DRAWER_MAX_WIDTH = 600;

// ---- Breakpoints (px) ----

export const BREAKPOINT_SM = 640;
export const BREAKPOINT_MD = 1024;
export const BREAKPOINT_LG = 1440;
export const BREAKPOINT_XL = 1920;

// ---- Lists ----

export const VIRTUAL_SCROLL_THRESHOLD = 50;
export const VIRTUAL_ITEM_HEIGHT = 32;
export const VIRTUAL_BUFFER = 10;

// ---- Data limits ----

export const TOKEN_HISTORY_SIZE = 20;
export const TIMELINE_RING_SIZE = 200;
export const ACTIVITY_FEED_LIMIT = 10;
export const SPARKLINE_HISTORY = 20;

// ---- Agent type colors (for JS-side usage) ----

export const AGENT_COLORS: Record<string, string> = {
  claude: 'var(--agent-claude)',
  codex: 'var(--agent-codex)',
  gemini: 'var(--agent-gemini)',
  copilot: 'var(--agent-copilot)',
};

// ---- Status variant mapping ----

export type BadgeVariant = 'info' | 'success' | 'warning' | 'error' | 'accent' | 'muted';

export const STATUS_VARIANTS: Record<string, BadgeVariant> = {
  // Task/workflow statuses
  pending: 'warning',
  in_progress: 'info',
  running: 'info',
  active: 'success',
  completed: 'success',
  resolved: 'success',
  failed: 'error',
  error: 'error',
  blocked: 'error',
  cancelled: 'muted',
  waiting: 'warning',
  idle: 'muted',
  offline: 'muted',
  degraded: 'warning',
  healthy: 'success',
  down: 'error',
  // Workflow-specific
  approved: 'success',
  rejected: 'error',
  waiting_approval: 'warning',
  // Memory-specific
  compressed: 'accent',
  expired: 'error',
};
