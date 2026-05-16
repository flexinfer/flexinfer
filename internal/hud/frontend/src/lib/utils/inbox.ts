// inbox - selectors that derive triage cards from the existing live stores.
//
// Each selector returns zero or more CardSpec entries for a single "pressure
// kind" defined by D2 in `.loom/116-product-spec-hud-ux-overhaul-2026-05-15.md`.
// Selectors are pure: they read stores and return data; the Overview composes
// the deck and the InboxCard component renders.
//
// Wiring philosophy:
//   - Every card has a primary action (handler) - either a drill (`router.navigate`)
//     or a real mutation. Real mutations are routed through useAction() at the
//     callsite so they pick up audit + toast + confirm.
//   - If a backend endpoint is missing for a card kind, the card stays drill-only
//     - no destructive action stub. New action endpoints are added in their own
//     slice (per product spec §"In Scope").
//   - Selectors return [] when their store flag is below threshold; an empty
//     deck collapses to a "System nominal" empty state in InboxDeck.
//
// Card severities follow the existing OverviewPanel `attentionLanes` tones:
//   - alert: red - operator action needed now (down servers, file conflicts)
//   - warn:  amber - work blocked / awaiting decision
//   - info:  neutral - drill suggestion only

import type { router } from '../stores/router.svelte.ts';
import type { coordinationStore } from '../stores/coordination.svelte.ts';
import type { taskStore } from '../stores/tasks.svelte.ts';
import type { workflowStore, WorkflowSummary } from '../stores/workflows.svelte.ts';
import type { healthStore } from '../stores/health.svelte.ts';
import type { fleetStore } from '../stores/fleet.svelte.ts';
import type { rbacStore } from '../stores/rbac.svelte.ts';
import type { liveSessionsStore } from '../stores/liveSessions.svelte.ts';

export type CardSeverity = 'alert' | 'warn' | 'info';

export type CardKind =
  | 'file_conflict'
  | 'blocked_task'
  | 'pending_approval'
  | 'server_down'
  | 'orphan_session'
  | 'rbac_denied_spike'
  | 'stale_session';

/**
 * Action verb shown on the card.
 *
 * Mutation actions carry an optional `confirm` payload; the card component
 * routes through ConfirmDialog when present. Drill-only actions navigate via
 * the supplied router callback.
 */
export interface CardAction {
  label: string;
  /** Primary handler. May be sync (drill) or async (mutation). */
  run: () => void | Promise<void>;
  /** Optional second-line confirmation copy. When present InboxCard gates with ConfirmDialog. */
  confirm?: {
    title: string;
    message: string;
    confirmLabel: string;
    variant?: 'danger' | 'warn' | 'default';
  };
  /** Mark the primary action as destructive (style hint). */
  destructive?: boolean;
}

export interface CardSpec {
  kind: CardKind;
  severity: CardSeverity;
  headline: string;
  detail: string;
  /** Primary action - drill or mutation. Always present. */
  primary: CardAction;
  /** Optional drill that supplements an inline primary mutation. */
  secondary?: CardAction;
  /** Stable key for keyed iteration; selectors generate one per card. */
  key: string;
}

export interface InboxStores {
  router: typeof router;
  coordination: typeof coordinationStore;
  tasks: typeof taskStore;
  workflows: typeof workflowStore;
  health: typeof healthStore;
  fleet: typeof fleetStore;
  rbac: typeof rbacStore;
  liveSessions: typeof liveSessionsStore;
}

// ───────────────────────────────────────────────────────────────────────────
// Per-kind selectors
// ───────────────────────────────────────────────────────────────────────────

export function selectFileConflicts({ router, coordination }: InboxStores): CardSpec[] {
  const n = coordination.summary.conflict_files ?? 0;
  if (n <= 0) return [];
  return [
    {
      kind: 'file_conflict',
      key: 'file_conflict',
      severity: 'alert',
      headline: `${n} file conflict${n === 1 ? '' : 's'} detected`,
      detail: 'Overlapping claims block parallel work. Resolve in Dispatch.',
      primary: {
        label: 'Open Dispatch',
        run: () => router.navigate('dispatch'),
      },
    },
  ];
}

export function selectBlockedTasks({ router, tasks, coordination }: InboxStores): CardSpec[] {
  const n = tasks.blockedCount;
  const cross = coordination.summary.cross_agent_blockers ?? 0;
  if (n <= 0 && cross <= 0) return [];
  return [
    {
      kind: 'blocked_task',
      key: 'blocked_task',
      severity: 'warn',
      headline: `${n} blocked task${n === 1 ? '' : 's'}`,
      detail: cross > 0
        ? `${cross} cross-agent blocker${cross === 1 ? '' : 's'} - inspect in Dispatch.`
        : 'Inspect the work queue in Dispatch.',
      primary: {
        label: 'Open Dispatch',
        run: () => router.navigate('dispatch'),
      },
      secondary: {
        label: 'View Tasks',
        run: () => router.navigate('tasks'),
      },
    },
  ];
}

