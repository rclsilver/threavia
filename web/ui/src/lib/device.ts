/**
 * Who this client instance is: a stable id, and the name its person gives the
 * device.
 *
 * Two clients of the same kind — a desktop tab and the phone — are both "web"
 * to Core. The id is what tells them apart, so a phone can ring while the
 * desktop sits unattended. A browser cannot read its machine's hostname, so
 * the name starts as a guess from the browser and the person can put the
 * hostname there themselves.
 */

const ID_KEY = 'threavia.client.id';
const NAME_KEY = 'threavia.client.name';

function stored(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Without storage the id lasts this visit, which is still one device.
  }
}

let id = stored(ID_KEY);
if (!id) {
  id = crypto.randomUUID();
  store(ID_KEY, id);
}

/** This client instance, for as long as the browser keeps its storage. */
export const clientId: string = id;

/** A name a person recognises in a list of their devices. */
export function guessedDeviceName(): string {
  const agent = navigator.userAgent;
  const system = /Android/.test(agent)
    ? 'Android'
    : /iPhone|iPad/.test(agent)
      ? 'iOS'
      : /Mac OS X/.test(agent)
        ? 'macOS'
        : /Windows/.test(agent)
          ? 'Windows'
          : /Linux/.test(agent)
            ? 'Linux'
            : 'Unknown system';
  const browser = /Firefox\//.test(agent)
    ? 'Firefox'
    : /Edg\//.test(agent)
      ? 'Edge'
      : /Chrome\//.test(agent)
        ? 'Chrome'
        : /Safari\//.test(agent)
          ? 'Safari'
          : 'browser';
  const installed = window.matchMedia('(display-mode: standalone)').matches ? ', installed' : '';
  return `${system} — ${browser}${installed}`;
}

/** What this device is called, as the person named it or as guessed. */
export function deviceName(): string {
  return stored(NAME_KEY) || guessedDeviceName();
}

export function setDeviceName(name: string) {
  store(NAME_KEY, name.trim());
}
