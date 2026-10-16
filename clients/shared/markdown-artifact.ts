/** Markdown is often uploaded as plain text or application/octet-stream. */
export function isMarkdown(mimeType: string, filename: string): boolean {
  const type = mimeType.split(';')[0].trim().toLowerCase();
  return ['text/markdown', 'text/x-markdown', 'application/markdown'].includes(type)
    || /\.(md|markdown|mdown|mkd)$/i.test(filename);
}

export const ARTIFACT_TEXT_LIMIT = 200_000;
