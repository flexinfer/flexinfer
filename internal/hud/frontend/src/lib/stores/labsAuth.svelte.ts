import { persistGet, persistRemove, persistSet } from './persist.ts';

const STORAGE_KEY = 'labs.admin_token';

class LabsAuthStore {
  adminToken = $state(persistGet<string>(STORAGE_KEY, ''));
  /** null = not checked, true = valid, false = invalid */
  tokenValid = $state<boolean | null>(null);
  validating = $state(false);

  get hasToken(): boolean {
    return this.adminToken.trim().length > 0;
  }

  setAdminToken(token: string): void {
    this.adminToken = token;
    const trimmed = token.trim();
    if (trimmed) {
      persistSet(STORAGE_KEY, trimmed);
      this.validate();
    } else {
      persistRemove(STORAGE_KEY);
      this.tokenValid = null;
    }
  }

  clearAdminToken(): void {
    this.adminToken = '';
    this.tokenValid = null;
    persistRemove(STORAGE_KEY);
  }

  async validate(): Promise<boolean> {
    const token = this.adminToken.trim();
    if (!token) {
      this.tokenValid = null;
      return false;
    }
    this.validating = true;
    try {
      const res = await globalThis.fetch('/api/labs/auth-check', {
        headers: { 'X-Admin-Token': token },
      });
      this.tokenValid = res.ok;
      return res.ok;
    } catch {
      this.tokenValid = false;
      return false;
    } finally {
      this.validating = false;
    }
  }

  requiredMessage(action: string): string {
    return `${action} requires an admin token.`;
  }
}

export const labsAuthStore = new LabsAuthStore();

export interface AdminFetchInit extends RequestInit {
  requireToken?: boolean;
  action?: string;
}

export async function adminFetch(input: RequestInfo | URL, init: AdminFetchInit = {}): Promise<Response> {
  const {
    requireToken = false,
    action = 'This action',
    headers,
    ...requestInit
  } = init;

  const token = labsAuthStore.adminToken.trim();
  if (requireToken && !token) {
    throw new Error(labsAuthStore.requiredMessage(action));
  }

  const nextHeaders = new Headers(headers);
  if (token) {
    nextHeaders.set('X-Admin-Token', token);
  }

  return globalThis.fetch(input, {
    ...requestInit,
    headers: nextHeaders,
  });
}
