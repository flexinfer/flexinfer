// Mills recursion helpers — Phase 6 slice 6.2.
//
// When a worker is running under a Mills pipeline run (i.e. the spawn
// orchestrator set LOOM_MILLS_RUN_ID + the operator URL + admin token
// on the agent's environment), it can fan out into a child run via
// the recursion endpoint shipped in slice 6.1:
//
//   POST {LOOM_MILLS_OPERATOR_URL}/api/mills/pipeline/runs/{parent}/subrun
//   Authorization: Bearer {LOOM_MILLS_ADMIN_TOKEN}
//   Content-Type: application/json
//   Body: {backlog_id, template, estimate_usd?, slice_spec?}
//
// The operator's SubrunGuard runs the depth/budget/cycle checks and
// either returns 201 + {run_id, parent_run_id, depth} or 4xx with a
// stable code prefix (e.g. `recursion_depth_exceeded:`) the worker
// can branch on.
//
// This module is a thin, dependency-free wrapper around the HTTP
// call. It does not import the Anthropic / Codex SDKs, so it stays
// usable from both drivers + the spawn orchestrator's own tests.

/** Subrun creation request body. Mirrors handlers_pipeline.go. */
export interface SubrunRequest {
  backlog_id: string;
  template: string;
  estimate_usd?: number;
  slice_spec?: string;
}

/** Successful response shape. */
export interface SubrunResponse {
  run_id: string;
  parent_run_id: string;
  depth: number;
}

/** Stable guard codes the worker can branch on. */
export type SubrunGuardCode =
  | "recursion_disabled"
  | "recursion_parent_not_found"
  | "recursion_depth_exceeded"
  | "budget_subrun_too_large"
  | "recursion_cycle_detected"
  | "recursion_slicespec_too_large"
  | "recursion_missing_fields";

/**
 * Error thrown when the operator rejects the subrun request. `code` is
 * the stable string from pkg/mills/pipeline/recursion.go's GuardCode
 * enum; the operator's HTTP body always begins with `<code>:` for
 * 4xx responses, which is what we parse here.
 */
export class SubrunGuardError extends Error {
  readonly code: SubrunGuardCode | "unknown_guard";
  readonly status: number;
  readonly raw: string;
  constructor(status: number, raw: string) {
    const code = parseGuardCode(raw);
    super(`${code}: ${raw.trim()}`);
    this.name = "SubrunGuardError";
    this.code = code;
    this.status = status;
    this.raw = raw;
  }
}

const KNOWN_GUARD_CODES = new Set<SubrunGuardCode>([
  "recursion_disabled",
  "recursion_parent_not_found",
  "recursion_depth_exceeded",
  "budget_subrun_too_large",
  "recursion_cycle_detected",
  "recursion_slicespec_too_large",
  "recursion_missing_fields",
]);

function parseGuardCode(body: string): SubrunGuardCode | "unknown_guard" {
  const colon = body.indexOf(":");
  const head = colon < 0 ? body.trim() : body.slice(0, colon).trim();
  return KNOWN_GUARD_CODES.has(head as SubrunGuardCode)
    ? (head as SubrunGuardCode)
    : "unknown_guard";
}

/**
 * Returns true when the worker is running under a Mills pipeline run
 * with a reachable operator + admin token. Slice 6.2 surface predicate
 * — drivers should advertise the subrun tool to their agent only when
 * this returns true.
 */
export function millsRecursionAvailable(env: NodeJS.ProcessEnv = process.env): boolean {
  return Boolean(
    env.LOOM_MILLS_RUN_ID &&
      env.LOOM_MILLS_OPERATOR_URL &&
      env.LOOM_MILLS_ADMIN_TOKEN,
  );
}

/**
 * Creates a child pipeline run under the current Mills parent. Reads
 * `LOOM_MILLS_RUN_ID`, `LOOM_MILLS_OPERATOR_URL`, and
 * `LOOM_MILLS_ADMIN_TOKEN` from the environment by default; tests can
 * override via the second arg.
 *
 * Throws SubrunGuardError on 4xx (so callers can branch on
 * .code === "recursion_depth_exceeded"), and a plain Error on network /
 * 5xx failures.
 */
export async function createMillsSubrun(
  req: SubrunRequest,
  opts: {
    env?: NodeJS.ProcessEnv;
    fetchImpl?: typeof globalThis.fetch;
    timeoutMs?: number;
  } = {},
): Promise<SubrunResponse> {
  const env = opts.env ?? process.env;
  const parent = env.LOOM_MILLS_RUN_ID;
  const base = env.LOOM_MILLS_OPERATOR_URL;
  const token = env.LOOM_MILLS_ADMIN_TOKEN;
  if (!parent || !base || !token) {
    throw new Error(
      "createMillsSubrun: LOOM_MILLS_RUN_ID, LOOM_MILLS_OPERATOR_URL, and LOOM_MILLS_ADMIN_TOKEN must all be set",
    );
  }
  if (!req.backlog_id || !req.template) {
    throw new Error("createMillsSubrun: backlog_id and template are required");
  }

  const url = `${base.replace(/\/$/, "")}/api/mills/pipeline/runs/${encodeURIComponent(parent)}/subrun`;
  const fetchImpl = opts.fetchImpl ?? globalThis.fetch;
  const ctrl = opts.timeoutMs ? new AbortController() : undefined;
  const timer = ctrl
    ? setTimeout(() => ctrl.abort(), opts.timeoutMs!)
    : undefined;

  let res: Response;
  try {
    res = await fetchImpl(url, {
      method: "POST",
      headers: {
        "Authorization": `Bearer ${token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(req),
      signal: ctrl?.signal,
    });
  } finally {
    if (timer) clearTimeout(timer);
  }

  const text = await res.text();
  if (res.status >= 400 && res.status < 500) {
    throw new SubrunGuardError(res.status, text);
  }
  if (!res.ok) {
    throw new Error(`createMillsSubrun: operator returned ${res.status}: ${text.trim()}`);
  }
  // Body is JSON on success; parse defensively.
  try {
    return JSON.parse(text) as SubrunResponse;
  } catch (e) {
    throw new Error(
      `createMillsSubrun: failed to parse 201 response body (${(e as Error).message}): ${text.slice(0, 200)}`,
    );
  }
}
