<script lang="ts">
  // PolicyProposalsCard — adaptive proposals panel (Phase 7 slice 7.4).
  // Reads /api/hive/policy/proposals (slice 7.1 → 7.2 proxy) via the
  // shared HiveStore so proposals refresh at the standard 15s cadence.
  // Apply/Reject hit POST /api/hive/policy/proposals/{id}/{apply|reject}
  // (proxy passthrough lands in slice 7.2).

  import { hiveStore } from '../../stores/hive.svelte.ts';
  import PanelShell from '../shared/PanelShell.svelte';

  // Trigger an initial fetch on mount; the polling loop owned by the
  // Backlog/Pipelines panels keeps it fresh thereafter, but if this
  // card mounts standalone we still want a populated first paint.
  $effect(() => {
    void hiveStore.fetchPolicyProposals();
  });

  let proposals = $derived(hiveStore.policyProposals);
  let disabled = $derived(hiveStore.disabled);

  async function onApply(id: number): Promise<void> {
    // Confirm so a stray click can't silently mutate policy. The
    // 24h revert window mirrors the operator's auto-revert logic.
    if (!globalThis.confirm('Apply this proposal? It can be reverted within 24h.')) {
      return;
    }
    try {
      await hiveStore.applyPolicyProposal(id);
    } catch (e) {
      // eslint-disable-next-line no-console
      console.warn('applyPolicyProposal failed', e);
    }
  }

  async function onReject(id: number): Promise<void> {
    try {
      await hiveStore.rejectPolicyProposal(id);
    } catch (e) {
      // eslint-disable-next-line no-console
      console.warn('rejectPolicyProposal failed', e);
    }
  }
</script>

<PanelShell
  title="Policy proposals"
  icon="⚙"
  count={proposals.length}
  empty={proposals.length === 0}
  emptyIcon={disabled ? '◯' : '✓'}
  emptyMessage={disabled ? 'Hive operator not configured' : 'No pending proposals'}
  emptyHint={disabled
    ? 'Set LOOM_HIVE_OPERATOR_URL on the HUD to connect.'
    : 'The Sunday adaptive job emits proposals when policy looks too tight or too loose.'}
>
  <ul class="proposals">
    {#each proposals as p (p.ID)}
      <li class="proposal kind-{p.Kind}">
        <header class="proposal-header">
          <span class="kind kind-{p.Kind}">{p.Kind}</span>
          <span class="target mono">{p.Target}</span>
          <span class="date">{p.ProposalDate}</span>
        </header>
        <pre class="diff mono">{p.Diff}</pre>
        <p class="rationale">{p.Rationale}</p>
        <footer class="proposal-actions">
          <button type="button" class="apply" onclick={() => onApply(p.ID)}>Apply</button>
          <button type="button" class="reject" onclick={() => onReject(p.ID)}>Reject</button>
        </footer>
      </li>
    {/each}
  </ul>
</PanelShell>

<style>
  .proposals {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.6rem;
  }
  .proposal {
    border: 1px solid var(--border-subtle, #233);
    border-left-width: 3px;
    border-radius: 4px;
    padding: 0.6rem 0.75rem;
    background: var(--bg-subtle, rgba(20, 28, 40, 0.4));
  }
  /* Kind colors mirror the semantic intent of the Sunday job. */
  .proposal.kind-relax           { border-left-color: rgb(120, 220, 160); }
  .proposal.kind-tighten         { border-left-color: rgb(240, 180, 100); }
  .proposal.kind-rotate_ensemble { border-left-color: rgb(180, 140, 220); }

  .proposal-header {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-size: 0.8rem;
    margin-bottom: 0.35rem;
  }
  .kind {
    padding: 0.05rem 0.45rem;
    border-radius: 3px;
    font-size: 0.7rem;
    font-weight: 600;
    text-transform: lowercase;
    letter-spacing: 0.02em;
  }
  .kind-relax           { background: rgba(72, 200, 128, 0.15); color: rgb(120, 220, 160); }
  .kind-tighten         { background: rgba(220, 140, 60, 0.15); color: rgb(240, 180, 100); }
  .kind-rotate_ensemble { background: rgba(160, 110, 220, 0.15); color: rgb(200, 160, 240); }
  .target { color: var(--text, #cdd); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .date { color: var(--text-muted, #889); font-size: 0.72rem; }

  .mono { font-family: ui-monospace, monospace; }
  .diff {
    margin: 0 0 0.4rem 0;
    padding: 0.45rem 0.6rem;
    background: rgba(0, 0, 0, 0.25);
    border-radius: 3px;
    font-size: 0.78rem;
    color: var(--text, #cdd);
    white-space: pre-wrap;
    word-break: break-word;
    max-height: 8rem;
    overflow-y: auto;
  }
  .rationale {
    margin: 0 0 0.5rem 0;
    color: var(--text-muted, #aab);
    font-size: 0.82rem;
    line-height: 1.35;
  }
  .proposal-actions {
    display: flex;
    gap: 0.4rem;
    justify-content: flex-end;
  }
  button {
    padding: 0.25rem 0.7rem;
    border-radius: 3px;
    border: 1px solid var(--border-subtle, #233);
    font-size: 0.78rem;
    cursor: pointer;
    background: var(--bg-subtle, #1a2030);
    color: var(--text, #cdd);
    transition: background 0.12s ease, border-color 0.12s ease;
  }
  button:hover { border-color: var(--border, #455); }
  button.apply  { background: rgba(72, 200, 128, 0.18); color: rgb(150, 230, 180); border-color: rgba(72, 200, 128, 0.35); }
  button.apply:hover  { background: rgba(72, 200, 128, 0.28); }
  button.reject { background: rgba(220, 80, 80, 0.15);  color: rgb(240, 150, 150); border-color: rgba(220, 80, 80, 0.35); }
  button.reject:hover { background: rgba(220, 80, 80, 0.25); }
</style>
