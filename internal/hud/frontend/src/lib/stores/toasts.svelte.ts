// Toast notification store — transient messages that auto-dismiss.

export type ToastType = 'info' | 'success' | 'warning' | 'error';

export interface Toast {
  id: number;
  message: string;
  type: ToastType;
  dismissAt: number;
}

let nextId = 1;

class ToastStore {
  items = $state<Toast[]>([]);

  private cleanupTimer: ReturnType<typeof setInterval> | null = null;

  /** Show a toast notification. Auto-dismisses after durationMs (default 3000). */
  show(message: string, type: ToastType = 'info', durationMs = 3000): void {
    const toast: Toast = {
      id: nextId++,
      message,
      type,
      dismissAt: Date.now() + durationMs,
    };
    this.items = [...this.items, toast];
    this.ensureCleanup();
  }

  success(message: string): void { this.show(message, 'success'); }
  error(message: string): void { this.show(message, 'error', 5000); }
  warning(message: string): void { this.show(message, 'warning', 4000); }
  info(message: string): void { this.show(message, 'info'); }

  dismiss(id: number): void {
    this.items = this.items.filter((t) => t.id !== id);
  }

  private ensureCleanup(): void {
    if (this.cleanupTimer) return;
    this.cleanupTimer = setInterval(() => {
      const now = Date.now();
      this.items = this.items.filter((t) => t.dismissAt > now);
      if (this.items.length === 0 && this.cleanupTimer) {
        clearInterval(this.cleanupTimer);
        this.cleanupTimer = null;
      }
    }, 500);
  }
}

export const toastStore = new ToastStore();
