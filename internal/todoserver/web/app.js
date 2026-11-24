// The to-do web app. Served under a strict CSP: the DOM is built with createElement and textContent
// only, and the CSRF token lives in this module, never in storage. localStorage keeps only the last
// list seen (to show offline) and the queue of changes not sent yet.

const LIST_KEY = 'cockpit-todo.list';
const QUEUE_KEY = 'cockpit-todo.queue';
const POLL_MS = 30_000;
const SETTLE_MS = 420;

const $ = (id) => document.getElementById(id);
const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)');

let csrf = null;
let mfa = null;
let auth = 'unknown';
let server = null;
let queue = [];
let reachable = true;
let unreachableWhy = null;
let inflight = null;
let changeSeq = 0;
let editingId = null;
let mfaFlow = null;
const settling = new Map();

// ---------- DOM ----------

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v === undefined || v === null || v === false) continue;
      if (k === 'text') el.textContent = v;
      else if (k === 'class') el.className = v;
      else if (k === 'on') for (const [ev, fn] of Object.entries(v)) el.addEventListener(ev, fn);
      else el.setAttribute(k, v === true ? '' : String(v));
    }
  }
  for (const kid of kids.flat()) if (kid !== null && kid !== undefined && kid !== false) el.append(kid);
  return el;
}

const cls = (...names) => names.filter(Boolean).join(' ');

function fill(el, ...kids) {
  el.replaceChildren(...kids.flat().filter((k) => k !== null && k !== undefined && k !== false));
}

const VIEWS = ['loading', 'signin', 'offline-empty', 'list', 'settings'];
let current = 'loading';

function show(name, focusId) {
  for (const v of VIEWS) $(`view-${v}`).hidden = v !== name;
  current = name;
  if (focusId) $(focusId)?.focus({ preventScroll: true });
}

function formError(form, message) {
  const el = form.querySelector('.form-error');
  el.textContent = message || '';
  el.hidden = !message;
}

function formOk(form, message) {
  const el = form.querySelector('.form-ok');
  if (!el) return;
  el.textContent = message || '';
  el.hidden = !message;
}

function busy(button, on) {
  button.disabled = on;
  button.setAttribute('aria-busy', on ? 'true' : 'false');
}

let toastTimer = 0;
function toast(message) {
  const el = $('toast');
  el.textContent = message;
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 3800);
}

async function copyText(text, button) {
  let ok = false;
  try {
    await navigator.clipboard.writeText(text);
    ok = true;
  } catch {
    ok = false;
  }
  if (!button.dataset.label) button.dataset.label = button.textContent;
  button.textContent = ok ? 'Copied' : 'Copy failed: select the text';
  setTimeout(() => { button.textContent = button.dataset.label; }, 1800);
}

// ---------- time ----------

const dateTimeFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });
const timeFmt = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' });
const dayMonthFmt = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short' });
const relFmt = new Intl.RelativeTimeFormat('en', { numeric: 'auto' });

const DAY = 86_400_000;

function startOfDay(d) { return new Date(d.getFullYear(), d.getMonth(), d.getDate()); }
function parseISODate(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s || '');
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null;
}
function dayDiff(a, b) { return Math.round((startOfDay(a) - startOfDay(b)) / DAY); }
function validTime(s) { const d = new Date(s); return Number.isNaN(d.getTime()) ? null : d; }

function fmtDateTime(s) { const d = validTime(s); return d ? dateTimeFmt.format(d) : ''; }

function fmtWhen(s, now = new Date()) {
  const d = validTime(s);
  if (!d) return '';
  return dayDiff(d, now) === 0 ? timeFmt.format(d) : dateTimeFmt.format(d);
}

function ago(s, now = new Date()) {
  const d = validTime(s);
  if (!d) return 'never';
  const secs = Math.round((now - d) / 1000);
  if (secs < 45) return 'just now';
  const mins = Math.round(secs / 60);
  if (mins < 60) return relFmt.format(-mins, 'minute');
  const hours = Math.round(mins / 60);
  if (hours < 24) return relFmt.format(-hours, 'hour');
  const days = Math.round(hours / 24);
  if (days < 30) return relFmt.format(-days, 'day');
  return dayMonthFmt.format(d);
}

// The dashboard's wording (internal/tui/rail.go, due): "2d late", "today", "tomorrow", "in 3d".
function dueLabel(due, now) {
  const at = parseISODate(due);
  if (!at) return null;
  const days = dayDiff(at, now);
  if (days < 0) return { text: `${-days}d late`, cls: 'due-late' };
  if (days === 0) return { text: 'today', cls: 'due-today' };
  if (days === 1) return { text: 'tomorrow', cls: 'due-tomorrow' };
  return { text: `in ${days}d`, cls: '' };
}

function doneLabel(done, now) {
  const d = validTime(done);
  if (!d) return 'done';
  const days = dayDiff(now, d);
  if (days === 0) return 'today';
  if (days === 1) return 'yesterday';
  return dayMonthFmt.format(d);
}

// ---------- quick-add syntax (mirror of internal/todo/parse.go, for showing changes not sent yet) ----------

function isoDate(d) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
}

function realDate(y, m, d) {
  const t = new Date(y, m - 1, d);
  return t.getFullYear() === y && t.getMonth() === m - 1 && t.getDate() === d ? t : null;
}

function parseDate(s, now) {
  const today = startOfDay(now);
  switch (s.toLowerCase()) {
    case 'today': case 'hoje':
      return isoDate(today);
    case 'tomorrow': case 'amanhã': case 'amanha':
      return isoDate(new Date(today.getFullYear(), today.getMonth(), today.getDate() + 1));
  }
  let m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (m) { const t = realDate(+m[1], +m[2], +m[3]); return t ? isoDate(t) : null; }
  m = /^(\d{1,2})\/(\d{1,2})\/(\d{4})$/.exec(s);
  if (m) { const t = realDate(+m[3], +m[2], +m[1]); return t ? isoDate(t) : null; }
  m = /^(\d{1,2})\/(\d{1,2})$/.exec(s);
  if (m) {
    let t = realDate(today.getFullYear(), +m[2], +m[1]);
    if (!t) return null;
    if (t < today) t = realDate(today.getFullYear() + 1, +m[2], +m[1]);
    return t ? isoDate(t) : null;
  }
  return null;
}

