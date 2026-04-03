<script>
  import { presenceActionsStore } from '../../stores/presenceActions.svelte.ts';
  import Modal from '../../widgets/Modal.svelte';
</script>

<Modal title="Create Handoff" open={presenceActionsStore.showHandoffModal} onClose={() => presenceActionsStore.closeHandoffModal()}>
  <div class="form-group">
    <label class="form-label" for="handoff-to-agent">Target Agent *</label>
    <input id="handoff-to-agent" type="text" bind:value={presenceActionsStore.newHandoffTo} placeholder="Target agent ID..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-summary">Instructions *</label>
    <input id="handoff-summary" type="text" bind:value={presenceActionsStore.newHandoffSummary} placeholder="What should the target agent do next..." class="form-input" />
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-context">Additional Details</label>
    <textarea id="handoff-context" bind:value={presenceActionsStore.newHandoffContext} placeholder="Relevant findings, constraints, and follow-up notes..." class="form-input" rows="4"></textarea>
  </div>
  <div class="form-group">
    <label class="form-label" for="handoff-type">Handoff Type</label>
    <div class="type-selector">
      <button class="type-btn" class:active={presenceActionsStore.newHandoffType === 'summary_only'} onclick={() => presenceActionsStore.newHandoffType = 'summary_only'}>Summary Only</button>
      <button class="type-btn" class:active={presenceActionsStore.newHandoffType === 'selective'} onclick={() => presenceActionsStore.newHandoffType = 'selective'}>Selective</button>
      <button class="type-btn" class:active={presenceActionsStore.newHandoffType === 'full'} onclick={() => presenceActionsStore.newHandoffType = 'full'}>Full</button>
    </div>
  </div>
  {#if presenceActionsStore.newHandoffType === 'full'}
    <div class="form-group">
      <label class="form-label" for="handoff-token-budget">Token Budget</label>
      <input id="handoff-token-budget" type="number" bind:value={presenceActionsStore.newHandoffTokenBudget} placeholder="Max tokens (0 = unlimited)" class="form-input" min="0" />
    </div>
  {/if}
  {#if presenceActionsStore.newHandoffType === 'selective'}
    <div class="form-group">
      <label class="form-label" for="handoff-entry-ids">Entry IDs (comma-separated)</label>
      <input id="handoff-entry-ids" type="text" value={presenceActionsStore.newHandoffEntryIds.join(', ')} oninput={(e) => presenceActionsStore.newHandoffEntryIds = e.target.value.split(',').map(s => s.trim()).filter(Boolean)} placeholder="entry-1, entry-2, ..." class="form-input" />
    </div>
  {/if}
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => presenceActionsStore.closeHandoffModal()}>Cancel</button>
    <button class="btn btn-primary" onclick={() => presenceActionsStore.submitHandoff()} disabled={presenceActionsStore.creatingHandoff || !presenceActionsStore.newHandoffTo.trim() || !presenceActionsStore.newHandoffSummary.trim()}>
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
  .type-selector {
    display: flex;
    gap: 4px;
  }
  .type-btn {
    padding: 4px 10px;
    font-size: 11px;
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
    background: transparent;
    color: var(--fg-secondary);
    cursor: pointer;
    transition: all 100ms;
  }
  .type-btn.active {
    border-color: var(--accent);
    background: rgba(var(--accent-rgb, 100, 160, 255), 0.12);
    color: var(--accent);
    font-weight: 600;
  }
  .type-btn:hover:not(.active) {
    border-color: var(--fg-muted);
  }
</style>
