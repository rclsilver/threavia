import { describe, expect, it } from 'vitest';

import { mediaType } from '../../shared/media';
import { CoreClient } from '../src/api/client';
import { effectsOf } from '../src/api/events';
import type { Event } from '../src/api/types';
import { artifactKind } from '../src/conversation/format';
import { conversationPage } from '../src/conversation/html';

describe('media in conversations', () => {
  it('keeps media types usable for generic uploads and mixed-case names', () => {
    expect(mediaType('application/octet-stream', 'PHOTO.JPEG')).toBe('image/jpeg');
    expect(mediaType('', 'voice.m4a')).toBe('audio/mp4');
    expect(mediaType('audio/wav; charset=binary', 'sound')).toBe('audio/wav');
    expect(mediaType('image/png" onload="evil', 'file')).toBe('application/octet-stream');
    expect(artifactKind('', 'voice.WAV')).toBe('audio');
    expect(artifactKind('', 'recording.webm')).toBe('video');
  });

  it('uploads binary files with credentials and passes their references in both sends', async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    const client = new CoreClient({
      baseUrl: () => 'https://core.example',
      identity: { clientId: 'editor', clientName: () => 'VS Code' },
      authorization: () => Promise.resolve('Bearer test-token'),
      fetch: (url, init) => { calls.push({ url: typeof url === 'string' ? url : url instanceof URL ? url.href : url.url, init }); return Promise.resolve(Response.json({ id: 'artifact' })); },
    });
    await client.uploadArtifact('project', 'voice.wav', '', new Uint8Array([0, 255, 1]), 'session');
    expect(calls[0].url).toContain('filename=voice.wav&sessionId=session');
    const headers = new Headers(calls[0].init?.headers);
    expect(headers.get('Authorization')).toBe('Bearer test-token');
    expect(headers.get('Content-Type')).toBe('audio/wav');
    expect(new Uint8Array(await (calls[0].init?.body as Blob).arrayBuffer())).toEqual(new Uint8Array([0, 255, 1]));
    await client.startSession({ projectId: 'project', backendInstanceId: 'backend', workingDirectoryId: null, message: '', artifactIds: ['artifact'] });
    await client.postMessage('session', '', 'QUEUE', ['artifact']);
    for (const call of calls.slice(1)) expect((JSON.parse(call.init?.body as string) as { artifactIds: string[] }).artifactIds).toEqual(['artifact']);
  });

  it('updates the artifact view for project-scoped uploads and deletions', () => {
    for (const type of ['artifact.created', 'artifact.deleted']) {
      expect(effectsOf({ type, projectId: 'project' } as Event)).toEqual([{ kind: 'artifacts', projectId: 'project' }]);
    }
  });

  it('allows local media playback while leaving network access blocked', () => {
    const html = conversationPage({ cspSource: 'webview:', scriptUri: 'webview:script.js', styleUri: 'webview:style.css', coreId: 'core', sessionId: 'session', nonce: 'nonce' });
    expect(html).toContain('media-src data:');
    expect(html).toContain('default-src &#39;none&#39;');
    expect(html).not.toContain('connect-src');
  });
});
