import { useParams } from '@tanstack/react-router';
import { Download, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';

import {
  useArtifacts,
  useAudit,
  useCreateDecision,
  useDecisions,
  useDeleteArtifact,
  useDeleteDecision,
  useInstallSkill,
  useProject,
  useProjectPolicy,
  useSkills,
  useUploadArtifact,
  useSetProjectPolicy,
  useUninstallSkill,
  useUpdateProject,
} from '@/api/queries';
import type { Decision, ExecutionPolicy, SkillSourceType } from '@/api/types';
import { PolicyForm, RulesEditor } from '@/components/policy-form';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, EmptyState } from '@/components/ui/card';
import { Input, Textarea } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { bytes, humanise, when } from '@/lib/utils';

/**
 * One part of what a Project carries beyond its Sessions.
 *
 * Each is a route of its own rather than a tab: the sidebar asks for them from
 * anywhere, and a tab only opens when the view around it is already on screen.
 */
function Section({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle: string;
  children: React.ReactNode;
}) {
  const { projectId } = useParams({ strict: false });
  const project = useProject(projectId);

  return (
    // Lists, not prose, so the column is wider than a conversation's — but still
    // a column: a record and the buttons that act on it should not end up at
    // opposite ends of a wide screen.
    <div className="mx-auto flex min-h-0 w-full max-w-page flex-1 flex-col gap-4 overflow-y-auto p-6">
      <header>
        <h2 className="text-lg font-semibold">{title}</h2>
        <p className="text-muted text-sm">
          {subtitle}
          {project.data?.name && <span className="opacity-70"> · {project.data.name}</span>}
        </p>
      </header>
      {children}
    </div>
  );
}

/** The projectId of the route, which every section below is scoped to. */
function useProjectId(): string {
  return useParams({ strict: false }).projectId ?? '';
}

export function MemoryView() {
  const projectId = useProjectId();
  return (
    <Section
      title="Memory"
      subtitle="What was decided, and why. Important decisions travel with every Job."
    >
      <DecisionsPane projectId={projectId} />
    </Section>
  );
}

export function ArtifactsView() {
  const projectId = useProjectId();
  return (
    <Section title="Artifacts" subtitle="Files produced by the work, kept with the Project.">
      <ArtifactsPane projectId={projectId} />
    </Section>
  );
}

export function SkillsView() {
  const projectId = useProjectId();
  return (
    <Section title="Skills" subtitle="What an agent may use here, and where it came from.">
      <SkillsPane projectId={projectId} />
    </Section>
  );
}

export function InstructionsView() {
  const projectId = useProjectId();
  return (
    <Section title="Instructions" subtitle="The standing rules every Job of this Project reads.">
      <InstructionsPane projectId={projectId} />
    </Section>
  );
}

export function AuditView() {
  return (
    <Section title="Audit" subtitle="Who decided what, through which client.">
      <AuditPane />
    </Section>
  );
}

/**
 * What every Session of this Project may do, unless it says otherwise.
 *
 * This is where a rule gets written once. The switches are defaults a Session
 * can loosen for one piece of work; the refusals are not — they travel into
 * every Session and are answered first, so none can lift one. That difference
 * is the only reason to have a level above the Session at all.
 */
export function PermissionsView() {
  const projectId = useProjectId();
  return (
    <Section
      title="Permissions"
      subtitle="What every Session of this Project may do, unless it says otherwise."
    >
      <PermissionsPane projectId={projectId} />
    </Section>
  );
}

// ----------------------------------------------------------------- decisions

