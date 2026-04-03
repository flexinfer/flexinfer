export interface MergeCandidate {
  agent_id: string;
  branch: string;
  namespace?: string;
  status: string;
  merge_ready: boolean;
  merge_blockers?: string[];
  conflict_files: number;
  blocked_tasks: number;
  task_count: number;
}

export interface MergeQueueSummary {
  total_branches: number;
  ready_to_merge: number;
  blocked: number;
  conflict_pairs: number;
}

export interface MergeQueueResponse {
  ready: MergeCandidate[];
  blocked: MergeCandidate[];
  summary: MergeQueueSummary;
}

export interface MergeConflictPair {
  left_agent: string;
  left_branch: string;
  right_agent: string;
  right_branch: string;
  conflict_type: string;
  files?: string[];
  detail?: string;
}

export interface MergeConflictsResponse {
  conflicts: MergeConflictPair[];
  count: number;
}

async function parseResponse(res: Response): Promise<any> {
  let data: any = null;
  try {
    data = await res.json();
  } catch {
    data = null;
  }
  if (!res.ok) {
    const msg = data?.error || `${res.status} ${res.statusText}`;
    throw new Error(msg);
  }
  return data;
}

export async function fetchMergeQueue(): Promise<MergeQueueResponse> {
  const res = await globalThis.fetch('/api/merge-queue');
  const data = await parseResponse(res);
  return {
    ready: data?.ready ?? [],
    blocked: data?.blocked ?? [],
    summary: data?.summary ?? { total_branches: 0, ready_to_merge: 0, blocked: 0, conflict_pairs: 0 },
  };
}

export async function fetchMergeConflicts(): Promise<MergeConflictsResponse> {
  const res = await globalThis.fetch('/api/merge-queue/conflicts');
  const data = await parseResponse(res);
  return {
    conflicts: data?.conflicts ?? [],
    count: data?.count ?? 0,
  };
}
