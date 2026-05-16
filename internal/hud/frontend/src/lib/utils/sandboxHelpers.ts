// Pure helpers for the Sandbox (Labs) panel. Extracted from
// SandboxPanel.svelte during the Slice B2.4 panel decomp.

export function formatUptime(seconds: number | null | undefined): string {
  if (!seconds || seconds <= 0) return '---';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function eventIcon(type: string): string {
  switch (type) {
    case 'exec':  return '▶';
    case 'build': return '⚒';
    case 'start': return '◉';
    case 'stop':  return '○';
    default:      return '◈';
  }
}

export function formatExecDuration(ms: number | null | undefined): string {
  if (!ms || ms <= 0) return 'pending';
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  if (minutes > 0) return `${minutes}m ${seconds % 60}s`;
  return `${seconds}s`;
}

export function execStatusTone(status: string): 'info' | 'success' | 'error' | 'muted' {
  switch (status) {
    case 'running': return 'info';
    case 'completed': return 'success';
    case 'failed': return 'error';
    default: return 'muted';
  }
}
