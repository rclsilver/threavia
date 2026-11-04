import { describe, expect, it } from 'vitest';

import { commandInsert, composerCommands, composerToken, matchCommands, modelPill, promptHistory, recall, renameRequest, replaceToken, skillMention } from '../../shared/composer';

describe('composer suggestions', () => {
  it('offers commands only for the first word and files for any @ word', () => {
    expect(composerToken('/mo', 3)).toEqual({ kind: 'command', query: 'mo', start: 0, end: 3 });
    expect(composerToken('  /re', 5)).toEqual({ kind: 'command', query: 're', start: 2, end: 5 });
    expect(composerToken('read /etc/hosts', 15)).toBeUndefined();
    expect(composerToken('look at @src/ma please', 15)).toEqual({ kind: 'file', query: 'src/ma', start: 8, end: 15 });
    expect(composerToken('@', 1)).toEqual({ kind: 'file', query: '', start: 0, end: 1 });
    expect(composerToken('mail me@example.com', 19)).toBeUndefined();
    // The whole word is replaced, also what follows the caret.
    expect(composerToken('@srcmain', 4)).toEqual({ kind: 'file', query: 'src', start: 0, end: 8 });
    expect(composerToken('plain words', 5)).toBeUndefined();
  });

  it('replaces the token and leaves the caret after one space', () => {
    const token = composerToken('see @we now', 7)!;
    expect(replaceToken('see @we now', token, '@web/ui/')).toEqual({ text: 'see @web/ui/ now', caret: 13 });
    const last = composerToken('see @we', 7)!;
    expect(replaceToken('see @we', last, '@web/')).toEqual({ text: 'see @web/ ', caret: 10 });
    const command = composerToken('/stop then', 5)!;
    expect(replaceToken('/stop then', command, '')).toEqual({ text: 'then', caret: 0 });
  });

  it('names a Skill the way each provider reads it', () => {
    expect(skillMention('claude', 'review')).toBe('/threavia-skills:review');
    expect(skillMention('codex', 'review')).toBe('$review');
    expect(skillMention('ollama', 'review')).toBe('$review');
    expect(skillMention(undefined, 'review')).toBeUndefined();
  });

  it('lists Skills once, the models and efforts, and only the deliveries offered', () => {
    const commands = composerCommands({
      provider: 'codex',
      skills: [{ name: 'review', description: 'Review a diff' }, { name: 'review' }],
      models: [
        { id: 'gpt-a', name: 'GPT A', efforts: ['low', 'high'], defaultEffort: 'low' },
        { id: 'gpt-b', name: 'gpt-b', efforts: ['medium'] },
      ],
      current: { model: 'gpt-a', effort: 'high' },
      deliveries: ['NEXT'],
      running: true,
    });
    expect(commands.map((command) => command.id)).toEqual(['skill:review', 'model:gpt-a', 'model:gpt-b', 'effort:low', 'effort:high', 'next', 'stop']);
    // Changing model keeps the effort when the new model has it.
    expect(commands.find((command) => command.id === 'model:gpt-a')).toMatchObject({ model: 'gpt-a', effort: 'high' });
    expect(commands.find((command) => command.id === 'model:gpt-b')).toMatchObject({ model: 'gpt-b', effort: 'medium', detail: undefined });
    // Without a provider that reads Skills, none is offered.
    expect(composerCommands({ skills: [{ name: 'review' }] })).toEqual([]);
  });

  it('matches commands by their start first, then any word, then the description', () => {
    const commands = composerCommands({
      provider: 'claude',
      skills: [{ name: 'opus-notes' }, { name: 'deploy', description: 'Ship to production' }],
      models: [{ id: 'claude-opus-5-5', name: 'Opus', efforts: [] }],
      running: true,
    });
    expect(matchCommands(commands, 'op').map((command) => command.id)).toEqual(['skill:opus-notes', 'model:claude-opus-5-5', 'stop']);
    expect(matchCommands(commands, 'opus').map((command) => command.id)).toEqual(['skill:opus-notes', 'model:claude-opus-5-5']);
    expect(matchCommands(commands, 'production').map((command) => command.id)).toEqual(['skill:deploy']);
    expect(matchCommands(commands, '').length).toBe(commands.length);
  });

  it('recalls what the person wrote, newest first, only from an empty field', () => {
    const history = promptHistory([
      { text: 'first', mine: true },
      { text: 'from a schedule', mine: false },
      { text: 'second', mine: true },
      { text: 'first', mine: true },
    ]);
    expect(history).toEqual(['first', 'second']);
    expect(recall(history, -1, 'older', 'typing')).toBeUndefined();
    const one = recall(history, -1, 'older', '')!;
    expect(one).toEqual({ position: 0, text: 'first' });
    const two = recall(history, one.position, 'older', one.text)!;
    expect(two).toEqual({ position: 1, text: 'second' });
    expect(recall(history, two.position, 'older', two.text)).toBeUndefined();
    // Editing a recalled message makes it the person's own again.
    expect(recall(history, two.position, 'older', 'second, edited')).toBeUndefined();
    expect(recall(history, two.position, 'newer', two.text)).toEqual({ position: 0, text: 'first' });
    expect(recall(history, 0, 'newer', 'first')).toEqual({ position: -1, text: '' });
    expect(recall(history, -1, 'newer', '')).toBeUndefined();
  });

  it('offers /rename only for an existing session, and writes it out for the name', () => {
    expect(composerCommands({ skills: [] }).some((command) => command.kind === 'rename')).toBe(false);
    const rename = composerCommands({ skills: [], renamable: true }).find((command) => command.kind === 'rename')!;
    expect(rename.id).toBe('rename');
    const token = composerToken('/ren', 4)!;
    expect(replaceToken('/ren', token, commandInsert(rename))).toEqual({ text: '/rename ', caret: 8 });
    expect(commandInsert(composerCommands({ skills: [], running: true })[0])).toBe('');
  });

  it('reads a /rename message as the new name, never as a message for the agent', () => {
    expect(renameRequest('/rename  Puppet   audit \n')).toBe('Puppet audit');
    expect(renameRequest('/rename')).toBe('');
    expect(renameRequest('/rename   ')).toBe('');
    expect(renameRequest('/renamed it')).toBeUndefined();
    expect(renameRequest('please /rename this')).toBeUndefined();
    expect(renameRequest('Look at the build')).toBeUndefined();
  });
});

describe('model pill', () => {
  const catalog = { defaultModel: 'sonnet', defaultEffort: 'high', models: [
    { id: 'sonnet', name: 'Sonnet', efforts: ['low', 'high'], defaultEffort: 'high' },
    { id: 'opus', name: 'Opus', efforts: ['low', 'max'], defaultEffort: 'low' },
    { id: 'haiku', name: 'Haiku', efforts: [] },
  ] };
  it('names the default, a choice, and one that went away', () => {
    expect(modelPill(catalog, '', '')).toEqual({ name: 'Default · Sonnet', effort: 'high', unavailable: false, efforts: ['low', 'high'] });
    expect(modelPill(catalog, 'opus', 'max')).toMatchObject({ name: 'Opus', effort: 'max' });
    expect(modelPill(catalog, 'opus', '')).toMatchObject({ effort: 'low' });
    expect(modelPill(catalog, 'haiku', '')).toMatchObject({ name: 'Haiku', effort: undefined, efforts: [] });
    expect(modelPill(catalog, 'gone', 'low')).toMatchObject({ name: 'gone (unavailable)', unavailable: true, efforts: [] });
    expect(modelPill(undefined, '', '')).toMatchObject({ name: 'Default', effort: undefined });
  });
});
