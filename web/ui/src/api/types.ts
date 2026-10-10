// The names the application uses, bound to the generated contract.
//
// Nothing here is hand-written data: every type resolves to a schema in
// api/openapi.yaml, so renaming a field in Go breaks this build rather than a
// user's screen.
import type { components } from './schema';

type Schemas = components['schemas'];

export type Project = Schemas['Project'];
export type Session = Schemas['Session'];
export type Run = Schemas['Run'];
export type Job = Schemas['Job'];
export type JobStatus = Schemas['JobStatus'];
export type Event = Schemas['Event'];
export type FileDiff = Schemas['FileDiff'];
export type Repository = Schemas['Repository'];
export type Schedule = Schemas['Schedule'];
export type PushConfig = Schemas['PushConfig'];
export type Snapshot = Schemas['Snapshot'];
export type Attention = Schemas['Attention'];
export type ValidationRequest = Schemas['ValidationRequest'];
export type UserInputRequest = Schemas['UserInputRequest'];
export type ExecutionPolicy = Schemas['ExecutionPolicy'];
export type ExecutionMode = Schemas['ExecutionMode'];
export type PermissionRule = Schemas['PermissionRule'];
export type PermissionEffect = Schemas['PermissionEffect'];
export type PermissionCapability = Schemas['PermissionCapability'];
export type SessionPolicy = Schemas['SessionPolicy'];
export type KnownDirectory = Schemas['KnownDirectory'];
export type KnownDirectoryBinding = Schemas['KnownDirectoryBinding'];
export type BackendInstance = Schemas['BackendInstance'];
export type BackendQuotas = Schemas['BackendQuotas'];
export type Condition = Schemas['Condition'];
export type Handoff = Schemas['Handoff'];
export type Task = Schemas['Task'];
export type TaskStatus = Schemas['TaskStatus'];
export type Decision = Schemas['Decision'];
export type Artifact = Schemas['Artifact'];
export type Skill = Schemas['Skill'];
export type SkillSource = Schemas['SkillSource'];
export type SkillSourceType = Schemas['SkillSourceType'];
export type SkillFile = Schemas['SkillFile'];
export type BackendSkill = Schemas['BackendSkill'];
export type AuditEntry = Schemas['AuditEntry'];
export type Usage = Schemas['Usage'];
export type Me = Schemas['Me'];

/** The envelope every collection response uses. */
export interface List<T> {
  items: T[];
}

/**
 * The payloads this client renders.
 *
 * The contract types `payload` as an open object, because its shape depends on
 * the event type and a backend may add one without a Core release. These are
 * the ones the timeline knows how to draw; anything else falls back to a plain
 * line.
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
  'artifact.created': { artifactId: string; filename: string; mimeType: string; size: number; title?: string };
  'workspace.changed': {
    knownDirectoryId?: string;
    files?: { path: string; state: 'ADDED' | 'MODIFIED' | 'DELETED' | 'RENAMED' }[];
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