function matchGroup(name, groups) {
  const n = name.replaceAll('_', ' ');
  const found = groups.find((g) => g.toLowerCase() === n.toLowerCase());
  if (found) return found;
  const first = String.fromCodePoint(n.codePointAt(0));
  return first.toUpperCase() + n.slice(first.length);
}

function parseLine(line, groups, now = new Date()) {
  const words = [];
  let group = '';
  let due = '';
  for (const w of line.split(/\s+/).filter(Boolean)) {
    if (w.length > 1 && w[0] === '#') group = matchGroup(w.slice(1), groups);
    else if (w.length > 1 && w[0] === '@') {
      const d = parseDate(w.slice(1), now);
      if (d) due = d; else words.push(w);
    } else words.push(w);
  }
  return { text: words.join(' '), group, due };
}

function formatLine(t) {
  const parts = [t.text];
  if (t.group) parts.push('#' + t.group.replaceAll(' ', '_'));
  if (t.due) parts.push('@' + t.due);
  return parts.join(' ');
}

// ---------- storage ----------

function load(key) {
  try {
    return JSON.parse(localStorage.getItem(key));
  } catch {
    return null;
  }
}

function persist() {
  try {
    if (server) localStorage.setItem(LIST_KEY, JSON.stringify(server));
    if (queue.length) localStorage.setItem(QUEUE_KEY, JSON.stringify(queue));
    else localStorage.removeItem(QUEUE_KEY);
  } catch {
    // Storage can be full or blocked; the app keeps working from memory.
  }
}

function clearLocal() {
  try {
    localStorage.removeItem(LIST_KEY);
    localStorage.removeItem(QUEUE_KEY);
  } catch {
    // Nothing to clear when storage is blocked.
  }
}

function restore() {
  const list = load(LIST_KEY);
  if (list && Array.isArray(list.tasks)) {
    server = {
      tasks: list.tasks.filter((t) => t && typeof t.id === 'string'),
      groups: Array.isArray(list.groups) ? list.groups.filter((g) => typeof g === 'string') : [],
      defaultGroup: typeof list.defaultGroup === 'string' ? list.defaultGroup : 'Tasks',
      at: typeof list.at === 'string' ? list.at : null,
    };
  }
  const q = load(QUEUE_KEY);
  queue = Array.isArray(q) ? q.filter((o) => o && ['add', 'patch', 'delete'].includes(o.op)) : [];
}

// ---------- API ----------

class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}
class NetworkError extends Error {
  constructor() { super("You're offline."); }
}

const isUnreachable = (e) => e instanceof NetworkError || (e instanceof ApiError && e.status >= 500);

function fallbackMessage(status) {
  if (status === 401) return 'Signed out.';
  if (status === 403) return 'Not allowed.';
  if (status === 404) return 'Not found.';
  if (status === 429) return 'Too many attempts. Wait a little and try again.';
  if (status >= 500) return `The server could not answer (${status}).`;
  return `Something went wrong (${status}).`;
}

async function api(method, path, body) {
  const init = { method, credentials: 'same-origin', cache: 'no-store', headers: { Accept: 'application/json' } };
  if (method !== 'GET') {
    init.headers['Content-Type'] = 'application/json';
    if (!path.startsWith('/api/login') && csrf) init.headers['X-CSRF-Token'] = csrf;
    if (method !== 'DELETE') init.body = JSON.stringify(body || {});
  }
  let res;
  try {
    res = await fetch(path, init);
  } catch {
    throw new NetworkError();
  }
  let data = null;
  if (res.status !== 204) {
    const text = await res.text().catch(() => '');
    if (text) {
      try { data = JSON.parse(text); } catch { data = null; }
    }
  }
  if (!res.ok) {
    const msg = data && typeof data.error === 'string' && data.error ? data.error : fallbackMessage(res.status);
    throw new ApiError(res.status, msg);
  }
  return data;
}

// call is api for a signed-in request: it tracks whether the server is reachable, and a 401 means the
// session ended. When the request checks a password (inputCheck), a 401 is that password's answer.
async function call(method, path, body, { inputCheck = false } = {}) {
  try {
    const r = await api(method, path, body);
    markReachable();
    return r;
  } catch (e) {
    if (isUnreachable(e)) markUnreachable(e);
    else markReachable();
    if (e instanceof ApiError && e.status === 401 && !inputCheck) signedOut('Your session ended. Sign in again.');
    throw e;
  }
}

function markReachable() {
  if (reachable) return;
  reachable = true;
  unreachableWhy = null;
  renderNet();
}

function markUnreachable(e) {
  reachable = false;
  unreachableWhy = e instanceof ApiError ? "Can't reach the server right now." : 'Offline.';
  renderNet();
}

async function refreshMe() {
  try {
    const r = await api('GET', '/api/me');
    csrf = r.csrf;
    mfa = r.mfa || null;
    auth = 'in';
    markReachable();
    return true;
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) signedOut();
    else if (isUnreachable(e)) markUnreachable(e);
    return false;
  }
}

// ---------- the list and its outbox ----------
// Every change goes into the queue first and is drawn from it at once; flush sends the queue in order.
// Offline, the queue waits in localStorage and goes out when the connection returns.

function groupsOf(tasks) {
  const out = server ? [...server.groups] : [];
  for (const t of tasks) if (t.group && !out.includes(t.group)) out.push(t.group);
  return out;
}

function currentTasks() {
  const tasks = (server ? server.tasks : []).filter((t) => !t.deleted).map((t) => ({ ...t }));
  const pending = new Set();
  const now = new Date();
  for (const q of queue) {
    if (q.op === 'add') {
      const p = parseLine(q.line, groupsOf(tasks), now);
      tasks.push({ id: q.tmp, text: p.text || q.line, group: p.group || undefined, due: p.due || undefined, created: q.at });
      pending.add(q.tmp);
      continue;
    }
    const i = tasks.findIndex((t) => t.id === q.id);
    if (i < 0) continue;
    pending.add(q.id);
    if (q.op === 'delete') tasks.splice(i, 1);
    else if (q.body && 'done' in q.body) tasks[i].done = q.body.done ? q.at : undefined;
    else if (q.body && typeof q.body.line === 'string') {
      const p = parseLine(q.body.line, groupsOf(tasks), now);
      Object.assign(tasks[i], { text: p.text || tasks[i].text, group: p.group || undefined, due: p.due || undefined });
    }
  }
  return { tasks, pending };
}