function DecisionsPane({ projectId }: { projectId: string }) {
  const decisions = useDecisions(projectId);
  const create = useCreateDecision(projectId);
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [importance, setImportance] = useState('NORMAL');

  return (
    <div className="space-y-3">
      <form
        className="space-y-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (!title.trim()) return;
          create.mutate({ title: title.trim(), content: content.trim(), importance });
          setTitle('');
          setContent('');
        }}
      >
        <div className="flex gap-2">
          <Input
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="What was decided"
          />
          <Select value={importance} onValueChange={setImportance}>
            <SelectTrigger className="w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="NORMAL">normal</SelectItem>
              <SelectItem value="IMPORTANT">important</SelectItem>
            </SelectContent>
          </Select>
          <Button variant="primary" type="submit" disabled={create.isPending}>
            Record
          </Button>
        </div>
        <Textarea
          rows={3}
          value={content}
          onChange={(event) => setContent(event.target.value)}
          placeholder="Why, and what it rules out"
        />
      </form>

      <p className="text-muted text-xs">
        Important decisions travel with every Job of this project.
      </p>

      <Records
        items={decisions.data ?? []}
        render={(decision) => (
          <Card key={decision.id}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{decision.title}</span>
              {decision.importance === 'IMPORTANT' && <Badge tone="warn">important</Badge>}
              {decision.status === 'SUPERSEDED' && <Badge>superseded</Badge>}
              <DeleteDecision decision={decision} />
            </div>
            {decision.content && <p className="mt-1.5 text-sm whitespace-pre-wrap">{decision.content}</p>}
            <p className="text-muted mt-1.5 font-mono text-xs">{when(decision.createdAt)}</p>
          </Card>
        )}
      />
    </div>
  );
}

/**
 * Taking a Decision out of the Project memory.
 *
 * It asks first, in place: this is for a decision that should never have been
 * recorded, and the one that was changed is superseded instead, which keeps the
 * history. Core makes whatever this one replaced current again.
 */
function DeleteDecision({ decision }: { decision: Decision }) {
  const remove = useDeleteDecision();
  const [asking, setAsking] = useState(false);

  if (asking) {
    return (
      <span className="text-muted ml-auto flex items-center gap-2 text-xs">
        Delete?
        <Button variant="ghost" size="sm" onClick={() => setAsking(false)}>
          No
        </Button>
        <Button
          variant="danger"
          size="sm"
          disabled={remove.isPending}
          onClick={() => remove.mutate(decision.id)}
        >
          Delete
        </Button>
      </span>
    );
  }

  return (
    <Button
      variant="ghost"
      size="icon"
      className="ml-auto size-7 [&_svg]:size-3.5"
      title="Delete this decision"
      onClick={() => setAsking(true)}
    >
      <Trash2 />
    </Button>
  );
}

// ----------------------------------------------------------------- artifacts

function ArtifactsPane({ projectId }: { projectId: string }) {
  const artifacts = useArtifacts(projectId);
  const upload = useUploadArtifact(projectId);
  const remove = useDeleteArtifact(projectId);
  const [file, setFile] = useState<File | null>(null);

  return (
    <div className="space-y-3">
      <form
        className="flex items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (file) upload.mutate(file, { onSuccess: () => setFile(null) });
        }}
      >
        <Input
          type="file"
          className="file:text-text file:mr-3 file:border-0 file:bg-transparent file:text-sm"
          onChange={(event) => setFile(event.target.files?.[0] ?? null)}
        />
        <Button variant="primary" type="submit" disabled={!file || upload.isPending}>
          Upload
        </Button>
      </form>

      {upload.error && <p className="text-danger text-sm">{(upload.error).message}</p>}

      <Records
        items={artifacts.data ?? []}
        render={(artifact) => (
          <Card key={artifact.id}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium break-all">{artifact.filename}</span>
              <span className="ml-auto flex gap-3">
                <Button asChild variant="link">
                  <a href={`/api/v1/artifacts/${artifact.id}/content`} download>
                    <Download /> download
                  </a>
                </Button>
                <Button
                  variant="link"
                  className="text-danger"
                  onClick={() => remove.mutate(artifact.id)}
                >
                  <Trash2 /> delete
                </Button>
              </span>
            </div>
            <p className="text-muted mt-1.5 font-mono text-xs break-all">
              {bytes(artifact.size)} · {artifact.sha256.slice(0, 12)} · {when(artifact.createdAt)}
            </p>
          </Card>
        )}
      />
    </div>
  );
}

// -------------------------------------------------------------------- skills

