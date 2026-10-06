// Threavia web client.
//
// It is a stateless view and controller of Core state: everything it shows comes
// from a snapshot plus the global event stream, and closing it never stops agent
// execution. There is no build step and no framework on purpose.

const api = {
  async call(method, path, body, headers = {}) {
    const response = await fetch(path, {
      method,
      headers: body ? { 'Content-Type': 'application/json', ...headers } : headers,
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
  for (const name of ['draft', 'session', 'empty']) {
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
      await api.post(`/api/v1/validations/${request.id}/resolve`, { approved, channel: 'web' });
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
      await api.post(`/api/v1/user-input/${request.id}/resolve`, { value, channel: 'web' });
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

function describe(event) {
  switch (event.type) {
    case 'session.created': return 'Session created.';
    case 'job.completed': return `Done. ${event.payload.summary ?? ''}`.trim();
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

  const stream = new EventSource(`/api/v1/events?after=${state.cursor}`);
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