function mutate(op) {
  op.at = new Date().toISOString();
  const add = op.id && queue.find((q) => q.op === 'add' && q.tmp === op.id && q !== inflight);
  if (add && op.op === 'delete') queue = queue.filter((q) => q !== add && q.id !== op.id);
  else if (add && op.body && typeof op.body.line === 'string') add.line = op.body.line;
  else queue.push(op);
  persist();
  render();
  sync();
}

function retarget(tmp, id) {
  if (id) for (const q of queue) { if (q.id === tmp) q.id = id; }
  else queue = queue.filter((q) => q.id !== tmp);
}

function upsert(task) {
  const i = server.tasks.findIndex((t) => t.id === task.id);
  if (i >= 0) server.tasks[i] = task; else server.tasks.push(task);
}

const taskPath = (id) => `/api/tasks/${encodeURIComponent(id)}`;

async function flush() {
  while (queue.length && auth !== 'out') {
    if (!csrf && !(await refreshMe())) return;
    const q = queue[0];
    inflight = q;
    try {
      if (!server) server = { tasks: [], groups: [], defaultGroup: 'Tasks', at: null };
      if (q.op === 'add') {
        const r = await call('POST', '/api/tasks', { line: q.line });
        upsert(r.task);
        retarget(q.tmp, r.task.id);
      } else if (q.op === 'patch') {
        const r = await call('PATCH', taskPath(q.id), q.body);
        upsert(r.task);
      } else {
        await call('DELETE', taskPath(q.id));
        server.tasks = server.tasks.filter((t) => t.id !== q.id);
      }
      queue.shift();
    } catch (e) {
      if (isUnreachable(e) || (e instanceof ApiError && e.status === 401)) {
        persist();
        render();
        return;
      }
      queue.shift();
      if (q.op === 'add') retarget(q.tmp, null);
      if (!(q.op === 'delete' && e.status === 404)) toast(`Not saved: ${e.message}`);
    } finally {
      inflight = null;
    }
    changeSeq++;
    persist();
    render();
  }
}

async function fetchTasks() {
  const seq = changeSeq;
  try {
    const r = await call('GET', '/api/tasks');
    if (seq !== changeSeq || queue.length) return false;
    server = {
      tasks: Array.isArray(r.tasks) ? r.tasks : [],
      groups: Array.isArray(r.groups) ? r.groups : [],
      defaultGroup: r.defaultGroup || 'Tasks',
      at: new Date().toISOString(),
    };
    persist();
    render();
    return true;
  } catch {
    render();
    return true;
  }
}

let syncing = null;
let syncAgain = false;

function sync() {
  if (syncing) {
    syncAgain = true;
    return syncing;
  }
  syncing = (async () => {
    do {
      syncAgain = false;
      if (auth === 'unknown' && !(await refreshMe())) break;
      if (auth !== 'in') break;
      await flush();
      if (auth !== 'in' || queue.length) break;
      if (!(await fetchTasks())) syncAgain = true;
    } while (syncAgain);
  })().finally(() => { syncing = null; });
  return syncing;
}

function tick() {
  if (document.visibilityState !== 'visible' || auth === 'out') return;
  sync().then(() => {
    if (auth === 'in' && current === 'offline-empty') enterList();
  });
}

// ---------- rendering the list ----------

function renderNet() {
  const el = $('net-note');
  const waiting = queue.length;
  if (reachable && !waiting) {
    el.hidden = true;
    return;
  }
  const parts = [];
  if (!reachable) {
    parts.push(unreachableWhy || 'Offline.');
    if (server && server.at) parts.push(`Showing the list from ${fmtWhen(server.at)}.`);
  }
  if (waiting) parts.push(waiting === 1 ? '1 change waiting to be sent.' : `${waiting} changes waiting to be sent.`);
  if (reachable && auth === 'out') parts.push('Sign in to send them.');
  el.textContent = parts.join(' ');
  el.hidden = false;
}

function taskRow(t, pending, now) {
  const done = Boolean(t.done);
  const isPending = pending.has(t.id);
  const li = h('li', { class: cls('task', done && 'is-done', isPending && 'is-pending', settling.has(t.id) && 'is-settling') });

  const check = h('button', {
    type: 'button',
    class: 'check',
    role: 'checkbox',
    'aria-checked': String(done || settling.has(t.id)),
    'aria-label': t.text,
    'data-key': `check:${t.id}`,
    on: { click: () => toggleDone(t) },
  });

  const side = h('span', { class: 'task-side' });
  if (done) side.append(h('span', { text: doneLabel(t.done, now) }));
  else {
    const d = dueLabel(t.due, now);
    if (d) side.append(h('span', { class: cls('due', d.cls), text: d.text }));
  }
  if (isPending) side.append(h('span', { class: 'pending-mark', text: 'not sent' }));

  const main = h('button', {
    type: 'button',
    class: 'task-main',
    'data-key': `edit:${t.id}`,
    on: { click: () => openEdit(t.id) },
  }, h('span', { class: 'task-text', text: t.text }), side.childNodes.length ? side : null);

  li.append(check, main);
  return li;
}

function emptyState(anyDone) {
  if (anyDone) {
    return h('div', { class: 'empty' },
      h('p', { class: 'empty-title', text: 'All done.' }),
      h('p', { text: 'Nothing open. Add the next thing below.' }));
  }
  return h('div', { class: 'empty' },
    h('p', { class: 'empty-title', text: 'Nothing to do yet.' }),
    h('p', {}, 'Add a task below. ', h('code', { text: '#Group' }), ' files it, ',
      h('code', { text: '@tomorrow' }), ' gives it a date.'));
}

function skeleton() {
  return h('div', { class: 'skeleton', 'aria-label': 'Loading the list' },
    h('span'), h('span'), h('span'), h('span'));
}

