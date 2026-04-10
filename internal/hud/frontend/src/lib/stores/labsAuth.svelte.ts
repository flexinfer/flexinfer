import { persistGet, persistRemove, persistSet } from './persist.ts';

const STORAGE_KEY = 'labs.admin_token';

class LabsAuthStore {
  adminToken = $state(persistGet<string>(STORAGE_KEY, ''));

  get hasToken(): boolean {
    return this.adminToken.trim().length > 0;
  }

  setAdminToken(token: string): void {
    this.adminToken = token;
    const trimmed = token.trim();
    if (trimmed) {
      persistSet(STORAGE_KEY, trimmed);
    } else {
      persistRemove(STORAGE_KEY);
    }
  }

  clearAdminToken(): void {
    this.adminToken = '';
    persistRemove(STORAGE_KEY);
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
