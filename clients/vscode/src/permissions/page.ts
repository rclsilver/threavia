import type { PolicyView } from '../api/types';

const escape = (value: string) => value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
const json = (value: unknown) => JSON.stringify(value).replace(/</g, '\\u003c');

/** Edit only this level's own policy; inherited rules remain separate. */
export function permissionsPage(nonce: string, label: string, view: PolicyView): string {
  return `<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'nonce-${escape(nonce)}'; script-src 'nonce-${escape(nonce)}';">
<meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Permissions</title>
<style nonce="${escape(nonce)}">
body { color: var(--vscode-foreground); background: var(--vscode-editor-background); font-family: var(--vscode-font-family); padding: 24px; }
main { max-width: 850px; } h1 { font-size: 1.5em; font-weight: 500; } label { display: block; margin: 12px 0; }
input, select, textarea { padding: 8px; color: var(--vscode-input-foreground); background: var(--vscode-input-background); border: 1px solid var(--vscode-input-border, transparent); font: inherit; }
textarea { box-sizing: border-box; width: 100%; resize: vertical; } input[type=number] { width: 100px; } fieldset { border: 1px solid var(--vscode-panel-border); margin: 20px 0; }
button { min-height: 36px; padding: 8px 12px; border: 0; color: var(--vscode-button-foreground); background: var(--vscode-button-background); cursor: pointer; font: inherit; }
button:hover { background: var(--vscode-button-hoverBackground); } button:disabled { opacity: .6; cursor: default; } :focus-visible { outline: 1px solid var(--vscode-focusBorder); outline-offset: 2px; }
.hint { color: var(--vscode-descriptionForeground); } #error { color: var(--vscode-errorForeground); } .rule { display: flex; gap: 8px; flex-wrap: wrap; margin: 12px 0; } .rule input { min-width: 120px; flex: 1; } footer { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 20px; }
</style></head><body><main><h1>Permissions: ${escape(label)}</h1>
<p class="hint">Instance → backend → project → session → job. Explicit refusals from a parent also apply here. A provider may apply changes only from the next message.</p>
<p id="origin"></p><button id="override" type="button">Override at this level</button>
<form><fieldset id="fields"><legend>This level's permissions</legend>
<label>Mode <select id="mode"><option value="INTERACTIVE">Interactive — asks before acting</option><option value="GUARDED">Guarded — asks outside allowed actions</option><option value="SUPERVISED">Supervised — a reviewer answers</option><option value="AUTONOMOUS">Autonomous — acts until a limit</option></select></label>
${[['allowFilesystemWrite', 'Write files'], ['allowGitCommit', 'Commit'], ['allowGitPush', 'Push'], ['allowNetwork', 'Network']].map(([key, name]) => `<label><input type="checkbox" id="${key}"> ${name}</label>`).join('')}
<label id="supervision-label">What the reviewer should know<textarea id="supervision" rows="5" maxlength="4000"></textarea></label>
<label>Maximum duration (seconds; 0 means unlimited) <input id="maxDurationSeconds" type="number" min="0" step="1" required></label>
<label>Maximum actions (0 means unlimited) <input id="maxActions" type="number" min="0" step="1" required></label>
<p class="hint">Autonomous mode requires at least one limit.</p>
<h2>Rules set here</h2><div id="rules"></div><button id="add" type="button">Add rule</button>
</fieldset><h2>Inherited rules</h2><div id="parent" class="hint"></div>
<p id="error" role="alert"></p><p id="status" role="status"></p>
<footer><button id="save" type="submit">Save permissions</button><button id="reset" type="button">Follow parent defaults</button><button id="reload" type="button">Reload / discard changes</button></footer></form>
<script nonce="${escape(nonce)}">
const vscode = acquireVsCodeApi(); let view = ${json(view)}; let editing = !view.inherited; let pending = false;
const field = (id) => document.getElementById(id);
const switches = ['allowFilesystemWrite','allowGitCommit','allowGitPush','allowNetwork'];
const options = (values, chosen) => values.map(([value, label]) => { const option = document.createElement('option'); option.value = value; option.textContent = label; option.selected = value === chosen; return option; });
function addRule(rule = {effect:'ASK', capability:'SHELL', match:'', note:''}) {
  const row = document.createElement('div'); row.className = 'rule';
  const effect = document.createElement('select'); effect.setAttribute('aria-label','Effect'); effect.append(...options([['ALLOW','Allow'],['ASK','Ask'],['DENY','Refuse']], rule.effect));
  const capability = document.createElement('select'); capability.setAttribute('aria-label','Capability'); capability.append(...options([['SHELL','Command'],['FILE_READ','Read file'],['FILE_WRITE','Write file'],['NETWORK','Network'],['GIT_COMMIT','Commit'],['GIT_PUSH','Push'],['TOOL','Named tool']], rule.capability));
  const match = document.createElement('input'); match.value = rule.match ?? ''; match.placeholder = 'Pattern, e.g. kubectl get *'; match.maxLength = 512; match.setAttribute('aria-label','Match');
  const note = document.createElement('input'); note.value = rule.note ?? ''; note.placeholder = 'Why (optional)'; note.maxLength = 512; note.setAttribute('aria-label','Note');
  const remove = document.createElement('button'); remove.type = 'button'; remove.textContent = 'Remove'; remove.onclick = () => { row.remove(); persist(); };
  row.append(effect, capability, match, note, remove); field('rules').append(row);
}
function policy() {
  const result = { mode:field('mode').value, supervision:field('supervision').value, maxDurationSeconds:Number(field('maxDurationSeconds').value), maxActions:Number(field('maxActions').value), rules:[...field('rules').children].map(row => { const [effect, capability, match, note] = row.querySelectorAll('select,input'); return {effect:effect.value, capability:capability.value, match:match.value, note:note.value}; }) };
  for (const key of switches) result[key] = field(key).checked; return result;
}
function persist() { vscode.setState({draft:policy(), editing, dirty:true}); field('status').textContent = ''; }
function controls() {
  field('fields').disabled = !editing || pending; field('save').disabled = !editing || pending; field('reset').disabled = view.inherited || pending; field('reload').disabled = pending;
  field('override').hidden = editing; field('override').disabled = pending; field('supervision-label').hidden = field('mode').value !== 'SUPERVISED';
}
function fill(draft = view.own ?? {...view.effective, rules:[]}) {
  field('mode').value = draft.mode; for (const key of switches) field(key).checked = draft[key];
  for (const key of ['supervision','maxDurationSeconds','maxActions']) field(key).value = draft[key] ?? (key === 'supervision' ? '' : 0);
  field('rules').replaceChildren(); for (const rule of draft.rules ?? []) addRule(rule);
  field('origin').textContent = view.inherited ? 'Following parent defaults. Override to make changes here.' : 'Permissions set at this level.';
  field('parent').replaceChildren(); for (const rule of view.parent.rules ?? []) { const line = document.createElement('p'); line.textContent = rule.effect + ' · ' + rule.capability + ' · ' + (rule.match || 'all') + (rule.note ? ' — ' + rule.note : ''); field('parent').append(line); }
  if (!(view.parent.rules ?? []).length) field('parent').textContent = 'No inherited rules.'; controls();
}
field('override').onclick = () => { editing = true; controls(); persist(); };
field('add').onclick = () => { if (field('rules').children.length >= 100) return; addRule(); persist(); };
document.querySelector('form').oninput = () => { controls(); persist(); };
function send(type) { pending = true; controls(); field('error').textContent = ''; vscode.postMessage({type, policy:policy()}); }
document.querySelector('form').onsubmit = (event) => { event.preventDefault(); if (pending || !editing) return; const p = policy(); if (p.mode === 'AUTONOMOUS' && !p.maxDurationSeconds && !p.maxActions) {field('error').textContent = 'Set a duration or action limit for autonomous mode.'; return;} if (p.rules.some(rule => rule.capability === 'TOOL' && !rule.match.trim())) {field('error').textContent = 'Name the tool in every named-tool rule.'; return;} send('save'); };
field('reset').onclick = () => send('reset'); field('reload').onclick = () => send('reload');
window.addEventListener('message', (event) => { if (event.data?.type === 'saved') {view = event.data.view; editing = !view.inherited; pending = false; fill(); vscode.setState(null); field('status').textContent = event.data.reloaded ? 'Permissions reloaded.' : 'Permissions saved.';} else if (event.data?.type === 'error') {pending = false; field('error').textContent = 'Permissions not saved: ' + event.data.message; controls();} });
const stored = vscode.getState(); if (stored?.dirty) {editing = stored.editing; fill(stored.draft);} else fill();
</script></main></body></html>`;
}