function render() {
  renderNet();
  const focused = document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.key : null;
  if (!server) {
    $('tasks').replaceChildren(skeleton());
    $('done').hidden = true;
    $('open-count').textContent = '';
    renderChips();
    return;
  }
  const now = new Date();
  const { tasks, pending } = currentTasks();
  const open = tasks.filter((t) => !t.done || settling.has(t.id));
  const done = tasks.filter((t) => t.done && !settling.has(t.id))
    .sort((a, b) => String(b.done).localeCompare(String(a.done)));

  const byGroup = new Map();
  for (const g of server.groups) byGroup.set(g, []);
  for (const t of open) {
    const g = t.group || server.defaultGroup;
    if (!byGroup.has(g)) byGroup.set(g, []);
    byGroup.get(g).push(t);
  }

  const frag = document.createDocumentFragment();
  if (!open.length) frag.append(emptyState(done.length > 0));
  for (const [g, list] of byGroup) {
    if (!list.length) continue;
    const sorted = list.map((t, i) => [t, i]).sort(([a, i], [b, j]) => {
      if (!a.due !== !b.due) return a.due ? -1 : 1;
      if (a.due !== b.due) return a.due < b.due ? -1 : 1;
      return i - j;
    }).map(([t]) => t);
    const headId = `g-${g.replace(/[^A-Za-z0-9_-]/g, '_')}`;
    frag.append(h('section', { class: 'group', 'aria-labelledby': headId },
      h('h2', { class: 'group-head', id: headId }, h('span', { text: g }), h('span', { class: 'group-count', text: String(list.length) })),
      h('ul', { class: 'task-list' }, sorted.map((t) => taskRow(t, pending, now)))));
  }
  $('tasks').replaceChildren(frag);

  $('done').hidden = done.length === 0;
  $('done-count').textContent = String(done.length);
  $('done-list').replaceChildren(...done.map((t) => taskRow(t, pending, now)));
  $('open-count').textContent = open.length ? String(open.length) : '';
  renderChips();

  if (focused) document.querySelector(`[data-key="${CSS.escape(focused)}"]`)?.focus({ preventScroll: true });
}

function toggleDone(t) {
  if (settling.has(t.id)) {
    clearTimeout(settling.get(t.id));
    settling.delete(t.id);
    render();
    return;
  }
  if (t.done) {
    mutate({ op: 'patch', id: t.id, body: { done: false } });
    return;
  }
  const finish = () => {
    settling.delete(t.id);
    mutate({ op: 'patch', id: t.id, body: { done: true } });
  };
  if (reduceMotion.matches) {
    finish();
    return;
  }
  settling.set(t.id, setTimeout(finish, SETTLE_MS));
  render();
}

// ---------- adding ----------

function renderChips() {
  const chips = $('chips');
  const input = $('add-input');
  const value = input.value;
  const words = value.split(/\s+/);
  const groups = server ? server.groups.filter((g) => g !== server.defaultGroup).concat(server.groups.includes(server.defaultGroup) ? [server.defaultGroup] : []) : [];
  const groupChips = groups.map((g) => {
    const token = '#' + g.replaceAll(' ', '_');
    return h('button', {
      type: 'button',
      class: cls('chip', words.some((w) => w.toLowerCase() === token.toLowerCase()) && 'is-on'),
      text: token,
      on: { mousedown: (e) => e.preventDefault(), click: () => insertToken('#', token) },
    });
  });
  const dateChips = ['@today', '@tomorrow'].map((token) => h('button', {
    type: 'button',
    class: cls('chip', words.includes(token) && 'is-on'),
    text: token,
    on: { mousedown: (e) => e.preventDefault(), click: () => insertToken('@', token) },
  }));
  fill(chips, ...groupChips, groupChips.length ? h('span', { class: 'chip-sep', 'aria-hidden': 'true' }) : null, ...dateChips);
  $('add-form').querySelector('.add-btn').disabled = !value.trim();
}

function insertToken(prefix, token) {
  const input = $('add-input');
  const now = new Date();
  const words = input.value.split(/\s+/).filter(Boolean);
  const on = words.some((w) => w.toLowerCase() === token.toLowerCase());
  const kept = words.filter((w) => {
    if (w.length < 2 || w[0] !== prefix) return true;
    return prefix === '@' ? !parseDate(w.slice(1), now) : false;
  });
  if (!on) kept.push(token);
  input.value = kept.join(' ') + (kept.length ? ' ' : '');
  input.focus();
  const end = input.value.length;
  input.setSelectionRange(end, end);
  renderChips();
}

let chipsTimer = 0;
function wireAdd() {
  const form = $('add-form');
  const input = $('add-input');
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const line = input.value.trim();
    if (!line) return;
    if (!parseLine(line, server ? server.groups : []).text) {
      toast('Type what to do, then #group or @date.');
      return;
    }
    mutate({ op: 'add', tmp: `tmp-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`, line });
    input.value = '';
    renderChips();
  });
  input.addEventListener('input', renderChips);
  form.addEventListener('focusin', () => {
    clearTimeout(chipsTimer);
    $('chips').hidden = false;
  });
  form.addEventListener('focusout', () => {
    clearTimeout(chipsTimer);
    chipsTimer = setTimeout(() => {
      if (!form.contains(document.activeElement) && !input.value) $('chips').hidden = true;
    }, 200);
  });
}

// ---------- editing ----------

function findTask(id) {
  return currentTasks().tasks.find((t) => t.id === id) || null;
}

function openEdit(id) {
  const t = findTask(id);
  if (!t) return;
  editingId = id;
  const form = $('form-edit');
  formError(form, '');
  const area = $('edit-line');
  area.value = formatLine(t);
  $('edit-sheet').showModal();
  area.focus();
  area.setSelectionRange(area.value.length, area.value.length);
}

function closeSheet(dialog) {
  if (dialog.open) dialog.close();
}

