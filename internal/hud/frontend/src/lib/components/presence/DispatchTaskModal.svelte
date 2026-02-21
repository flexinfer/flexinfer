<script>
  import { presenceActionsStore } from '../../stores/presenceActions.svelte.ts';
  import Modal from '../../widgets/Modal.svelte';
</script>

<Modal title="Dispatch Task" open={presenceActionsStore.showDispatchModal} onClose={() => presenceActionsStore.closeDispatchModal()}>
  <div class="form-group">
    <label class="form-label" for="dispatch-target">Target Agent</label>
    <input id="dispatch-target" type="text" bind:value={presenceActionsStore.dispatchTargetAgent} class="form-input" readonly />
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-title">Title *</label>
    <input id="dispatch-title" type="text" bind:value={presenceActionsStore.dispatchTitle} placeholder="Task title..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-context">Context</label>
    <textarea id="dispatch-context" bind:value={presenceActionsStore.dispatchContext} placeholder="Additional instructions..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-group">
    <label class="form-label" for="dispatch-priority">Priority</label>
    <select id="dispatch-priority" bind:value={presenceActionsStore.dispatchPriority} class="form-input">
      <option value="low">Low</option>
      <option value="medium">Medium</option>
      <option value="high">High</option>
      <option value="critical">Critical</option>
    </select>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => presenceActionsStore.closeDispatchModal()}>Cancel</button>
    <button class="btn btn-primary" onclick={() => presenceActionsStore.submitDispatch()} disabled={presenceActionsStore.dispatchSubmitting || !presenceActionsStore.dispatchTitle.trim()}>
      {presenceActionsStore.dispatchSubmitting ? 'Dispatching...' : 'Dispatch'}
    </button>
  </div>
</Modal>

<style>
  .form-group { margin-bottom: 12px; }
  .form-label {
    display: block;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.3px;
    color: var(--fg-muted);
    margin-bottom: 4px;
  }
  .form-input { width: 100%; box-sizing: border-box; }
  textarea.form-input {
    resize: vertical;
    font-family: var(--font-sans);
    font-size: 13px;
  }
  .form-actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
  }
</style>
