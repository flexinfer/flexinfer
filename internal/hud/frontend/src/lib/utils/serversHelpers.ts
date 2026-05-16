// Pure helpers for the Servers panel. Extracted from ServersPanel.svelte
// during the Slice B2.2 panel decomp so display formatting + filtering +
// sorting live in one testable module.

import type { MergedServer } from '../stores/health.svelte.ts';
import { sanitizeText } from './format.ts';

export const STATUS_SORT_ORDER: Record<string, number> = {
  healthy: 0,
  idle: 1,
  degraded: 2,
  down: 3,
};

export function filterServers(
  servers: MergedServer[],
  searchQuery: string,
  categoryFilter: string,
  statusFilter: string,
): MergedServer[] {
  let result = servers;
  if (searchQuery.trim()) {
    const q = sanitizeText(searchQuery).toLowerCase();
    result = result.filter((s) =>
      sanitizeText(s.name ?? '').toLowerCase().includes(q) ||
      sanitizeText(s.description ?? '').toLowerCase().includes(q)
    );
  }
  if (categoryFilter) {
    result = result.filter((s) => (s.categories ?? []).includes(categoryFilter));
  }
  if (statusFilter) {
    result = result.filter((s) => s.status === statusFilter);
  }
  return result;
}

export function sortServers(
  servers: MergedServer[],
  sortKey: string,
  sortDir: 'asc' | 'desc',
): MergedServer[] {
  const rows = [...servers];
  rows.sort((a, b) => {
    let av: string | number;
    let bv: string | number;
    if (sortKey === 'status') {
      av = STATUS_SORT_ORDER[a.status as string] ?? 9;
      bv = STATUS_SORT_ORDER[b.status as string] ?? 9;
    } else {
      const rawA = (a as any)[sortKey];
      const rawB = (b as any)[sortKey];
      av = typeof rawA === 'string' ? sanitizeText(rawA) : (rawA ?? '');
      bv = typeof rawB === 'string' ? sanitizeText(rawB) : (rawB ?? '');
    }
    let cmp: number;
    if (typeof av === 'number' && typeof bv === 'number') {
      cmp = av - bv;
    } else {
      cmp = String(av).toLowerCase().localeCompare(String(bv).toLowerCase());
    }
    return sortDir === 'desc' ? -cmp : cmp;
  });
  return rows;
}

// U3: Treat 0/missing as "no recent call" and render em-dash. Previously
// idle servers (no data) showed a misleading "<1ms".
export function formatLatency(ms: number | null | undefined): string {
  if (ms == null || ms <= 0) return '—';
  if (ms < 1) return '<1ms';
  return ms.toFixed(0) + 'ms';
}

export function tunnelStateVariant(state: string): 'success' | 'warning' | 'error' {
  if (state === 'connected') return 'success';
  if (state === 'connecting' || state === 'reconnecting') return 'warning';
  return 'error';
}

export function formatRelativeTime(isoTs: string | null | undefined): string {
  const ts = Date.parse(isoTs ?? '');
  if (!Number.isFinite(ts)) return '';
  const diffSec = Math.max(0, Math.floor((Date.now() - ts) / 1000));
  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  return `${Math.floor(diffSec / 86400)}d ago`;
}

export function percentile(data: number[] | null | undefined, p: number): number {
  if (!data?.length) return 0;
  const sorted = [...data].filter((v) => v > 0).sort((a, b) => a - b);
  if (!sorted.length) return 0;
  const idx = Math.ceil((p / 100) * sorted.length) - 1;
  return sorted[Math.max(0, idx)];
}

export function categoryOptionsFrom(servers: MergedServer[]): Array<{ value: string; label: string }> {
  const cats = new Set<string>();
  servers.forEach((s) => {
    (s.categories ?? []).forEach((c) => cats.add(c));
  });
  return Array.from(cats).sort().map((c) => ({ value: c, label: c }));
}
