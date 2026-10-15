import type { AuditEntry } from '@/api/types';

/** Names come from history: revocation removes the backend from the live list. */
export function backendAudit(entry: AuditEntry): { title: string; detail?: string; tone: 'accent' | 'danger' | 'muted' } | undefined {
  const detail = entry.detail ?? {};
  const text = (value: unknown) => typeof value === 'string' ? value : '';
  const name = `“${text(detail.name) || 'an unnamed backend'}”`;
  switch (entry.action) {
    case 'backend.registered': return { title: `Registered backend ${name}`, tone: 'accent',
      detail: detail.registrationFlow === 'shared_key' ? 'Registered with a shared key; history attached when claimed.' : 'Registered with a one-time token.' };
    case 'backend.claimed': return { title: `Claimed backend ${name}`, tone: 'accent' };
    case 'backend.revoked': return { title: `Revoked backend ${name}`, tone: 'danger', detail: 'Its credential no longer works. It must register again.' };
    case 'backend.availability_changed': return { title: `Backend ${name} is ${text(detail.to).toLowerCase()}`, tone: detail.to === 'READY' ? 'accent' : 'muted',
      detail: `${text(detail.from).toLowerCase()} → ${text(detail.to).toLowerCase()}. Provider authentication: ${text(detail.providerAuthState).toLowerCase().replaceAll('_', ' ')}.` };
    default: return undefined;
  }
}
