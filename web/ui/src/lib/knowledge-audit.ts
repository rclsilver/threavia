import type { AuditEntry } from '@/api/types';

export interface KnowledgeAudit {
  kind: 'PROJECT' | 'TASK' | 'MEMORY';
  tone: 'accent' | 'danger' | 'muted';
  title: string;
  detail?: string;
}

/** Compact metadata, readable even after the subject has been deleted. */
export function knowledgeAudit(entry: AuditEntry): KnowledgeAudit | undefined {
  const detail = entry.detail ?? {};
  const text = (value: unknown) => typeof value === 'string' ? value : '';
  const fields = Array.isArray(detail.changedFields) ? detail.changedFields.filter((field): field is string => typeof field === 'string') : [];
  const name = `“${text(detail.name) || text(detail.title) || 'Untitled'}”`;
  const edited = (values: string[]) => values.length ? `Changed ${values.map((field) => field === 'dependsOn' ? 'dependencies' : field).join(', ')}.` : undefined;
  const status = (value: unknown) => text(value).toLowerCase().replaceAll('_', ' ');

  switch (entry.action) {
    case 'project.created': return { kind: 'PROJECT', tone: 'accent', title: `Created project ${name}` };
    case 'project.archived': return { kind: 'PROJECT', tone: 'muted', title: `Archived project ${name}` };
    case 'project.restored': return { kind: 'PROJECT', tone: 'accent', title: `Restored project ${name}` };
    case 'project.deleted': return { kind: 'PROJECT', tone: 'danger', title: `Deleted project ${name}`, detail: 'Its sessions and project knowledge were removed.' };
    case 'project.updated': return { kind: 'PROJECT', tone: 'accent',
      title: detail.previousName ? `Renamed project “${text(detail.previousName)}” to ${name}` : `Updated project ${name}`,
      detail: edited(fields.filter((field) => field !== 'name')) };
    case 'task.created': return { kind: 'TASK', tone: 'accent', title: `Created task ${name}`,
      detail: Array.isArray(detail.dependsOn) && detail.dependsOn.length ? `Depends on ${detail.dependsOn.length} other ${detail.dependsOn.length === 1 ? 'task' : 'tasks'}.` : undefined };
    case 'task.deleted': return { kind: 'TASK', tone: 'danger', title: `Deleted task ${name}` };
    case 'task.updated': {
      let description = edited(fields);
      if (detail.previousStatus) description = `Status: ${status(detail.previousStatus)} → ${status(detail.status)}.${fields.some((field) => field !== 'status') ? ' ' + edited(fields.filter((field) => field !== 'status')) : ''}`;
      if (detail.dependencyAction) description = `${detail.dependencyAction === 'removed' ? 'No longer waits on' : 'Now waits on'} “${text(detail.dependencyTitle) || 'another task'}”.`;
      return { kind: 'TASK', tone: 'accent', title: `Updated task ${name}`, detail: description };
    }
    case 'decision.created': return { kind: 'MEMORY', tone: 'accent',
      title: detail.previousProjectId ? `Made memory ${name} global` : `Recorded memory ${name}`,
      detail: detail.previousProjectId ? 'Shared across your projects; the earlier project fact remains in history.' : detail.supersedes ? 'Replaces an earlier fact.' : undefined };
    case 'decision.superseded': return { kind: 'MEMORY', tone: 'muted', title: `Moved memory ${name} to history`,
      detail: detail.previousProjectId ? 'An active global fact now replaces it.' : 'A newer fact now replaces it.' };
    case 'decision.deleted': return { kind: 'MEMORY', tone: 'danger', title: `Deleted memory ${name}` };
    case 'decision.updated': return detail.restored
      ? { kind: 'MEMORY', tone: 'accent', title: `Restored memory ${name}`, detail: 'Its replacement was deleted; this earlier fact is active again.' }
      : { kind: 'MEMORY', tone: 'accent', title: `${detail.importance === 'IMPORTANT' ? 'Pinned' : 'Unpinned'} memory ${name}`,
        detail: detail.importance === 'IMPORTANT' ? 'Included in every new Job in its memory scope.' : 'Still searchable; no longer included in every new Job.' };
    default: return undefined;
  }
}
