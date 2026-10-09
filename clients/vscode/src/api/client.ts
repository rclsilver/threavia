import type {
  Attention,
  AuthPublic,
  List,
  Me,
  Project,
  Repository,
  Session,
  Snapshot,
  UserInputRequest,
  ValidationRequest,
} from './types';

/**
 * The HTTP client.
 *
 * Everything goes through here so three things are true everywhere: the
 * extension says which client it is, an error carries the message Core wrote
 * rather than a status code, and a 204 is not mistaken for an empty body.
 *
 * Nothing in this file imports `vscode`: the editor supplies where Core is, the
 * credential and the identity, so the same client runs in a test or a script.
 */

/**
 * Which kind of client this is (spec section 6). Core records it on the work
 * the extension starts, so a device already following that work is not also
 * made to ring.
 */
export const CHANNEL = 'vscode';

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

  /** True when Core does not know who is asking, or no longer believes them. */
  get isUnauthorized() {
    return this.status === 401;
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

/** Who this client instance is, as every request declares it. */
export interface Identity {
  /** Stable for the installation, so Core counts one device however many windows. */
  clientId: string;
  /** What the person sees in their list of devices. */
  clientName: () => string;
}

export interface ClientOptions {
  /** Where Core answers, without a trailing slash. Read on every request. */
  baseUrl: () => string;
  identity: Identity;
  /** The Authorization header for the current credential, or nothing. */
  authorization: () => Promise<string | undefined>;
  /**
   * Told when Core answers 401, once per request. Returning true means the
   * credential was renewed and the request is worth one more try.
   */
  onUnauthorized?: () => Promise<boolean>;
  fetch?: typeof fetch;
}

export class CoreClient {
  private readonly options: ClientOptions;
  private readonly fetcher: typeof fetch;

  constructor(options: ClientOptions) {
    this.options = options;
    this.fetcher = options.fetch ?? ((input, init) => fetch(input, init));
  }

  get baseUrl(): string {
    return this.options.baseUrl();
  }

  /** The headers that say who is asking, shared with the event stream. */
  async headers(extra?: Record<string, string>): Promise<Headers> {
    const headers = new Headers(extra);
    headers.set('X-Threavia-Channel', CHANNEL);
    headers.set('X-Threavia-Client', this.options.identity.clientId);
    // URI-encoded, a header being Latin-1 and a device name not: the default
    // carries an em dash.
    headers.set('X-Threavia-Client-Name', encodeURIComponent(this.options.identity.clientName()));
    const authorization = await this.options.authorization();
    if (authorization) headers.set('Authorization', authorization);
    return headers;
  }

  /** Opens a request with the client's headers, retrying once after a renewal. */
  async raw(path: string, init: RequestInit & { headers?: Record<string, string> } = {}): Promise<Response> {
    const send = async () =>
      this.fetcher(`${this.baseUrl}${path}`, { ...init, headers: await this.headers(init.headers) });
    let response = await send();
    if (response.status === 401 && this.options.onUnauthorized && (await this.options.onUnauthorized())) {
      response = await send();
    }
    return response;
  }

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const response = await this.raw(path, {
      method,
      headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    });

    if (response.status === 204) return undefined as T;

    const text = await response.text();
    let payload: unknown = null;
    try {
      payload = text ? (JSON.parse(text) as unknown) : null;
    } catch {
      // A proxy in front of Core answers in HTML when it fails; the status is
      // then all there is to say.
    }

    if (!response.ok) {
      const error = payload as ErrorBody | null;
      throw new ApiError(
        response.status,
        error?.error?.code ?? 'unknown',
        error?.error?.message ?? `${method} ${path} failed (${response.status})`,
      );
    }
    return payload as T;
  }

  get<T>(path: string) {
    return this.request<T>('GET', path);
  }

  post<T>(path: string, body: unknown = {}) {
    return this.request<T>('POST', path, body);
  }

  patch<T>(path: string, body: unknown) {
    return this.request<T>('PATCH', path, body);
  }

  // ------------------------------------------------------------- the routes

  /**
   * How this deployment authenticates. Unauthenticated by necessity: this is
   * how a client learns there is anything to sign in to.
   */
  async authConfig(): Promise<AuthPublic> {
    const response = await this.fetcher(`${this.baseUrl}/api/v1/auth`);
    if (!response.ok) throw new ApiError(response.status, 'unknown', `Core did not answer (${response.status})`);
    return (await response.json()) as AuthPublic;
  }

  me() {
    return this.get<Me>('/api/v1/me');
  }

  projects() {
    return this.get<List<Project>>('/api/v1/projects').then(items);
  }

  sessions(projectId: string) {
    return this.get<List<Session>>(`/api/v1/projects/${projectId}/sessions?includeArchived=false`).then(items);
  }

  pinnedSessions() {
    return this.get<List<Session>>('/api/v1/me/pinned-sessions').then(items);
  }

  /** The snapshot a Session opens with; `history` bounds the events it carries. */
  snapshot(sessionId: string, history = 200) {
    return this.get<Snapshot>(`/api/v1/sessions/${sessionId}?history=${history}`);
  }

  async attention(): Promise<{ validations: ValidationRequest[]; userInputs: UserInputRequest[] }> {
    const attention = await this.get<Attention>('/api/v1/me/attention');
    return { validations: attention.validations ?? [], userInputs: attention.userInputs ?? [] };
  }

  /** Where the Session's repository stands; `fetch` asks origin first. */
  repository(sessionId: string, fetch = false) {
    return this.get<Repository>(`/api/v1/sessions/${sessionId}/repository${fetch ? '?fetch=true' : ''}`);
  }

  renameSession(sessionId: string, title: string) {
    return this.patch<Session>(`/api/v1/sessions/${sessionId}`, { title });
  }

  pinSession(sessionId: string, pinned: boolean) {
    return this.patch<Session>(`/api/v1/sessions/${sessionId}`, { pinned });
  }

  archiveSession(sessionId: string) {
    return this.post<Session>(`/api/v1/sessions/${sessionId}/archive`);
  }

  restoreSession(sessionId: string) {
    return this.post<Session>(`/api/v1/sessions/${sessionId}/restore`);
  }

  resolveValidation(id: string, approved: boolean, note = '') {
    return this.post<ValidationRequest>(`/api/v1/validations/${id}/resolve`, { approved, note });
  }

  resolveUserInput(id: string, value: string) {
    return this.post<UserInputRequest>(`/api/v1/user-input/${id}/resolve`, { value });
  }

  presence(active: boolean) {
    return this.post<void>('/api/v1/me/presence', { active });
  }
}

const items = <T>(list: List<T>) => list.items ?? [];
