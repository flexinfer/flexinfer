// useAction - Svelte 5 helper that wraps an async mutation in a uniform
// pending/error/retry surface, records it in the action audit ring, and emits
// a toast on success/failure.
//
// Designed for the HUD UX overhaul Slice A (Operator Inbox). Callers:
//   const approve = useAction({
//     label: 'Approve workflow',
//     source: 'OverviewPanel:inbox/approve',
//     run: () => fetch(`/api/workflows/${id}/approve`, { method: 'POST' }),
//     optimistic: () => workflowStore.markApproving(id),
//     rollback:   () => workflowStore.unmarkApproving(id),
//   });
//   await approve.run();
//
// If `run` resolves without throwing, the action is recorded as success.
// If it throws or returns a non-OK Response, rollback (if provided) is called
// and the action is recorded as error with the message.

import { actionStore } from '../stores/action.svelte.ts';
import { toastStore } from '../stores/toasts.svelte.ts';

export interface ActionConfig<T> {
  /** Human-readable label shown in toast + audit drawer. */
  label: string;
  /** Identifier of the originating panel/component, e.g. "OverviewPanel:inbox/approve". */
  source: string;
  /** Async mutation. May return Response (auto-checked for `.ok`) or any value. */
  run: () => Promise<T>;
  /** Optional optimistic update applied before `run`. */
  optimistic?: () => void;
  /** Optional rollback called when `run` rejects (only if `optimistic` provided). */
  rollback?: () => void;
  /** Suppress the success toast (audit entry still recorded). */
  silentSuccess?: boolean;
  /** Suppress the error toast (audit entry still recorded). */
  silentError?: boolean;
  /** Mark the action as non-retryable in the audit drawer. */
  nonRetryable?: boolean;
}

export interface ActionHandle<T> {
  /** Trigger the action. Returns the resolved value, or `undefined` on failure. */
  run: () => Promise<T | undefined>;
  /** Re-run the most recent invocation. No-op if never called. */
  retry: () => Promise<T | undefined>;
  /** True while an invocation is in flight. */
  readonly pending: boolean;
  /** Last error message, or null. */
  readonly error: string | null;
  /** Last successful result, or undefined. */
  readonly lastResult: T | undefined;
  /** The action store entry id of the most recent invocation, or null. */
  readonly currentId: string | null;
}

async function extractErrorMessage(value: unknown): Promise<string> {
  if (value instanceof Error) return value.message || 'Unknown error';
  if (value instanceof Response) {
    const status = value.status;
    let detail = '';
    try {
      const ct = value.headers.get('content-type') ?? '';
      if (ct.includes('application/json')) {
        const body = await value.clone().json();
        detail = body?.error || body?.message || JSON.stringify(body);
      } else {
        detail = (await value.clone().text()).slice(0, 200);
      }
    } catch {
      // ignore; status alone is informative
    }
    return detail ? `HTTP ${status}: ${detail}` : `HTTP ${status}`;
  }
  if (typeof value === 'string') return value;
  try { return JSON.stringify(value); } catch { return 'Unknown error'; }
}

export function useAction<T = unknown>(config: ActionConfig<T>): ActionHandle<T> {
  let pending = $state(false);
  let error = $state<string | null>(null);
  let lastResult = $state<T | undefined>(undefined);
  let currentId = $state<string | null>(null);

  async function invoke(): Promise<T | undefined> {
    if (pending) return undefined;
    pending = true;
    error = null;

    const id = actionStore.start(config.label, config.source, !config.nonRetryable);
    currentId = id;

    if (config.optimistic) {
      try { config.optimistic(); } catch { /* optimistic must not throw, but be defensive */ }
    }

    try {
      const result = await config.run();
      if (result instanceof Response && !result.ok) {
        const msg = await extractErrorMessage(result);
        throw new Error(msg);
      }
      lastResult = result;
      actionStore.succeed(id);
      if (!config.silentSuccess) toastStore.success(`${config.label} ✓`);
      return result;
    } catch (e) {
      const msg = await extractErrorMessage(e);
      error = msg;
      actionStore.fail(id, msg);
      if (config.optimistic && config.rollback) {
        try { config.rollback(); } catch { /* rollback best-effort */ }
        actionStore.markRolledBack(id);
      }
      if (!config.silentError) toastStore.error(`${config.label} failed: ${msg}`);
      return undefined;
    } finally {
      pending = false;
    }
  }

  return {
    run: invoke,
    retry: invoke,
    get pending() { return pending; },
    get error() { return error; },
    get lastResult() { return lastResult; },
    get currentId() { return currentId; },
  };
}
