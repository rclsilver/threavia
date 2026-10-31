import { useRef } from 'react';
import { File, Image, LoaderCircle, Paperclip, X } from 'lucide-react';

import type { usePromptAttachments } from '@/lib/prompt-attachments';
import { Button } from '@/components/ui/button';
import { bytes, cn } from '@/lib/utils';

type Attachments = ReturnType<typeof usePromptAttachments>;

/**
 * The files going with the message, one small tag each, above the field. It
 * takes no room until there is something attached or something went wrong.
 */
export function AttachedFiles({ attachments, disabled = false, className }: { attachments: Attachments; disabled?: boolean; className?: string }) {
  if (attachments.files.length === 0 && !attachments.error) return null;
  return (
    <div className={cn('space-y-1', className)} data-tour="prompt-attachments">
      {attachments.files.length > 0 && (
        <ul className="flex flex-wrap gap-1.5" aria-label="Attached files">
          {attachments.files.map((file) => {
            const Icon = file.mimeType.startsWith('image/') ? Image : File;
            return (
              <li key={file.id} className="bg-surface-2 border-border flex max-w-full min-w-0 items-center gap-1.5 rounded-md border py-0.5 pr-0.5 pl-2 text-xs">
                <Icon className="text-muted size-3.5 shrink-0" />
                <span className="min-w-0 truncate">{file.filename}</span>
                <span className="text-muted shrink-0">· {bytes(file.size)}</span>
                <button
                  type="button"
                  disabled={disabled || attachments.busy}
                  onClick={() => attachments.remove(file.id)}
                  aria-label={`Remove ${file.filename}`}
                  className="text-muted hover:text-text grid size-11 shrink-0 place-items-center rounded sm:size-6"
                >
                  <X className="size-3.5" />
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {attachments.error && <p role="alert" className="text-danger text-xs">{attachments.error}</p>}
    </div>
  );
}

/** The paperclip: pick files to send. Pasting or dropping them works as well. */
export function AttachButton({ attachments, disabled = false }: { attachments: Attachments; disabled?: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  const label = attachments.busy ? 'Attaching…' : 'Attach files';
  return (
    <>
      <input ref={input} type="file" multiple hidden onChange={(event) => { void attachments.add(Array.from(event.target.files ?? [])); event.target.value = ''; }} />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="text-muted size-11 shrink-0 sm:size-7"
        title={`${label} — or paste them, or drop them on the message. Files go as a new queued message.`}
        aria-label={label}
        aria-busy={attachments.busy}
        disabled={disabled || attachments.busy}
        onClick={() => input.current?.click()}
      >
        {attachments.busy ? <LoaderCircle className="animate-spin" /> : <Paperclip />}
      </Button>
    </>
  );
}
