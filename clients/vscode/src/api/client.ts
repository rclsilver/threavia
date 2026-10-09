import type {
  Attention,
  AuthPublic,
  BackendInstance,
  Decision,
  DecisionImportance,
  Delivery,
  Event,
  FileDiff,
  Job,
  KnownDirectory,
  List,
  Me,
  Project,
  Repository,
  Run,
  Session,
  Snapshot,
  Task,
  TaskStatus,
  UserInputRequest,
  ValidationRequest,
} from './types';

/** The body of POST /projects/{id}/tasks, as the web client's form sends it. */
export interface NewTaskBody {
  title: string;
  description: string;
  dependsOn: string[];
}

/**
 * The body of PATCH /tasks/{id}: only what changes. Core reads an empty string
 * as "unchanged", so a field left out and a field emptied mean the same.
 */
export type TaskPatch = { title?: string; description?: string; status?: TaskStatus };

/** The body of POST /projects/{id}/decisions; `supersedes` names the one it replaces. */
export interface NewDecisionBody {
  title: string;
  content: string;
  importance: DecisionImportance;
  supersedes?: string;
}

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

  async request<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<T> {
    const response = await this.raw(path, {
      method,
      headers: body === undefined ? headers : { 'Content-Type': 'application/json', ...headers },
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

  post<T>(path: string, body: unknown = {}, headers?: Record<string, string>) {
    return this.request<T>('POST', path, body, headers);
  }

  patch<T>(path: string, body: unknown) {
    return this.request<T>('PATCH', path, body);
  }

  delete<T>(path: string) {
    return this.request<T>('DELETE', path);
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

  /** History before `before`, oldest first; a page shorter than `limit` is the start. */
  earlierEvents(sessionId: string, before: number, limit = EARLIER_PAGE) {
    return this.get<List<Event>>(`/api/v1/sessions/${sessionId}/events?before=${before}&limit=${limit}`).then(items);
  }

  /** The diff of one file a workspace.changed event listed, asked of the backend. */
  diff(sessionId: string, sequence: number, path: string) {
    return this.get<FileDiff>(
      `/api/v1/sessions/${sessionId}/diff?event=${sequence}&path=${encodeURIComponent(path)}`,
    );
  }

  backends() {
    return this.get<List<BackendInstance>>('/api/v1/backends').then(items);
  }

  directories(projectId: string) {
    return this.get<List<KnownDirectory>>(`/api/v1/projects/${projectId}/directories`).then(items);
  }

  /**
   * The first send: Session, Run, Job and message in one transaction. The key
   * makes a retry after a lost answer start nothing twice.
   */
  startSession(input: { projectId: string; backendInstanceId: string; workingDirectoryId: string | null; message: string }) {
    return this.post<{ session: Session; run: Run; job: Job }>('/api/v1/sessions/start', input, idempotencyKey());
  }

  /** A message to a Session. Only a queued one carries a key: Core applies it only to QUEUE. */
  postMessage(sessionId: string, message: string, delivery: Delivery = 'QUEUE') {
    return this.post<Job>(
      `/api/v1/sessions/${sessionId}/messages`,
      delivery === 'QUEUE' ? { message } : { message, delivery },
      delivery === 'QUEUE' ? idempotencyKey() : undefined,
    );
  }

  cancelJob(jobId: string) {
    return this.post<Job>(`/api/v1/jobs/${jobId}/cancel`, { reason: 'cancelled from VS Code' });
  }

  // ------------------------------------------------------- project memory

  /** A Project's Tasks; finished ones too when asked, as the Done group needs them. */
  tasks(projectId: string, includeDone: boolean) {
    return this.get<List<Task>>(`/api/v1/projects/${projectId}/tasks?includeDone=${includeDone}`).then(items);
  }

  /**
   * The Tasks that can start now. Core derives readiness from the graph and
   * never stores it, so asking is the only way to know.
   */
  readyTasks(projectId: string) {
    return this.get<List<Task>>(`/api/v1/projects/${projectId}/tasks/ready`).then(items);
  }

  createTask(projectId: string, body: NewTaskBody) {
    return this.post<Task>(`/api/v1/projects/${projectId}/tasks`, body);
  }

  updateTask(taskId: string, patch: TaskPatch) {
    return this.patch<Task>(`/api/v1/tasks/${taskId}`, patch);
  }

  deleteTask(taskId: string) {
    return this.delete<void>(`/api/v1/tasks/${taskId}`);
  }

  addTaskDependency(taskId: string, dependsOn: string) {
    return this.post<Task>(`/api/v1/tasks/${taskId}/dependencies`, { dependsOn });
  }

  removeTaskDependency(taskId: string, dependsOn: string) {
    return this.delete<Task>(`/api/v1/tasks/${taskId}/dependencies/${dependsOn}`);
  }

  /**
   * A Project's Decisions. Core lists the current ones unless asked for the
   * superseded too, which the Memory view folds away at the end.
   */
  decisions(projectId: string, includeSuperseded: boolean) {
    return this.get<List<Decision>>(
      `/api/v1/projects/${projectId}/decisions${includeSuperseded ? '?includeSuperseded=true' : ''}`,
    ).then(items);
  }

  createDecision(projectId: string, body: NewDecisionBody) {
    return this.post<Decision>(`/api/v1/projects/${projectId}/decisions`, body);
  }

  /** Pins a Decision to every Job, or unpins it: the only thing about one that changes. */
  setDecisionImportance(decisionId: string, importance: DecisionImportance) {
    return this.patch<Decision>(`/api/v1/decisions/${decisionId}`, { importance });
  }

  deleteDecision(decisionId: string) {
    return this.delete<void>(`/api/v1/decisions/${decisionId}`);
  }

  /** The bytes of an Artifact, read with the client's credential. */
  async artifactContent(artifactId: string): Promise<Uint8Array> {
    const response = await this.raw(`/api/v1/artifacts/${artifactId}/content`);
    if (!response.ok) {
      const body = (await response.json().catch(() => null)) as ErrorBody | null;
      throw new ApiError(
        response.status,
        body?.error?.code ?? 'unknown',
        body?.error?.message ?? `The file could not be read (${response.status})`,
      );
    }
    return new Uint8Array(await response.arrayBuffer());
  }
}

/** How many earlier events one page brings, as the web client asks. */
export const EARLIER_PAGE = 200;

function idempotencyKey(): Record<string, string> {
  return { 'Idempotency-Key': crypto.randomUUID() };
}

const items = <T>(list: List<T>) => list.items ?? [];
