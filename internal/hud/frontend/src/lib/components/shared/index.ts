/**
 * Shared component barrel export.
 *
 * Usage:
 *   import { PanelShell, DataTable, FilterBar } from './shared';
 */
export { default as PanelShell } from './PanelShell.svelte';
export { default as DataTable } from './DataTable.svelte';
export { default as FilterBar } from './FilterBar.svelte';
export { default as DetailDrawer } from './DetailDrawer.svelte';
export { default as EmptyState } from './EmptyState.svelte';
export { default as MetricCard } from './MetricCard.svelte';
export { default as ConfirmDialog } from './ConfirmDialog.svelte';
export { default as ErrorCard } from './action/ErrorCard.svelte';
export { default as AuditDrawer } from './action/AuditDrawer.svelte';
export { useAction } from '../../utils/useAction.svelte.ts';
export type { ActionConfig, ActionHandle } from '../../utils/useAction.svelte.ts';
