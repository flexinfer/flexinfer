export interface HandoffRecord {
  id: string;
  from_agent: string;
  to_agent?: string;
  summary: string;
  context?: string;
  status: string;
  created_at: string;
  accepted_at?: string;
}

export interface TemplateRecord {
  id: string;
  name: string;
  description?: string;
}

export interface DispatchTaskInput {
  target_agent_id: string;
  title: string;
  context?: string;
  priority: string;
}

export interface NudgeInput {
  target_agent_id: string;
  type: string;
  content: string;
  from_agent: string;
}

export interface CreateHandoffInput {
  to_agent?: string;
  summary: string;
  context?: string;
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

export async function fetchHandoffs(): Promise<HandoffRecord[]> {
  const res = await globalThis.fetch('/api/handoffs');
  const data = await parseResponse(res);
  return data?.handoffs ?? [];
}

export async function fetchTemplates(): Promise<TemplateRecord[]> {
  const res = await globalThis.fetch('/api/templates');
  const data = await parseResponse(res);
  return data?.templates ?? [];
}

export async function createHandoff(input: CreateHandoffInput): Promise<void> {
  const res = await globalThis.fetch('/api/handoffs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  await parseResponse(res);
}

export async function acceptHandoff(id: string): Promise<void> {
  const res = await globalThis.fetch(`/api/handoffs/${id}/accept`, { method: 'POST' });
  await parseResponse(res);
}

export async function dispatchTask(input: DispatchTaskInput): Promise<void> {
  const res = await globalThis.fetch('/api/agent/dispatch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  await parseResponse(res);
}

export async function sendNudge(input: NudgeInput): Promise<void> {
  const res = await globalThis.fetch('/api/agent/nudge', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  });
  await parseResponse(res);
}

export async function releaseClaim(agentId: string, filePath: string): Promise<void> {
  const res = await globalThis.fetch(`/api/claims/${encodeURIComponent(agentId)}/${encodeURIComponent(filePath)}`, {
    method: 'DELETE',
  });
  await parseResponse(res);
}
