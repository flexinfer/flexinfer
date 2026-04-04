export interface CapacityInfo {
  agent_id: string;
  status: string;
  active_tasks: number;
  max_tasks: number;
  tokens_used: number;
  token_budget: number;
  utilization: number;
  available_slots: number;
  idle_since?: string;
}

export interface DispatchRecommendation {
  task_id: string;
  task_title: string;
  recommended_agent: string;
  score: number;
  reason: string;
}

export interface ShuttleSnapshot {
  capacities: CapacityInfo[];
  recommendations: DispatchRecommendation[];
  pending_tasks: number;
  active_agents: number;
  system_load: number;
  updated_at: string;
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

export async function fetchShuttleStatus(): Promise<ShuttleSnapshot> {
  const res = await globalThis.fetch('/api/shuttle/status');
  const data = await parseResponse(res);
  return {
    capacities: data?.capacities ?? [],
    recommendations: data?.recommendations ?? [],
    pending_tasks: data?.pending_tasks ?? 0,
    active_agents: data?.active_agents ?? 0,
    system_load: data?.system_load ?? 0,
    updated_at: data?.updated_at ?? '',
  };
}
