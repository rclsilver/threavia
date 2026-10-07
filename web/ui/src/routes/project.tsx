import { useParams } from '@tanstack/react-router';
import { Download, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';

import {
  useArtifacts,
  useAudit,
  useCreateDecision,
  useCreateTask,
  useDecisions,
  useDeleteArtifact,
  useInstallSkill,
  useProject,
  useSkills,
  useUploadArtifact,
  useTasks,
  useUninstallSkill,
  useUpdateProject,
  useUpdateTask,
} from '@/api/queries';
import type { SkillSourceType, Task, TaskStatus } from '@/api/types';
import { Badge, type BadgeTone } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, EmptyState } from '@/components/ui/card';
import { CheckboxField } from '@/components/ui/checkbox';
import { Input, Textarea } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { bytes, humanise, when } from '@/lib/utils';

/**
 * Everything a Project carries beyond its Sessions: the memory an agent reads
 * and writes across sessions, the Skills it may use, the rules it follows, and
 * the trail of what was decided about execution.
 */
export function ProjectView() {
  const { projectId } = useParams({ from: '/projects/$projectId' });
  const project = useProject(projectId);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 p-5">
      <header>
        <h2 className="text-lg font-semibold">{project.data?.name ?? 'Project'}</h2>
        <p className="text-muted text-sm">Project memory, skills and audit trail.</p>
      </header>

      <Tabs defaultValue="tasks" className="flex min-h-0 flex-1 flex-col">
        <TabsList>
          <TabsTrigger value="tasks">Tasks</TabsTrigger>
          <TabsTrigger value="decisions">Decisions</TabsTrigger>
          <TabsTrigger value="artifacts">Artifacts</TabsTrigger>
          <TabsTrigger value="skills">Skills</TabsTrigger>
          <TabsTrigger value="instructions">Instructions</TabsTrigger>
          <TabsTrigger value="audit">Audit</TabsTrigger>
        </TabsList>

        <TabsContent value="tasks">
          <TasksPane projectId={projectId} />
        </TabsContent>
        <TabsContent value="decisions">
          <DecisionsPane projectId={projectId} />
        </TabsContent>
        <TabsContent value="artifacts">
          <ArtifactsPane projectId={projectId} />
        </TabsContent>
        <TabsContent value="skills">
          <SkillsPane projectId={projectId} />
        </TabsContent>
        <TabsContent value="instructions">
          <InstructionsPane projectId={projectId} />
        </TabsContent>
        <TabsContent value="audit">
          <AuditPane />
        </TabsContent>
      </Tabs>
    </div>
  );
}

// --------------------------------------------------------------------- tasks

/** The moves that make sense from here. Blocked is derived, never set by hand. */
function nextStatuses(status: TaskStatus): TaskStatus[] {
  switch (status) {
    case 'TODO':
      return ['IN_PROGRESS', 'DONE'];
    case 'IN_PROGRESS':
      return ['DONE', 'TODO'];
    default:
      return ['TODO'];
  }
}

function taskTone(status: TaskStatus): BadgeTone {
  if (status === 'DONE') return 'ok';
  if (status === 'IN_PROGRESS') return 'warn';
  return 'neutral';
}

function TasksPane({ projectId }: { projectId: string }) {
  const [includeDone, setIncludeDone] = useState(false);
  const [title, setTitle] = useState('');
  const tasks = useTasks(projectId, includeDone);
  const create = useCreateTask(projectId);
  const update = useUpdateTask();

  return (
    <div className="space-y-3">
      <form
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (!title.trim()) return;
          create.mutate(title.trim());
          setTitle('');
        }}
      >
        <Input
          value={title}
          onChange={(event) => setTitle(event.target.value)}
          placeholder="What needs doing"
        />
        <Button variant="primary" type="submit" disabled={create.isPending}>
          Add
        </Button>
      </form>

      <CheckboxField checked={includeDone} onCheckedChange={setIncludeDone}>
        show finished
      </CheckboxField>

      <Records
        items={tasks.data ?? []}
        render={(task: Task) => (
          <Card key={task.id}>
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{task.title}</span>
              <Badge tone={taskTone(task.status)}>{humanise(task.status)}</Badge>
              <span className="ml-auto flex gap-3">
                {nextStatuses(task.status).map((status) => (
                  <Button
                    key={status}
                    variant="link"
                    onClick={() => update.mutate({ id: task.id, status })}
                  >
                    {humanise(status)}
                  </Button>
                ))}
              </span>
            </div>
            {task.description && <p className="mt-1.5 text-sm whitespace-pre-wrap">{task.description}</p>}
            {task.dependsOn?.length ? (
              <p className="text-muted mt-1.5 font-mono text-xs">
                depends on {task.dependsOn.length} task(s)
              </p>
            ) : null}
          </Card>
        )}
      />
    </div>
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
            </div>
            {decision.content && <p className="mt-1.5 text-sm whitespace-pre-wrap">{decision.content}</p>}
            <p className="text-muted mt-1.5 font-mono text-xs">{when(decision.createdAt)}</p>
          </Card>
        )}
      />
    </div>
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
            <dl className="mt-1.5 text-sm">
              {Object.entries(entry.detail ?? {})
                .filter(([, value]) => value !== null && value !== '' && value !== 0 && value !== false)
                .map(([key, value]) => (
                  <div key={key} className="flex gap-2">
                    <dt className="text-muted">{humanise(key.replace(/([A-Z])/g, ' $1'))}:</dt>
                    <dd>{String(value)}</dd>
                  </div>
                ))}
            </dl>
            <p className="text-muted mt-1.5 font-mono text-xs">
              {[entry.actorId, entry.channel, when(entry.createdAt)].filter(Boolean).join(' · ')}
            </p>
          </Card>
        )}
      />
    </div>
  );
}

/** A list that says so when it is empty, rather than showing nothing at all. */
function Records<T>({ items, render }: { items: T[]; render: (item: T) => React.ReactNode }) {
  if (items.length === 0) return <EmptyState>Nothing yet.</EmptyState>;
  return <div className="space-y-2">{items.map(render)}</div>;
}
