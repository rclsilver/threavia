import { Link, useParams } from '@tanstack/react-router';
import {
  BookText,
  Check,
  Copy,
  Download,
  File as FileIcon,
  FileArchive,
  FileCode,
  FileImage,
  FileText,
  Pin,
  PinOff,
  Sparkles,
  Trash2,
  Upload,
} from 'lucide-react';
import { useCallback, useEffect, useRef, useState } from 'react';

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
  useSetDecisionImportance,
  useSetProjectPolicy,
  useUninstallSkill,
  useUpdateProject,
} from '@/api/queries';
import type { Artifact, Decision, ExecutionPolicy, Skill, SkillSourceType } from '@/api/types';
import { PolicyForm, RulesEditor } from '@/components/policy-form';
import {
  ConfirmLine,
  NewButton,
  NewPanel,
  RecordGroup,
  RecordList,
  RecordRow,
} from '@/components/record-list';
import { ActionError } from '@/components/ui/action-error';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { CheckboxField } from '@/components/ui/checkbox';
import { Card, EmptyState } from '@/components/ui/card';
import { Input, Textarea } from '@/components/ui/input';
import { MenuItem, MenuSeparator } from '@/components/ui/menu';
import { useNewShortcut } from '@/use-new-shortcut';
import { bytes, cn, humanise, when } from '@/lib/utils';

/**
 * One part of what a Project carries beyond its Sessions.
 *
 * Each is a route of its own rather than a tab: the sidebar asks for them from
 * anywhere, and a tab only opens when the view around it is already on screen.
 */
