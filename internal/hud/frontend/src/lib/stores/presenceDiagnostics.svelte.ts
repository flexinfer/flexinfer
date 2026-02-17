import type { AgentPresence } from './presence.svelte.ts';
import { toastStore } from './toasts.svelte.ts';

interface NudgePolicyLike {
  cap?: number;
  debounce_ms?: number;
  drop_policy?: string;
  lane_priority?: string[];
}

class PresenceDiagnosticsStore {
  diagnosticsAgentId = $state('');
  diagnosticsLoading = $state(false);
  diagnosticsError = $state('');

  contextInspect = $state<any>(null);
  contextInspectError = $state('');
  nudgeQueueStatus = $state<any>(null);
  nudgeQueueStatusError = $state('');
  nudgeQueuePolicy = $state<any>(null);
  nudgeQueuePolicyError = $state('');

  nudgePolicyCapInput = $state('');
  nudgePolicyDebounceInput = $state('');
  nudgePolicyDropPolicy = $state('drop_old');
  nudgePolicyLanePriorityInput = $state('');
  nudgePolicyUpdatedBy = $state('hud-ui');
  nudgePolicyAdminToken = $state('');
  nudgePolicyUpdating = $state(false);
  nudgePolicyMutationError = $state('');
  nudgePolicyFormDirty = $state(false);

  readonly nudgeDropPolicyOptions = ['drop_old', 'drop_new', 'summarize'];

  private pollTimer: ReturnType<typeof setInterval> | null = null;

  private parseLanePriorityInput(raw: string): string[] {
    return raw
      .split(',')
      .map((lane) => lane.trim())
      .filter((lane) => lane.length > 0);
  }

  private hydrateNudgePolicyForm(policy: NudgePolicyLike | null | undefined): void {
    if (!policy || this.nudgePolicyFormDirty || this.nudgePolicyUpdating) return;
    this.nudgePolicyCapInput = String(policy.cap ?? '');
    this.nudgePolicyDebounceInput = String(policy.debounce_ms ?? 0);
    this.nudgePolicyDropPolicy = this.nudgeDropPolicyOptions.includes(policy.drop_policy ?? '')
      ? (policy.drop_policy as string)
      : 'drop_old';
    this.nudgePolicyLanePriorityInput = (policy.lane_priority ?? []).join(', ');
    this.nudgePolicyMutationError = '';
    this.nudgePolicyFormDirty = false;
  }

  private async fetchJSON(url: string): Promise<any> {
    const res = await globalThis.fetch(url);
    let data: any = null;
    try {
      data = await res.json();
    } catch {
      data = null;
    }
    if (!res.ok) {
      const msg = data?.error || `${res.status} ${res.statusText}`;
      throw new Error(msg);
    }
    return data;
  }

  syncAgents(agents: AgentPresence[]): void {
    const current = this.diagnosticsAgentId;
    if (current && agents.some((a) => a.agent_id === current)) return;

    const next = agents.find((a) => a.status === 'active')?.agent_id || agents[0]?.agent_id || '';
    if (next !== this.diagnosticsAgentId) {
      this.diagnosticsAgentId = next;
      if (next) {
        void this.fetchDiagnostics();
      }
    }
  }

  startPolling(intervalMs = 10000): void {
    if (this.pollTimer) return;
    this.pollTimer = setInterval(() => {
      void this.fetchDiagnostics();
    }, intervalMs);
    void this.fetchDiagnostics();
  }

  stopPolling(): void {
    if (!this.pollTimer) return;
    clearInterval(this.pollTimer);
    this.pollTimer = null;
  }

  markNudgePolicyDirty(): void {
    this.nudgePolicyFormDirty = true;
    this.nudgePolicyMutationError = '';
  }

  resetNudgePolicyForm(): void {
    const source = this.nudgeQueuePolicy ?? this.nudgeQueueStatus;
    if (!source) return;
    this.nudgePolicyFormDirty = false;
    this.hydrateNudgePolicyForm(source);
  }