function wireEdit() {
  const sheet = $('edit-sheet');
  const form = $('form-edit');
  const area = $('edit-line');
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const t = findTask(editingId);
    if (!t) {
      closeSheet(sheet);
      toast('This task was removed elsewhere.');
      return;
    }
    const line = area.value.replace(/\s+/g, ' ').trim();
    if (!line || !parseLine(line, server ? server.groups : []).text) {
      formError(form, 'Type the task, or delete it.');
      return;
    }
    closeSheet(sheet);
    if (line !== formatLine(t)) mutate({ op: 'patch', id: t.id, body: { line } });
  });
  area.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      form.requestSubmit();
    }
  });
  $('edit-cancel').addEventListener('click', () => closeSheet(sheet));
  $('edit-delete').addEventListener('click', async () => {
    const t = findTask(editingId);
    if (!t) return closeSheet(sheet);
    await confirmSheet({
      title: 'Delete this task?',
      text: t.text,
      ok: 'Delete',
      action: async () => {
        closeSheet(sheet);
        mutate({ op: 'delete', id: t.id });
      },
    });
  });
  sheet.addEventListener('close', () => { editingId = null; });
  lightDismiss(sheet);
}

function lightDismiss(dialog) {
  dialog.addEventListener('click', (e) => {
    if (e.target === dialog) dialog.close();
  });
}

// confirmSheet asks before something that cannot be undone. action runs on confirm; when it throws,
// its message shows in the sheet and the sheet stays open.
let confirmState = null;

function confirmSheet({ title, text, ok, password = false, action }) {
  const sheet = $('confirm-sheet');
  const form = $('form-confirm');
  $('confirm-title').textContent = title;
  $('confirm-text').textContent = text || '';
  $('confirm-text').hidden = !text;
  $('confirm-ok').textContent = ok;
  $('confirm-password-field').hidden = !password;
  form.elements.password.value = '';
  formError(form, '');
  return new Promise((resolve) => {
    confirmState = { resolve, action, password, done: false };
    sheet.showModal();
    if (password) form.elements.password.focus();
    else $('confirm-cancel').focus();
  });
}

function wireConfirm() {
  const sheet = $('confirm-sheet');
  const form = $('form-confirm');
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const st = confirmState;
    if (!st) return;
    const pw = form.elements.password.value;
    if (st.password && !pw) {
      formError(form, 'Enter your password.');
      return;
    }
    const button = $('confirm-ok');
    busy(button, true);
    try {
      await st.action(pw);
      st.done = true;
      sheet.close();
    } catch (err) {
      formError(form, err.message);
    } finally {
      busy(button, false);
    }
  });
  $('confirm-cancel').addEventListener('click', () => sheet.close());
  sheet.addEventListener('close', () => {
    const st = confirmState;
    confirmState = null;
    form.elements.password.value = '';
    if (st) st.resolve(st.done);
  });
  lightDismiss(sheet);
}

// ---------- signing in and out ----------

function showSignIn(message) {
  const pwForm = $('form-password');
  const mfaForm = $('form-mfa');
  pwForm.hidden = false;
  mfaForm.hidden = true;
  formError(pwForm, '');
  formError(mfaForm, '');
  pwForm.elements.password.value = '';
  setRecovery(false);
  const notes = [];
  if (message) notes.push(message);
  if (queue.length) notes.push(queue.length === 1 ? '1 change made offline will be sent after you sign in.' : `${queue.length} changes made offline will be sent after you sign in.`);
  $('signin-notice').textContent = notes.join(' ');
  $('signin-notice').hidden = notes.length === 0;
  for (const d of document.querySelectorAll('dialog[open]')) d.close();
  show('signin', 'signin-title');
}

function signedOut(message) {
  const was = auth;
  auth = 'out';
  csrf = null;
  mfa = null;
  mfaFlow = null;
  renderNet();
  if (was !== 'out' || current !== 'signin') showSignIn(message);
}

function enterList() {
  show('list');
  render();
}

async function afterSignIn(token) {
  csrf = token;
  auth = 'in';
  await refreshMe();
  enterList();
  $('list-title').focus({ preventScroll: true });
  sync();
}

function setRecovery(on) {
  const input = $('signin-code');
  input.value = '';
  input.classList.toggle('is-recovery', on);
  input.setAttribute('inputmode', on ? 'text' : 'numeric');
  input.setAttribute('autocomplete', on ? 'off' : 'one-time-code');
  if (on) {
    input.removeAttribute('pattern');
    input.setAttribute('maxlength', '64');
  } else {
    input.setAttribute('pattern', '[0-9]*');
    input.setAttribute('maxlength', '6');
  }
  $('mfa-label').textContent = on ? 'Recovery code' : '6-digit code from your authenticator';
  $('mfa-toggle').textContent = on ? 'Use a 6-digit code' : 'Use a recovery code';
}

function wireSignIn() {
  const pwForm = $('form-password');
  const mfaForm = $('form-mfa');
  const codeInput = $('signin-code');

  pwForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const password = pwForm.elements.password.value;
    if (!password) {
      formError(pwForm, 'Enter your password.');
      return;
    }
    const button = pwForm.querySelector('button[type="submit"]');
    busy(button, true);
    formError(pwForm, '');
    try {
      const r = await api('POST', '/api/login', { password });
      pwForm.elements.password.value = '';
      if (r && r.next === 'mfa') {
        pwForm.hidden = true;
        mfaForm.hidden = false;
        $('signin-notice').hidden = true;
        setRecovery(false);
        formError(mfaForm, '');
        codeInput.focus();
      } else if (r && r.next === 'done') {
        await afterSignIn(r.csrf);
      } else {
        formError(pwForm, 'Unexpected answer from the server.');
      }
    } catch (err) {
      formError(pwForm, err.message);
      pwForm.elements.password.select();
    } finally {
      busy(button, false);
    }
  });

  mfaForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const recovery = codeInput.classList.contains('is-recovery');
    const code = recovery ? codeInput.value.trim() : codeInput.value.replace(/\D/g, '');
    if (!recovery && code.length !== 6) {
      formError(mfaForm, 'Enter the 6 digits your authenticator shows.');
      return;
    }
    if (recovery && !code) {
      formError(mfaForm, 'Enter one of your recovery codes.');
      return;
    }
    const button = mfaForm.querySelector('button[type="submit"]');
    busy(button, true);
    formError(mfaForm, '');
    try {
      const r = await api('POST', '/api/login/mfa', { code });
      if (r && r.next === 'done') {
        codeInput.value = '';
        await afterSignIn(r.csrf);
      } else {
        formError(mfaForm, 'Unexpected answer from the server.');
      }
    } catch (err) {
      formError(mfaForm, err.message);
      codeInput.select();
    } finally {
      busy(button, false);
    }
  });

  codeInput.addEventListener('input', () => {
    if (codeInput.classList.contains('is-recovery')) return;
    const digits = codeInput.value.replace(/\D/g, '').slice(0, 6);
    if (digits !== codeInput.value) codeInput.value = digits;
    if (digits.length === 6) mfaForm.requestSubmit();
  });

  $('mfa-toggle').addEventListener('click', () => {
    setRecovery(!codeInput.classList.contains('is-recovery'));
    formError(mfaForm, '');
    codeInput.focus();
  });
  $('mfa-back').addEventListener('click', () => {
    mfaForm.hidden = true;
    pwForm.hidden = false;
    formError(pwForm, '');
    pwForm.elements.password.focus();
  });
  $('retry-connect').addEventListener('click', async () => {
    const button = $('retry-connect');
    busy(button, true);
    if (await refreshMe()) {
      enterList();
      sync();
    }
    busy(button, false);
  });
}

