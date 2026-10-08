import { FileCode, FileText, File as FileIcon } from 'lucide-react';
import { useState } from 'react';

import { useSkillFiles } from '@/api/queries';
import type { Skill, SkillFile } from '@/api/types';
import { CodeBlock } from '@/components/code-block';
import { Markdown } from '@/components/markdown';
import { ActionError } from '@/components/ui/action-error';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { bytes, cn, humanise, when } from '@/lib/utils';

const MANIFEST = 'SKILL.md';

/**
 * An installed Skill, read: its files, and the text of each.
 *
 * Read from the bundle Core stored, which is exactly what an agent is given —
 * not what the source holds today, which may have moved on. Nothing here edits:
 * a Skill is an immutable version, and a new one comes from its source.
 */
export function SkillViewer({
  skill,
  open,
  onOpenChange,
}: {
  skill: Skill;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const files = useSkillFiles(open ? skill.id : undefined);
  const list = files.data ?? [];
  const [chosen, setChosen] = useState(MANIFEST);
  // The manifest first: it is what the agent reads first.
  const shown = list.find((file) => file.path === chosen) ?? list.find((file) => file.path === MANIFEST) ?? list[0];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="h-[min(48rem,calc(100dvh-2rem))] w-[min(64rem,calc(100vw-2rem))] gap-3 overflow-hidden">
        <div className="pr-8">
          <DialogTitle>{skill.name}</DialogTitle>
          <DialogDescription className="figures font-mono text-xs [overflow-wrap:anywhere]">
            {humanise(skill.source.type)} · {skill.source.url || 'uploaded'}
            {skill.source.path && ` · ${skill.source.path}`} · {skill.installedRevision.slice(0, 12)} ·{' '}
            {when(skill.installedAt)}
          </DialogDescription>
        </div>

        {files.isPending ? (
          <p className="text-muted text-sm">Reading the bundle…</p>
        ) : files.error ? (
          <ActionError error={files.error} outcome="The files could not be read" recovery="Close and open it again." />
        ) : (
          <div className="border-border flex min-h-0 flex-1 flex-col overflow-hidden rounded-(--radius-card) border sm:flex-row">
            <nav
              aria-label="Files"
              className="border-border max-h-36 shrink-0 overflow-y-auto border-b p-1 sm:max-h-none sm:w-56 sm:border-r sm:border-b-0"
            >
              <ul>
                {list.map((file) => (
                  <li key={file.path}>
                    <button
                      type="button"
                      aria-current={file === shown ? 'true' : undefined}
                      onClick={() => setChosen(file.path)}
                      className={cn(
                        'hover:bg-surface-2 flex min-h-11 w-full items-center gap-2 rounded px-2 text-left text-sm sm:min-h-8',
                        file === shown && 'bg-surface-2 font-medium',
                      )}
                    >
                      <FileMark file={file} />
                      <span className="min-w-0 flex-1 truncate font-mono text-xs" title={file.path}>
                        {file.path}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </nav>
            <div className="min-h-0 min-w-0 flex-1 overflow-y-auto p-4">{shown && <FileContent file={shown} />}</div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

function FileMark({ file }: { file: SkillFile }) {
  const Icon = file.text === undefined ? FileIcon : /\.(md|txt)$/i.test(file.path) ? FileText : FileCode;
  return <Icon className="text-muted size-3.5 shrink-0" />;
}

/** A file as it reads best: prose rendered, code highlighted, the rest named. */
function FileContent({ file }: { file: SkillFile }) {
  if (file.text === undefined) {
    return (
      <p className="text-muted text-sm">
        <span className="text-text font-mono text-xs">{file.path}</span> · {bytes(file.size)} — not shown:{' '}
        {file.size > 256 * 1024 ? 'too large to read here.' : 'not a text file.'}
      </p>
    );
  }
  if (/\.md$/i.test(file.path)) {
    const { fields, body } = frontMatter(file.text);
    return (
      <div className="space-y-4">
        {fields.length > 0 && (
          // What Core read the name and description from, and whatever else
          // the agent is told up front.
          <dl className="border-border bg-surface-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 rounded-md border px-3 py-2 text-sm">
            {fields.map(([key, value]) => (
              <div key={key} className="contents">
                <dt className="text-muted font-mono text-xs leading-5">{key}</dt>
                <dd className="break-words">{value}</dd>
              </div>
            ))}
          </dl>
        )}
        <Markdown>{body}</Markdown>
      </div>
    );
  }
  return <CodeBlock language={file.path.split('.').pop() ?? ''} code={file.text} />;
}

/**
 * The front matter of a Markdown file, read as the flat `key: value` lines a
 * Skill manifest uses. Anything it cannot read stays in the body, so nothing
 * of the file is hidden.
 */
function frontMatter(text: string): { fields: [string, string][]; body: string } {
  const match = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(text);
  if (!match) return { fields: [], body: text };
  const fields: [string, string][] = [];
  for (const line of match[1].split(/\r?\n/)) {
    const pair = /^([A-Za-z0-9_-]+):\s*(.*)$/.exec(line);
    if (!pair) return { fields: [], body: text };
    fields.push([pair[1], pair[2].replace(/^["']|["']$/g, '')]);
  }
  return { fields, body: text.slice(match[0].length) };
}
