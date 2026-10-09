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

/** The envelope every collection response uses. */
export interface List<T> {
  items: T[];
}

/**
 * The payloads the extension reads.
 *
 * The contract types `payload` as an open object, because its shape depends on
 * the event type and a backend may add one without a Core release. These are
 * the ones documented in the `Event.payload` description of the contract.
 */
export interface EventPayloads {
  'job.completed': { summary?: string };
  'job.failed': { error?: string };
  'validation.resolved': { validationId: string; approved: boolean; note?: string; title?: string };
  'user_input.resolved': { requestId: string; value: string };
  'session.pinned': { pinned: boolean };
}

/** Reads a payload as the shape its event type implies. */
export function payloadOf<K extends keyof EventPayloads>(
  event: Event,
  type: K,
): EventPayloads[K] | undefined {
  return event.type === type ? (event.payload as EventPayloads[K]) : undefined;
}