async function signOut() {
  const pendingNote = queue.length
    ? ` ${queue.length === 1 ? '1 change made offline has' : `${queue.length} changes made offline have`} not been sent and will be lost.`
    : '';
  await confirmSheet({
    title: 'Sign out?',
    text: `This device forgets the list.${pendingNote}`,
    ok: 'Sign out',
    action: async () => {
      try {
        await api('POST', '/api/logout', {});
      } catch (e) {
        if (e instanceof NetworkError) throw new Error("You're offline: signing out needs the server.");
        if (!(e instanceof ApiError && e.status === 401)) throw e;
      }
      clearLocal();
      server = null;
      queue = [];
      settling.clear();
      signedOut('Signed out.');
    },
  });
}

// ---------- settings ----------

function openSettings() {
  mfaFlow = null;
  $('device-token').hidden = true;
  $('device-token').replaceChildren();
  const pwForm = $('form-change-password');
  pwForm.reset();
  formError(pwForm, '');
  formOk(pwForm, '');
  show('settings', 'settings-title');
  window.scrollTo(0, 0);
  renderMfa();
  refreshMe().then(renderMfa);
  loadSessions();
  loadDevices();
  loadAudit();
}

function closeSettings() {
  mfaFlow = null;
  $('device-token').replaceChildren();
  $('device-token').hidden = true;
  enterList();
  $('open-settings').focus({ preventScroll: true });
}

function panelNote(text) { return h('p', { class: 'panel-note', text }); }

function panelError(message, retry) {
  return h('div', { class: 'panel-error', role: 'alert' },
    h('span', { text: message }),
    h('button', { type: 'button', class: 'btn btn-secondary btn-small', text: 'Retry', on: { click: retry } }));
}

async function loadInto(panel, path, draw, retry) {
  fill(panel, panelNote('Loading'));
  try {
    const data = await call('GET', path);
    draw(data || {});
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return;
    fill(panel, panelError(e.message, retry));
  }
}

function deviceName(ua) {
  const s = String(ua || '');
  if (!s) return 'Unknown device';
  if (/cockpit/i.test(s)) return 'cockpit';
  const os = /iPhone/.test(s) ? 'iPhone' : /iPad/.test(s) ? 'iPad' : /Android/.test(s) ? 'Android'
    : /Macintosh|Mac OS X/.test(s) ? 'Mac' : /Windows/.test(s) ? 'Windows' : /Linux/.test(s) ? 'Linux' : '';
  const browser = /Edg\//.test(s) ? 'Edge' : /CriOS|Chrome\//.test(s) ? 'Chrome' : /FxiOS|Firefox\//.test(s) ? 'Firefox'
    : /Safari\//.test(s) ? 'Safari' : /AppleWebKit/.test(s) && /iPhone|iPad/.test(s) ? 'Home Screen app' : '';
  if (os && browser) return `${browser} on ${os}`;
  return os || browser || s.slice(0, 60);
}

function loadSessions() {
  const panel = $('sessions-panel');
  const endOthers = $('end-others');
  endOthers.hidden = true;
  loadInto(panel, '/api/sessions', (data) => {
    const sessions = Array.isArray(data.sessions) ? data.sessions : [];
    sessions.sort((a, b) => (b.current ? 1 : 0) - (a.current ? 1 : 0));
    endOthers.hidden = !sessions.some((s) => !s.current);
    if (!sessions.length) {
      fill(panel, panelNote('No sessions.'));
      return;
    }
    fill(panel, ...sessions.map((s) => {
      const sub = [s.ip, `started ${fmtDateTime(s.started)}`, s.current ? null : `active ${ago(s.lastUsed)}`].filter(Boolean).join(' · ');
      const end = s.current ? null : h('button', {
        type: 'button',
        class: 'btn btn-secondary btn-small',
        text: 'End',
        'aria-label': `End session: ${deviceName(s.agent)}`,
        on: {
          click: async (e) => {
            const button = e.currentTarget;
            busy(button, true);
            try {
              await call('DELETE', `/api/sessions/${encodeURIComponent(s.id)}`);
              toast('Session ended.');
              loadSessions();
              loadAudit();
            } catch (err) {
              toast(err.message);
              busy(button, false);
            }
          },
        },
      });
      return h('div', { class: 'srow' },
        h('div', { class: 'srow-main' },
          h('span', { class: 'srow-title', title: s.agent || '' }, deviceName(s.agent), s.current ? h('span', { class: 'srow-tag', text: 'This device' }) : null),
          h('span', { class: 'srow-sub', text: sub })),
        end);
    }));
  }, loadSessions);
}

