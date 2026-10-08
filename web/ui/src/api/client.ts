import { clientId, deviceName } from '@/lib/device';

/**
 * The HTTP client.
 *
 * Everything goes through here so three things are true everywhere: the calling
 * channel is declared, an error carries the message Core wrote rather than a
 * status code, and a 204 is not mistaken for an empty body.
 */

/**
 * Which kind of client this is (spec section 6). Core records it on the work it
 * creates, so a device already following that work is not also made to ring.
 */
export const CHANNEL = 'web';

/** An error the server described, carrying the code it chose. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
  }

  /** True when the server refused because the state contradicts the request. */
  get isConflict() {
    return this.status === 409;
  }

  /** True when the capability is missing from the deployment, not the request. */
  get isUnavailable() {
    return this.status === 503;
  }
}

interface ErrorBody {
  error?: { code?: string; message?: string };
}

/**
 * The token every request carries, when the deployment asks for one.
 *
 * Held here rather than passed through every call site: the token belongs to
 * the client, not to the question being asked, and a call that forgot it would
 * come back 401 for a reason nobody could see at the call site.
 */
let bearer: string | null = null;

export function setBearer(token: string | null) {
  bearer = token;
}

/** Adds the credential, when there is one, to a request going to Core. */
export function authorize(headers: Headers): Headers {
  headers.set('X-Threavia-Channel', CHANNEL);
  // Which device, beyond which kind: the desktop and the phone are both web.
  // The name is URI-encoded, a header being Latin-1 and a name not.
  headers.set('X-Threavia-Client', clientId);
  headers.set('X-Threavia-Client-Name', encodeURIComponent(deviceName()));
  if (bearer) headers.set('Authorization', `Bearer ${bearer}`);
  return headers;
}

/**
 * Somewhere else to send requests: the demo answers them in the browser, and
 * nothing reaches a server. Unset, every request goes to Core.
 */
export interface Transport {
  request: (method: string, path: string, body: unknown) => Promise<unknown>;
  upload: (path: string, file: File) => Promise<unknown>;
  href: (path: string) => string;
  /** Told after every change, so the views reread what changed. */
  changed: () => void;
}

let transport: Transport | null = null;

export function setTransport(next: Transport) {
  transport = next;
}

/** The address of something to download, wherever requests go. */
export function hrefFor(path: string): string {
  return transport ? transport.href(path) : path;
}

async function request<T>(method: string, path: string, init: RequestInit = {}): Promise<T> {
  if (transport) {
    const body = typeof init.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined;
    const result = (await transport.request(method, path, body)) as T;
    if (method !== 'GET') transport.changed();
    return result;
  }
  const headers = authorize(new Headers(init.headers));

  const response = await fetch(path, { ...init, method, headers });

  if (response.status === 204) {
    return undefined as T;
  }

  const text = await response.text();
  const payload = text ? (JSON.parse(text) as unknown) : null;

  if (!response.ok) {
    const body = payload as ErrorBody | null;
    throw new ApiError(
      response.status,
      body?.error?.code ?? 'unknown',
      body?.error?.message ?? `${method} ${path} failed (${response.status})`,
    );
  }
  return payload as T;
}

function json<T>(method: string, path: string, body?: unknown, extra?: HeadersInit): Promise<T> {
  return request<T>(method, path, {
    headers: { 'Content-Type': 'application/json', ...(extra ?? {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown, headers?: HeadersInit) =>
    json<T>('POST', path, body ?? {}, headers),
  patch: <T>(path: string, body: unknown) => json<T>('PATCH', path, body),
  put: <T>(path: string, body: unknown) => json<T>('PUT', path, body),
  delete: <T = void>(path: string) => request<T>('DELETE', path),

  /** Uploads a file. An artifact and a skill bundle travel as bytes, not as a field. */
  upload: <T>(path: string, file: File, fields: Record<string, string> = {}) => {
    if (transport) {
      const done = transport.upload(path, file) as Promise<T>;
      return done.then((result) => {
        transport?.changed();
        return result;
      });
    }
    const form = new FormData();
    form.append('file', file);
    for (const [key, value] of Object.entries(fields)) {
      if (value) form.append(key, value);
    }
    return request<T>('POST', path, { body: form });
  },
};

/** A fresh key for a command a client may retry (spec section 27). */
export function idempotencyKey(): HeadersInit {
  return { 'Idempotency-Key': crypto.randomUUID() };
}
