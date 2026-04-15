interface TraceBreakdownLike {
  route_ms?: number;
  build_ms?: number;
  execute_ms?: number;
  send_ms?: number;
  recv_ms?: number;
}

export function formatTraceDuration(ms?: number | null): string {
  if (!ms) return '0ms';
  if (ms < 1) return '<1ms';
  return `${Math.round(ms)}ms`;
}

export function traceBreakdown(entry: TraceBreakdownLike): string {
  const parts = [];
  if (entry.route_ms) parts.push(`route ${formatTraceDuration(entry.route_ms)}`);
  if (entry.build_ms) parts.push(`build ${formatTraceDuration(entry.build_ms)}`);
  if (entry.execute_ms) parts.push(`exec ${formatTraceDuration(entry.execute_ms)}`);
  if (entry.send_ms) parts.push(`send ${formatTraceDuration(entry.send_ms)}`);
  if (entry.recv_ms) parts.push(`recv ${formatTraceDuration(entry.recv_ms)}`);
  return parts.join(' · ');
}

export function traceStatusVariant(status?: string | null): 'error' | 'warning' | 'success' {
  if (status === 'error') return 'error';
  if (status === 'denied') return 'warning';
  return 'success';
}
