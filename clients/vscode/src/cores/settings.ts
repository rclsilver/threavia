/**
 * The Cores the extension connects to, as the `threavia.cores` setting lists
 * them, and how the single `threavia.coreUrl` of earlier versions becomes the
 * first of them.
 *
 * Every Core is connected at the same time, each with its own sign-in, stream
 * and cursor. Its id is generated once and never shown: it is what keeps a
 * renamed Core, or one moved to another address, the same Core to the
 * conversations, drafts and saved sign-ins that name it.
 *
 * Nothing here imports `vscode`, so the reading and the migration are tested
 * as they are.
 */

export interface CoreSetting {
  /** Stable and generated: never typed by a person. */
  id: string;
  /** What the sidebar, the pickers and the notifications call it. */
  name: string;
  /** Where it answers, without a trailing slash. */
  url: string;
  /** The Project this Core's sessions open on, by name or id. */
  project?: string;
}

/** The URL as requests are built from it: trimmed, without a trailing slash. */
export function normaliseUrl(raw: string): string {
  return raw.trim().replace(/\/+$/, '');
}

/** Says what is wrong with a URL, or nothing when it will do. */
export function validateUrl(raw: string): string | undefined {
  const value = normaliseUrl(raw);
  if (!value) return 'Enter the address Core answers on.';
  try {
    const url = new URL(value);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return 'Use an http:// or https:// address.';
  } catch {
    return 'That is not an address, such as https://threavia.example.com.';
  }
  return undefined;
}

/** The host and port, which is how a person tells two Cores apart when they have no name. */
export function hostOf(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}

/** A new id: short, since it ends up in every URI the extension makes up. */
export function newCoreId(): string {
  return crypto.randomUUID().replace(/-/g, '').slice(0, 12);
}

/**
 * The Cores as the setting holds them, made usable: an entry without a valid
 * address is left out, one without an id or with an id already taken gets a
 * new one, one without a name is called by its host. `changed` says whether
 * the result differs from what was read, so it is worth writing back: an id
 * generated for a hand-written entry must be the same next time.
 */
export function readCores(raw: unknown, newId: () => string = newCoreId): { cores: CoreSetting[]; changed: boolean } {
  if (!Array.isArray(raw)) return { cores: [], changed: raw !== undefined && raw !== null };
  const cores: CoreSetting[] = [];
  const ids = new Set<string>();
  let changed = false;
  for (const entry of raw as unknown[]) {
    const value = (entry && typeof entry === 'object' ? entry : {}) as Record<string, unknown>;
    const url = typeof value.url === 'string' ? normaliseUrl(value.url) : '';
    if (!url || validateUrl(url)) {
      changed = true;
      continue;
    }
    let id = typeof value.id === 'string' ? value.id.trim() : '';
    if (!id || ids.has(id)) id = newId();
    ids.add(id);
    const name = typeof value.name === 'string' && value.name.trim() ? value.name.trim() : hostOf(url);
    const project = typeof value.project === 'string' && value.project.trim() ? value.project.trim() : undefined;
    const core: CoreSetting = { id, name, url, ...(project ? { project } : {}) };
    if (id !== value.id || name !== value.name || url !== value.url || project !== value.project) changed = true;
    cores.push(core);
  }
  return { cores, changed };
}

/** What the settings of an earlier version held, as the migration reads them. */
export interface LegacySettings {
  /** The `threavia.cores` value set in the user settings, or undefined when never set. */
  cores: unknown;
  coreUrl: string;
  project: string;
}

/**
 * The one Core of an earlier version, as the first entry of `threavia.cores`.
 *
 * It happens once: as soon as `threavia.cores` is set at all, even to an empty
 * list after the last Core was removed, the old keys are no longer read, so a
 * removed Core does not come back from a setting the person forgot about.
 */
export function migrateLegacy(legacy: LegacySettings, newId: () => string = newCoreId): CoreSetting | undefined {
  if (legacy.cores !== undefined) return undefined;
  const url = normaliseUrl(legacy.coreUrl);
  if (!url || validateUrl(url)) return undefined;
  const project = legacy.project.trim();
  return { id: newId(), name: hostOf(url), url, ...(project ? { project } : {}) };
}

/** The keys an earlier version kept per Core URL, and where they live now. */
export function legacyKeys(url: string, coreId: string): { from: string; to: string }[] {
  return [
    { from: `threavia.credential:${url}`, to: credentialKey(coreId) },
    { from: `threavia.cursor:${url}`, to: cursorKey(coreId) },
  ];
}

/** Where a Core's sign-in is kept in SecretStorage. */
export function credentialKey(coreId: string): string {
  return `threavia.credential:${coreId}`;
}

/** Where a Core's stream cursor is kept: sequences are per Core, so are cursors. */
export function cursorKey(coreId: string): string {
  return `threavia.cursor:${coreId}`;
}
