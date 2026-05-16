// Pure helpers for the Graph panel. Extracted from GraphPanel.svelte
// during the Slice B2.5 panel decomp.

export function typeVariant(type: string | null | undefined): string {
  const map: Record<string, string> = {
    service: 'info', file: 'accent', function: 'success', variable: 'warning',
    class: 'accent', module: 'info', config: 'warning', person: 'success',
  };
  return map[(type ?? '').toLowerCase()] ?? 'info';
}

export function typeBarColor(type: string | null | undefined): string {
  const map: Record<string, string> = {
    service: 'var(--info)', file: 'var(--accent)', function: 'var(--success)',
    variable: 'var(--warning)', class: 'var(--accent)', module: 'var(--info)',
  };
  return map[(type ?? '').toLowerCase()] ?? 'var(--fg-muted)';
}
