<script>
  import StatusDot from './StatusDot.svelte';
  import SparkLine from './SparkLine.svelte';

  /** @type {{ agent: import('../stores/presence.svelte.ts').AgentPresence, heartbeatData?: number[], sharedFileAgents?: string[], ondispatch?: (agentId: string) => void, onnudge?: (agentId: string) => void }} */
  let { agent, heartbeatData = [], sharedFileAgents = [], ondispatch, onnudge } = $props();

  const AGENT_COLORS = {
    claude: '#E95D74',
    codex: '#22B255',
    gemini: '#018799',
    copilot: '#E7B312',
  };

  function agentColor(agentType) {
    if (!agentType) return '#5EBDC9';
    const lower = agentType.toLowerCase();
    for (const [key, color] of Object.entries(AGENT_COLORS)) {
      if (lower.includes(key)) return color;
    }
    return '#5EBDC9';
  }

  function presenceStatus(status) {
    const map = { active: 'healthy', idle: 'degraded', offline: 'down' };
    return map[status] ?? 'down';
  }

  function agentIcon(agentType) {
    if (!agentType) return '\u25C9';
    const lower = agentType.toLowerCase();
    if (lower.includes('claude')) return '\u25CF';
    if (lower.includes('codex')) return '\u25A0';
    if (lower.includes('gemini')) return '\u2B22';
    return '\u25C6';
  }

  // Relative time.
  let _tick = $state(0);
  $effect(() => {
    const t = setInterval(() => { _tick++ }, 5000);
    return () => clearInterval(t);
  });

  function relativeTime(ts) {
    void _tick;
    if (!ts) return '---';
    const diff = Math.floor((Date.now() - new Date(ts).getTime()) / 1000);
    if (diff < 10) return 'just now';
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    return `${Math.floor(diff / 3600)}h ago`;
  }

  let color = $derived(agentColor(agent.agent_type));
</script>

<div class="agent-card" style="--agent-color: {color}">
  <!-- Header -->
  <div class="card-header">
    <div class="header-left">
      <span class="agent-icon" style="color: {color}">{agentIcon(agent.agent_type)}</span>
      <span class="agent-id">{agent.agent_id}</span>
    </div>
    <div class="header-right">
      <StatusDot status={presenceStatus(agent.status)} />
      <span class="status-label">{agent.status}</span>
    </div>
  </div>
  <div class="card-subheader">
    <span class="agent-type">{agent.agent_type || 'unknown'}{#if agent.active_files?.length} · {agent.active_files.length} files{/if}</span>
    <span class="heartbeat-time">{relativeTime(agent.last_heartbeat)}</span>
  </div>

  <!-- Sparkline: heartbeat frequency -->
  {#if heartbeatData.length >= 2}
    <div class="sparkline-row">
      <span class="sparkline-label">Activity</span>
      <SparkLine data={heartbeatData} width={100} height={20} color={color} />
    </div>
  {/if}

  <!-- Details -->
  <div class="card-details">
    {#if agent.current_task}
      <div class="detail-row">
        <span class="detail-icon">{'\u2611'}</span>
        <span class="detail-text truncate" title={agent.current_task}>{agent.current_task}</span>
      </div>
    {/if}
    {#if agent.branch}
      <div class="detail-row">
        <span class="detail-icon">{'\u2387'}</span>
        <span class="detail-text text-mono">{agent.branch}</span>
        {#if agent.pr_url}
          <a href={agent.pr_url} target="_blank" rel="noopener" class="pr-badge">PR</a>
        {/if}
      </div>
    {/if}
    {#if sharedFileAgents.length > 0}
      <div class="detail-row">
        <span class="detail-icon">{'\u2637'}</span>
        <span class="detail-text">shared files</span>
        <span class="overlap-dots">
          {#each sharedFileAgents.slice(0, 3) as overlapAgent}
            <span class="overlap-dot" title={overlapAgent} style="background: {agentColor('')}"></span>
          {/each}
          {#if sharedFileAgents.length > 3}
            <span class="overlap-more">+{sharedFileAgents.length - 3}</span>
          {/if}
        </span>
      </div>
    {/if}
  </div>

  <!-- Actions -->
  {#if agent.status === 'active' && (ondispatch || onnudge)}
    <div class="card-actions">
      {#if onnudge}
        <button class="btn btn-xs btn-nudge" onclick={() => onnudge(agent.agent_id)}>Nudge</button>
      {/if}
      {#if ondispatch}
        <button class="btn btn-xs btn-dispatch" onclick={() => ondispatch(agent.agent_id)}>Dispatch</button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .agent-card {
    background: var(--bg-secondary);
    border: 1px solid var(--border);
    border-radius: var(--border-radius);
    border-top: 3px solid var(--agent-color);
    padding: 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    transition: border-color 0.15s;
  }
  .agent-card:hover { border-color: var(--agent-color); }

  .card-header { display: flex; align-items: center; justify-content: space-between; }
  .header-left { display: flex; align-items: center; gap: 6px; }
  .header-right { display: flex; align-items: center; gap: 4px; }
  .agent-icon { font-size: 14px; }
  .agent-id { font-size: 13px; font-weight: 600; font-family: var(--font-mono); color: var(--fg-primary); }
  .status-label { font-size: 10px; font-family: var(--font-mono); color: var(--fg-muted); text-transform: uppercase; }

  .card-subheader { display: flex; align-items: center; justify-content: space-between; }
  .agent-type { font-size: 11px; color: var(--fg-secondary); }
  .heartbeat-time { font-size: 10px; font-family: var(--font-mono); color: var(--fg-muted); }

  .sparkline-row { display: flex; align-items: center; gap: 8px; }
  .sparkline-label { font-size: 10px; color: var(--fg-muted); text-transform: uppercase; letter-spacing: 0.3px; width: 48px; flex-shrink: 0; }

  .card-details { display: flex; flex-direction: column; gap: 4px; }
  .detail-row { display: flex; align-items: center; gap: 6px; font-size: 11px; color: var(--fg-secondary); }
  .detail-icon { font-size: 11px; color: var(--fg-muted); width: 14px; text-align: center; flex-shrink: 0; }
  .detail-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .pr-badge { font-size: 9px; padding: 1px 4px; border-radius: var(--radius-sm); background: rgba(129, 240, 254, 0.1); color: var(--accent); text-decoration: none; border: 1px solid rgba(129, 240, 254, 0.2); flex-shrink: 0; }
  .pr-badge:hover { background: rgba(129, 240, 254, 0.2); }

  .overlap-dots { display: flex; align-items: center; gap: 2px; margin-left: auto; }
  .overlap-dot { width: 8px; height: 8px; border-radius: 50%; }
  .overlap-more { font-size: 9px; color: var(--fg-muted); font-family: var(--font-mono); }

  .card-actions { display: flex; justify-content: flex-end; padding-top: 4px; border-top: 1px solid var(--border); }
  .btn-xs { padding: 2px 8px; font-size: 11px; }
  .btn-nudge { background: rgba(231, 179, 18, 0.1); color: var(--warning); border: 1px solid rgba(231, 179, 18, 0.25); border-radius: var(--radius-sm); cursor: pointer; }
  .btn-nudge:hover { background: rgba(231, 179, 18, 0.2); }
  .btn-dispatch { background: rgba(129, 240, 254, 0.1); color: var(--accent); border: 1px solid rgba(129, 240, 254, 0.25); border-radius: var(--radius-sm); cursor: pointer; }
  .btn-dispatch:hover { background: rgba(129, 240, 254, 0.2); }
</style>