function SkillsPane({ projectId }: { projectId: string }) {
  const skills = useSkills(projectId);
  const install = useInstallSkill(projectId);
  const uninstall = useUninstallSkill(projectId);

  const [type, setType] = useState<SkillSourceType>('GIT');
  const [url, setUrl] = useState('');
  const [path, setPath] = useState('');
  const [revision, setRevision] = useState('');
  const [file, setFile] = useState<File | null>(null);

  const submit = (event: React.FormEvent) => {
    event.preventDefault();
    if (type === 'UPLOAD') {
      if (file) install.mutate({ source: { type, path }, file }, { onSuccess: () => setFile(null) });
      return;
    }
    if (url.trim()) {
      install.mutate(
        { source: { type, url: url.trim(), path: path.trim(), revision: revision.trim() } },
        { onSuccess: () => setUrl('') },
      );
    }
  };

  return (
    <div className="space-y-3">
      <form className="space-y-2" onSubmit={submit}>
        <div className="flex gap-2">
          <Select value={type} onValueChange={(value) => setType(value as SkillSourceType)}>
            <SelectTrigger className="w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="GIT">git url</SelectItem>
              <SelectItem value="ARCHIVE">archive url</SelectItem>
              <SelectItem value="UPLOAD">upload</SelectItem>
            </SelectContent>
          </Select>

          {type === 'UPLOAD' ? (
            <Input
              type="file"
              className="file:text-text file:mr-3 file:border-0 file:bg-transparent file:text-sm"
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            />
          ) : (
            <Input
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              placeholder="https://github.com/owner/repo"
            />
          )}

          <Button variant="primary" type="submit" disabled={install.isPending}>
            Install
          </Button>
        </div>

        <div className="flex gap-2">
          <Input
            value={path}
            onChange={(event) => setPath(event.target.value)}
            placeholder="path inside the source (optional)"
          />
          {type !== 'UPLOAD' && (
            <Input
              value={revision}
              onChange={(event) => setRevision(event.target.value)}
              placeholder="branch, tag or commit (optional)"
            />
          )}
        </div>
      </form>

      <p className="text-muted text-xs">
        Core fetches and repacks a skill. It never runs anything the source contains.
      </p>

      {install.error && <p className="text-danger text-sm">{(install.error).message}</p>}

      <Records
        items={skills.data ?? []}
        render={(skill) => (
          <Card key={skill.id}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{skill.name}</span>
              <Badge>{humanise(skill.source.type)}</Badge>
              <Button
                variant="link"
                className="text-danger ml-auto"
                onClick={() => uninstall.mutate(skill.id)}
              >
                uninstall
              </Button>
            </div>
            {skill.description && <p className="mt-1.5 text-sm">{skill.description}</p>}
            {/* The installed revision is what is actually there, which is not the
                same as the branch someone asked for. */}
            <p className="text-muted mt-1.5 font-mono text-xs break-all">
              {skill.installedRevision.slice(0, 12)} · {skill.source.url || 'uploaded'} ·{' '}
              {when(skill.installedAt)}
            </p>
          </Card>
        )}
      />
    </div>
  );
}

// -------------------------------------------------------------- instructions

function InstructionsPane({ projectId }: { projectId: string }) {
  const project = useProject(projectId);
  const update = useUpdateProject(projectId);
  const [draft, setDraft] = useState<string | null>(null);

  useEffect(() => {
    if (project.data) setDraft(project.data.instructions ?? '');
  }, [project.data]);

  return (
    <div className="max-w-3xl space-y-3">
      <p className="text-muted text-sm">
        Provider-independent rules every agent on this project follows. The backend maps them to
        whatever its provider reads.
      </p>
      <Textarea
        rows={14}
        value={draft ?? ''}
        onChange={(event) => setDraft(event.target.value)}
        placeholder="Always run the chart tests before packaging."
      />
      <Button
        variant="primary"
        disabled={draft === null || update.isPending}
        onClick={() => update.mutate({ instructions: draft ?? '' })}
      >
        {update.isSuccess && !update.isPending ? 'Saved' : 'Save'}
      </Button>
    </div>
  );
}

// --------------------------------------------------------------------- audit

