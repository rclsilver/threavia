// Threavia web client.
//
// It is a stateless view and controller of Core state: everything it shows comes
// from a snapshot plus the global event stream, and closing it never stops agent
// execution. There is no build step and no framework on purpose.

// CHANNEL tells Core which kind of client this is, so work followed here does
// not also make an unrelated device ring (specification section 6).
const CHANNEL = 'web';

const api = {
  async call(method, path, body, headers = {}) {
    const response = await fetch(path, {
      method,
      headers: {
        'X-Threavia-Channel': CHANNEL,
        ...(body ? { 'Content-Type': 'application/json' } : {}),
        ...headers,
      },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (response.status === 204) return null;

    const payload = await response.json().catch(() => null);
    if (!response.ok) {
      throw new Error(payload?.error?.message || `${method} ${path} failed (${response.status})`);
    }
    return payload;
  },
  get: (path) => api.call('GET', path),
  post: (path, body, headers) => api.call('POST', path, body ?? {}, headers),
};

// state holds only what is needed to render; Core remains the source of truth.
const state = {
  projects: [],
  backends: [],
  sessions: [],
  directories: [],
  projectId: null,
  sessionId: null,
  jobs: new Map(),
  tab: 'tasks',
  cursor: 0,
  stream: null,
};

const el = (id) => document.getElementById(id);

function toast(message) {
  const node = el('toast');
  node.textContent = message;
  node.classList.remove('hidden');
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => node.classList.add('hidden'), 6000);
}

function show(view) {
  for (const name of ['draft', 'project-view', 'session', 'empty']) {
    el(name).classList.toggle('hidden', name !== view);
  }
}

function when(value) {
  if (!value) return '';
  const date = new Date(value);
  return date.toLocaleString(undefined, { dateStyle: 'short', timeStyle: 'short' });
}

// ---------------------------------------------------------------- bootstrap

async function boot() {
  wireEvents();
  await Promise.all([loadProjects(), loadBackends()]);
  openStream();
}

async function loadProjects() {
  const { items } = await api.get('/api/v1/projects');
  state.projects = items;

  const select = el('project');
  select.innerHTML = '';
  for (const project of items) {
    const option = document.createElement('option');
    option.value = project.id;
    option.textContent = project.name;
    select.append(option);
  }

  if (items.length === 0) {
    state.projectId = null;
    el('sessions').innerHTML = '';
    show('empty');
    return;
  }

  state.projectId = items.some((p) => p.id === state.projectId) ? state.projectId : items[0].id;
  select.value = state.projectId;
  await loadProjectContent();
}

async function loadProjectContent() {
  if (!state.projectId) return;
  const [sessions, directories] = await Promise.all([
    api.get(`/api/v1/projects/${state.projectId}/sessions`),
    api.get(`/api/v1/projects/${state.projectId}/directories`),
  ]);
  state.sessions = sessions.items;
  state.directories = directories.items;
  renderSessions();
}

async function loadBackends() {
  const { items } = await api.get('/api/v1/backends');
  state.backends = items;

  const list = el('backends');
  list.innerHTML = '';
  for (const backend of items) {
    const item = document.createElement('li');
    const name = document.createElement('span');
    name.textContent = backend.name;
    const status = document.createElement('span');
    status.className = `pill ${backendPill(backend.operationalStatus)}`;
    status.textContent = backend.operationalStatus.toLowerCase();
    item.append(name, status);

    // Why it is in that state. A status alone sends someone to the logs; the
    // condition says which thing is wrong (specification section 7).
    for (const condition of backend.conditions ?? []) {
      const why = document.createElement('span');
      why.className = 'condition';
      why.textContent = condition.message || condition.reason || condition.type;
      why.title = `${condition.type}=${condition.status}`;
      item.append(why);
    }
    list.append(item);
  }
}

function backendPill(status) {
  if (status === 'READY') return 'pill-ok';
  if (status === 'DEGRADED') return 'pill-warn';
  return 'pill-muted';
}

function renderSessions() {
  const list = el('sessions');
  list.innerHTML = '';

  for (const session of state.sessions) {
    const item = document.createElement('li');
    item.dataset.id = session.id;
    item.classList.toggle('active', session.id === state.sessionId);

    const title = document.createElement('span');
    title.className = 'title';
    title.textContent = session.title || 'Untitled session';
    const stamp = document.createElement('span');
    stamp.className = 'when';
    stamp.textContent = when(session.updatedAt);

    item.append(title, stamp);
    item.addEventListener('click', () => openSession(session.id));
    list.append(item);
  }
}

// ------------------------------------------------------------------- drafts

function openDraft() {
  if (!state.projectId) {
    toast('Create a project first.');
    return;
  }

  const backends = el('draft-backend');
  backends.innerHTML = '';
  for (const backend of state.backends) {
    const option = document.createElement('option');
    option.value = backend.id;
    option.textContent = `${backend.name} (${backend.operationalStatus.toLowerCase()})`;
    backends.append(option);
  }

  const directories = el('draft-directory');
  directories.innerHTML = '<option value="">— none —</option>';
  for (const directory of state.directories) {
    const option = document.createElement('option');
    option.value = directory.id;
    option.textContent = directory.name;
    directories.append(option);
  }

  state.sessionId = null;
  renderSessions();
  el('draft-message').value = '';
  show('draft');
}

async function sendDraft() {
  const message = el('draft-message').value.trim();
  if (!message) return;

  const button = el('draft-send');
  button.disabled = true;
  try {
    // The first send is one atomic operation: it creates the Session, its Run,
    // its Job and the message, or nothing at all.
    const result = await api.post('/api/v1/sessions/start', {
      projectId: state.projectId,
      backendInstanceId: el('draft-backend').value,
      workingDirectoryId: el('draft-directory').value || null,
      message,
    }, { 'Idempotency-Key': crypto.randomUUID() });

    await loadProjectContent();
    await openSession(result.session.id);
  } catch (error) {
    toast(error.message);
  } finally {
    button.disabled = false;
  }
}


// ------------------------------------------------------------------ project
//
// Everything a Project carries beyond its Sessions: the memory an agent reads
// and writes across sessions, the Skills it may use, the rules it follows, and
// the trail of what was decided about execution.

async function openProject() {
  if (!state.projectId) {
    toast('Create a project first.');
    return;
  }

  state.sessionId = null;
  renderSessions();
  el('project-name').textContent = currentProject()?.name ?? 'Project';
  show('project-view');
  await loadTab(state.tab);
}

function currentProject() {
  return state.projects.find((project) => project.id === state.projectId);
}

function selectTab(tab) {
  state.tab = tab;
  for (const button of el('project-tabs').children) {
    button.classList.toggle('active', button.dataset.tab === tab);
  }
  for (const pane of document.querySelectorAll('#project-view .pane')) {
    pane.classList.toggle('hidden', pane.dataset.pane !== tab);
  }
  loadTab(tab).catch((error) => toast(error.message));
}

async function loadTab(tab) {
  switch (tab) {
    case 'tasks': return loadTasks();
    case 'decisions': return loadDecisions();
    case 'artifacts': return loadArtifacts();
    case 'skills': return loadSkills();
    case 'instructions': return loadInstructions();
    case 'audit': return loadAudit();
    default: return undefined;
  }
}

// record builds one list entry: a title line, an optional body, and a muted
// meta line. Every project list uses it, so they read the same way.
function record({ title, badge, body, meta, actions = [] }) {
  const item = document.createElement('li');

  const head = document.createElement('div');
  head.className = 'record-head';
  const name = document.createElement('span');
  name.className = 'record-title';
  name.textContent = title;
  head.append(name);
  if (badge) {
    const pill = document.createElement('span');
    pill.className = `pill ${badge.className ?? 'pill-muted'}`;
    pill.textContent = badge.text;
    head.append(pill);
  }
  for (const action of actions) {
    const button = document.createElement('button');
    button.className = action.className ?? 'link';
    button.textContent = action.label;
    button.addEventListener('click', action.run);
    head.append(button);
  }
  item.append(head);

  if (body) {
    const text = document.createElement('p');
    text.className = 'record-body';
    text.textContent = body;
    item.append(text);
  }
  if (meta) {
    const line = document.createElement('p');
    line.className = 'record-meta';
    line.textContent = meta;
    item.append(line);
  }
  return item;
}

function fill(listId, items, render) {
  const list = el(listId);
  list.innerHTML = '';
  if (items.length === 0) {
    const empty = document.createElement('li');
    empty.className = 'record-empty';
    empty.textContent = 'Nothing yet.';
    list.append(empty);
    return;
  }
  for (const item of items) list.append(render(item));
}

async function loadTasks() {
  const includeDone = el('task-done').checked;
  const { items } = await api.get(
    `/api/v1/projects/${state.projectId}/tasks?includeDone=${includeDone}`);

  fill('tasks', items, (task) => record({
    title: task.title,
    badge: { text: task.status.toLowerCase().replace(/_/g, ' '), className: taskPill(task.status) },
    body: task.description,
    meta: task.dependsOn?.length ? `depends on ${task.dependsOn.length} task(s)` : '',
    actions: nextStatuses(task.status).map((status) => ({
      label: status.toLowerCase().replace(/_/g, ' '),
      run: async () => {
        try {
          await api.call('PATCH', `/api/v1/tasks/${task.id}`, { status });
          await loadTasks();
        } catch (error) {
          toast(error.message);
        }
      },
    })),
  }));
}

function taskPill(status) {
  if (status === 'DONE') return 'pill-ok';
  if (status === 'IN_PROGRESS') return 'pill-warn';
  return 'pill-muted';
}

// nextStatuses offers the moves that make sense from here. Blocked is derived
// from the dependency graph, never set by hand.
function nextStatuses(status) {
  switch (status) {
    case 'TODO': return ['IN_PROGRESS', 'DONE'];
    case 'IN_PROGRESS': return ['DONE', 'TODO'];
    default: return ['TODO'];
  }
}

async function loadDecisions() {
  const { items } = await api.get(`/api/v1/projects/${state.projectId}/decisions`);
  fill('decisions', items, (decision) => record({
    title: decision.title,
    badge: decision.importance === 'IMPORTANT'
      ? { text: 'important', className: 'pill-warn' }
      : null,
    body: decision.content,
    meta: when(decision.createdAt),
  }));
}

async function loadArtifacts() {
  const { items } = await api.get(`/api/v1/projects/${state.projectId}/artifacts`);
  fill('artifacts', items, (artifact) => record({
    title: artifact.filename,
    body: '',
    meta: `${bytes(artifact.size)} · ${artifact.sha256.slice(0, 12)} · ${when(artifact.createdAt)}`,
    actions: [
      {
        label: 'download',
        run: () => window.open(`/api/v1/artifacts/${artifact.id}/content`, '_blank'),
      },
      {
        label: 'delete',
        className: 'link danger-link',
        run: async () => {
          try {
            await api.call('DELETE', `/api/v1/artifacts/${artifact.id}`);
            await loadArtifacts();
          } catch (error) {
            toast(error.message);
          }
        },
      },
    ],
  }));
}

function bytes(size) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} kB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