export function selectPendingApprovals(stores: InboxStores): CardSpec[] {
  const { router, workflows } = stores;
  const waiting = workflows.activeWorkflows.filter((w) => w.status === 'waiting_approval');
  if (waiting.length === 0) return [];
  // Single aggregate card; the Workflows panel handles per-workflow approval UI.
  // When exactly one is waiting we can act inline via useAction at the callsite.
  const single = waiting.length === 1 ? waiting[0] : null;
  const inlineApprove = single ? approvalAction(single, stores) : null;
  return [
    {
      kind: 'pending_approval',
      key: 'pending_approval',
      severity: 'warn',
      headline: `${waiting.length} workflow${waiting.length === 1 ? '' : 's'} awaiting approval`,
      detail: single
        ? `${single.name ?? single.definition_id} - step ${single.current_step || 'pending'}`
        : 'Review approvals in the Workflows panel.',
      primary: inlineApprove ?? {
        label: 'Review approvals',
        run: () => router.navigate('workflows'),
      },
      secondary: inlineApprove ? { label: 'Open Workflows', run: () => router.navigate('workflows') } : undefined,
    },
  ];
}

function approvalAction(wf: WorkflowSummary, { workflows }: InboxStores): CardAction | null {
  // The approve endpoint needs the step_id; pull it from the workflow shape
  // (`current_step` is the step name; `steps[].id` is the canonical id).
  // Fall back to `current_step` when no explicit id matches.
  const stepId = (wf.steps ?? []).find((s) => s.name === wf.current_step || s.id === wf.current_step)?.id
    ?? wf.current_step
    ?? '';
  if (!wf.id || !stepId) return null;
  return {
    label: 'Approve',
    run: () => workflows.approveStep(wf.id, stepId),
    confirm: {
      title: 'Approve workflow step?',
      message: `${wf.name ?? wf.definition_id} - step "${wf.current_step || stepId}" will advance.`,
      confirmLabel: 'Approve',
      variant: 'default',
    },
  };
}

export function selectServerDown({ router, health }: InboxStores): CardSpec[] {
  const n = health.downCount;
  if (n <= 0) return [];
  return [
    {
      kind: 'server_down',
      key: 'server_down',
      severity: 'alert',
      headline: `${n} server${n === 1 ? '' : 's'} down`,
      detail: 'Open server diagnostics to investigate health.',
      primary: {
        label: 'Open Servers',
        run: () => router.navigate('servers'),
      },
    },
  ];
}

export function selectOrphanSessions({ router, fleet }: InboxStores): CardSpec[] {
  const n = fleet.unifiedSummary.orphans ?? 0;
  if (n <= 0) return [];
  // No desktop end-session endpoint today; drill into Fleet's orphan view.
  // Real reap action lives behind the bridge POST /api/agent/session-end and
  // will be wired in a follow-up slice that adds the desktop handler.
  return [
    {
      kind: 'orphan_session',
      key: 'orphan_session',
      severity: 'warn',
      headline: `${n} orphan session${n === 1 ? '' : 's'}`,
      detail: 'Tasks without a live agent. Review in Fleet to reap or reattach.',
      primary: {
        label: 'Open Fleet',
        run: () => router.navigate('fleet'),
      },
    },
  ];
}

export function selectRbacDenials({ router, rbac }: InboxStores): CardSpec[] {
  if (!rbac.enabled) return [];
  const n = rbac.deniedCount;
  if (n <= 0) return [];
  return [
    {
      kind: 'rbac_denied_spike',
      key: 'rbac_denied_spike',
      severity: 'info',
      headline: `${n} RBAC denial${n === 1 ? '' : 's'}`,
      detail: 'Inspect policy to find which rule is blocking work.',
      primary: {
        label: 'Open RBAC',
        run: () => router.navigate('rbac'),
      },
    },
  ];
}

export function selectStaleSessions({ router, liveSessions }: InboxStores): CardSpec[] {
  // "Stale" = sessions whose last_activity is older than 5min and not yet ended.
  const STALE_MS = 5 * 60 * 1000;
  const now = Date.now();
  const stale = liveSessions.visibleSessions.filter(
    (s) => s.ended_at === undefined && now - s.last_activity > STALE_MS
  );
  if (stale.length === 0) return [];
  return [
    {
      kind: 'stale_session',
      key: 'stale_session',
      severity: 'info',
      headline: `${stale.length} stale session${stale.length === 1 ? '' : 's'}`,
      detail: 'No activity in 5+ minutes. Open Fleet to recover or end.',
      primary: {
        label: 'Open Fleet',
        run: () => router.navigate('fleet'),
      },
    },
  ];
}

// ───────────────────────────────────────────────────────────────────────────
// Aggregate
// ───────────────────────────────────────────────────────────────────────────

const SELECTORS: Array<(s: InboxStores) => CardSpec[]> = [
  selectFileConflicts,
  selectServerDown,
  selectBlockedTasks,
  selectPendingApprovals,
  selectOrphanSessions,
  selectStaleSessions,
  selectRbacDenials,
];

/** Ordered card list. Alerts surface first, then warnings, then info. */
export function selectInboxCards(stores: InboxStores): CardSpec[] {
  const cards: CardSpec[] = [];
  for (const sel of SELECTORS) cards.push(...sel(stores));
  // Stable severity sort - keep within-kind order from SELECTORS.
  const weight: Record<CardSeverity, number> = { alert: 0, warn: 1, info: 2 };
  return cards.sort((a, b) => weight[a.severity] - weight[b.severity]);
}
