import { useRef } from 'react';

import type { usePromptAttachments } from '@/lib/prompt-attachments';
import { ArtifactCard } from '@/components/artifact-preview';
import { Button } from '@/components/ui/button';


export function PromptAttachments({ attachments, disabled = false }: { attachments: ReturnType<typeof usePromptAttachments>; disabled?: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  return (
    <div className="space-y-2 p-2" data-tour="prompt-attachments">
      {attachments.files.map((file) => (
        <div key={file.id} className="space-y-1">
          <ArtifactCard artifactId={file.id} filename={file.filename} mimeType={file.mimeType} size={file.size} />
          <Button type="button" size="sm" disabled={disabled || attachments.busy} onClick={() => attachments.remove(file.id)}>Remove {file.filename}</Button>
        </div>
      ))}
      <input ref={input} type="file" multiple hidden onChange={(event) => { void attachments.add(Array.from(event.target.files ?? [])); event.target.value = ''; }} />
      <Button type="button" size="sm" disabled={disabled || attachments.busy} onClick={() => input.current?.click()}>{attachments.busy ? 'Attaching…' : 'Attach files'}</Button>
      {attachments.files.length > 0 && <p className="text-muted text-xs">Files will be sent as a new queued message.</p>}
      {attachments.error && <p role="alert" className="text-danger text-xs">{attachments.error}</p>}
    </div>
  );
}