function AuditPane() {
  const audit = useAudit();

  return (
    <div className="space-y-3">
      <p className="text-muted text-sm">
        Who changed what the agent may do, and who answered what it asked.
      </p>
      <Records
        items={audit.data ?? []}
        render={(entry) => (
          <Card key={entry.id}>
            <span className="font-medium">{entry.action.replace(/[._]/g, ' ')}</span>
            <AuditDetail detail={entry.detail ?? {}} />
            <p className="text-muted mt-1.5 font-mono text-xs">
              {[entry.actorId, entry.channel, when(entry.createdAt)].filter(Boolean).join(' · ')}
            </p>
          </Card>
        )}
      />
    </div>
  );
}

/**
 * What an audit entry recorded. The detail is whatever the action chose to
 * keep — a whole policy, with its rules, for a permission change — so it is
 * read as the nested value it is rather than flattened into `[object Object]`.
 */
function AuditDetail({ detail }: { detail: Record<string, unknown> }) {
  const fields = Object.entries(detail).filter(([, value]) => !blank(value));
  if (fields.length === 0) return null;
  return (
    <dl className="mt-1.5 space-y-0.5 text-sm">
      {fields.map(([key, value]) => (
        <div key={key} className="flex gap-2">
          <dt className="text-muted shrink-0">{humanise(key.replace(/([A-Z])/g, ' $1'))}:</dt>
          <dd className="min-w-0 break-words">
            <AuditValue value={value} />
          </dd>
        </div>
      ))}
    </dl>
  );
}

function AuditValue({ value }: { value: unknown }) {
  if (Array.isArray(value)) {
    return (
      <ul className="space-y-0.5">
        {value.map((item, index) => (
          <li key={index}>
            <AuditValue value={item} />
          </li>
        ))}
      </ul>
    );
  }
  if (isRule(value)) {
    // A permission rule reads as the sentence it stands for.
    return (
      <span>
        {humanise(value.effect)} {humanise(value.capability)}
        {value.match && <code className="ml-1.5 font-mono text-xs">{value.match}</code>}
      </span>
    );
  }
  if (value !== null && typeof value === 'object') {
    return <AuditDetail detail={value as Record<string, unknown>} />;
  }
  // A switch turned off is as much the policy as one turned on, so both show.
  if (typeof value === 'boolean') return <>{value ? 'yes' : 'no'}</>;
  if (typeof value === 'string' && /^[A-Z][A-Z_]+$/.test(value)) return <>{humanise(value)}</>;
  return <>{String(value)}</>;
}

function blank(value: unknown): boolean {
  if (value === null || value === undefined || value === '' || value === 0) return true;
  if (Array.isArray(value)) return value.length === 0;
  return false;
}

function isRule(value: unknown): value is { effect: string; capability: string; match?: string } {
  return (
    value !== null &&
    typeof value === 'object' &&
    typeof (value as { effect?: unknown }).effect === 'string' &&
    typeof (value as { capability?: unknown }).capability === 'string'
  );
}

/** A list that says so when it is empty, rather than showing nothing at all. */
function Records<T>({ items, render }: { items: T[]; render: (item: T) => React.ReactNode }) {
  if (items.length === 0) return <EmptyState>Nothing yet.</EmptyState>;
  return <div className="space-y-2">{items.map(render)}</div>;
}

// --------------------------------------------------------------- permissions

function PermissionsPane({ projectId }: { projectId: string }) {
  const { data } = useProjectPolicy(projectId);
  const save = useSetProjectPolicy(projectId);
  const [draft, setDraft] = useState<ExecutionPolicy | null>(null);

  useEffect(() => {
    if (data) setDraft(data);
  }, [data]);

  if (!draft) return null;

  return (
    <div className="max-w-3xl space-y-4">
      <PolicyForm value={draft} onChange={setDraft} idPrefix="project-policy" />

      <RulesEditor rules={draft.rules ?? []} onChange={(rules) => setDraft({ ...draft, rules })} />

      <p className="text-muted text-xs">
        A refusal written here cannot be lifted by a Session. The switches above can: a Session that
        needs to push once says so, and the audit records it.
      </p>

      {save.error && <p className="text-danger text-sm">{save.error.message}</p>}

      <Button
        variant="primary"
        disabled={save.isPending}
        onClick={() => save.mutate(draft)}
      >
        {save.isSuccess && !save.isPending ? 'Saved' : 'Save'}
      </Button>
    </div>
  );
}
