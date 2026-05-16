<script lang="ts">
  /**
   * CreateTaskModal — new-task launch form. Owns its own form state; on
   * success calls taskStore.createTask and emits toast + close.
   *
   * @type {{
   *   open: boolean,
   *   onClose: () => void,
   * }}
   */
  let { open, onClose } = $props();

  import { taskStore } from '../../stores/tasks.svelte.ts';
  import { agentStore } from '../../stores/agents.svelte.ts';
  import { toastStore } from '../../stores/toasts.svelte.ts';
  import Modal from '../../widgets/Modal.svelte';
  import { agentStatusIcon } from '../../utils/tasksHelpers';

  let availableAgents = $derived(agentStore.agents ?? []);
  let tasks = $derived(taskStore.tasks ?? []);
  let blockableTasks = $derived(
    tasks.filter((t) => t.status === 'pending' || t.status === 'in_progress')
  );

  let newTitle = $state('');
  let newPriority = $state('medium');
  let newSessionId = $state('');
  let newTags = $state('');
  let newContext = $state('');
  let newFilePath = $state('');
  let newBlockedBy = $state<string[]>([]);
  let selectedAgentId = $state('');
  let creating = $state(false);
  let showOptional = $state(false);

  function resetForm() {
    newTitle = '';
    newPriority = 'medium';
    newSessionId = '';
    newTags = '';
    newContext = '';
    newFilePath = '';
    newBlockedBy = [];
    selectedAgentId = '';
    creating = false;
    showOptional = false;
  }

  $effect(() => {
    if (!open) resetForm();
  });

  function onAgentSelect(agentId: string) {
    selectedAgentId = agentId;
    if (agentId) {
      const agent = availableAgents.find((a) => a.agent_id === agentId);
      if (agent?.session_id) newSessionId = agent.session_id;
    } else {
      newSessionId = '';
    }
  }

  function addBlockedBy(taskId: string) {
    if (taskId && !newBlockedBy.includes(taskId)) newBlockedBy = [...newBlockedBy, taskId];
  }
  function removeBlockedBy(taskId: string) {
    newBlockedBy = newBlockedBy.filter((id) => id !== taskId);
  }

  async function submit() {
    if (!newTitle.trim()) return;
    creating = true;
    const tags = newTags.trim() ? newTags.split(',').map((t) => t.trim()).filter(Boolean) : undefined;
    const ok = await taskStore.createTask({
      title: newTitle.trim(),
      priority: newPriority,
      sessionId: newSessionId.trim() || undefined,
      tags,
      context: newContext.trim() || undefined,
      filePath: newFilePath.trim() || undefined,
      blockedBy: newBlockedBy.length ? newBlockedBy : undefined,
    });
    if (ok) {
      toastStore.success('Task created');
      onClose();
    } else {
      toastStore.error(taskStore.error ?? 'Failed to create task');
      creating = false;
    }
  }
</script>