  async updateNudgePolicy(): Promise<void> {
    if (this.nudgePolicyUpdating) return;

    const token = this.nudgePolicyAdminToken.trim();
    if (!token) {
      this.nudgePolicyMutationError = 'Admin token is required to update policy.';
      return;
    }

    const cap = Number.parseInt(this.nudgePolicyCapInput.trim(), 10);
    if (!Number.isInteger(cap) || cap <= 0) {
      this.nudgePolicyMutationError = 'Cap must be a positive integer.';
      return;
    }

    const debounceMs = Number.parseInt(this.nudgePolicyDebounceInput.trim(), 10);
    if (!Number.isInteger(debounceMs) || debounceMs < 0) {
      this.nudgePolicyMutationError = 'Debounce must be a non-negative integer (ms).';
      return;
    }

    const dropPolicy = this.nudgePolicyDropPolicy.trim();
    if (!this.nudgeDropPolicyOptions.includes(dropPolicy)) {
      this.nudgePolicyMutationError = 'Drop policy must be drop_old, drop_new, or summarize.';
      return;
    }

    const lanePriority = this.parseLanePriorityInput(this.nudgePolicyLanePriorityInput);
    if (lanePriority.length === 0) {
      this.nudgePolicyMutationError = 'Lane priority must include at least one lane.';
      return;
    }

    const currentPolicy = this.nudgeQueuePolicy ?? this.nudgeQueueStatus;
    if (
      currentPolicy &&
      currentPolicy.cap === cap &&
      currentPolicy.debounce_ms === debounceMs &&
      currentPolicy.drop_policy === dropPolicy &&
      JSON.stringify(currentPolicy.lane_priority ?? []) === JSON.stringify(lanePriority)
    ) {
      this.nudgePolicyMutationError = '';
      this.nudgePolicyFormDirty = false;
      toastStore.info('Nudge queue policy is already up to date');
      return;
    }

    this.nudgePolicyMutationError = '';
    this.nudgePolicyUpdating = true;
    try {
      const res = await globalThis.fetch('/api/agent/nudge-queue-policy', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Admin-Token': token,
        },
        body: JSON.stringify({
          cap,
          debounce_ms: debounceMs,
          drop_policy: dropPolicy,
          lane_priority: lanePriority,
          updated_by: this.nudgePolicyUpdatedBy.trim() || 'hud-ui',
        }),
      });
      let data: any = null;
      try {
        data = await res.json();
      } catch {
        data = null;
      }
      if (!res.ok) {
        throw new Error(data?.error || `${res.status} ${res.statusText}`);
      }
      this.nudgeQueuePolicy = data?.policy ?? null;
      this.nudgePolicyFormDirty = false;
      this.hydrateNudgePolicyForm(this.nudgeQueuePolicy);
      toastStore.success('Nudge queue policy updated');
      await this.fetchDiagnostics();
    } catch (e) {
      this.nudgePolicyMutationError = e instanceof Error ? e.message : 'Failed to update policy';
      toastStore.error(this.nudgePolicyMutationError);
    } finally {
      this.nudgePolicyUpdating = false;
    }
  }

  async fetchDiagnostics(): Promise<void> {
    const agentID = this.diagnosticsAgentId?.trim();
    if (!agentID) return;

    this.diagnosticsLoading = true;
    this.diagnosticsError = '';
    this.contextInspectError = '';
    this.nudgeQueueStatusError = '';
    this.nudgeQueuePolicyError = '';

    const [ctxResult, queueResult, policyResult] = await Promise.allSettled([
      this.fetchJSON(`/api/agent/context-inspect?agent_id=${encodeURIComponent(agentID)}&detail=true&limit=200`),
      this.fetchJSON(`/api/agent/nudge-queue?agent_id=${encodeURIComponent(agentID)}`),
      this.fetchJSON('/api/agent/nudge-queue-policy'),
    ]);

    if (ctxResult.status === 'fulfilled') {
      this.contextInspect = ctxResult.value ?? null;
    } else {
      this.contextInspect = null;
      this.contextInspectError = ctxResult.reason?.message ?? 'Failed to load context diagnostics';
    }

    if (queueResult.status === 'fulfilled') {
      this.nudgeQueueStatus = queueResult.value?.status ?? null;
    } else {
      this.nudgeQueueStatus = null;
      this.nudgeQueueStatusError = queueResult.reason?.message ?? 'Failed to load queue status';
    }

    if (policyResult.status === 'fulfilled') {
      this.nudgeQueuePolicy = policyResult.value?.policy ?? null;
    } else {
      this.nudgeQueuePolicy = null;
      this.nudgeQueuePolicyError = policyResult.reason?.message ?? 'Failed to load queue policy';
    }

    if (!this.nudgePolicyFormDirty && !this.nudgePolicyUpdating) {
      this.hydrateNudgePolicyForm(this.nudgeQueuePolicy ?? this.nudgeQueueStatus);
    }

    if (this.contextInspectError && this.nudgeQueueStatusError && this.nudgeQueuePolicyError) {
      this.diagnosticsError = 'Unable to load diagnostics from HUD API.';
    }

    this.diagnosticsLoading = false;
  }
}

export const presenceDiagnosticsStore = new PresenceDiagnosticsStore();