async function loadSkills() {
  const { items } = await api.get(`/api/v1/projects/${state.projectId}/skills`);
  fill('skills', items, (skill) => record({
    title: skill.name,
    badge: { text: skill.source.type.toLowerCase(), className: 'pill-muted' },
    body: skill.description,
    // The installed revision is the immutable identity of what is actually
    // there, which is not the same as the branch someone asked for.
    meta: `${skill.installedRevision.slice(0, 12)} · ${skill.source.url || 'uploaded'} · ${when(skill.installedAt)}`,
    actions: [{
      label: 'uninstall',
      className: 'link danger-link',
      run: async () => {
        try {
          await api.call('DELETE', `/api/v1/skills/${skill.id}`);
          await loadSkills();
        } catch (error) {
          toast(error.message);
        }
      },
    }],
  }));
}

async function loadInstructions() {
  const project = await api.get(`/api/v1/projects/${state.projectId}`);
  el('instructions').value = project.instructions ?? '';
}

async function loadAudit() {
  const { items } = await api.get('/api/v1/me/audit?limit=100');
  fill('audit', items, (entry) => record({
    title: entry.action.replace(/[._]/g, ' '),
    body: describeDetail(entry.detail),
    meta: [entry.actorId, entry.channel, when(entry.createdAt)].filter(Boolean).join(' · '),
  }));
}

