import { File as FileIcon, FileCode, FileImage, FileText, FileAudio, FileVideo } from 'lucide-react';

/** What an artifact is, as far as showing it goes. */
export type Kind = 'image' | 'audio' | 'video' | 'html' | 'text' | 'other';

export function kindOf(mimeType: string, filename: string): Kind {
  const type = mimeType.toLowerCase();
  const extension = filename.split('.').pop()?.toLowerCase() ?? '';
  if (type.startsWith('image/') || ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(extension)) return 'image';
  if (type.startsWith('audio/') || ['mp3', 'wav', 'ogg', 'flac', 'm4a', 'aac'].includes(extension)) return 'audio';
  if (type.startsWith('video/') || ['mp4', 'webm', 'mov'].includes(extension)) return 'video';
  if (type.startsWith('text/html') || ['html', 'htm'].includes(extension)) return 'html';
  if (type.startsWith('text/') || /json|yaml|xml|csv/.test(type) || ['md', 'txt', 'csv', 'json', 'yaml', 'yml', 'log'].includes(extension)) {
    return 'text';
  }
  return 'other';
}

export function iconOf(kind: Kind) {
  return kind === 'audio' ? FileAudio : kind === 'video' ? FileVideo : kind === 'image' ? FileImage : kind === 'html' ? FileCode : kind === 'text' ? FileText : FileIcon;
}
