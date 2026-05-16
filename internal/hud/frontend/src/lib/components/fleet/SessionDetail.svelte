<script>
  /**
   * SessionDetail — drill-down drawer body for a single session. Owns the
   * entire drawer including the lifecycle that loads session entries when
   * the sessionId prop changes, the lineage / child / trace blocks, and the
   * timeline of context entries.
   *
   * @type {{
   *   sessionId: string | null,
   *   onNavigateSession: (id: string) => void,
   *   onNavigateTrace: (agentId: string) => void,
   *   onNavigateSpawn: (e: Event, spawnId: string) => void,
   *   onClose: () => void,
   * }}
   */
  let { sessionId, onNavigateSession, onNavigateTrace, onNavigateSpawn, onClose } = $props();

  import { fleetStore } from '../../stores/fleet.svelte.ts';
  import { spawnStore } from '../../stores/spawn.svelte.ts';
  import { traceStore } from '../../stores/traces.svelte.ts';
  import { formatTime, relativeTime, formatNumber, entryVariant, sanitizeText, inferAgentType } from '../../utils/format.ts';
  import { formatTraceDuration, traceBreakdown, traceStatusVariant } from '../../utils/traces.ts';
  import { buildSpawnByAgentId } from '../../utils/fleetRows.ts';
  import Badge from '../../widgets/Badge.svelte';
  import DetailDrawer from '../shared/DetailDrawer.svelte';
  import EmptyState from '../shared/EmptyState.svelte';

  let sessionEntries = $state([]);
  let sessionEvents = $state([]);
  let sessionTraceEntries = $state([]);
  let sessionTraceErrors = $state([]);
  let sessionTraceMeta = $state(null);
  let loadingEntries = $state(false);

  let drawerError = $derived(fleetStore.drawerError);
  let spawnByAgentId = $derived(buildSpawnByAgentId(spawnStore.spawns));
  let agentLookup = $derived.by(() => {
    const map = new Map();
    for (const a of fleetStore.liveAgents ?? []) map.set(a.agent_id, a);
    return map;
  });

  let detailSession = $derived(
    sessionId ? (fleetStore.sessions ?? []).find(s => s.id === sessionId) : null
  );
  let detailAgent = $derived(detailSession ? agentLookup.get(detailSession.agent_id) : null);
  let detailLineage = $derived(detailSession ? fleetStore.sessionLineage(detailSession.id) : []);
  let detailChildren = $derived(detailSession ? fleetStore.childSessions(detailSession.id) : []);
  let detailParentSession = $derived(detailSession ? fleetStore.parentSession(detailSession.id) : null);
  let detailRootSession = $derived(detailSession ? fleetStore.rootSession(detailSession.id) : null);

  let detailTraceEntries = $derived.by(() => {
    if (sessionTraceEntries.length > 0) return sessionTraceEntries.slice(0, 5);
    const agentId = (detailSession?.agent_id ?? '').trim();
    if (!agentId) return [];
    return (traceStore.entries ?? []).filter((entry) => (entry.agent_id ?? '') === agentId).slice(0, 5);
  });
  let traceError = $derived(traceStore.error);
  let traceLoading = $derived(traceStore.loading);

  async function loadSessionTrace(id, limit = 100) {
    loadingEntries = true;
    try {
      const data = await fleetStore.fetchSessionTrace(id, limit);
      sessionEntries = data?.entries ?? [];
      sessionEvents = data?.events ?? [];
      sessionTraceEntries = data?.traces ?? [];
      sessionTraceErrors = data?.errors ?? [];
      sessionTraceMeta = data;
    } finally {
      loadingEntries = false;
    }
  }

  function retrySessionEntries() {
    if (!sessionId) return;
    void loadSessionTrace(sessionId, 100);
  }

  $effect(() => {
    if (sessionId) {
      void loadSessionTrace(sessionId, 100);
    } else {
      sessionEntries = [];
      sessionEvents = [];
      sessionTraceEntries = [];
      sessionTraceErrors = [];
      sessionTraceMeta = null;
      fleetStore.clearDrawerError();
    }
  });

  function sessionLabel(session) {
    if (!session) return 'unknown';
    return sanitizeText(session.agent || session.agent_id || session.id.slice(0, 8));
  }

  function sessionMetaLabel(session) {
    if (!session) return '---';
    const state = session.active ? 'active' : sanitizeText(session.status || 'ended');
    return `${state} · ${relativeTime(session.started_at)}`;
  }
