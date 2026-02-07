// localStorage persistence wrapper with hud: key prefix.
// Provides a simple get/set interface with JSON serialization.

const PREFIX = 'hud:';

export function persistGet<T>(key: string, fallback: T): T {
  try {
    const raw = globalThis.localStorage?.getItem(PREFIX + key);
    if (raw === null) return fallback;
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

export function persistSet<T>(key: string, value: T): void {
  try {
    globalThis.localStorage?.setItem(PREFIX + key, JSON.stringify(value));
  } catch {
    // Quota exceeded or unavailable — silently ignore.
  }
}

export function persistRemove(key: string): void {
  try {
    globalThis.localStorage?.removeItem(PREFIX + key);
  } catch {
    // ignore
  }
}
