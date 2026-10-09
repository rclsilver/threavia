import { useQuery } from '@tanstack/react-query';
import { Download, Maximize2 } from 'lucide-react';
import { useEffect, useState } from 'react';

import { fetchBlob, saveBlob } from '@/api/client';
import { ActionError } from '@/components/ui/action-error';
import { Button } from '@/components/ui/button';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { iconOf, kindOf, type Kind } from '@/lib/artifact';
import { bytes, cn } from '@/lib/utils';

const contentPath = (artifactId: string) => `/api/v1/artifacts/${artifactId}/content`;

/** Text files are shown up to here; past it, the person downloads them. */
const TEXT_LIMIT = 200_000;

/**
 * The bytes of an artifact, fetched once with the client's credential: a Blob
 * for an image, its text for a page or a text file.
 */
function useArtifactContent(artifactId: string, kind: Kind, enabled: boolean) {
  const content = useQuery({
    queryKey: ['artifact-content', artifactId],
    queryFn: async () => {
      const blob = await fetchBlob(contentPath(artifactId));
      // Served as bytes, whatever the file is: the type is set again here
      // from what the artifact says it is, so an image renders as one.
      return kind === 'image' ? { blob } : { text: (await blob.text()).slice(0, TEXT_LIMIT) };
    },
    enabled: enabled && kind !== 'other',
    staleTime: Infinity,
  });
  return content;
}

/**
 * An object URL for a Blob, released when it is no longer shown. Made in the
 * same effect that releases it, so a component mounted twice — as React does
 * while developing — never shows a URL already let go.
 */
function useObjectUrl(blob: Blob | undefined, type: string) {
  const [url, setUrl] = useState<string>();
  useEffect(() => {
    if (!blob) return;
    const made = URL.createObjectURL(new Blob([blob], { type }));
    setUrl(made);
    return () => {
      URL.revokeObjectURL(made);
      setUrl(undefined);
    };
  }, [blob, type]);
  return url;
}

/**
 * The artifact itself: an image as an image, a page in a sandbox, a text file
 * as text. A page an agent wrote runs its scripts — a chart needs them — in a
 * frame with an origin of its own, so nothing it does reaches the client.
 */
function Rendering({
  artifactId,
  filename,
  mimeType,
  kind,
  large = false,
}: {
  artifactId: string;
  filename: string;
  mimeType: string;
  kind: Kind;
  large?: boolean;
}) {
  const content = useArtifactContent(artifactId, kind, true);
  const imageType = mimeType.startsWith('image/') ? mimeType : filename.endsWith('.svg') ? 'image/svg+xml' : 'image/png';
  const url = useObjectUrl(content.data && 'blob' in content.data ? content.data.blob : undefined, imageType);

  if (content.isPending) {
    return <div className={cn('bg-surface-2 animate-pulse rounded-md motion-reduce:animate-none', large ? 'h-full' : 'h-48')} />;
  }
  if (content.error) {
    return <ActionError error={content.error} outcome="It could not be shown" recovery="Download it instead." className="text-xs" />;
  }
  if (kind === 'image' && url) {
    return (
      <img
        src={url}
        alt={filename}
        className={cn(
          'bg-surface-2 mx-auto rounded-md object-contain',
          large ? 'max-h-full max-w-full' : 'max-h-80 w-full',
        )}
      />
    );
  }
  const text = content.data && 'text' in content.data ? content.data.text : '';
  if (kind === 'html') {
    return (
      <iframe
        title={filename}
        srcDoc={text}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        className={cn('border-border w-full rounded-md border bg-white', large ? 'h-full' : 'h-80')}
      />
    );
  }
  return (
    <pre
      className={cn(
        'bg-surface-2 border-border overflow-auto rounded-md border p-3 font-mono text-xs whitespace-pre-wrap',
        large ? 'h-full' : 'max-h-64',
      )}
    >
      {text}
    </pre>
  );
}

/** Opens an artifact as large as the window allows. */
export function ArtifactViewer({
  artifactId,
  filename,
  mimeType,
  size,
  title,
  open,
  onOpenChange,
}: {
  artifactId: string;
  filename: string;
  mimeType: string;
  size: number;
  title?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const kind = kindOf(mimeType, filename);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="h-[min(56rem,calc(100dvh-2rem))] w-[min(80rem,calc(100vw-2rem))] gap-3">
        <div className="flex items-start gap-3 pr-8">
          <div className="min-w-0 flex-1">
            <DialogTitle className="truncate">{title || filename}</DialogTitle>
            <DialogDescription className="figures font-mono text-xs">
              {filename} · {bytes(size)}
            </DialogDescription>
          </div>
          <DownloadButton artifactId={artifactId} filename={filename} />
        </div>
        <div className="min-h-0 flex-1">
          {open && <Rendering artifactId={artifactId} filename={filename} mimeType={mimeType} kind={kind} large />}
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function DownloadButton({ artifactId, filename, compact = false }: { artifactId: string; filename: string; compact?: boolean }) {
  const [error, setError] = useState<Error | null>(null);
  const [saving, setSaving] = useState(false);
  return (
    <>
      <Button
        size="sm"
        className={cn(compact && 'max-sm:size-11 max-sm:p-0')}
        disabled={saving}
        aria-label={`Download ${filename}`}
        onClick={() => {
          setSaving(true);
          setError(null);
          saveBlob(contentPath(artifactId), filename)
            .catch((reason: unknown) => setError(reason instanceof Error ? reason : new Error(String(reason))))
            .finally(() => setSaving(false));
        }}
      >
        <Download />
        <span className={cn(compact && 'max-sm:sr-only')}>Download</span>
      </Button>
      <ActionError error={error} outcome="Not downloaded" recovery="Try again." className="basis-full text-xs" />
    </>
  );
}

/**
 * A file the agent published, where it published it: in the conversation,
 * shown rather than named when it can be — the chart, the page, the screenshot
 * — and one click from full size or from disk.
 */
export function ArtifactCard({
  artifactId,
  filename,
  mimeType,
  size,
  title,
}: {
  artifactId: string;
  filename: string;
  mimeType: string;
  size: number;
  title?: string;
}) {
  const kind = kindOf(mimeType, filename);
  const Icon = iconOf(kind);
  const [open, setOpen] = useState(false);
  const previewable = kind === 'image' || kind === 'html';

  return (
    <div data-tour="artifact" className="bg-surface border-border space-y-2 rounded-(--radius-card) border p-2 sm:p-3">
      <div className="flex flex-wrap items-center gap-2">
        <Icon className="text-muted size-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{title || filename}</p>
          <p className="text-muted figures truncate font-mono text-xs">
            {title ? `${filename} · ` : ''}
            {bytes(size)}
          </p>
        </div>
        {kind !== 'other' && (
          <Button variant="ghost" size="sm" className="max-sm:size-11 max-sm:p-0" onClick={() => setOpen(true)} aria-label={`Open ${filename}`}>
            <Maximize2 />
            <span className="max-sm:sr-only">Open</span>
          </Button>
        )}
        <DownloadButton artifactId={artifactId} filename={filename} compact />
      </div>
      {previewable && <Rendering artifactId={artifactId} filename={filename} mimeType={mimeType} kind={kind} />}
      <ArtifactViewer
        artifactId={artifactId}
        filename={filename}
        mimeType={mimeType}
        size={size}
        title={title}
        open={open}
        onOpenChange={setOpen}
      />
    </div>
  );
}