function loadDevices() {
  const panel = $('devices-panel');
  loadInto(panel, '/api/devices', (data) => {
    const devices = Array.isArray(data.devices) ? data.devices : [];
    if (!devices.length) {
      fill(panel, panelNote('No computer syncs with this list yet. Add one below to get its token.'));
      return;
    }
    fill(panel, ...devices.map((d) => h('div', { class: 'srow' },
      h('div', { class: 'srow-main' },
        h('span', { class: 'srow-title', text: d.name || 'Unnamed' }),
        h('span', { class: 'srow-sub', text: `added ${fmtDateTime(d.created)} · ${d.lastUsed ? `last synced ${ago(d.lastUsed)}` : 'never synced'}` })),
      h('button', {
        type: 'button',
        class: 'btn btn-danger-quiet btn-small',
        text: 'Revoke',
        'aria-label': `Revoke ${d.name || 'device'}`,
        on: {
          click: () => confirmSheet({
            title: `Revoke ${d.name || 'this computer'}?`,
            text: 'Its token stops working at once. To sync it again, add it again and connect it with the new token.',
            ok: 'Revoke',
            action: async () => {
              await call('DELETE', `/api/devices/${encodeURIComponent(d.id)}`);
              toast('Computer revoked.');
              loadDevices();
              loadAudit();
            },
          }),
        },
      }))));
  }, loadDevices);
}

function showDeviceToken(name, token) {
  const box = $('device-token');
  const cmd = `cockpit todo connect ${location.origin}`;
  const copyToken = h('button', { type: 'button', class: 'btn btn-primary', text: 'Copy token' });
  copyToken.addEventListener('click', () => copyText(token, copyToken));
  const copyCmd = h('button', { type: 'button', class: 'btn btn-secondary', text: 'Copy command' });
  copyCmd.addEventListener('click', () => copyText(cmd, copyCmd));
  fill(box,
    h('p', { class: 'status-line' }, h('strong', { text: `Token for ${name}` })),
    h('p', { class: 'callout', text: 'Copy it now: it is shown only once.' }),
    h('p', { class: 'secret', text: token }),
    h('div', { class: 'btn-row' }, copyToken),
    h('p', { class: 'hint', text: 'On that computer, run this, then paste the token when it asks:' }),
    h('p', { class: 'cmd', text: cmd }),
    h('div', { class: 'btn-row' }, copyCmd,
      h('button', {
        type: 'button',
        class: 'btn btn-secondary',
        text: 'Done',
        on: { click: () => { box.replaceChildren(); box.hidden = true; } },
      })),
  );
  box.hidden = false;
  box.scrollIntoView({ block: 'nearest', behavior: reduceMotion.matches ? 'auto' : 'smooth' });
}

function auditDetails(details) {
  if (details === null || details === undefined || details === '') return '';
  if (typeof details === 'string') return details;
  if (typeof details === 'object') {
    return Object.entries(details).map(([k, v]) => `${k}: ${typeof v === 'object' ? JSON.stringify(v) : v}`).join(' · ');
  }
  return String(details);
}

function loadAudit() {
  const panel = $('audit-panel');
  loadInto(panel, '/api/audit', (data) => {
    const events = Array.isArray(data.events) ? data.events : [];
    if (!events.length) {
      fill(panel, panelNote('No activity yet.'));
      return;
    }
    fill(panel, ...events.map((ev) => {
      const sub = [ev.ip, auditDetails(ev.details)].filter(Boolean).join(' · ');
      return h('div', { class: 'audit-row' },
        h('span', { class: 'audit-event', text: ev.event || '' }),
        h('time', { class: 'audit-time', datetime: ev.at || '', text: fmtDateTime(ev.at) }),
        sub ? h('span', { class: 'audit-sub', text: sub }) : null);
    }));
  }, loadAudit);
}

function groupSecret(secret) {
  return (String(secret).replace(/\s+/g, '').match(/.{1,4}/g) || []).join(' ');
}