</script>

<DetailDrawer
  open={!!sessionId}
  title={sanitizeText(detailSession?.agent ?? sessionId?.slice(0, 12) ?? '')}
  subtitle={sanitizeText(detailSession?.namespace ?? '')}
  {onClose}
>
  {#snippet header()}
    {#if detailSession}
      <div class="detail-stats">
        <div class="stat-chip">
          <span class="stat-chip-value">{detailSession.entry_count ?? 0}</span>
          <span class="stat-chip-label">entries</span>
        </div>
        <div class="stat-chip">
          <span class="stat-chip-value">{detailSession.task_count ?? 0}</span>
          <span class="stat-chip-label">tasks</span>
        </div>
        <div class="stat-chip">
          <span class="stat-chip-value">{formatNumber(detailSession.tokens_used ?? 0)}</span>
          <span class="stat-chip-label">tokens</span>
        </div>
        <div class="stat-chip">
          <span class="stat-chip-value">{detailSession.memory_items ?? 0}</span>
          <span class="stat-chip-label">memory</span>
        </div>
        <div class="stat-chip">
          <span class="stat-chip-value">{relativeTime(detailSession.started_at)}</span>
          <span class="stat-chip-label">started</span>
        </div>
        {#if inferAgentType(detailSession.agent_id)}
          <div class="stat-chip">
            <span class="stat-chip-value">{inferAgentType(detailSession.agent_id)}</span>
            <span class="stat-chip-label">type</span>
          </div>
        {/if}
        {#if detailAgent?.current_task}
          <div class="stat-chip">
            <span class="stat-chip-value">{detailAgent.current_task}</span>
            <span class="stat-chip-label">current task</span>
          </div>
        {/if}
        {#if detailAgent?.pr_url}
          <div class="stat-chip">
            <a href={detailAgent.pr_url} target="_blank" rel="noopener" class="stat-chip-value pr-link">PR</a>
            <span class="stat-chip-label">pull request</span>
          </div>
        {/if}
        {#if detailAgent?.agent_id}
          <div class="stat-chip spawn-chip">
            <button class="stat-chip-value spawn-chip-link" onclick={() => onNavigateTrace(detailAgent.agent_id)}>
              {'▦'} Traces
            </button>
            <span class="stat-chip-label">agent trace view</span>
          </div>
        {/if}
        {#if detailSession && spawnByAgentId.has(detailSession.agent_id)}
          {@const detailSpawn = spawnByAgentId.get(detailSession.agent_id)}
          <div class="stat-chip spawn-chip">
            <button class="stat-chip-value spawn-chip-link" onclick={(e) => onNavigateSpawn(e, detailSpawn.spawn_id)}>
              {'⬢'} Spawn
            </button>
            <span class="stat-chip-label">{detailSpawn.status}</span>
          </div>
        {/if}
      </div>
      {#if detailSession.description}
        <div class="detail-description text-sm text-secondary">{sanitizeText(detailSession.description)}</div>
      {/if}
      {#if sessionTraceErrors.length > 0}
        <div class="trace-health-banner">
          <span class="trace-health-label">Partial trace</span>
          <span class="trace-health-copy">
            {sessionTraceErrors.map((err) => `${sanitizeText(err.source)}: ${sanitizeText(err.message)}`).join(' · ')}
          </span>
        </div>
      {/if}
      {#if detailLineage.length > 0}
        <div class="hierarchy-section">
          <div class="section-header">
            <span class="section-title">Session Path</span>
            <span class="text-mono text-xs text-muted">{detailLineage.length} level{detailLineage.length === 1 ? '' : 's'}</span>
          </div>
          <div class="session-breadcrumbs">
            {#each detailLineage as lineageSession, index (lineageSession.id)}
              <button
                class="session-crumb"
                class:session-crumb-current={detailSession && lineageSession.id === detailSession.id}
                onclick={() => onNavigateSession(lineageSession.id)}
                title={lineageSession.namespace || lineageSession.id}
              >
                {sessionLabel(lineageSession)}
              </button>
              {#if index < detailLineage.length - 1}
                <span class="session-crumb-sep">›</span>
              {/if}
            {/each}
          </div>
        </div>
      {/if}
      {#if detailParentSession || detailRootSession}
        <div class="hierarchy-section hierarchy-section-inline">
          {#if detailRootSession}
            <button class="session-link-chip" onclick={() => onNavigateSession(detailRootSession.id)}>
              Root · {sessionLabel(detailRootSession)}
            </button>
          {/if}
          {#if detailParentSession}
            <button class="session-link-chip" onclick={() => onNavigateSession(detailParentSession.id)}>
              Parent · {sessionLabel(detailParentSession)}
            </button>
          {/if}
        </div>
      {/if}
      {#if detailChildren.length > 0}
        <div class="hierarchy-section">
          <div class="section-header">
            <span class="section-title">Child Sessions</span>
            <span class="text-mono text-xs text-muted">{detailChildren.length}</span>
          </div>
          <div class="child-session-list">
            {#each detailChildren as childSession (childSession.id)}
              <button class="child-session-chip" onclick={() => onNavigateSession(childSession.id)}>
                <span class="child-session-name">{sessionLabel(childSession)}</span>
                <span class="child-session-meta">{sessionMetaLabel(childSession)}</span>
              </button>
            {/each}
          </div>
        </div>
      {/if}
      {#if detailSession?.agent_id}
        <div class="hierarchy-section">
          <div class="section-header">
            <span class="section-title">Recent Traces</span>
            <div class="section-header-tools">
              <span class="text-mono text-xs text-muted">{detailTraceEntries.length} shown</span>
              <button class="session-link-chip" onclick={() => onNavigateTrace(detailSession.agent_id)}>
                Open full traces
              </button>
            </div>
          </div>
          {#if traceLoading && detailTraceEntries.length === 0}
            <div class="trace-preview-empty text-sm text-muted">Loading recent traces...</div>
          {:else if traceError}
            <div class="trace-preview-empty trace-preview-error text-sm">{sanitizeText(traceError)}</div>
          {:else if sessionTraceMeta && !sessionTraceMeta.trace_enabled && detailTraceEntries.length === 0}
            <div class="trace-preview-empty text-sm text-muted">Daemon audit trace stream unavailable.</div>
          {:else if !sessionTraceMeta && !traceStore.enabled}
            <div class="trace-preview-empty text-sm text-muted">Trace stream unavailable.</div>
          {:else if detailTraceEntries.length === 0}
            <div class="trace-preview-empty text-sm text-muted">No recent traces for this agent yet.</div>
          {:else}
            <div class="trace-preview-list">
              {#each detailTraceEntries as trace, index (`${trace.timestamp}-${trace.server}-${trace.tool}-${index}`)}
                <div class="trace-preview-row">
                  <div class="trace-preview-top">
                    <div class="trace-preview-id">
                      <span class="text-mono text-xs text-muted">{formatTime(trace.timestamp)}</span>
                      <span class="trace-preview-server">{sanitizeText(trace.server)}</span>
                      <span class="trace-preview-tool">{sanitizeText(trace.tool)}</span>
                    </div>
                    <div class="trace-preview-badges">
                      <span class="trace-preview-duration">{formatTraceDuration(trace.duration_ms)}</span>
                      <Badge text={sanitizeText(trace.status)} variant={traceStatusVariant(trace.status)} />
                    </div>
                  </div>
                  <div class="trace-preview-meta">
                    {#if trace.pipeline_stage}
                      <span class="trace-preview-chip">{sanitizeText(trace.pipeline_stage)}</span>
                    {/if}
                    {#if traceBreakdown(trace)}
                      <span class="trace-preview-chip trace-preview-breakdown">{traceBreakdown(trace)}</span>
                    {/if}
                  </div>
                  {#if trace.error}
                    <div class="trace-preview-error text-sm">{sanitizeText(trace.error)}</div>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
    {/if}
  {/snippet}

  <div class="section-header" style="margin-top: 4px">
    <span class="section-title">Session Events</span>
    <span class="text-mono text-xs text-muted">{sessionEvents.length} events</span>
  </div>

  <div class="session-event-list">
    {#each sessionEvents.slice(0, 12) as event, index (`${event.timestamp}-${event.event_type}-${index}`)}
      <div class="session-event-row">
        <div class="timeline-dot event-dot"></div>
        <div class="session-event-content">
          <div class="timeline-meta">
            <span class="text-mono text-xs text-muted">{formatTime(event.timestamp)}</span>
            <Badge text={sanitizeText(event.event_type ?? 'event')} variant="info" />
          </div>
          {#if event.agent_id}
            <div class="timeline-title">{sanitizeText(event.agent_id)}</div>
          {/if}
          {#if event.data}
            <div class="timeline-body text-sm text-muted">{sanitizeText(JSON.stringify(event.data)).slice(0, 180)}</div>
          {/if}
        </div>
      </div>
    {:else}
      {#if !loadingEntries}
        <EmptyState icon={'○'} heading="No lifecycle events for this session" compact />
      {/if}
    {/each}
  </div>

  <div class="section-header" style="margin-top: 4px">
    <span class="section-title">Context Entries</span>
    <span class="text-mono text-xs text-muted">{sessionEntries.length} entries</span>
  </div>

  {#if loadingEntries}
    <div class="loading-bar"><div class="loading-bar-inner"></div></div>
  {/if}

  <div class="entries-timeline">
    {#if drawerError}
      <div class="drawer-error-card" role="alert">
        <div class="drawer-error-title">Could not load session entries</div>
        <div class="drawer-error-body text-sm text-muted">{sanitizeText(drawerError)}</div>
        <div class="drawer-error-actions">
          <button class="btn btn-xs btn-ghost" onclick={retrySessionEntries}>
            Retry
          </button>
        </div>
      </div>
    {/if}
    {#each sessionEntries as entry (entry.id ?? entry.timestamp)}
      {@const entryContent = sanitizeText(entry.content ?? '')}
      <div class="timeline-entry">
        <div class="timeline-dot" style="background: var(--{entryVariant(entry.entry_type) === 'accent' ? 'accent' : entryVariant(entry.entry_type) === 'error' ? 'error' : entryVariant(entry.entry_type) === 'warning' ? 'warning' : entryVariant(entry.entry_type) === 'success' ? 'success' : 'info'})"></div>
        <div class="timeline-content">
          <div class="timeline-meta">
            <span class="text-mono text-xs text-muted">{formatTime(entry.timestamp)}</span>
            <Badge text={sanitizeText(entry.entry_type ?? 'note')} variant={entryVariant(entry.entry_type)} />
          </div>
          <div class="timeline-title">{sanitizeText(entry.title ?? '---')}</div>
          {#if entry.file_path}
            <div class="timeline-file text-mono text-xs">
              {entry.file_path}{#if entry.line_start}:{entry.line_start}{#if entry.line_end && entry.line_end !== entry.line_start}-{entry.line_end}{/if}{/if}
              {#if entry.token_count}<span class="text-muted"> ({entry.token_count} tok)</span>{/if}
            </div>
          {/if}
          {#if entryContent}
            <div class="timeline-body text-sm text-muted">
              {entryContent.slice(0, 200)}{entryContent.length > 200 ? '...' : ''}
            </div>
          {/if}
        </div>
      </div>
    {:else}
      {#if !loadingEntries && !drawerError}
        <EmptyState icon={'○'} heading="No context entries for this session" compact />
      {/if}
    {/each}
  </div>
</DetailDrawer>

<style>
  .detail-stats {
    display: flex;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .stat-chip {
    display: flex;
    align-items: baseline;
    gap: 4px;
    background: var(--bg-primary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    padding: 6px var(--space-3);
    position: relative;
  }

  .stat-chip::before {
    content: '';
    position: absolute;
    top: 0;
    left: 10%;
    right: 10%;
    height: 1px;
    background: linear-gradient(90deg, transparent, rgba(0, 200, 255, 0.1), transparent);
    pointer-events: none;
  }

  .stat-chip-value {
    font-family: var(--font-mono);
    font-weight: 700;
    font-size: 14px;
    color: var(--fg-primary);
  }

  .stat-chip-label {
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    color: var(--fg-dim);
  }

  .pr-link {
    font-size: var(--text-xs);
    text-decoration: none;
    margin-left: 3px;
    color: var(--accent);
    transition: opacity var(--transition-fast);
  }

  .pr-link:hover {
    opacity: 0.8;
    text-shadow: 0 0 6px var(--glow-accent);
  }

  .spawn-chip-link {
    background: none;
    border: none;
    padding: 0;
    font-family: var(--font-mono);
    font-weight: 700;
    font-size: 14px;
    color: var(--accent);
    cursor: pointer;
    transition: opacity var(--transition-fast);
  }

  .spawn-chip-link:hover {
    opacity: 0.8;
  }

  .detail-description {
    margin-top: var(--space-2);
    line-height: 1.5;
  }

  .hierarchy-section {
    display: grid;
    gap: var(--space-2);
    margin-top: var(--space-3);
  }

  .hierarchy-section-inline {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  .section-header {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--space-2);
  }

  .section-header-tools {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
  }

  .session-breadcrumbs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
  }

  .session-crumb,
  .session-link-chip,
  .child-session-chip {
    border: 1px solid var(--border-subtle);
    background: var(--bg-primary);
    color: var(--fg-secondary);
    border-radius: var(--radius-sm);
    cursor: pointer;
    transition: border-color var(--transition-fast), color var(--transition-fast), background var(--transition-fast);
  }

  .session-crumb {
    padding: 6px 8px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .session-crumb:hover,
  .session-link-chip:hover,
  .child-session-chip:hover,
  .session-crumb-current {
    border-color: color-mix(in srgb, var(--accent) 35%, var(--border-subtle));
    color: var(--fg-primary);
    background: color-mix(in srgb, var(--accent) 8%, var(--bg-primary));
  }

  .session-crumb-current {
    cursor: default;
  }

  .session-crumb-sep {
    color: var(--fg-dim);
    font-size: 12px;
  }

  .session-link-chip {
    padding: 6px 10px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .child-session-list {
    display: grid;
    gap: var(--space-2);
  }

  .child-session-chip {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding: 8px 10px;
    text-align: left;
  }

  .child-session-name {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    color: var(--fg-primary);
  }

  .child-session-meta {
    font-size: 10px;
    color: var(--fg-dim);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }

  .trace-health-banner {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    padding: 8px 10px;
    border-radius: var(--radius-md);
    border: 1px solid color-mix(in srgb, var(--warning) 30%, var(--border));
    background: color-mix(in srgb, var(--warning) 7%, var(--bg-primary));
  }

  .trace-health-label {
    color: var(--warning);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    white-space: nowrap;
  }

  .trace-health-copy {
    color: var(--fg-secondary);
    font-size: var(--text-sm);
    line-height: 1.4;
  }

  .entries-timeline {
    padding: var(--space-2) 0;
  }

  .session-event-list {
    display: grid;
    gap: var(--space-1);
    padding: var(--space-2) 0;
  }

  .session-event-row {
    display: flex;
    gap: var(--space-3);
    padding: 8px 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .session-event-row:last-child {
    border-bottom: none;
  }

  .session-event-content {
    flex: 1;
    min-width: 0;
  }

  .event-dot {
    color: var(--info);
    background: var(--info);
  }

  .trace-preview-list {
    display: grid;
    gap: var(--space-2);
  }

  .trace-preview-row {
    padding: 10px 12px;
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--bg-secondary) 82%, transparent);
  }

  .trace-preview-top,
  .trace-preview-id,
  .trace-preview-badges,
  .trace-preview-meta {
    display: flex;
    gap: 8px;
    flex-wrap: wrap;
    align-items: center;
  }

  .trace-preview-top {
    justify-content: space-between;
  }

  .trace-preview-server {
    color: var(--fg-secondary);
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .trace-preview-tool {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
  }

  .trace-preview-duration,
  .trace-preview-chip {
    font-family: var(--font-mono);
    font-size: var(--text-xs);
  }

  .trace-preview-meta {
    margin-top: 8px;
  }

  .trace-preview-chip {
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
    color: var(--fg-secondary);
  }

  .trace-preview-breakdown {
    white-space: normal;
  }

  .trace-preview-empty {
    padding: 10px 12px;
    border: 1px dashed var(--border-subtle);
    border-radius: var(--radius-md);
    background: color-mix(in srgb, var(--bg-secondary) 72%, transparent);
  }

  .trace-preview-error {
    color: var(--error);
  }

  .timeline-entry {
    display: flex;
    gap: var(--space-3);
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--border-subtle);
  }

  .timeline-entry:last-child {
    border-bottom: none;
  }

  .timeline-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    flex-shrink: 0;
    margin-top: 6px;
    box-shadow: 0 0 6px currentColor;
  }

  .timeline-content {
    flex: 1;
    min-width: 0;
  }

  .timeline-meta {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin-bottom: 2px;
  }

  .timeline-title {
    font-size: var(--text-sm);
    color: var(--fg-primary);
    font-weight: 500;
  }

  .timeline-body {
    margin-top: 2px;
    line-height: 1.4;
    word-break: break-word;
  }

  .timeline-file {
    color: var(--fg-secondary);
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border-radius: var(--radius-sm);
    margin-top: 2px;
    display: inline-block;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    border: 1px solid var(--border-subtle);
  }

  .loading-bar {
    height: 2px;
    background: var(--bg-tertiary);
    border-radius: 1px;
    overflow: hidden;
    margin-bottom: 4px;
  }

  .loading-bar-inner {
    width: 40%;
    height: 100%;
    background: linear-gradient(90deg, var(--info), var(--accent));
    border-radius: 1px;
    animation: loadingSlide 1.2s ease-in-out infinite;
  }

  .drawer-error-card {
    margin-bottom: var(--space-3);
    padding: var(--space-3);
    border-radius: var(--radius-md);
    border: 1px solid color-mix(in srgb, var(--error) 30%, var(--border));
    background: color-mix(in srgb, var(--error) 7%, var(--bg-primary));
  }

  .drawer-error-title {
    font-size: var(--text-sm);
    font-weight: 600;
    color: var(--fg-primary);
    margin-bottom: 4px;
  }

  .drawer-error-body {
    line-height: 1.5;
  }

  .drawer-error-actions {
    display: flex;
    gap: var(--space-2);
    margin-top: var(--space-3);
  }

  @keyframes loadingSlide {
    0% { transform: translateX(-100%); }
    100% { transform: translateX(350%); }
  }
</style>
