import type { components } from '@/api/schema';

export type ControlScope = components['schemas']['ControlAccess']['scopes'][number];

/** The rights an MCP access can hold, as Settings and the OAuth consent page name them. */
export const CONTROL_SCOPES: { value: ControlScope; label: string }[] = [
  { value: 'read', label: 'Read projects, sessions, events and configuration' },
  { value: 'manage', label: 'Create and change resources; send messages and cancel work' },
  { value: 'delete', label: 'Delete projects, sessions, memory, tasks and files, and revoke backends' },
  { value: 'answer', label: 'Answer agents’ questions' },
  { value: 'approve', label: 'Accept or refuse permissions on my behalf' },
];
