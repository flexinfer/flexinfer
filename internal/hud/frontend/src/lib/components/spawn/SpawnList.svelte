<script lang="ts">
  /**
   * SpawnList — the recent-spawns grid (header + filter bar + cards +
   * stop confirm dialog). Reads spawnStore directly; owns the local
   * "stop confirm" modal id.
   */
  import { spawnStore } from '../../stores/spawn.svelte';
  import type { SpawnState } from '../../stores/spawn.svelte';
  import { labsAuthStore } from '../../stores/labsAuth.svelte.ts';
  import { router } from '../../stores/router.svelte';
  import StatusDot from '../../widgets/StatusDot.svelte';
  import BudgetBar from '../../widgets/BudgetBar.svelte';
  import EmptyState from '../shared/EmptyState.svelte';
  import ConfirmDialog from '../shared/ConfirmDialog.svelte';
  import SpawnFilters from './SpawnFilters.svelte';
  import {
    filterSpawns,
    taskSummary,
    classifySpawnError,
    statusColor,
    formatDuration,
    hasBudget,
    formatCostShort,
    formatTurns,
    rowTelemetry,
    spawnStatusDot,
  } from '../../utils/spawnHelpers';

  let spawns = $derived(spawnStore.spawns);
  let hasAdminToken = $derived(labsAuthStore.hasToken);
  let filteredSpawns = $derived(filterSpawns(spawns, spawnStore.statusFilter, spawnStore.searchQuery));
  let stopConfirmId = $state<string | null>(null);

  function handleStop(spawnId: string) {
    void spawnStore.stop(spawnId);
  }
</script>

