<script>
  import { presenceActionsStore } from '../../stores/presenceActions.svelte.ts';
  import Modal from '../../widgets/Modal.svelte';
</script>

<Modal title="Create Handoff" open={presenceActionsStore.showHandoffModal} onClose={() => presenceActionsStore.closeHandoffModal()}>
  <div class="form-group">
    <label class="form-label" for="handoff-to-agent">To Agent (optional)</label>
    <input id="handoff-to-agent" type="text" bind:value={presenceActionsStore.newHandoffTo} placeholder="Agent ID or leave blank for any..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-summary">Summary *</label>
    <input id="handoff-summary" type="text" bind:value={presenceActionsStore.newHandoffSummary} placeholder="What needs to be done..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-context">Context</label>
    <textarea id="handoff-context" bind:value={presenceActionsStore.newHandoffContext} placeholder="Additional context, findings, decisions..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => presenceActionsStore.closeHandoffModal()}>Cancel</button>
    <button class="btn btn-primary" onclick={() => presenceActionsStore.submitHandoff()} disabled={presenceActionsStore.creatingHandoff || !presenceActionsStore.newHandoffSummary.trim()}>
      {presenceActionsStore.creatingHandoff ? 'Creating...' : 'Create Handoff'}
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
