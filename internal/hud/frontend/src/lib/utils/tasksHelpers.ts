// Pure helpers for the Tasks panel. Extracted from TasksPanel.svelte
// during the Slice B2.3 panel decomp.

import type { Task } from '../stores/tasks.svelte.ts';

export const PRIORITY_ORDER: Record<string, number> = {
  critical: 0,
  high: 1,
  medium: 2,
  low: 3,
};

export const STATUS_ORDER: Record<string, number> = {
  in_progress: 0,
  blocked: 1,
  pending: 2,
  completed: 3,
  cancelled: 4,
};

export const PRIORITY_CYCLE = ['low', 'medium', 'high', 'critical'] as const;
export const STATUS_OPTIONS = ['pending', 'in_progress', 'blocked', 'completed', 'cancelled'] as const;

const GITLAB_BASE = 'https://gitlab.flexinfer.ai';
const SERVICE_PROJECTS = new Set([
  'loom-core', 'loom', 'loom-zed', 'flexdeck', 'flexinfer', 'flexinfer-site',
  'homelab-homepage', 'jobsearch-app', 'storyboard-generator',
  'streamslate', 'streamslate-site', 'substack', 'tech-radar',
  'mcp-gateway', 'mcp-orchestra', 'mcp-sandbox', 'mentatlab',
  'news-analyzer', 'diff-surgeon', 'comfyui-images-proxy',
]);

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function gitlabIssueUrl(project: string, num: string): string {
  const group = SERVICE_PROJECTS.has(project) ? 'services' : 'libs';
  return `${GITLAB_BASE}/${group}/${project}/-/issues/${num}`;
}

export function parseIssueRefs(title: string | null | undefined): string {
  if (!title) return '---';
  const escaped = escapeHtml(title);
  return escaped.replace(
    /\[([a-zA-Z0-9_-]+)#(\d+)\]/g,
    (_, proj, num) =>
      `<a href="${gitlabIssueUrl(proj, num)}" class="issue-link" target="_blank" rel="noopener" onclick="event.stopPropagation()">${proj}#${num}</a>`
  );
}

export function filterTasks(
  tasks: Task[],
  searchQuery: string,
  priorityFilter: string,
  agentFilter: string,
  statusFilter: string,
): Task[] {
  let result = tasks;
  if (searchQuery.trim()) {
    const q = searchQuery.toLowerCase().trim();
    result = result.filter((t) =>
      (t.title ?? '').toLowerCase().includes(q) ||
      (t.description ?? '').toLowerCase().includes(q)
    );
  }
  if (priorityFilter) {
    result = result.filter((t) => t.priority === priorityFilter);
  }
  if (agentFilter) {
    result = result.filter((t) => ((t as any).agent_id ?? (t as any).agent) === agentFilter);
  }
  if (statusFilter) {
    result = result.filter((t) => t.status === statusFilter);
  }
  return result;
}

export function sortTasks(
  tasks: Task[],
  sortKey: string,
  sortDir: 'asc' | 'desc',
): Task[] {
  const rows = [...tasks];
  rows.sort((a, b) => {
    let cmp = 0;
    switch (sortKey) {
      case 'title':
        cmp = (a.title ?? '').localeCompare(b.title ?? '');
        break;
      case 'priority':
        cmp = (PRIORITY_ORDER[a.priority] ?? 9) - (PRIORITY_ORDER[b.priority] ?? 9);
        break;
      case 'status':
        cmp = (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9);
        break;
      case 'created_at':
        cmp = new Date(a.created_at).getTime() - new Date(b.created_at).getTime();
        break;
      default:
        break;
    }
    return sortDir === 'desc' ? -cmp : cmp;
  });
  return rows;
}

export function groupTasksByStatus(tasks: Task[]): Array<[string, Task[]]> {
  const groups: Record<string, Task[]> = {};
  const order = ['in_progress', 'blocked', 'pending', 'completed', 'cancelled'];
  order.forEach((s) => { groups[s] = []; });
  tasks.forEach((t) => {
    const key = t.status ?? 'pending';
    if (!groups[key]) groups[key] = [];
    groups[key].push(t);
  });
  return Object.entries(groups).filter(([, items]) => items.length > 0);
}

export function taskAgentName(task: Task): string {
  return (task as any).agent_id ?? (task as any).agent ?? '---';
}

export function agentStatusIcon(status: string): string {
  if (status === 'active') return '🟢';
  if (status === 'idle') return '🟡';
  return '⚪';
}

export function agentOptionsFrom(tasks: Task[]): Array<{ value: string; label: string }> {
  const set = new Set<string>();
  tasks.forEach((t) => {
    const agentName = (t as any).agent_id ?? (t as any).agent;
    if (agentName) set.add(agentName);
  });
  return Array.from(set).sort().map((a) => ({ value: a, label: a }));
}