<Modal {open} title="New Task" {onClose}>
  <form class="create-form" onsubmit={(e) => { e.preventDefault(); submit(); }}>
    <div class="form-field">
      <label class="form-label" for="task-title">Title <span class="required">*</span></label>
      <input id="task-title" type="text" bind:value={newTitle} placeholder="What needs to be done?" required />
    </div>

    <div class="form-row">
      <div class="form-field form-field-half">
        <label class="form-label" for="task-agent">Assign to Agent</label>
        <select id="task-agent" value={selectedAgentId} onchange={(e) => onAgentSelect((e.target as HTMLSelectElement).value)}>
          <option value="">Unassigned</option>
          {#each availableAgents as agent}
            <option value={agent.agent_id}>
              {agentStatusIcon(agent.status)} {agent.agent_id} ({agent.agent_type})
            </option>
          {/each}
        </select>
      </div>
      <div class="form-field form-field-half">
        <label class="form-label" for="task-priority">Priority</label>
        <select id="task-priority" bind:value={newPriority}>
          <option value="low">Low</option>
          <option value="medium">Medium</option>
          <option value="high">High</option>
          <option value="critical">Critical</option>
        </select>
      </div>
    </div>

    <div class="form-field">
      <label class="form-label" for="task-context">Context / Description</label>
      <textarea
        id="task-context"
        bind:value={newContext}
        placeholder="Describe what needs to be done, include relevant details..."
        rows="3"
      ></textarea>
    </div>

    <button type="button" class="optional-toggle" onclick={() => showOptional = !showOptional}>
      {showOptional ? '▼' : '▶'} Optional fields
    </button>

    {#if showOptional}
      <div class="optional-section">
        <div class="form-field">
          <label class="form-label" for="task-filepath">File Path</label>
          <input id="task-filepath" type="text" bind:value={newFilePath} placeholder="services/api/auth.go" />
        </div>
        <div class="form-field">
          <label class="form-label" for="task-tags">Tags (comma-separated)</label>
          <input id="task-tags" type="text" bind:value={newTags} placeholder="auth, refactor, bug..." />
        </div>
        <div class="form-field">
          <label class="form-label" for="task-blocked-by">Blocked By</label>
          <div class="blocked-by-picker">
            <select id="task-blocked-by" onchange={(e) => { const el = e.target as HTMLSelectElement; addBlockedBy(el.value); el.value = ''; }}>
              <option value="">Select a task...</option>
              {#each blockableTasks as t}
                {#if !newBlockedBy.includes(t.id)}
                  <option value={t.id}>{t.title} ({t.id.slice(0, 8)})</option>
                {/if}
              {/each}
            </select>
            {#if newBlockedBy.length > 0}
              <div class="blocked-chips">
                {#each newBlockedBy as depId}
                  <span class="dep-chip">
                    {depId.slice(0, 8)}
                    <button type="button" class="chip-remove" onclick={() => removeBlockedBy(depId)}>×</button>
                  </span>
                {/each}
              </div>
            {/if}
          </div>
        </div>
        {#if selectedAgentId}
          <div class="form-field">
            <label class="form-label" for="task-session">Session ID (auto-filled from agent)</label>
            <input id="task-session" type="text" bind:value={newSessionId} placeholder="Link to session..." />
          </div>
        {/if}
      </div>
    {/if}

    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={onClose}>Cancel</button>
      <button type="submit" class="btn btn-success" disabled={creating || !newTitle.trim()}>
        {creating ? 'Creating...' : 'Create Task'}
      </button>
    </div>
  </form>
</Modal>

<style>
  .create-form { display: flex; flex-direction: column; }
  .create-form textarea {
    width: 100%;
    resize: vertical;
    font-family: inherit;
    font-size: var(--text-sm);
    padding: 6px var(--space-2);
    background: var(--bg-primary);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    color: var(--fg-primary);
    transition: border-color var(--transition-fast);
  }
  .create-form textarea:focus {
    border-color: var(--info);
    outline: none;
    box-shadow: 0 0 4px var(--glow-info);
  }
  .required { color: var(--error); }
  .form-row { display: flex; gap: var(--space-3); }
  .form-field-half { flex: 1; }
  .optional-toggle {
    background: none;
    border: none;
    color: var(--fg-muted);
    font-size: var(--text-xs);
    cursor: pointer;
    padding: 6px 0;
    text-align: left;
    transition: color var(--transition-fast);
  }
  .optional-toggle:hover { color: var(--fg-secondary); }
  .optional-section {
    border: 1px solid var(--border);
    border-radius: var(--radius-md);
    padding: var(--space-3);
    margin-bottom: var(--space-2);
    background: var(--bg-primary);
  }
  .blocked-by-picker { display: flex; flex-direction: column; gap: 6px; }
  .blocked-chips { display: flex; flex-wrap: wrap; gap: 4px; }
  .dep-chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-family: var(--font-mono);
    font-size: var(--text-xs);
    padding: 2px 6px;
    background: var(--bg-tertiary);
    border: 1px solid var(--border-subtle);
    border-radius: var(--radius-sm);
    color: var(--fg-secondary);
  }
  .chip-remove {
    background: none;
    border: none;
    color: var(--fg-muted);
    cursor: pointer;
    padding: 0 2px;
    font-size: 12px;
    line-height: 1;
    transition: color var(--transition-fast);
  }
  .chip-remove:hover { color: var(--error); }
</style>
