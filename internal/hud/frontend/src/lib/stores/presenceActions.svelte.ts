import { presenceStore } from './presence.svelte.ts';
import { toastStore } from './toasts.svelte.ts';
import {
  acceptHandoff,
  createHandoff,
  dispatchTask,
  fetchHandoffs,
  fetchTemplates,
  releaseClaim,
  sendNudge,
  type HandoffRecord,
  type TemplateRecord,
} from '../clients/presenceActions.ts';

class PresenceActionsStore {
  handoffs = $state<HandoffRecord[]>([]);
  templates = $state<TemplateRecord[]>([]);
  handoffLoading = $state(false);
  handoffError = $state('');

  showHandoffModal = $state(false);
  newHandoffTo = $state('');
  newHandoffSummary = $state('');
  newHandoffContext = $state('');
  newHandoffType = $state<'full' | 'selective' | 'summary_only'>('summary_only');
  newHandoffEntryIds = $state<string[]>([]);
  newHandoffTokenBudget = $state(0);
  creatingHandoff = $state(false);

  showDispatchModal = $state(false);
  dispatchTargetAgent = $state('');
  dispatchTitle = $state('');
  dispatchContext = $state('');
  dispatchPriority = $state('medium');
  dispatchSubmitting = $state(false);

  showNudgeModal = $state(false);
  nudgeTargetAgent = $state('');
  nudgeType = $state('message');
  nudgeContent = $state('');
  nudgeSubmitting = $state(false);

  async refreshHandoffs(): Promise<void> {
    this.handoffLoading = true;
    this.handoffError = '';

    const [handoffResult, templateResult] = await Promise.allSettled([
      fetchHandoffs(),
      fetchTemplates(),
    ]);

    if (handoffResult.status === 'fulfilled') {
      this.handoffs = handoffResult.value;
    }

    if (templateResult.status === 'fulfilled') {
      this.templates = templateResult.value;
    }

    if (handoffResult.status === 'rejected' && templateResult.status === 'rejected') {
      this.handoffError = 'Failed to load handoffs';
    }

    this.handoffLoading = false;
  }

  openHandoffModal(): void {
    this.showHandoffModal = true;
  }

  closeHandoffModal(): void {
    this.showHandoffModal = false;
  }

  async submitHandoff(): Promise<void> {
    if (!this.newHandoffTo.trim() || !this.newHandoffSummary.trim()) return;

    this.creatingHandoff = true;
    try {
      const payload: Parameters<typeof createHandoff>[0] = {
        target_agent_id: this.newHandoffTo.trim(),
        instructions: this.newHandoffContext.trim()
          ? `${this.newHandoffSummary.trim()}\n\n${this.newHandoffContext.trim()}`
          : this.newHandoffSummary.trim(),
        handoff_type: this.newHandoffType,
      };
      if (this.newHandoffType === 'selective' && this.newHandoffEntryIds.length > 0) {
        payload.entry_ids = this.newHandoffEntryIds;
      }
      if (this.newHandoffType === 'full' && this.newHandoffTokenBudget > 0) {
        payload.token_budget = this.newHandoffTokenBudget;
      }
      await createHandoff(payload);
      toastStore.success('Handoff created');
      this.showHandoffModal = false;
      this.newHandoffTo = '';
      this.newHandoffSummary = '';
      this.newHandoffContext = '';
      this.newHandoffType = 'summary_only';
      this.newHandoffEntryIds = [];
      this.newHandoffTokenBudget = 0;
      await this.refreshHandoffs();
    } catch {
      toastStore.error('Failed to create handoff');
    } finally {
      this.creatingHandoff = false;
    }
  }

  async onAcceptHandoff(id: string, targetAgentID: string): Promise<void> {
    if (!targetAgentID.trim()) {
      toastStore.error('Cannot accept handoff without a target agent');
      return;
    }
    try {
      await acceptHandoff(id, { target_agent_id: targetAgentID.trim() });
      toastStore.success('Handoff accepted');
      await this.refreshHandoffs();
    } catch {
      toastStore.error('Failed to accept handoff');
    }
  }

  onOpenDispatch(agentId: string): void {
    this.dispatchTargetAgent = agentId;
    this.showDispatchModal = true;
  }

  onOpenDispatchWithDefaults(agentId: string, title: string, priority?: string): void {
    this.dispatchTargetAgent = agentId;
    this.dispatchTitle = title;
    if (priority) this.dispatchPriority = priority;
    this.showDispatchModal = true;
  }

  closeDispatchModal(): void {
    this.showDispatchModal = false;
  }

  async submitDispatch(): Promise<void> {
    if (!this.dispatchTargetAgent || !this.dispatchTitle.trim()) return;

    this.dispatchSubmitting = true;
    try {
      await dispatchTask({
        target_agent_id: this.dispatchTargetAgent,
        title: this.dispatchTitle.trim(),
        context: this.dispatchContext.trim() || undefined,
        priority: this.dispatchPriority,
      });
      toastStore.success(`Task dispatched to ${this.dispatchTargetAgent}`);
      this.showDispatchModal = false;
      this.dispatchTitle = '';
      this.dispatchContext = '';
      this.dispatchPriority = 'medium';
    } catch {
      toastStore.error('Failed to dispatch task');
    } finally {
      this.dispatchSubmitting = false;
    }
  }

  onOpenNudge(agentId: string): void {
    this.nudgeTargetAgent = agentId;
    this.nudgeType = 'message';
    this.nudgeContent = '';
    this.showNudgeModal = true;
  }

  closeNudgeModal(): void {
    this.showNudgeModal = false;
  }

  async submitNudge(): Promise<void> {
    if (!this.nudgeTargetAgent || !this.nudgeContent.trim()) return;

    this.nudgeSubmitting = true;
    try {
      await sendNudge({
        target_agent_id: this.nudgeTargetAgent,
        type: this.nudgeType,
        content: this.nudgeContent.trim(),
        from_agent: 'hud',
      });
      toastStore.success(`Nudge sent to ${this.nudgeTargetAgent}`);
      this.showNudgeModal = false;
    } catch {
      toastStore.error('Failed to send nudge');
    } finally {
      this.nudgeSubmitting = false;
    }
  }

  async onReleaseClaim(agentId: string, filePath: string): Promise<void> {
    try {
      await releaseClaim(agentId, filePath);
      toastStore.success('Claim released');
      await presenceStore.fetch();
    } catch {
      toastStore.error('Failed to release claim');
    }
  }
}

export const presenceActionsStore = new PresenceActionsStore();
