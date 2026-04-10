<script lang="ts">
  import { labsAuthStore } from '../../stores/labsAuth.svelte.ts';

  interface Props {
    label?: string;
    hint?: string;
    compact?: boolean;
  }

  let {
    label = 'Operator token',
    hint = 'Required for protected spawn, telemetry, and sandbox actions.',
    compact = false,
  }: Props = $props();

  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  function handleInput(event: Event): void {
    const target = event.currentTarget as HTMLInputElement;
    const value = target.value;
    labsAuthStore.adminToken = value;
    const trimmed = value.trim();
    if (trimmed) {
      // Debounce validation to avoid hammering on every keystroke.
      if (debounceTimer) clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => {
        labsAuthStore.setAdminToken(value);
      }, 600);
    } else {
      if (debounceTimer) clearTimeout(debounceTimer);
      labsAuthStore.setAdminToken('');
    }
  }

  let validationIcon = $derived.by(() => {
    if (!labsAuthStore.hasToken) return '';
    if (labsAuthStore.validating) return 'validating';
    if (labsAuthStore.tokenValid === true) return 'valid';
    if (labsAuthStore.tokenValid === false) return 'invalid';
    return '';
  });
</script>

<div class="labs-access" class:compact>
  <div class="labs-access-copy">
    <div class="labs-access-label">{label}</div>
    <div class="labs-access-hint">
      {#if labsAuthStore.hasToken && labsAuthStore.tokenValid === true}
        Token verified and stored for this browser.
      {:else if labsAuthStore.hasToken && labsAuthStore.tokenValid === false}
        Token rejected by HUD. Check the value and try again.
      {:else if labsAuthStore.hasToken}
        Token stored in this browser for protected Labs actions.
      {:else}
        {hint}
      {/if}
    </div>
  </div>

  <div class="labs-access-controls">
    <div class="input-wrapper">
      <input
        class="labs-access-input"
        class:input-valid={validationIcon === 'valid'}
        class:input-invalid={validationIcon === 'invalid'}
        type="password"
        value={labsAuthStore.adminToken}
        placeholder="HUD admin token"
        autocomplete="current-password"
        spellcheck="false"
        oninput={handleInput}
      />
      {#if validationIcon === 'valid'}
        <span class="validation-badge valid" title="Token verified">&#10003;</span>
      {:else if validationIcon === 'invalid'}
        <span class="validation-badge invalid" title="Token rejected">&#10007;</span>
      {:else if validationIcon === 'validating'}
        <span class="validation-badge validating" title="Checking...">&#8230;</span>
      {/if}
    </div>
    {#if labsAuthStore.hasToken}
      <button type="button" class="labs-access-clear" onclick={() => labsAuthStore.clearAdminToken()}>
        Clear
      </button>
    {/if}
  </div>
</div>

<style>
  .labs-access {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(220px, 360px);
    gap: var(--space-3);
    align-items: center;
    padding: var(--space-3) var(--space-4);
    border: 1px solid color-mix(in srgb, var(--border-focus) 26%, var(--border));
    border-radius: var(--radius-lg);
    background:
      linear-gradient(180deg, rgba(255, 255, 255, 0.025), transparent),
      color-mix(in srgb, var(--bg-secondary) 94%, transparent);
  }

  .labs-access.compact {
    grid-template-columns: minmax(0, 1fr);
    padding: var(--space-3);
  }

  .labs-access-copy {
    display: flex;
    flex-direction: column;
    gap: 4px;
    min-width: 0;
  }

  .labs-access-label {
    font-size: var(--text-xs);
    font-weight: 700;
    letter-spacing: var(--tracking-wide);
    text-transform: uppercase;
    color: var(--fg-secondary);
  }

  .labs-access-hint {
    font-size: var(--text-sm);
    color: var(--fg-muted);
    line-height: 1.45;
  }

  .labs-access-controls {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    min-width: 0;
  }

  .input-wrapper {
    position: relative;
    flex: 1;
    min-width: 0;
  }

  .labs-access-input {
    width: 100%;
    min-width: 0;
    padding: 10px 12px;
    padding-right: 36px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-primary);
    color: var(--fg-primary);
    font-family: var(--font-mono);
    font-size: var(--text-sm);
    transition: border-color var(--transition-fast);
  }

  .labs-access-input:focus {
    outline: none;
    border-color: var(--border-focus);
    box-shadow: 0 0 0 1px color-mix(in srgb, var(--info) 32%, transparent);
  }

  .labs-access-input.input-valid {
    border-color: var(--color-success, #22c55e);
  }

  .labs-access-input.input-invalid {
    border-color: var(--color-error, #ef4444);
  }

  .validation-badge {
    position: absolute;
    right: 10px;
    top: 50%;
    transform: translateY(-50%);
    font-size: var(--text-sm);
    font-weight: 700;
    pointer-events: none;
  }

  .validation-badge.valid {
    color: var(--color-success, #22c55e);
  }

  .validation-badge.invalid {
    color: var(--color-error, #ef4444);
  }

  .validation-badge.validating {
    color: var(--fg-muted);
    animation: pulse 1.2s ease-in-out infinite;
  }

  @keyframes pulse {
    0%, 100% { opacity: 0.4; }
    50% { opacity: 1; }
  }

  .labs-access-clear {
    padding: 10px 12px;
    border-radius: var(--radius-md);
    border: 1px solid var(--border);
    background: var(--bg-tertiary);
    color: var(--fg-secondary);
    font-size: var(--text-xs);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: var(--tracking-wide);
    cursor: pointer;
    flex-shrink: 0;
  }

  .labs-access-clear:hover {
    color: var(--fg-primary);
    border-color: var(--border-focus);
  }

  @media (max-width: 720px) {
    .labs-access {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
