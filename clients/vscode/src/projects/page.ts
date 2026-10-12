import type { Project } from '../api/types';

const escape = (value: string) => value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);

/** A small form using the editor's theme, with multiline project rules. */
export function projectPage(nonce: string, project?: Project): string {
  return `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'nonce-${escape(nonce)}'; script-src 'nonce-${escape(nonce)}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>${project ? 'Edit Project' : 'New Project'}</title>
<style nonce="${escape(nonce)}">
body { color: var(--vscode-foreground); background: var(--vscode-editor-background); font-family: var(--vscode-font-family); padding: 24px; }
form { max-width: 640px; } h1 { font-size: 1.5em; font-weight: 500; }
label { display: block; margin: 20px 0 8px; }
input, textarea { box-sizing: border-box; width: 100%; padding: 8px; color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border, transparent); font: inherit; }
textarea { resize: vertical; } :focus-visible { outline: 1px solid var(--vscode-focusBorder); outline-offset: 2px; }
button { margin: 16px 8px 0 0; padding: 8px 16px; border: 0; color: var(--vscode-button-foreground); background: var(--vscode-button-background); cursor: pointer; font: inherit; }
button:hover { background: var(--vscode-button-hoverBackground); } button:disabled { opacity: .6; cursor: wait; }
#cancel { color: var(--vscode-button-secondaryForeground); background: var(--vscode-button-secondaryBackground); }
#error { color: var(--vscode-errorForeground); } .hint { color: var(--vscode-descriptionForeground); }
</style></head><body>
<form><h1>${project ? 'Edit Project' : 'New Project'}</h1>
<p class="hint">${project ? 'Changes appear on all clients.' : 'Create a project, then start a session in it.'}</p>
<label for="name">Name</label><input id="name" required value="${escape(project?.name ?? '')}">
<label for="description">Description <span class="hint">(optional)</span></label>
<textarea id="description" rows="4">${escape(project?.description ?? '')}</textarea>
${project ? `<label for="instructions">Instructions <span class="hint">(optional)</span></label>
<textarea id="instructions" rows="12">${escape(project.instructions ?? '')}</textarea>
<p class="hint">Rules the agents follow for this project, across providers.</p>` : ''}
<p id="error" role="alert"></p>
<button id="save" type="submit">${project ? 'Save Changes' : 'Create Project'}</button><button id="cancel" type="button">Cancel</button>
</form><script nonce="${escape(nonce)}">
const vscode = acquireVsCodeApi();
const form = document.querySelector('form');
const name = document.getElementById('name');
const description = document.getElementById('description');
const instructions = document.getElementById('instructions');
const error = document.getElementById('error');
const save = document.getElementById('save');
const cancel = document.getElementById('cancel');
const saveLabel = save.textContent;
const busy = (value) => {
  for (const control of form.querySelectorAll('input, textarea, button')) control.disabled = value;
  save.textContent = value ? 'Saving…' : saveLabel;
};
const draft = vscode.getState();
if (draft) { name.value = draft.name; description.value = draft.description; if (instructions) instructions.value = draft.instructions; }
const fields = () => ({ name: name.value, description: description.value, instructions: instructions?.value ?? '' });
form.addEventListener('input', () => vscode.setState(fields()));
form.addEventListener('submit', (event) => {
  event.preventDefault();
  if (save.disabled) return;
  if (!name.value.trim()) { error.textContent = 'Enter a project name.'; name.focus(); return; }
  error.textContent = ''; busy(true);
  vscode.postMessage({ type: 'save', ...fields() });
});
cancel.addEventListener('click', () => vscode.postMessage({ type: 'cancel' }));
window.addEventListener('message', (event) => {
  if (event.data?.type === 'error') { error.textContent = event.data.message; busy(false); }
});
name.focus();
</script></body></html>`;
}
