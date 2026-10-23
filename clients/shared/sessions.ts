interface TitledSession {
  id: string;
  title?: string;
}

// The clients use the same collation regardless of the browser/editor locale.
// Case and accents do not split the alphabet; numbered titles read naturally.
const titles = new Intl.Collator('en', { sensitivity: 'base', numeric: true });

/** Sort the displayed titles without mutating an API response or query cache. */
export function sessionsByTitle<T extends TitledSession>(sessions: readonly T[]): T[] {
  return [...sessions].sort((a, b) =>
    titles.compare(a.title || 'Untitled session', b.title || 'Untitled session') ||
    (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
  );
}
