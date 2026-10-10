// The names the extension uses, bound to the generated contract.
//
// Nothing here is hand-written data: every type resolves to a schema in
// api/openapi.yaml, generated the same way as the web client's, so renaming a
// field in Go breaks this build rather than the sidebar.
import type { components } from './schema';

type Schemas = components['schemas'];

export type Project = Schemas['Project'];
export type Session = Schemas['Session'];
export type Job = Schemas['Job'];
export type JobStatus = Schemas['JobStatus'];
export type Event = Schemas['Event'];
export type Repository = Schemas['Repository'];
export type Snapshot = Schemas['Snapshot'];
export type Attention = Schemas['Attention'];
export type ValidationRequest = Schemas['ValidationRequest'];
export type UserInputRequest = Schemas['UserInputRequest'];
export type AuthPublic = Schemas['AuthPublic'];
export type Me = Schemas['Me'];
export type FileDiff = Schemas['FileDiff'];
export type Run = Schemas['Run'];
export type BackendInstance = Schemas['BackendInstance'];
export type BackendQuotas = Schemas['BackendQuotas'];
export type BackendQuota = Schemas['BackendQuota'];
export type KnownDirectory = Schemas['KnownDirectory'];
export type Usage = Schemas['Usage'];
export type Task = Schemas['Task'];
export type TaskStatus = Schemas['TaskStatus'];
export type Decision = Schemas['Decision'];
export type DecisionImportance = Schemas['DecisionImportance'];

/** The envelope every collection response uses. */
export interface List<T> {
  items: T[];
}

/** How a message reaches a Session already working (spec section 3.5). */
export type Delivery = 'QUEUE' | 'NEXT' | 'NOW';

/** One file a Job changed, as workspace.changed lists it. */
export interface ChangedFile {
  path: string;
  state: 'ADDED' | 'MODIFIED' | 'DELETED' | 'RENAMED';
}

/**
 * The payloads the extension reads.
 *
 * The contract types `payload` as an open object, because its shape depends on
 * the event type and a backend may add one without a Core release. These are
 * the ones documented in the `Event.payload` description of the contract, the
 * same as the web client's (web/ui/src/api/types.ts).
 */
export interface EventPayloads {
  'backend.quotas_updated': { backendInstanceId: string };
  'project.updated': { projectId: string; name: string };
  'project.deleted': { projectId: string };
  'session.created': { title: string; managerSessionId?: string };
  'user.message': { text: string; delivery?: 'NOW' | 'NEXT'; scheduleId?: string; actorJobId?: string };
  'schedule.skipped': { scheduleId: string; outcome: string; reason: string; due: string };
  'agent.message': { text: string };
  'tool.started': { toolCallId: string; name: string; input?: Record<string, unknown> };
  // The backend keeps a bounded excerpt of the output and marks it with an
  // ellipsis; the whole thing stays on the machine that produced it.
  'tool.completed': { toolCallId: string; name: string; output?: { output?: string } };
  'tool.failed': { toolCallId: string; name: string; error?: string };
  'job.completed': { summary?: string; usage?: Usage };
  'job.failed': { error?: string; usage?: Usage };
  'validation.resolved': { validationId: string; approved: boolean; note?: string; title?: string; actorJobId?: string };
  'user_input.resolved': { requestId: string; value: string; actorJobId?: string };
  'session.pinned': { pinned: boolean };
  'artifact.created': { artifactId: string; filename: string; mimeType: string; size: number; title?: string };
  'workspace.changed': {
    knownDirectoryId?: string;
    files?: ChangedFile[];
    additions: number;
    deletions: number;
    // Present when the backend can be asked for the diff of a file.
    directory?: string;
    baseTree?: string;
    headTree?: string;
  };
}

/** Reads a payload as the shape its event type implies. */
export function payloadOf<K extends keyof EventPayloads>(
  event: Event,
  type: K,
): EventPayloads[K] | undefined {
  return event.type === type ? (event.payload as EventPayloads[K]) : undefined;
}