// describeDetail flattens the recorded payload into readable pairs. The entry is
// an audit record, so what it says has to be legible without a JSON viewer.
function describeDetail(detail) {
  if (!detail || typeof detail !== 'object') return '';
  return Object.entries(detail)
    .filter(([, value]) => value !== null && value !== '' && value !== 0 && value !== false)
    .map(([key, value]) => `${key.replace(/([A-Z])/g, ' $1').toLowerCase()}: ${value}`)
    .join('\n');
}

// upload posts a file without the JSON envelope: an artifact and a skill bundle
// both travel as bytes, not as a field in a command.
async function upload(path, file) {
  const form = new FormData();
  form.append('file', file);

  const response = await fetch(path, {
    method: 'POST',
    headers: { 'X-Threavia-Channel': CHANNEL },
    body: form,
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(payload?.error?.message || `upload failed (${response.status})`);
  }
  return payload;
}

// ------------------------------------------------------------------- policy

async function loadPolicy() {
  const policy = await api.get(`/api/v1/sessions/${state.sessionId}/policy`);
  el('policy-mode').value = policy.mode;
  el('policy-write').checked = policy.allowFilesystemWrite;
  el('policy-commit').checked = policy.allowGitCommit;
  el('policy-push').checked = policy.allowGitPush;
  el('policy-network').checked = policy.allowNetwork;
  el('policy-duration').value = policy.maxDurationSeconds || '';
  el('policy-actions').value = policy.maxActions || '';
}

async function savePolicy() {
  await api.call('PUT', `/api/v1/sessions/${state.sessionId}/policy`, {
    mode: el('policy-mode').value,
    allowFilesystemWrite: el('policy-write').checked,
    allowGitCommit: el('policy-commit').checked,
    allowGitPush: el('policy-push').checked,
    allowNetwork: el('policy-network').checked,
    maxDurationSeconds: Number(el('policy-duration').value) || 0,
    maxActions: Number(el('policy-actions').value) || 0,
  });
}

// ------------------------------------------------------------------ session

async function openSession(sessionId) {
  state.sessionId = sessionId;
  renderSessions();

  const snapshot = await api.get(`/api/v1/sessions/${sessionId}`);
  state.jobs = new Map(snapshot.jobs.map((job) => [job.id, job]));
  // The cursor the snapshot carries is where the live stream takes over.
  state.cursor = Math.max(state.cursor, snapshot.cursor);

  el('session-title').textContent = snapshot.session.title || 'Untitled session';
  renderMeta();

  const timeline = el('timeline');
  timeline.innerHTML = '';
  for (const event of snapshot.events) appendEvent(event, false);
  timeline.scrollTop = timeline.scrollHeight;

  renderAttention(snapshot.attention);
  show('session');
}

function activeJob() {
  for (const job of state.jobs.values()) {
    if (!['COMPLETED', 'FAILED', 'CANCELLED'].includes(job.status)) return job;
  }
  return null;
}

function renderMeta() {
  const job = activeJob();
  el('session-meta').textContent = job ? `Job ${job.status.toLowerCase().replace(/_/g, ' ')}` : 'Idle';
  el('cancel-job').classList.toggle('hidden', !job || job.status === 'CANCELLING');
}

// renderAttention shows what is waiting for the user. It is current state: once
// resolved anywhere, it disappears everywhere.
function renderAttention(attention) {
  const container = el('attention');
  container.innerHTML = '';
  if (!attention) return;

  for (const request of attention.validations ?? []) {
    container.append(validationCard(request));
  }
  for (const request of attention.userInputs ?? []) {
    container.append(inputCard(request));
  }
}

function validationCard(request) {
  const card = document.createElement('div');
  card.className = 'card';

  const title = document.createElement('h3');
  title.textContent = request.title || 'Permission requested';
  const summary = document.createElement('p');
  summary.className = 'hint';
  summary.textContent = request.summary || '';

  const payload = document.createElement('pre');
  payload.textContent = JSON.stringify(request.requestPayload ?? {}, null, 2);

  const actions = document.createElement('div');
  actions.className = 'actions';
  const approve = document.createElement('button');
  approve.className = 'primary';
  approve.textContent = 'Approve';
  const deny = document.createElement('button');
  deny.className = 'danger';
  deny.textContent = 'Deny';

  const resolve = async (approved) => {
    approve.disabled = deny.disabled = true;
    try {
      await api.post(`/api/v1/validations/${request.id}/resolve`, { approved, channel: CHANNEL });
      card.remove();
    } catch (error) {
      toast(error.message);
      approve.disabled = deny.disabled = false;
    }
  };
  approve.addEventListener('click', () => resolve(true));
  deny.addEventListener('click', () => resolve(false));

  actions.append(approve, deny);
  card.append(title, summary, payload, actions);
  return card;
}

function inputCard(request) {
  const card = document.createElement('div');
  card.className = 'card';

  const title = document.createElement('h3');
  title.textContent = request.prompt || 'The agent is asking a question';

  const actions = document.createElement('div');
  actions.className = 'actions';

  const resolve = async (value) => {
    if (!value) return;
    try {
      await api.post(`/api/v1/user-input/${request.id}/resolve`, { value, channel: CHANNEL });
      card.remove();
    } catch (error) {
      toast(error.message);
    }
  };

  if (request.choices?.length) {
    for (const choice of request.choices) {
      const button = document.createElement('button');
      button.textContent = choice;
      button.addEventListener('click', () => resolve(choice));
      actions.append(button);
    }
  }
  if (request.freeText || !request.choices?.length) {
    const field = document.createElement('input');
    field.placeholder = 'Your answer…';
    field.addEventListener('keydown', (event) => {
      if (event.key === 'Enter') resolve(field.value.trim());
    });
    const send = document.createElement('button');
    send.className = 'primary';
    send.textContent = 'Answer';
    send.addEventListener('click', () => resolve(field.value.trim()));
    actions.append(field, send);
  }

  card.append(title, actions);
  return card;
}

// ----------------------------------------------------------------- timeline

const RENDERED = new Set([
  'user.message', 'agent.message', 'tool.started', 'tool.failed',
  'job.completed', 'job.failed', 'job.cancelled',
  'session.created', 'validation.resolved', 'user_input.resolved',
  'workspace.changed',
]);

function appendEvent(event, autoscroll = true) {
  if (!RENDERED.has(event.type)) return;

  const timeline = el('timeline');
  const entry = document.createElement('div');
  const body = document.createElement('div');
  body.className = 'body';

  switch (event.type) {
    case 'user.message':
      entry.className = 'entry user';
      body.textContent = event.payload.text;
      break;
    case 'agent.message':
      entry.className = 'entry agent';
      body.textContent = event.payload.text;
      break;
    case 'tool.started':
      entry.className = 'entry tool';
      body.textContent = `▸ ${event.payload.name}`;
      break;
    case 'workspace.changed':
      entry.className = 'entry workspace';
      body.append(workspaceSummary(event.payload));
      break;
    case 'tool.failed':
      entry.className = 'entry tool';
      body.textContent = `✕ ${event.payload.name}: ${event.payload.error ?? ''}`;
      break;
    default:
      entry.className = 'entry system';
      body.textContent = describe(event);
  }

  const who = document.createElement('span');
  who.className = 'who';
  who.textContent = event.type === 'user.message' ? 'you'
    : event.type === 'agent.message' ? 'agent' : '';
  if (who.textContent) entry.append(who);

  entry.append(body);
  timeline.append(entry);
  if (autoscroll) timeline.scrollTop = timeline.scrollHeight;
}

// workspaceSummary renders what a Job changed on disk. The list of files comes
// from the backend; the diff itself never leaves it, so there is nothing to
// expand here.
function workspaceSummary(payload) {
  const wrapper = document.createElement('div');

  const counts = document.createElement('div');
  counts.className = 'counts';
  const files = payload.files ?? [];
  counts.textContent =
    `${files.length} file${files.length === 1 ? '' : 's'} changed, `
    + `+${payload.additions ?? 0} −${payload.deletions ?? 0}`;
  wrapper.append(counts);

  const list = document.createElement('ul');
  list.className = 'files';
  for (const file of files.slice(0, 20)) {
    const item = document.createElement('li');
    item.dataset.state = file.state;
    item.textContent = `${STATE_MARK[file.state] ?? '·'} ${file.path}`;
    list.append(item);
  }
  if (files.length > 20) {
    const more = document.createElement('li');
    more.className = 'more';
    more.textContent = `… and ${files.length - 20} more`;
    list.append(more);
  }
  wrapper.append(list);
  return wrapper;
}

const STATE_MARK = {
  ADDED: '+', MODIFIED: '~', DELETED: '−', RENAMED: '→',
};

function describe(event) {
  switch (event.type) {
    case 'session.created': return 'Session created.';
    // The provider result repeats the last agent message, so only the fact
    // that the turn ended is worth showing.
    case 'job.completed': return 'Done.';
    case 'job.failed': return `Failed: ${event.payload.error ?? 'unknown error'}`;
    case 'job.cancelled': return 'Cancelled.';
    case 'validation.resolved':
      return event.payload.approved ? 'Permission granted.' : 'Permission denied.';
    case 'user_input.resolved': return `Answered: ${event.payload.value}`;
    default: return event.type;
  }
}

// ------------------------------------------------------------------- stream

// openStream subscribes to the global event stream. One stream carries every
// Session, and the browser resumes it from the last id it saw.
function openStream() {
  state.stream?.close();

  const stream = new EventSource(`/api/v1/events?after=${state.cursor}&channel=${CHANNEL}`);
  state.stream = stream;

  stream.addEventListener('open', () => setConnection('live', 'pill-ok'));
  stream.addEventListener('error', () => setConnection('reconnecting', 'pill-warn'));
  stream.addEventListener('event', (message) => handleEvent(JSON.parse(message.data)));
  stream.addEventListener('ephemeral', (message) => {
    const event = JSON.parse(message.data);
    if (event.sessionId === state.sessionId) el('session-meta').textContent = 'Agent working…';
  });
}

function setConnection(text, className) {
  const node = el('connection');
  node.textContent = text;
  node.className = `pill ${className}`;
}

async function handleEvent(event) {
  if (event.sequence > state.cursor) state.cursor = event.sequence;

  // A session appearing or being renamed changes the sidebar whatever is open.
  if (['session.created', 'session.renamed', 'session.archived'].includes(event.type)) {
    await loadProjectContent();
  }
  if (event.sessionId !== state.sessionId) return;

  if (event.type.startsWith('job.')) {
    await refreshJobs();
  }
  if (['validation.requested', 'user_input.requested',
       'validation.resolved', 'user_input.resolved'].includes(event.type)) {
    const attention = await api.get(`/api/v1/me/attention?sessionId=${state.sessionId}`);
    renderAttention(attention);
  }
  appendEvent(event);
}

async function refreshJobs() {
  const snapshot = await api.get(`/api/v1/sessions/${state.sessionId}?history=1`);
  state.jobs = new Map(snapshot.jobs.map((job) => [job.id, job]));
  renderMeta();
}

// ------------------------------------------------------------------- wiring

function wireEvents() {
  el('project').addEventListener('change', async (event) => {
    state.projectId = event.target.value;
    state.sessionId = null;
    await loadProjectContent();
    show('empty');
  });

  el('new-project').addEventListener('click', async () => {
    const name = prompt('Project name');
    if (!name) return;
    try {
      const project = await api.post('/api/v1/projects', { name, description: '' });
      state.projectId = project.id;
      await loadProjects();
    } catch (error) {
      toast(error.message);
    }
  });

  el('new-directory').addEventListener('click', async () => {
    const name = prompt('Directory name, for example: puppet');
    if (!name) return;
    try {
      const directory = await api.post(`/api/v1/projects/${state.projectId}/directories`, { name });
      // A logical directory is useless until it is bound to a real path on the
      // backend that will work in it.
      const path = prompt(`Absolute path of "${name}" on the selected backend`);
      if (path) {
        await api.post(`/api/v1/directories/${directory.id}/bindings`, {
          backendInstanceId: el('draft-backend').value,
          path,
        });
      }
      await loadProjectContent();
      openDraft();
    } catch (error) {
      toast(error.message);
    }
  });

  el('open-project').addEventListener('click', () => {
    openProject().catch((error) => toast(error.message));
  });

  el('project-tabs').addEventListener('click', (event) => {
    const tab = event.target.dataset?.tab;
    if (tab) selectTab(tab);
  });

  el('task-done').addEventListener('change', () => {
    loadTasks().catch((error) => toast(error.message));
  });

  el('task-form').addEventListener('submit', async (event) => {
    event.preventDefault();
    const field = el('task-title');
    const title = field.value.trim();
    if (!title) return;
    try {
      await api.post(`/api/v1/projects/${state.projectId}/tasks`, { title, description: '' });
      field.value = '';
      await loadTasks();
    } catch (error) {
      toast(error.message);
    }
  });

  el('decision-form').addEventListener('submit', async (event) => {
    event.preventDefault();
    const title = el('decision-title').value.trim();
    if (!title) return;
    try {
      await api.post(`/api/v1/projects/${state.projectId}/decisions`, {
        title,
        content: el('decision-content').value.trim(),
        importance: el('decision-importance').value,
      });
      el('decision-title').value = '';
      el('decision-content').value = '';
      await loadDecisions();
    } catch (error) {
      toast(error.message);
    }
  });

  el('artifact-form').addEventListener('submit', async (event) => {
    event.preventDefault();
    const file = el('artifact-file').files?.[0];
    if (!file) return;
    try {
      await upload(`/api/v1/projects/${state.projectId}/artifacts`, file);
      el('artifact-file').value = '';
      await loadArtifacts();
    } catch (error) {
      toast(error.message);
    }
  });

  // An uploaded skill carries bytes; a git or archive skill carries a url. The
  // form shows whichever the chosen source needs.
  el('skill-source').addEventListener('change', () => {
    const upload = el('skill-source').value === 'UPLOAD';
    el('skill-file').classList.toggle('hidden', !upload);
    el('skill-url').classList.toggle('hidden', upload);
  });

  el('skill-form').addEventListener('submit', async (event) => {
    event.preventDefault();
    const type = el('skill-source').value;
    try {
      if (type === 'UPLOAD') {
        const file = el('skill-file').files?.[0];
        if (!file) return;
        const path = el('skill-path').value.trim();
        await upload(
          `/api/v1/projects/${state.projectId}/skills${path ? `?path=${encodeURIComponent(path)}` : ''}`,
          file);
        el('skill-file').value = '';
      } else {
        const url = el('skill-url').value.trim();
        if (!url) return;
        await api.post(`/api/v1/projects/${state.projectId}/skills`, {
          source: {
            type,
            url,
            path: el('skill-path').value.trim(),
            revision: el('skill-revision').value.trim(),
          },
        });
        el('skill-url').value = '';
      }
      await loadSkills();
    } catch (error) {
      toast(error.message);
    }
  });

  el('save-instructions').addEventListener('click', async () => {
    try {
      await api.call('PATCH', `/api/v1/projects/${state.projectId}`, {
        instructions: el('instructions').value,
      });
      toast('Instructions saved.');
    } catch (error) {
      toast(error.message);
    }
  });

  // A backend change is explicit, and says what it costs: the native provider
  // session stays where it was, and a skill that only exists on the old machine
  // does not travel (specification sections 18 and 34).
  el('move-session').addEventListener('click', async () => {
    const choices = state.backends
      .filter((backend) => backend.ownershipStatus !== 'REVOKED')
      .map((backend) => `${backend.name} (${backend.id})`)
      .join('\n');
    const answer = prompt(`Move this session to which backend?\n\n${choices}\n\nAnswer with its id.`);
    if (!answer) return;

    try {
      const handoff = await api.call('PATCH', `/api/v1/sessions/${state.sessionId}`, {
        backendInstanceId: answer.trim(),
      });
      await openSession(state.sessionId);
      if (handoff.missingSkills?.length) {
        toast(`Moved. These local skills are not on the new backend: ${handoff.missingSkills.join(', ')}.`);
      } else {
        toast('Moved. The next message starts a fresh provider session there.');
      }
    } catch (error) {
      toast(error.message);
    }
  });

  el('toggle-policy').addEventListener('click', async () => {
    const form = el('policy');
    const opening = form.classList.contains('hidden');
    form.classList.toggle('hidden', !opening);
    if (opening) {
      try {
        await loadPolicy();
      } catch (error) {
        toast(error.message);
      }
    }
  });

  el('policy').addEventListener('submit', async (event) => {
    event.preventDefault();
    try {
      await savePolicy();
      toast('Execution policy applied.');
    } catch (error) {
      toast(error.message);
    }
  });

  el('new-session').addEventListener('click', openDraft);
  el('draft-send').addEventListener('click', sendDraft);

  el('composer').addEventListener('submit', async (event) => {
    event.preventDefault();
    const field = el('message');
    const message = field.value.trim();
    if (!message) return;

    field.value = '';
    try {
      await api.post(`/api/v1/sessions/${state.sessionId}/messages`, { message },
        { 'Idempotency-Key': crypto.randomUUID() });
    } catch (error) {
      field.value = message;
      toast(error.message);
    }
  });

  el('message').addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      el('composer').requestSubmit();
    }
  });

  el('cancel-job').addEventListener('click', async () => {
    const job = activeJob();
    if (!job) return;
    try {
      await api.post(`/api/v1/jobs/${job.id}/cancel`, { reason: 'cancelled from the web client' });
      await refreshJobs();
    } catch (error) {
      toast(error.message);
    }
  });
}

boot().catch((error) => toast(error.message));
