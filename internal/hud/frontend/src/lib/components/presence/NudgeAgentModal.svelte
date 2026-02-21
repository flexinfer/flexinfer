<script>
  import { presenceActionsStore } from '../../stores/presenceActions.svelte.ts';
  import Modal from '../../widgets/Modal.svelte';
</script>

<Modal title="Nudge Agent" open={presenceActionsStore.showNudgeModal} onClose={() => presenceActionsStore.closeNudgeModal()}>
  <div class="form-group">
    <label class="form-label" for="nudge-target">Target Agent</label>
    <input id="nudge-target" type="text" bind:value={presenceActionsStore.nudgeTargetAgent} class="form-input" readonly />
  </div>
  <div class="form-group">
    <label class="form-label" for="nudge-type">Type</label>
    <select id="nudge-type" bind:value={presenceActionsStore.nudgeType} class="form-input">
      <option value="message">Message</option>
      <option value="context_inject">Context Inject</option>
      <option value="task_redirect">Task Redirect</option>
      <option value="pause_request">Pause Request</option>
    </select>
  </div>
  <div class="form-group">
    <label class="form-label" for="nudge-content">Content *</label>
    <textarea id="nudge-content" bind:value={presenceActionsStore.nudgeContent} placeholder="Message or context to send to the agent..." class="form-input" rows="4"></textarea>
  </div>
  <div class="nudge-hint">
    Nudge delivered on the agent's next heartbeat (5-15s latency).
  </div>
  <div class="form-actions">
    <button class="btn btn-ghost" onclick={() => presenceActionsStore.closeNudgeModal()}>Cancel</button>
    <button class="btn btn-primary" onclick={() => presenceActionsStore.submitNudge()} disabled={presenceActionsStore.nudgeSubmitting || !presenceActionsStore.nudgeContent.trim()}>
      {presenceActionsStore.nudgeSubmitting ? 'Sending...' : 'Send Nudge'}
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
  .nudge-hint {
    font-size: 11px;
    color: var(--fg-muted);
    font-style: italic;
    margin-top: 8px;
  }
</style>