function renderMfa() {
  const panel = $('mfa-panel');
  panel.classList.add('stack');

  if (mfaFlow && mfaFlow.stage === 'codes') {
    const codes = mfaFlow.codes;
    const copyAll = h('button', { type: 'button', class: 'btn btn-primary', text: 'Copy all' });
    copyAll.addEventListener('click', () => copyText(codes.join('\n'), copyAll));
    fill(panel, 
      h('p', { class: 'status-line' }, h('span', { class: 'status-dot is-on', 'aria-hidden': 'true' }), h('strong', { text: 'Two-step verification is on' })),
      h('p', { class: 'callout', text: 'Save these recovery codes now, in your password manager or on paper. Each one signs you in once without the authenticator. They are not shown again.' }),
      h('ul', { class: 'codes', 'aria-label': 'Recovery codes' }, codes.map((c) => h('li', { text: c }))),
      h('div', { class: 'btn-row' }, copyAll,
        h('button', {
          type: 'button',
          class: 'btn btn-secondary',
          text: "I've saved them",
          on: { click: async () => { mfaFlow = null; await refreshMe(); renderMfa(); loadAudit(); } },
        })),
    );
    return;
  }

  if (mfaFlow && mfaFlow.stage === 'setup') {
    const copyKey = h('button', { type: 'button', class: 'btn btn-secondary', text: 'Copy key' });
    copyKey.addEventListener('click', () => copyText(String(mfaFlow.secret).replace(/\s+/g, ''), copyKey));
    const uri = String(mfaFlow.uri || '');
    const addLink = /^otpauth:\/\//i.test(uri) ? h('a', { class: 'btn btn-primary', href: uri, text: 'Add to Passwords or an authenticator' }) : null;
    const form = h('form', { class: 'stack', novalidate: true },
      h('label', { class: 'field' },
        h('span', { class: 'field-label', text: 'Then enter the 6-digit code it shows' }),
        h('input', { type: 'text', name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', pattern: '[0-9]*', maxlength: '6', enterkeyhint: 'done', autocapitalize: 'off', spellcheck: 'false' })),
      h('p', { class: 'form-error', role: 'alert', hidden: true }),
      h('div', { class: 'btn-row' },
        h('button', { type: 'submit', class: 'btn btn-primary', text: 'Turn on' }),
        h('button', { type: 'button', class: 'btn btn-secondary', text: 'Cancel', on: { click: () => { mfaFlow = null; renderMfa(); } } })));
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const code = form.elements.code.value.replace(/\D/g, '');
      if (code.length !== 6) {
        formError(form, 'Enter the 6 digits your authenticator shows.');
        return;
      }
      const button = form.querySelector('button[type="submit"]');
      busy(button, true);
      formError(form, '');
      try {
        const r = await call('POST', '/api/mfa/confirm', { code }, { inputCheck: true });
        mfaFlow = { stage: 'codes', codes: Array.isArray(r.recoveryCodes) ? r.recoveryCodes.map(String) : [] };
        renderMfa();
      } catch (err) {
        formError(form, err.message);
        busy(button, false);
      }
    });
    fill(panel, 
      h('p', { class: 'quiet', text: 'Add this key to Passwords or your authenticator app. On this iPhone, the button below does it for you.' }),
      h('p', { class: 'secret', 'aria-label': 'Setup key', text: groupSecret(mfaFlow.secret) }),
      h('div', { class: 'btn-row' }, addLink, copyKey),
      form,
    );
    return;
  }

  if (!mfa) {
    fill(panel, panelNote(reachable ? 'Loading' : 'Not available offline.'));
    return;
  }

  if (mfa.on) {
    const left = Number(mfa.recoveryLeft) || 0;
    fill(panel, 
      h('p', { class: 'status-line' }, h('span', { class: 'status-dot is-on', 'aria-hidden': 'true' }), h('strong', { text: 'On' })),
      h('p', { class: 'quiet', text: `Signing in asks for a code from your authenticator. ${left === 1 ? '1 recovery code' : `${left} recovery codes`} left.` }),
      left <= 2 ? h('p', { class: 'callout', text: 'Few recovery codes left. Turning two-step verification off and on again gives a new set.' }) : null,
      h('div', { class: 'btn-row' }, h('button', {
        type: 'button',
        class: 'btn btn-danger-quiet',
        text: 'Turn off',
        on: {
          click: () => confirmSheet({
            title: 'Turn off two-step verification?',
            text: 'Signing in will ask only for the password. Every session ends, this one too.',
            ok: 'Turn off',
            password: true,
            action: async (password) => {
              await call('POST', '/api/mfa/disable', { password }, { inputCheck: true });
              setTimeout(() => signedOut('Two-step verification is off. Every session ended: sign in again.'), 0);
            },
          }),
        },
      })),
    );
    return;
  }

  const start = h('button', { type: 'button', class: 'btn btn-primary', text: mfa.pending ? 'Start again' : 'Turn on' });
  start.addEventListener('click', async () => {
    busy(start, true);
    try {
      const r = await call('POST', '/api/mfa/start', {});
      mfaFlow = { stage: 'setup', secret: r.secret || '', uri: r.uri || '' };
      renderMfa();
    } catch (err) {
      toast(err.message);
      busy(start, false);
    }
  });
  fill(panel, 
    h('p', { class: 'status-line' }, h('span', { class: 'status-dot', 'aria-hidden': 'true' }), h('strong', { text: 'Off' })),
    h('p', { class: 'quiet', text: 'Ask for a 6-digit code from an authenticator after the password.' }),
    mfa.pending ? h('p', { class: 'hint', text: 'A setup was started and not finished.' }) : null,
    h('div', { class: 'btn-row' }, start),
  );
}

function wireSettings() {
  $('open-settings').addEventListener('click', openSettings);
  $('close-settings').addEventListener('click', closeSettings);
  $('sign-out').addEventListener('click', signOut);

  $('end-others').addEventListener('click', () => confirmSheet({
    title: 'End all other sessions?',
    text: 'Every other browser signed in to this list is signed out. Computers that sync keep their tokens.',
    ok: 'End sessions',
    action: async () => {
      const r = await call('POST', '/api/sessions/end-others', {});
      const n = Number(r && r.ended) || 0;
      toast(n === 1 ? '1 session ended.' : `${n} sessions ended.`);
      loadSessions();
      loadAudit();
    },
  }));

  const pwForm = $('form-change-password');
  pwForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const { current: cur, new: next, confirm } = pwForm.elements;
    formOk(pwForm, '');
    if (!cur.value) return formError(pwForm, 'Enter your current password.');
    if ([...next.value].length < 12 || [...next.value].length > 128) return formError(pwForm, 'The new password needs 12 to 128 characters.');
    if (next.value !== confirm.value) return formError(pwForm, "The two new passwords don't match.");
    const button = pwForm.querySelector('button[type="submit"]');
    busy(button, true);
    formError(pwForm, '');
    try {
      const r = await call('POST', '/api/password', { current: cur.value, new: next.value }, { inputCheck: true });
      if (r && r.csrf) csrf = r.csrf;
      pwForm.reset();
      formOk(pwForm, 'Password changed. Every other session was signed out.');
      loadSessions();
      loadAudit();
    } catch (err) {
      formError(pwForm, err.message);
    } finally {
      busy(button, false);
    }
  });

  const devForm = $('form-add-device');
  devForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const name = devForm.elements.name.value.trim();
    if (!name) return formError(devForm, 'Give the computer a name you will recognize.');
    const button = devForm.querySelector('button[type="submit"]');
    busy(button, true);
    formError(devForm, '');
    try {
      const r = await call('POST', '/api/devices', { name });
      devForm.reset();
      showDeviceToken((r.device && r.device.name) || name, String(r.token || ''));
      loadDevices();
      loadAudit();
    } catch (err) {
      formError(devForm, err.message);
    } finally {
      busy(button, false);
    }
  });
}

// ---------- start ----------

function registerWorker() {
  if (!('serviceWorker' in navigator)) return;
  navigator.serviceWorker.register('sw.js').catch(() => {});
}

async function boot() {
  restore();
  wireSignIn();
  wireAdd();
  wireEdit();
  wireConfirm();
  wireSettings();
  registerWorker();

  if (server) enterList();
  const ok = await refreshMe();
  if (ok) {
    enterList();
    sync();
  } else if (auth !== 'out') {
    if (server) enterList(); else show('offline-empty');
  }

  setInterval(tick, POLL_MS);
  document.addEventListener('visibilitychange', tick);
  window.addEventListener('online', tick);
  window.addEventListener('offline', () => markUnreachable(new NetworkError()));
}

boot();
