import type { ProjectPatch } from '../api/client';
import type { Project } from '../api/types';

export interface ProjectFields {
  name: string;
  description: string;
  instructions: string;
}

/** Webview messages are untrusted; accept only the form's string fields. */
export function readProjectFields(message: unknown): ProjectFields | undefined {
  if (!message || typeof message !== 'object') return undefined;
  const value = message as Record<string, unknown>;
  if (value.type !== 'save' || typeof value.name !== 'string' ||
      typeof value.description !== 'string' || typeof value.instructions !== 'string') return undefined;
  return { name: value.name.trim(), description: value.description.trim(), instructions: value.instructions.trim() };
}

/** Send only changes, preserving other clients' edits to untouched fields. */
export function projectPatch(project: Project, fields: ProjectFields): ProjectPatch {
  const patch: ProjectPatch = {};
  if (fields.name !== project.name) patch.name = fields.name;
  for (const field of ['description', 'instructions'] as const) {
    if (fields[field] === (project[field] ?? '')) continue;
    // Core keeps an empty field unchanged, but trims non-empty fields before
    // storing them. One space is how this contract permits clearing a field.
    patch[field] = fields[field] || ' ';
  }
  return patch;
}