<section class="spawn-results">
  <div class="results-header">
    <div>
      <div class="results-eyebrow">Recent activity</div>
      <h2 class="results-title">Recent spawns</h2>
      <p class="results-description">Runs stay here until they finish, fail, or are stopped.</p>
    </div>
    <div class="results-count">{filteredSpawns.length === spawns.length ? `${spawns.length} tracked` : `${filteredSpawns.length} / ${spawns.length}`}</div>
  </div>

  {#if spawns.length > 0}
    <SpawnFilters />
  {/if}

  {#if spawns.length === 0}
    <div class="spawn-empty">
      <EmptyState
        icon={'🤖'}
        heading="No agent spawns yet"
        description="The first run will show live status, timing, budget usage, and a stop control here. Use the composer above to launch a scoped task."
      />
    </div>
  {:else if filteredSpawns.length === 0}
    <div class="spawn-empty">
      <EmptyState
        icon={'🔍'}
        heading="No matching spawns"
        description="Try adjusting your filters or search query."
      />
    </div>
  {:else}
    <div class="spawns-list">
      {#each filteredSpawns as spawn (spawn.spawn_id)}
        <div
          class="spawn-row"
          class:active={spawn.status === 'running' || spawn.status === 'creating' || spawn.status === 'building'}
          role="button"
          tabindex="0"
          onclick={() => router.navigateDetail(spawn.spawn_id)}
          onkeydown={(e) => {
            if (e.key === 'Enter' || e.key === ' ') {
              e.preventDefault();
              router.navigateDetail(spawn.spawn_id);
            }
          }}
        >
          <div class="spawn-header">
            <div class="spawn-head-main">
              <StatusDot status={spawnStatusDot(spawn.status)} />
              <span class="spawn-project">{spawn.request.project}</span>
              <span class="spawn-status" style="color: {statusColor(spawn.status)}">{spawn.status}</span>
            </div>

            <div class="spawn-head-actions">
              <span class="spawn-duration">{formatDuration(spawn.started_at, spawn.ended_at)}</span>
              {#if spawn.status === 'running' || spawn.status === 'creating' || spawn.status === 'building'}
                <button
                  class="stop-button"
                  disabled={!hasAdminToken}
                  onclick={(e) => { e.stopPropagation(); stopConfirmId = spawn.spawn_id; }}
                >Stop</button>
              {/if}
            </div>
          </div>

          {#if hasBudget(spawn)}
            {@const rt = rowTelemetry(spawn, spawnStore.telemetryBySpawnId)}
            <div class="spawn-budgets">
              {#if spawn.request.max_cost_usd}
                <BudgetBar
                  label="Cost"
                  current={rt?.total_cost_usd ?? 0}
                  max={spawn.request.max_cost_usd}
                  formatValue={formatCostShort}
                  costEstimated={rt?.cost_estimated ?? false}
                />
              {/if}
              {#if spawn.request.max_turns}
                <BudgetBar
                  label="Turns"
                  current={rt?.turn_count ?? 0}
                  max={spawn.request.max_turns}
                  formatValue={formatTurns}
                />
              {/if}
            </div>
          {/if}

          {#if spawn.request.task_description}
            {@const task = taskSummary(spawn.request.task_description)}
            <div class="spawn-task" class:has-more={task.hasMore} title={task.hasMore ? spawn.request.task_description : undefined}>
              {task.firstLine}
            </div>
          {:else}
            <div class="spawn-task spawn-task-empty">(no task)</div>
          {/if}
          {#if !hasBudget(spawn)}
            {@const rt = rowTelemetry(spawn, spawnStore.telemetryBySpawnId)}
            {#if rt && (rt.total_cost_usd > 0 || rt.turn_count > 0)}
              <div class="spawn-inline-stats">
                {#if rt.total_cost_usd > 0}
                  <span class="inline-stat">{rt.cost_estimated ? '~' : ''}{formatCostShort(rt.total_cost_usd)}</span>
                {/if}
                {#if rt.turn_count > 0}
                  <span class="inline-stat">{formatTurns(rt.turn_count)} turns</span>
                {/if}
                {#if rt.token_usage?.output_tokens > 0}
                  <span class="inline-stat">{Math.round(rt.token_usage.output_tokens / 1000)}k out</span>
                {/if}
              </div>
            {/if}
          {/if}
          <div class="spawn-meta">
            <span class="spawn-agent-type">{spawn.request.agent_type}</span>
            <span class="spawn-agent-id">{spawn.agent_id}</span>
          </div>
          {#if spawn.error}
            {@const errInfo = classifySpawnError(spawn.error)}
            <details class="spawn-error-block">
              <summary>
                <span class="spawn-error-class kind-{errInfo.kind}">{errInfo.kind}</span>
                <span class="spawn-error-headline">{errInfo.headline}</span>
              </summary>
              <pre class="spawn-error-raw">{spawn.error}</pre>
            </details>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</section>

<ConfirmDialog
  open={stopConfirmId !== null}
  title="Stop spawn?"
  message="This will terminate the running agent. The spawn cannot be resumed after stopping."
  confirmLabel="Stop"
  variant="danger"
  onConfirm={() => { const id = stopConfirmId; stopConfirmId = null; if (id) handleStop(id); }}
  onCancel={() => (stopConfirmId = null)}
/>

<style>
  .spawn-results {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .results-header {
    display: flex;
    justify-content: space-between;
    align-items: end;
    gap: var(--space-3);
  }

  .results-eyebrow {
    font-size: var(--text-xs);
    font-weight: 700;
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    color: var(--fg-muted);
    margin-bottom: 4px;
  }

  .results-title {
    font-size: clamp(20px, 1.8vw, 26px);
    line-height: 1.1;
    margin: 0;
    color: var(--fg-primary);
  }

  .results-description {
    margin-top: 6px;
    font-size: var(--text-sm);
    color: var(--fg-secondary);
  }

  .results-count {
    padding: 6px 10px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border);
    background: rgba(255, 255, 255, 0.02);
    font-size: var(--text-xs);
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    white-space: nowrap;
  }

  .spawn-empty {
    border: 1px dashed color-mix(in srgb, var(--border-focus) 42%, var(--border));
    border-radius: var(--radius-xl);
    background:
      radial-gradient(circle at top, rgba(0, 200, 255, 0.05), transparent 48%),
      color-mix(in srgb, var(--bg-secondary) 84%, transparent);
    padding: var(--space-3);
  }

  .spawns-list {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
    gap: var(--space-3);
  }

  .spawn-row {
    padding: var(--space-4);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.018), transparent 40%),
      var(--bg-secondary);
    border-radius: var(--radius-lg);
    border: 1px solid var(--border);
    position: relative;
    transition: border-color var(--transition-fast), transform var(--transition-fast), box-shadow var(--transition-fast);
    cursor: pointer;
    min-height: 220px;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .spawn-row::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .spawn-row:hover {
    border-color: var(--border-active);
    transform: translateY(-2px);
    box-shadow: 0 16px 36px rgba(0, 0, 0, 0.18);
  }

  .spawn-row:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }

  .spawn-row.active {
    border-color: rgba(34, 224, 118, 0.28);
    box-shadow: 0 0 16px var(--glow-success);
  }

  .spawn-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
  }

  .spawn-head-main,
  .spawn-head-actions {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .spawn-head-main {
    min-width: 0;
  }

  .spawn-budgets {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .spawn-project {
    font-weight: 600;
    font-size: var(--text-base);
    color: var(--fg-primary);
    font-family: var(--font-mono);
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .spawn-status {
    font-size: var(--text-xs);
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    padding: 4px 8px;
    border-radius: var(--radius-full);
    background: rgba(255, 255, 255, 0.03);
  }

  .spawn-duration {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    font-variant-numeric: tabular-nums;
    font-family: var(--font-mono);
    margin-left: auto;
  }

  .stop-button {
    padding: 4px 9px;
    font-size: var(--text-xs);
    background: transparent;
    border: 1px solid rgba(255, 61, 113, 0.2);
    color: var(--error);
    border-radius: var(--radius-xs);
    cursor: pointer;
    transition: background var(--transition-fast), border-color var(--transition-fast);
  }

  .stop-button:hover {
    background: var(--error-dim);
    border-color: var(--error);
  }

  .spawn-task {
    font-size: var(--text-sm);
    color: var(--fg-secondary);
    line-height: 1.65;
    display: -webkit-box;
    -webkit-line-clamp: 4;
    -webkit-box-orient: vertical;
    overflow: hidden;
    letter-spacing: var(--tracking-normal);
  }

  .spawn-inline-stats {
    display: flex;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .inline-stat {
    display: inline-flex;
    align-items: center;
    padding: 2px 7px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border-subtle);
    background: rgba(255, 255, 255, 0.02);
    font-size: var(--text-2xs);
    font-family: var(--font-mono);
    color: var(--fg-muted);
    font-variant-numeric: tabular-nums;
  }

  .spawn-meta {
    display: flex;
    gap: var(--space-2);
    font-size: var(--text-xs);
    color: var(--fg-dim);
    margin-top: auto;
    padding-top: var(--space-2);
    border-top: 1px solid var(--border-subtle);
    flex-wrap: wrap;
  }

  .spawn-error-block {
    margin-top: var(--space-2);
    border: 1px solid color-mix(in srgb, var(--error) 30%, var(--border-subtle));
    border-radius: var(--radius-sm);
    background: color-mix(in srgb, var(--error) 6%, transparent);
    overflow: hidden;
  }
  .spawn-error-block > summary {
    list-style: none;
    cursor: pointer;
    padding: 4px var(--space-2);
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
    font-size: var(--text-xs);
    color: var(--fg-secondary);
  }
  .spawn-error-block > summary::-webkit-details-marker { display: none; }
  .spawn-error-block[open] > summary {
    border-bottom: 1px solid color-mix(in srgb, var(--error) 24%, var(--border-subtle));
  }
  .spawn-error-class {
    padding: 1px 6px;
    border-radius: var(--radius-sm);
    border: 1px solid color-mix(in srgb, var(--error) 38%, var(--border));
    background: color-mix(in srgb, var(--error) 14%, transparent);
    color: var(--error);
    font-family: var(--font-mono);
    font-size: var(--text-2xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    flex-shrink: 0;
  }
  .spawn-error-class.kind-quota { color: var(--warning); border-color: color-mix(in srgb, var(--warning) 38%, var(--border)); background: color-mix(in srgb, var(--warning) 14%, transparent); }
  .spawn-error-class.kind-throttle { color: var(--warning); border-color: color-mix(in srgb, var(--warning) 38%, var(--border)); background: color-mix(in srgb, var(--warning) 14%, transparent); }
  .spawn-error-class.kind-budget { color: var(--warning); border-color: color-mix(in srgb, var(--warning) 38%, var(--border)); background: color-mix(in srgb, var(--warning) 14%, transparent); }
  .spawn-error-class.kind-timeout { color: var(--warning); border-color: color-mix(in srgb, var(--warning) 38%, var(--border)); background: color-mix(in srgb, var(--warning) 14%, transparent); }
  .spawn-error-class.kind-missing { color: var(--fg-dim); border-color: var(--border); background: transparent; }
  .spawn-error-headline {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
    color: var(--fg-primary);
    font-family: var(--font-mono);
  }
  .spawn-error-raw {
    margin: 0;
    padding: var(--space-2);
    max-height: 240px;
    overflow: auto;
    font-family: var(--font-mono);
    font-size: var(--text-2xs);
    color: var(--fg-secondary);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .spawn-task.has-more::after {
    content: ' …';
    color: var(--fg-muted);
  }

  .spawn-agent-type {
    padding: 2px 6px;
    border-radius: var(--radius-full);
    border: 1px solid var(--border-subtle);
    background: rgba(255, 255, 255, 0.03);
    font-family: var(--font-mono);
    font-size: var(--text-2xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .spawn-agent-id {
    font-family: var(--font-mono);
  }

  @media (max-width: 720px) {
    .spawns-list {
      grid-template-columns: 1fr;
    }

    .results-header,
    .spawn-header {
      flex-direction: column;
      align-items: start;
    }

    .spawn-head-actions {
      width: 100%;
      justify-content: space-between;
    }
  }
</style>
