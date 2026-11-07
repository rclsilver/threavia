import type { InstructionsView } from '../api/types';

export type InstructionsScope = 'global' | 'backend' | 'project';

const escape = (value: string) => value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
const json = (value: unknown) => JSON.stringify(value).replace(/</g, '\\u003c');

/**
 * Edit one level's agent and supervisor instructions. What the levels above
 * say stays beside them, read-only: a level only adds to it. Every text is
 * put in by the page's script as text, never as markup.
 */
export function instructionsPage(nonce: string, label: string, view: InstructionsView, scope: InstructionsScope): string {
  // The project agent text is the Project's `instructions`, which has never
  // had a limit; Core would refuse a longer text everywhere else.
  const agentLimit = scope === 'project' ? '' : ' maxlength="4000"';
  const inherited = (id: string) => scope === 'global' ? '' : `<h3>Inherited from above</h3><pre id="inherited-${id}" class="inherited"></pre>`;
  return `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'nonce-${escape(nonce)}'; script-src 'nonce-${escape(nonce)}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Instructions</title>
<style nonce="${escape(nonce)}">
body { color: var(--vscode-foreground); background: var(--vscode-editor-background); font-family: var(--vscode-font-family); padding: 24px; }
main { max-width: 850px; } h1 { font-size: 1.5em; font-weight: 500; } h3 { font-size: 1em; font-weight: 500; margin: 8px 0 4px; } label { display: block; margin: 20px 0 8px; font-weight: 600; }
textarea { box-sizing: border-box; width: 100%; resize: vertical; padding: 8px; color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border, transparent); font: inherit; }
pre { white-space: pre-wrap; word-break: break-word; margin: 0; padding: 8px; font: inherit; border: 1px solid var(--vscode-panel-border); color: var(--vscode-descriptionForeground); }
button { min-height: 36px; padding: 8px 12px; border: 0; color: var(--vscode-button-foreground); background: var(--vscode-button-background); cursor: pointer; font: inherit; }
button:hover { background: var(--vscode-button-hoverBackground); } button:disabled { opacity: .6; cursor: default; } :focus-visible { outline: 1px solid var(--vscode-focusBorder); outline-offset: 2px; }
.hint { color: var(--vscode-descriptionForeground); } #error { color: var(--vscode-errorForeground); } footer { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 20px; } section { margin-top: 32px; }
</style></head><body><main><h1>Instructions: ${escape(label)}</h1>
<p class="hint">Global → project → backend → session → job, broadest first. Each level adds to the ones above; when two conflict, the narrower one wins. Changes apply from the next job.</p>
<form><label for="agent">Agent instructions</label>
<p class="hint">How to work, added to the agent's system prompt.</p>
<textarea id="agent" rows="10"${agentLimit}></textarea>
${inherited('agent')}
<label for="supervision">Supervisor instructions</label>
<p class="hint">What counts as ordinary work and what to stop. Only the reviewer of a SUPERVISED session reads this.</p>
<textarea id="supervision" rows="6" maxlength="4000"></textarea>
${inherited('supervision')}
<p id="error" role="alert"></p><p id="status" role="status"></p><p id="notice" class="hint" role="status"></p>
<footer><button id="save" type="submit">Save instructions</button><button id="reload" type="button">Reload / discard changes</button>${scope === 'project' ? '<button id="preview" type="button">Preview…</button>' : ''}</footer></form>
${scope === 'project' ? `<section id="preview-panel" hidden><h2 id="preview-title">Preview</h2>
<p class="hint">The saved instructions, as a job started now would read them. The Threavia base prompt, this machine's LOCAL_INSTRUCTIONS and per-job notes are added when a job starts.</p>
<h3>Agent instructions</h3><pre id="preview-agent"></pre><h3>Supervisor instructions</h3><pre id="preview-supervision"></pre></section>` : ''}
<script nonce="${escape(nonce)}">
const vscode = acquireVsCodeApi(); const scope = ${json(scope)}; let view = ${json(view)}; let pending = false;
const field = (id) => document.getElementById(id);
const texts = () => ({ agent: field('agent').value, supervision: field('supervision').value });
const show = (id, text, empty) => { const block = field(id); if (block) block.textContent = text.trim() || empty; };
function controls() { for (const id of ['agent', 'supervision', 'save', 'reload', 'preview']) if (field(id)) field(id).disabled = pending; }
function fill(draft = view.own) {
  field('agent').value = draft.agent; field('supervision').value = draft.supervision;
  show('inherited-agent', view.inherited.agent, 'Nothing inherited.'); show('inherited-supervision', view.inherited.supervision, 'Nothing inherited.'); controls();
}
document.querySelector('form').oninput = () => { vscode.setState({ draft: texts() }); field('status').textContent = ''; };
// A project agent text left as loaded is left out, so Core keeps what it has:
// it is also edited through the project form, and a tab open for long would
// otherwise send back an old copy. The other levels replace both texts.
function changes() {
  const draft = texts();
  return scope === 'project' && draft.agent === view.own.agent ? { supervision: draft.supervision } : draft;
}
function send(type) { pending = true; controls(); field('error').textContent = ''; vscode.postMessage(type === 'save' ? { type, instructions: changes() } : { type }); }
document.querySelector('form').onsubmit = (event) => { event.preventDefault(); if (!pending) send('save'); };
field('reload').onclick = () => send('reload');
if (field('preview')) field('preview').onclick = () => send('preview');
window.addEventListener('message', (event) => {
  const data = event.data;
  if (data?.type === 'saved') { view = data.view; pending = false; fill(); vscode.setState(null); field('notice').textContent = ''; field('status').textContent = data.reloaded ? 'Instructions reloaded.' : 'Instructions saved.'; }
  // Saved elsewhere. Unsaved changes here are kept, and saving them replaces
  // what was saved there, so the page says so instead of reloading.
  else if (data?.type === 'changed') {
    if (pending) return;
    const draft = texts();
    if (draft.agent === view.own.agent && draft.supervision === view.own.supervision) send('reload');
    else field('notice').textContent = 'These instructions changed elsewhere. Saving replaces that change; Reload / discard changes shows it.';
  }
  else if (data?.type === 'preview') {
    pending = false; controls();
    // No preview means the backend choice was dismissed.
    if (!data.preview) return;
    field('preview-title').textContent = 'Preview on ' + data.backend;
    show('preview-agent', data.preview.agent, 'Nothing from the levels.'); show('preview-supervision', data.preview.supervision, 'Nothing from the levels.');
    field('preview-panel').hidden = false;
  }
  else if (data?.type === 'error') { pending = false; controls(); field('error').textContent = (data.preview ? 'Preview not available: ' : 'Instructions not saved: ') + data.message; }
});
const stored = vscode.getState(); fill(stored?.draft ?? view.own);
</script></main></body></html>`;
}
