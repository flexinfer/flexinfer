/**
 * arraysEqualById — compares two arrays of objects by their `id` field
 * and an optional hash function. Used by stores to skip state updates
 * when SSE pushes unchanged data.
 */
export function arraysEqualById<T extends { id: string }>(
  prev: T[],
  next: T[],
  hashFn?: (item: T) => string,
): boolean {
  if (prev.length !== next.length) return false;
  for (let i = 0; i < prev.length; i++) {
    if (prev[i].id !== next[i].id) return false;
    if (hashFn && hashFn(prev[i]) !== hashFn(next[i])) return false;
  }
  return true;
}

/**
 * arraysEqualByKey — like arraysEqualById but uses a custom key field.
 * Used for stores where the identity field isn't `id` (e.g. `name`).
 */
export function arraysEqualByKey<T>(
  prev: T[],
  next: T[],
  keyFn: (item: T) => string,
  hashFn?: (item: T) => string,
): boolean {
  if (prev.length !== next.length) return false;
  for (let i = 0; i < prev.length; i++) {
    if (keyFn(prev[i]) !== keyFn(next[i])) return false;
    if (hashFn && hashFn(prev[i]) !== hashFn(next[i])) return false;
  }
  return true;
}
