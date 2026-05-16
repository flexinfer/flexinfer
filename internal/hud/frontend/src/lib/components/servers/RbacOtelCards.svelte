<script lang="ts">
  /**
   * RbacOtelCards — RBAC + Observability card pair (the "Enterprise"
   * row). Wraps the rbacStore + otelStore consumption so the parent
   * panel only sees one card-pair component.
   */
  import { rbacStore } from '../../stores/rbac.svelte.ts';
  import { otelStore } from '../../stores/otel.svelte.ts';
  import Badge from '../../widgets/Badge.svelte';
  import { formatRelativeTime } from '../../utils/serversHelpers';

  let rbacView = $state<'policy' | 'denied'>('policy');

  let otelInfo = $derived({
    otlp_endpoint: otelStore.data?.otlp_endpoint ?? '',
    otlp_configured: otelStore.data?.otlp_configured ?? false,
    log_format: otelStore.data?.log_format ?? 'text',
    json_logs_enabled: otelStore.data?.json_logs_enabled ?? false,
    traced_servers: otelStore.data?.traced_servers ?? 0,
    total_servers: otelStore.data?.total_servers ?? 0,
    trace_coverage: otelStore.data?.trace_coverage ?? '0%',
  });
</script>

<div class="infra-cards">
  <div class="infra-card">
    <div class="infra-card-header">
      <span class="infra-card-title">RBAC</span>
      <Badge text={rbacStore.enabled ? 'active' : 'off'} variant={rbacStore.enabled ? 'success' : 'info'} />
    </div>
    <div class="infra-card-body">
      {#if rbacStore.enabled}
        <div class="rbac-tabs">
          <button class="rbac-tab-btn" class:active={rbacView === 'policy'} onclick={() => { rbacView = 'policy'; }}>Policy</button>
          <button class="rbac-tab-btn" class:active={rbacView === 'denied'} onclick={() => { rbacView = 'denied'; }}>Denied ({rbacStore.deniedCount})</button>
        </div>
        {#if rbacView === 'policy'}
          {#if rbacStore.roles.length > 0}
            <div class="rbac-section">
              <span class="text-xs uppercase text-muted">Roles</span>
              <div class="rbac-list">
                {#each rbacStore.roles as role}
                  <div class="rbac-row">
                    <span class="text-mono rbac-name">{role.name}</span>
                    <span class="text-muted text-xs">{role.allow?.length ?? 0} allow, {role.deny?.length ?? 0} deny</span>
                  </div>
                {/each}
              </div>
            </div>
          {/if}
          {#if rbacStore.bindings.length > 0}
            <div class="rbac-section">
              <span class="text-xs uppercase text-muted">Bindings</span>
              <div class="rbac-list">
                {#each rbacStore.bindings as binding}
                  <div class="rbac-row">
                    <span class="text-mono rbac-name">{binding.agent_id || binding.agent_type || '*'}</span>
                    <span class="text-muted text-xs">→ {binding.role}</span>
                  </div>
                {/each}
              </div>
            </div>
          {/if}
          {#if rbacStore.roles.length === 0 && rbacStore.bindings.length === 0}
            <span class="text-muted text-xs">No roles or bindings configured</span>
          {/if}
        {:else}
          {#if rbacStore.recentDenied.length > 0}
            <div class="rbac-section">
              <span class="text-xs uppercase text-muted">Recent Denied Calls</span>
              <div class="rbac-list">
                {#each rbacStore.recentDenied.slice(0, 8) as denied}
                  <div class="rbac-row rbac-denied">
                    <div class="rbac-main">
                      <span class="text-mono rbac-name">{denied.agent_id}</span>
                      <span class="text-muted text-xs">{denied.server}/{denied.tool}</span>
                    </div>
                    <div class="rbac-meta text-muted text-xs">
                      <span>{formatRelativeTime(denied.timestamp)}</span>
                      {#if denied.role}
                        <span class="text-mono">role:{denied.role}</span>
                      {/if}
                    </div>
                    <span class="text-xs denied-reason">{denied.reason}</span>
                  </div>
                {/each}
              </div>
            </div>
          {:else}
            <span class="text-muted text-xs">No denied calls captured</span>
          {/if}
        {/if}
        <div class="rbac-section">
          <span class="text-xs uppercase text-muted">Audit</span>
          <div class="rbac-list">
            <div class="rbac-row">
              <span class="text-mono rbac-name">status</span>
              <span class="text-muted text-xs">{rbacStore.auditEnabled ? 'active' : 'off'}</span>
            </div>
          </div>
        </div>
      {:else}
        <span class="text-muted text-xs">RBAC is disabled</span>
      {/if}
    </div>
  </div>

  <div class="infra-card">
    <div class="infra-card-header">
      <span class="infra-card-title">Observability</span>
      <Badge text={otelInfo.otlp_configured ? 'active' : 'off'} variant={otelInfo.otlp_configured ? 'success' : 'info'} />
    </div>
    <div class="infra-card-body">
      <div class="cache-grid">
        <div class="cache-stat">
          <span class="cache-stat-value text-mono">{otelInfo.traced_servers}/{otelInfo.total_servers}</span>
          <span class="cache-stat-label">Traced</span>
        </div>
        <div class="cache-stat">
          <span class="cache-stat-value text-mono" style:color={otelInfo.otlp_configured ? 'var(--success)' : 'var(--fg-muted)'}>{otelInfo.trace_coverage}</span>
          <span class="cache-stat-label">Coverage</span>
        </div>
        <div class="cache-stat">
          <span class="cache-stat-value text-mono">{otelInfo.log_format}</span>
          <span class="cache-stat-label">Log Format</span>
        </div>
      </div>
      {#if otelInfo.otlp_endpoint}
        <div class="otel-endpoint text-mono text-xs text-muted">{otelInfo.otlp_endpoint}</div>
      {/if}
    </div>
  </div>
</div>

<style>
  .infra-cards {
    display: grid;
    grid-template-columns: 1fr;
    gap: var(--space-3);
  }

  .infra-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3) var(--space-4);
    position: relative;
  }

  .infra-card::before {
    content: '';
    position: absolute;
    inset: 0;
    border-radius: inherit;
    background: var(--surface-highlight);
    pointer-events: none;
  }

  .infra-card-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: var(--space-2);
  }

  .infra-card-title {
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-muted);
  }

  .infra-card-body {
    font-size: var(--text-sm);
  }

  .cache-grid {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--space-3);
  }

  .cache-stat {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
  }

  .cache-stat-value {
    font-size: 20px;
    font-weight: 700;
    color: var(--fg-primary);
    line-height: 1.1;
  }

  .cache-stat-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
  }

  .rbac-tabs {
    display: flex;
    gap: 4px;
    margin-bottom: var(--space-2);
  }

  .rbac-tab-btn {
    border: 1px solid var(--border);
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-weight: 500;
    padding: 3px var(--space-2);
    border-radius: var(--radius-sm);
    cursor: pointer;
    transition: all var(--transition-fast);
    letter-spacing: var(--tracking-normal);
  }

  .rbac-tab-btn:hover {
    background: var(--bg-elevated);
    color: var(--fg-primary);
  }

  .rbac-tab-btn.active {
    border-color: var(--accent);
    color: var(--fg-primary);
    background: var(--accent-dim);
    box-shadow: 0 0 8px var(--glow-accent);
  }

  .rbac-section {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-bottom: var(--space-2);
  }

  .rbac-section:last-child {
    margin-bottom: 0;
  }

  .rbac-list {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .rbac-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-xs);
    padding: 2px 0;
  }

  .rbac-name {
    color: var(--fg-primary);
    min-width: 80px;
  }

  .rbac-main {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  .rbac-meta {
    display: flex;
    gap: var(--space-2);
  }

  .rbac-denied {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    color: var(--warning);
    padding: 4px 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .denied-reason {
    color: var(--fg-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 320px;
  }

  .otel-endpoint {
    margin-top: 6px;
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    border: 1px solid var(--border-subtle);
  }
</style>
