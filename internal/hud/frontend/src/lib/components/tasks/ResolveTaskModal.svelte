<script lang="ts">
  /**
   * ResolveTaskModal — task resolution form. Owns the resolution text;
   * on submit calls taskStore.resolve and closes.
   *
   * @type {{
   *   open: boolean,
   *   taskId: string,
   *   taskTitle: string,
   *   onClose: () => void,
   * }}
   */
  let { open, taskId, taskTitle, onClose } = $props();

  import { taskStore } from '../../stores/tasks.svelte.ts';
  import { toastStore } from '../../stores/toasts.svelte.ts';
  import Modal from '../../widgets/Modal.svelte';

  let resolutionText = $state('');
  let resolving = $state(false);

  $effect(() => {
    if (!open) {
      resolutionText = '';
      resolving = false;
    }
  });

  async function submit() {
    if (!taskId) return;
    resolving = true;
    await taskStore.resolve(taskId, resolutionText.trim());
    toastStore.success('Task resolved');
    onClose();
  }
</script>

<Modal {open} title="Resolve Task" {onClose}>
  <form class="create-form" onsubmit={(e) => { e.preventDefault(); submit(); }}>
    <p class="resolve-title">{taskTitle}</p>
    <div class="form-field">
      <label class="form-label" for="resolution-text">Resolution</label>
      <textarea
        id="resolution-text"
        bind:value={resolutionText}
        placeholder="What was done to complete this task?"
        rows="3"
      ></textarea>
    </div>
    <div class="form-actions">
      <button type="button" class="btn btn-ghost" onclick={onClose}>Cancel</button>
      <button type="submit" class="btn btn-success" disabled={resolving}>
        {resolving ? 'Resolving...' : 'Resolve'}
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
  .resolve-title {
    font-weight: 600;
    color: var(--fg-primary);
    margin-bottom: var(--space-2);
    font-size: var(--text-sm);
  }
</style>