function Section({
  title,
  subtitle,
  action,
  children,
}: {
  title: string;
  subtitle: string;
  /** What makes a new one, at the header's end. */
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  const { projectId } = useParams({ strict: false });
  const project = useProject(projectId);

  return (
    // Lists, not prose, so the column is wider than a conversation's — but still
    // a column: a record and the buttons that act on it should not end up at
    // opposite ends of a wide screen.
    <div className="mx-auto flex min-h-0 w-full max-w-page flex-1 flex-col gap-5 overflow-y-auto p-4 sm:p-6">
      <header className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-lg font-semibold">{title}</h2>
          <p className="text-muted text-sm">
            {subtitle}
            {project.data?.name && <span className="opacity-70"> · {project.data.name}</span>}
          </p>
        </div>
        {action}
      </header>
      {children}
    </div>
  );
}

/** The projectId of the route, which every section below is scoped to. */
function useProjectId(): string {
  return useParams({ strict: false }).projectId ?? '';
}

/** Whether the panel that makes a new one is open, and N to open it. */
function useCreating(): [boolean, (open: boolean) => void] {
  const [creating, setCreating] = useState(false);
  useNewShortcut(useCallback(() => setCreating(true), []));
  return [creating, setCreating];
}

export function MemoryView() {
  const projectId = useProjectId();
  const [creating, setCreating] = useCreating();
  return (
    <Section
      title="Memory"
      subtitle="What was decided, and why."
      action={!creating && <NewButton label="Record decision" onClick={() => setCreating(true)} />}
    >
      <DecisionsPane projectId={projectId} creating={creating} onCreatingChange={setCreating} />
    </Section>
  );
}

export function ArtifactsView() {
  const projectId = useProjectId();
  const upload = useUploadArtifact(projectId);
  const picker = useRef<HTMLInputElement>(null);
  useNewShortcut(useCallback(() => picker.current?.click(), []));
  return (
    <Section
      title="Artifacts"
      subtitle="Files produced by the work, kept with the Project."
      action={
        <>
          <Button
            size="lg"
            className="gap-1.5"
            title="Upload files (N)"
            aria-keyshortcuts="N"
            disabled={upload.isPending}
            onClick={() => picker.current?.click()}
          >
            <Upload />
            {upload.isPending ? 'Uploading…' : 'Upload'}
            <kbd className="text-muted border-border ml-1 hidden rounded border px-1 font-sans text-[0.6875rem] leading-4 sm:inline">
              N
            </kbd>
          </Button>
          <input
            ref={picker}
            type="file"
            multiple
            hidden
            onChange={(event) => {
              for (const file of event.target.files ?? []) upload.mutate(file);
              event.target.value = '';
            }}
          />
        </>
      }
    >
      <ArtifactsPane projectId={projectId} upload={upload} />
    </Section>
  );
}

export function SkillsView() {
  const projectId = useProjectId();
  const [creating, setCreating] = useCreating();
  return (
    <Section
      title="Skills"
      subtitle="What an agent may use here, and where it came from."
      action={!creating && <NewButton label="Install skill" onClick={() => setCreating(true)} />}
    >
      <SkillsPane projectId={projectId} creating={creating} onCreatingChange={setCreating} />
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

/** Copies a value, best effort: the menu item is a convenience, not a promise. */
function copy(text: string) {
  void navigator.clipboard?.writeText(text).catch(() => {});
}

// ----------------------------------------------------------------- decisions

/**
 * The Project's memory, in the order it is relied on: what travels with every
 * Job, then what is on record. Core lists only what is current; a superseded
 * decision lives on in the one that replaced it.
 */
function DecisionsPane({
  projectId,
  creating,
  onCreatingChange,
}: {
  projectId: string;
  creating: boolean;
  onCreatingChange: (open: boolean) => void;
}) {
  const decisions = useDecisions(projectId);
  const all = decisions.data ?? [];
  const important = all.filter((decision) => decision.importance === 'IMPORTANT');
  const recorded = all.filter((decision) => decision.importance !== 'IMPORTANT');

  return (
    <>
      {creating && <NewDecision projectId={projectId} onClose={() => onCreatingChange(false)} />}
      {decisions.isPending ? null : all.length === 0 && !creating ? (
        <EmptyState>
          Nothing recorded yet. Agents record what they decide as they work; a decision of your own
          goes in with <span className="text-text font-medium">Record decision</span>.
        </EmptyState>
      ) : null}
      {important.length > 0 && (
        <RecordGroup title="Travels with every Job">
          <DecisionList decisions={important} />
        </RecordGroup>
      )}
      {recorded.length > 0 && (
        <RecordGroup title="On record">
          <DecisionList decisions={recorded} />
        </RecordGroup>
      )}
    </>
  );
}

function NewDecision({ projectId, onClose }: { projectId: string; onClose: () => void }) {
  const create = useCreateDecision(projectId);
  const [title, setTitle] = useState('');
  const [content, setContent] = useState('');
  const [important, setImportant] = useState(false);
  const submit = () =>
    create.mutate(
      { title: title.trim(), content: content.trim(), importance: important ? 'IMPORTANT' : 'NORMAL' },
      { onSuccess: onClose },
    );

  return (
    <NewPanel onSubmit={submit} onCancel={onClose} verb="Record" pending={create.isPending} ready={Boolean(title.trim())}>
      <Input
        autoFocus
        aria-label="Decision"
        value={title}
        onChange={(event) => setTitle(event.target.value)}
        placeholder="What was decided?"
        className="h-auto border-0 px-1 py-1 text-[0.9375rem] font-medium shadow-none outline-none"
      />
      <Textarea
        aria-label="Why"
        rows={3}
        value={content}
        onChange={(event) => setContent(event.target.value)}
        placeholder="Why, and what it rules out (optional)"
        className="min-h-16 resize-none border-0 px-1 py-0.5 shadow-none outline-none"
        onKeyDown={(event) => {
          // A newline belongs here, so only a modified Enter records.
          if (event.key === 'Enter' && (event.metaKey || event.ctrlKey) && title.trim()) {
            event.preventDefault();
            submit();
          }
        }}
      />
      {/* Said as what it does, not as a level of importance to pick. */}
      <div className="px-1">
        <CheckboxField checked={important} onCheckedChange={setImportant}>
          Travels with every Job — the agent reads it before it starts
        </CheckboxField>
      </div>
      <ActionError error={create.error} outcome="Not recorded" recovery="What you wrote is still here; record it again." />
    </NewPanel>
  );
}

function DecisionList({ decisions }: { decisions: Decision[] }) {
  return (
    <RecordList>
      {decisions.map((decision) => (
        <DecisionRow key={decision.id} decision={decision} />
      ))}
    </RecordList>
  );
}

/**
 * One decision. Deleting is for one that should never have been recorded; a
 * decision that changed is superseded instead, which keeps the history, and
 * Core makes whatever this one replaced current again.
 */
function DecisionRow({ decision }: { decision: Decision }) {
  const remove = useDeleteDecision();
  const pin = useSetDecisionImportance(decision.projectId);
  const [asking, setAsking] = useState(false);
  const important = decision.importance === 'IMPORTANT';

  return (
    <RecordRow
      icon={important ? Pin : BookText}
      mark={
        <PinMark
          pinned={important}
          title={decision.title}
          onToggle={() => pin.mutate({ id: decision.id, importance: important ? 'NORMAL' : 'IMPORTANT' })}
        />
      }
      title={decision.title}
      body={decision.content}
      meta={when(decision.createdAt)}
      menuLabel={`More for ${decision.title}`}
      menu={
        <>
          <MenuItem onSelect={() => copy(`${decision.title}\n\n${decision.content ?? ''}`.trim())}>
            <Copy className="text-muted size-4" />
            Copy
          </MenuItem>
          <MenuSeparator />
          <MenuItem className="text-danger" onSelect={() => setAsking(true)}>
            <Trash2 className="size-4" />
            Delete…
          </MenuItem>
        </>
      }
      below={
        asking || remove.error ? (
          <>
            <ConfirmLine
              question="Delete this decision?"
              confirm="Delete"
              pending={remove.isPending}
              onConfirm={() => remove.mutate(decision.id)}
              onCancel={() => {
                setAsking(false);
                remove.reset();
              }}
            />
            <ActionError error={remove.error} recovery="It is still recorded; try again." className="mt-1 text-xs" />
          </>
        ) : (
          // A pin that did not hold goes back on its own; this says why.
          pin.error && (
            <ActionError
              error={pin.error}
              outcome={important ? 'Not unpinned' : 'Not pinned'}
              recovery="Press the mark again."
              className="text-xs"
            />
          )
        )
      }
    />
  );
}

/**
 * The mark of a decision, which pins it to every Job or unpins it.
 *
 * A pinned decision shows a solid pin; one on record shows its page, and the
 * pin it would take on hover, so the gesture is found where the eye already is.
 */
function PinMark({ pinned, title, onToggle }: { pinned: boolean; title: string; onToggle: () => void }) {
  return (
    <button
      type="button"
      aria-pressed={pinned}
      aria-label={pinned ? `Unpin ${title}: stop sending it with every Job` : `Pin ${title}: send it with every Job`}
      title={pinned ? 'Pinned: travels with every Job. Click to unpin.' : 'Pin it: travel with every Job'}
      onClick={onToggle}
      className="group/pin hover:bg-surface-2 flex size-11 shrink-0 items-center justify-center rounded-md sm:size-8"
    >
      {pinned ? (
        <>
          <Pin className="text-warn-text size-4 fill-current group-hover/pin:hidden" />
          <PinOff className="text-muted hidden size-4 group-hover/pin:block" />
        </>
      ) : (
        <>
          <BookText className="text-muted size-4 group-hover/pin:hidden" />
          <Pin className="text-warn-text hidden size-4 group-hover/pin:block" />
        </>
      )}
    </button>
  );
}

// ----------------------------------------------------------------- artifacts

/** A file's mark, by what it holds, so a list of them scans by shape. */
function fileIcon(artifact: Artifact) {
  // The type a browser sends is often only "application/octet-stream", so the
  // name's extension is asked too.
  const type = `${artifact.mimeType} ${artifact.filename.split('.').pop() ?? ''}`.toLowerCase();
  if (/image\/|\b(png|jpe?g|gif|svg|webp)$/.test(type)) return FileImage;
  if (/zip|tar|gzip|compressed|\b(tgz|gz|7z)$/.test(type)) return FileArchive;
  if (/json|yaml|xml|javascript|x-sh|x-python|\b(ya?ml|tf|sh|py|go|ts|js|toml)$/.test(type)) return FileCode;
  if (/text\/|\b(md|txt|csv|log)$/.test(type)) return FileText;
  return FileIcon;
}

function ArtifactsPane({
  projectId,
  upload,
}: {
  projectId: string;
  upload: ReturnType<typeof useUploadArtifact>;
}) {
  const artifacts = useArtifacts(projectId);
  const all = artifacts.data ?? [];
  const [dragging, setDragging] = useState(false);

  return (
    // A file dropped anywhere on the list is uploaded, as it would be in any
    // file manager.
    <div
      className={cn('-m-2 space-y-3 rounded-xl p-2', dragging && 'bg-accent/5 outline-accent/50 outline-2 outline-dashed')}
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes('Files')) return;
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(event) => {
        event.preventDefault();
        setDragging(false);
        for (const file of event.dataTransfer.files) upload.mutate(file);
      }}
    >
      <ActionError error={upload.error} outcome="Not uploaded" recovery="Pick the file again." />
      {artifacts.isPending ? null : all.length === 0 ? (
        <EmptyState>
          No files yet. What the agents produce lands here; drop a file on this page or use{' '}
          <span className="text-text font-medium">Upload</span> to add your own.
        </EmptyState>
      ) : (
        <RecordList>
          {all.map((artifact) => (
            <ArtifactRow key={artifact.id} artifact={artifact} projectId={projectId} />
          ))}
        </RecordList>
      )}
      {all.length > 0 && <p className="text-muted text-xs">Drop files anywhere on the list to upload them.</p>}
    </div>
  );
}

function ArtifactRow({ artifact, projectId }: { artifact: Artifact; projectId: string }) {
  const remove = useDeleteArtifact(projectId);
  const [asking, setAsking] = useState(false);
  const href = `/api/v1/artifacts/${artifact.id}/content`;

  return (
    <RecordRow
      icon={fileIcon(artifact)}
      title={<span className="[overflow-wrap:anywhere]">{artifact.filename}</span>}
      meta={
        <>
          {bytes(artifact.size)} · {artifact.sha256.slice(0, 12)} · {when(artifact.createdAt)}
          {/* Where it came from, when a Session made it. */}
          {artifact.sessionId && (
            <>
              {' · '}
              <Link
                to="/sessions/$sessionId"
                params={{ sessionId: artifact.sessionId }}
                className="hover:text-text underline underline-offset-2"
              >
                its session
              </Link>
            </>
          )}
        </>
      }
      action={
        <Button asChild size="sm" className="max-sm:size-11 max-sm:p-0">
          <a href={href} download aria-label={`Download ${artifact.filename}`}>
            <Download />
            <span className="max-sm:sr-only">Download</span>
          </a>
        </Button>
      }
      menuLabel={`More for ${artifact.filename}`}
      menu={
        <>
          <MenuItem onSelect={() => copy(artifact.sha256)}>
            <Copy className="text-muted size-4" />
            Copy SHA-256
          </MenuItem>
          <MenuSeparator />
          <MenuItem className="text-danger" onSelect={() => setAsking(true)}>
            <Trash2 className="size-4" />
            Delete…
          </MenuItem>
        </>
      }
      below={
        (asking || remove.error) && (
          <>
            <ConfirmLine
              question="Delete this file?"
              confirm="Delete"
              pending={remove.isPending}
              onConfirm={() => remove.mutate(artifact.id)}
              onCancel={() => {
                setAsking(false);
                remove.reset();
              }}
            />
            <ActionError error={remove.error} recovery="The file is still here; try again." className="mt-1 text-xs" />
          </>
        )
      }
    />
  );
}

// -------------------------------------------------------------------- skills

function SkillsPane({
  projectId,
  creating,
  onCreatingChange,
}: {
  projectId: string;
  creating: boolean;
  onCreatingChange: (open: boolean) => void;
}) {
  const skills = useSkills(projectId);
  const all = skills.data ?? [];

  return (
    <>
      {creating && <NewSkill projectId={projectId} onClose={() => onCreatingChange(false)} />}
      {skills.isPending ? null : all.length === 0 && !creating ? (
        <EmptyState>
          No skills yet. Install one from a git repository, an archive, or a file with{' '}
          <span className="text-text font-medium">Install skill</span>.
        </EmptyState>
      ) : (
        all.length > 0 && (
          <RecordList>
            {all.map((skill) => (
              <SkillRow key={skill.id} skill={skill} projectId={projectId} />
            ))}
          </RecordList>
        )
      )}
    </>
  );
}

const SOURCES: { type: SkillSourceType; label: string }[] = [
  { type: 'GIT', label: 'Git repository' },
  { type: 'ARCHIVE', label: 'Archive URL' },
  { type: 'UPLOAD', label: 'Upload' },
];

/**
 * Installing a skill: where it comes from, then where in it, then which
 * version. Core fetches and repacks it; nothing in the source is ever run.
 */
function NewSkill({ projectId, onClose }: { projectId: string; onClose: () => void }) {
  const install = useInstallSkill(projectId);
  const [type, setType] = useState<SkillSourceType>('GIT');
  const [url, setUrl] = useState('');
  const [path, setPath] = useState('');
  const [revision, setRevision] = useState('');
  const [file, setFile] = useState<File | null>(null);

  const ready = type === 'UPLOAD' ? Boolean(file) : Boolean(url.trim());
  const submit = () => {
    if (type === 'UPLOAD') {
      if (file) install.mutate({ source: { type, path: path.trim() }, file }, { onSuccess: onClose });
      return;
    }
    install.mutate(
      { source: { type, url: url.trim(), path: path.trim(), revision: revision.trim() } },
      { onSuccess: onClose },
    );
  };

  return (
    <NewPanel
      onSubmit={submit}
      onCancel={onClose}
      verb={install.isPending ? 'Installing…' : 'Install'}
      pending={install.isPending}
      ready={ready}
      hint="Core fetches and repacks it; nothing in the source is run."
    >
      <div role="radiogroup" aria-label="Where it comes from" className="border-border bg-surface-2 text-muted flex w-fit rounded-md border p-0.5 text-xs">
        {SOURCES.map((source) => (
          <button
            key={source.type}
            type="button"
            role="radio"
            aria-checked={type === source.type}
            onClick={() => setType(source.type)}
            className={cn(
              'min-h-11 rounded px-2.5 whitespace-nowrap sm:min-h-7',
              type === source.type && 'bg-surface text-text shadow-sm',
            )}
          >
            {source.label}
          </button>
        ))}
      </div>

      {type === 'UPLOAD' ? (
        <Input
          type="file"
          aria-label="Skill archive"
          accept=".zip,.tar,.tar.gz,.tgz"
          className="file:text-text h-auto py-2 file:mr-3 file:border-0 file:bg-transparent file:text-sm file:font-medium"
          onChange={(event) => setFile(event.target.files?.[0] ?? null)}
        />
      ) : (
        <Input
          autoFocus
          aria-label={type === 'GIT' ? 'Repository URL' : 'Archive URL'}
          value={url}
          onChange={(event) => setUrl(event.target.value)}
          placeholder={type === 'GIT' ? 'https://github.com/owner/repo' : 'https://example.com/skill.tar.gz'}
        />
      )}

      <div className="grid gap-2 sm:grid-cols-2">
        <Input
          aria-label="Path inside the source"
          value={path}
          onChange={(event) => setPath(event.target.value)}
          placeholder="Path inside it (optional)"
        />
        {type !== 'UPLOAD' && (
          <Input
            aria-label="Revision"
            value={revision}
            onChange={(event) => setRevision(event.target.value)}
            placeholder="Branch, tag or commit (optional)"
          />
        )}
      </div>
      <ActionError error={install.error} outcome="Not installed" recovery="Check the source and install again." />
    </NewPanel>
  );
}

function SkillRow({ skill, projectId }: { skill: Skill; projectId: string }) {
  const uninstall = useUninstallSkill(projectId);
  const [asking, setAsking] = useState(false);

  return (
    <RecordRow
      icon={Sparkles}
      tone="text-accent"
      title={skill.name}
      badges={<Badge>{humanise(skill.source.type)}</Badge>}
      body={skill.description}
      // The installed revision is what is actually there, which is not the
      // same as the branch someone asked for.
      meta={`${skill.installedRevision.slice(0, 12)} · ${skill.source.url || 'uploaded'} · ${when(skill.installedAt)}`}
      menuLabel={`More for ${skill.name}`}
      menu={
        <>
          {skill.source.url && (
            <MenuItem onSelect={() => copy(skill.source.url ?? '')}>
              <Copy className="text-muted size-4" />
              Copy source URL
            </MenuItem>
          )}
          <MenuItem onSelect={() => copy(skill.installedRevision)}>
            <Check className="text-muted size-4" />
            Copy installed revision
          </MenuItem>
          <MenuSeparator />
          <MenuItem className="text-danger" onSelect={() => setAsking(true)}>
            <Trash2 className="size-4" />
            Uninstall…
          </MenuItem>
        </>
      }
      below={
        (asking || uninstall.error) && (
          <>
            <ConfirmLine
              question="Uninstall this skill?"
              confirm="Uninstall"
              pending={uninstall.isPending}
              onConfirm={() => uninstall.mutate(skill.id)}
              onCancel={() => {
                setAsking(false);
                uninstall.reset();
              }}
            />
            <ActionError error={uninstall.error} recovery="It is still installed; try again." className="mt-1 text-xs" />
          </>
        )
      }
    />
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
        onChange={(event) => {
          setDraft(event.target.value);
          // "Saved" describes the text that was saved, not this one.
          update.reset();
        }}
        placeholder="Always run the chart tests before packaging."
      />
      <Button
        variant="primary"
        disabled={draft === null || update.isPending || update.isSuccess}
        onClick={() => update.mutate({ instructions: draft ?? '' })}
      >
        {update.isPending ? 'Saving…' : update.isSuccess ? (
          <>
            <Check /> Saved
          </>
        ) : (
          'Save'
        )}
      </Button>
      <ActionError error={update.error} />
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
      <PolicyForm
        value={draft}
        onChange={(next) => {
          setDraft(next);
          save.reset();
        }}
        idPrefix="project-policy"
      />

      <RulesEditor
        rules={draft.rules ?? []}
        onChange={(rules) => {
          setDraft({ ...draft, rules });
          save.reset();
        }}
      />

      <p className="text-muted text-xs">
        A refusal written here cannot be lifted by a Session. The switches above can: a Session that
        needs to push once says so, and the audit records it.
      </p>
      <p className="text-muted text-xs">
        A job already running keeps the permissions it started with: a change here applies to each
        session from its next message.
      </p>

      <ActionError error={save.error} />

      <Button
        variant="primary"
        disabled={save.isPending || save.isSuccess}
        onClick={() => save.mutate(draft)}
      >
        {save.isPending ? 'Saving…' : save.isSuccess ? (
          <>
            <Check /> Saved
          </>
        ) : (
          'Save'
        )}
      </Button>
    </div>
  );
}
