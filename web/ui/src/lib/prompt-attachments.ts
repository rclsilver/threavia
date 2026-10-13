import { mediaType } from '../../../../clients/shared/media';
import { useRef, useState } from 'react';
import type { ClipboardEvent } from 'react';
import { useUploadArtifact } from '@/api/queries';
import type { Artifact } from '@/api/types';

export function usePromptAttachments(projectId: string, disabled = false) {
  const upload = useUploadArtifact(projectId);
  const [files, setFiles] = useState<Artifact[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
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
  return { files, busy, error, add, paste, clear: () => setFiles([]), remove: (id: string) => setFiles((previous) => previous.filter((file) => file.id !== id)) };
}
