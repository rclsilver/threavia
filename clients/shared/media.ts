/** MIME types for local files and previews, without parameters or HTML syntax. */
export function mediaType(mimeType: string, filename: string): string {
  const type = mimeType.split(';')[0].trim().toLowerCase();
  if (/^(image|audio|video)\/[a-z0-9.+-]+$/.test(type)) return type;
  const extension = filename.split('.').pop()?.toLowerCase() ?? '';
  const types: Record<string, string> = {
    png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif',
    webp: 'image/webp', svg: 'image/svg+xml', avif: 'image/avif', bmp: 'image/bmp',
    mp3: 'audio/mpeg', wav: 'audio/wav', ogg: 'audio/ogg', flac: 'audio/flac',
    m4a: 'audio/mp4', aac: 'audio/aac', mp4: 'video/mp4', webm: 'video/webm', mov: 'video/quicktime',
  };
  return types[extension] ?? 'application/octet-stream';
}
