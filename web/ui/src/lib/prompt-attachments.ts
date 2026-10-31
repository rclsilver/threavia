import { mediaType } from '../../../../clients/shared/media';
import { useRef, useState } from 'react';
import type { ClipboardEvent, DragEvent } from 'react';
import { useUploadArtifact } from '@/api/queries';
import type { Artifact } from '@/api/types';

/** A file already uploaded, as a message that carried it names it. */
export type AttachedFile = Pick<Artifact, 'id' | 'filename' | 'mimeType' | 'size'>;

const carriesFiles = (event: DragEvent) => Array.from(event.dataTransfer.types).includes('Files');

export function usePromptAttachments(projectId: string, disabled = false) {
  const upload = useUploadArtifact(projectId);
  const [files, setFiles] = useState<AttachedFile[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [dragging, setDragging] = useState(false);
  const locked = useRef(false);
  const add = async (incoming: File[]) => {
    if (disabled || locked.current || incoming.length === 0) return;
    if (files.length + incoming.length > 8 || [...files, ...incoming].reduce((sum, file) => sum + file.size, 0) > 3 * 1024 * 1024) {
      setError('Attach up to 8 files, totalling at most 3 MiB.');
      return;
    }
    locked.current = true;
    setBusy(true);
    setError('');
    try {
      for (const file of incoming) {
        const artifact = await upload.mutateAsync(file.type ? file : new File([file], file.name, {type: mediaType('', file.name)}));
        setFiles((previous) => [...previous, artifact]);
      }
    } catch (reason) {
      setError(`Not attached: ${reason instanceof Error ? reason.message : String(reason)}`);
    } finally {
      locked.current = false;
      setBusy(false);
    }
  };
  const paste = (event: ClipboardEvent) => {
    const incoming = Array.from(event.clipboardData.files);
    if (incoming.length) { event.preventDefault(); void add(incoming); }
  };
  // Files dropped anywhere on the message panel are attached, as pasted ones
  // are. Text dragged in is left to the field.
  const drop = {
    onDragOver: (event: DragEvent) => {
      if (disabled || !carriesFiles(event)) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = 'copy';
      setDragging(true);
    },
    onDragLeave: (event: DragEvent) => {
      if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false);
    },
    onDrop: (event: DragEvent) => {
      setDragging(false);
      if (disabled || !carriesFiles(event)) return;
      event.preventDefault();
      void add(Array.from(event.dataTransfer.files));
    },
  };
  return {
    files, busy, error, dragging, add, paste, drop,
    clear: () => setFiles([]),
    remove: (id: string) => setFiles((previous) => previous.filter((file) => file.id !== id)),
    /** Puts back the files of a message taken back to be rewritten. */
    restore: (restored: AttachedFile[]) => setFiles((previous) => [...previous, ...restored.filter((file) => !previous.some((kept) => kept.id === file.id))].slice(0, 8)),
  };
}
