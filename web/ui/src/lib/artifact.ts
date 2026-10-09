import { File as FileIcon, FileCode, FileImage, FileText } from 'lucide-react';

/** What an artifact is, as far as showing it goes. */
export type Kind = 'image' | 'html' | 'text' | 'other';

export function kindOf(mimeType: string, filename: string): Kind {
  const type = mimeType.toLowerCase();
  const extension = filename.split('.').pop()?.toLowerCase() ?? '';
  if (type.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(extension)) return 'image';
  if (type.startsWith('text/html') || ['html', 'htm'].includes(extension)) return 'html';
  if (type.startsWith('text/') || /json|yaml|xml|csv/.test(type) || ['md', 'txt', 'csv', 'json', 'yaml', 'yml', 'log'].includes(extension)) {
    return 'text';
  }
  return 'other';
}

export function iconOf(kind: Kind) {
  return kind === 'image' ? FileImage : kind === 'html' ? FileCode : kind === 'text' ? FileText : FileIcon;
}
