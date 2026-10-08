import { CodeBlock } from '@/components/code-block';
import { diffLines, languageOf, type FileChange, type Line } from '@/lib/file-change';
import { cn } from '@/lib/utils';

/**
 * A change to a file, shown the way a reviewer reads one.
 *
 * An edit is a diff of what is replaced against what replaces it, rather than
 * two strings side by side to compare in one's head. A whole file written is
 * shown as the file, coloured by its extension.
 */
export function FileChangeView({ change, className }: { change: FileChange; className?: string }) {
  return (
    <div className={cn('border-border overflow-hidden rounded-md border', className)}>
      <p className="bg-surface-2 border-border text-muted truncate border-b px-2 py-1 font-mono text-xs">
        {change.path}
        {change.kind === 'write' && <span className="ml-2 opacity-70">(whole file)</span>}
      </p>
      {change.kind === 'write' ? (
        <div className="max-h-96 overflow-auto text-xs">
          <CodeBlock language={languageOf(change.path)} code={change.content} />
        </div>
      ) : (
        <div className="max-h-96 overflow-auto">
          {change.edits.map((edit, index) => (
            <div key={index} className={cn(index > 0 && 'border-border border-t')}>
              {edit.all && (
                <p className="text-muted px-2 pt-1 text-[0.6875rem]">every occurrence</p>
              )}
              <Diff lines={diffLines(edit.before, edit.after)} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function Diff({ lines }: { lines: Line[] }) {
  return (
    <pre className="py-1 font-mono text-xs leading-5">
      {lines.map((line, index) => (
        <div
          key={index}
          className={cn(
            'px-2 break-all whitespace-pre-wrap',
            line.kind === '-' && 'bg-danger/10 text-danger',
            line.kind === '+' && 'bg-ok/10 text-ok',
          )}
        >
          <span className="mr-2 inline-block w-2 opacity-60 select-none">{line.kind}</span>
          {line.text || ' '}
        </div>
      ))}
    </pre>
  );
}
