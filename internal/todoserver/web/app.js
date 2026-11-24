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

const VIEWS = ['loading', 'signin', 'offline-empty', 'list', 'settings', 'sub'];
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
  el.classList.toggle('is-raised', current === 'list');
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
const dayWordFmt = new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', month: 'short' });
const dayMonthWordFmt = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short' });

const DAY = 86_400_000;

function startOfDay(d) { return new Date(d.getFullYear(), d.getMonth(), d.getDate()); }
function parseISODate(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s || '');
  return m ? new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3])) : null;
}
function dayDiff(a, b) { return Math.round((startOfDay(a) - startOfDay(b)) / DAY); }
function validTime(s) { const d = new Date(s); return Number.isNaN(d.getTime()) ? null : d; }

function fmtDateTime(s) { const d = validTime(s); return d ? dateTimeFmt.format(d) : ''; }

function fmtDay(s) { const d = validTime(s); return d ? dayMonthWordFmt.format(d).toLowerCase() : ''; }

function fmtClock(s, now = new Date()) {
  const d = validTime(s);
  if (!d) return '';
  return dayDiff(d, now) === 0 ? timeFmt.format(d) : `${fmtDay(s)} ${timeFmt.format(d)}`;
}

function fmtWhen(s, now = new Date()) {
  const d = validTime(s);
  if (!d) return '';
  return dayDiff(d, now) === 0 ? timeFmt.format(d) : dateTimeFmt.format(d);
}

// agoShort is the dashboard's age column: "now", "5m ago", "2h ago", "3d ago".
function agoShort(s, now = new Date()) {
  const d = validTime(s);
  if (!d) return 'never';
  const mins = Math.round((now - d) / 60000);
  if (mins < 1) return 'now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days}d ago`;
  return fmtDay(s);
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

function dueDays(due, now) {
  const at = parseISODate(due);
  return at ? dayDiff(at, now) : null;
}

// The dashboard's wording (internal/tui/rail.go, due): "2d late", "today", "tomorrow", "in 3d", with
// its state glyphs: ▲ for late (stuck), ● for today (waiting).
function dueLabel(due, now) {
  const days = dueDays(due, now);
  if (days === null) return null;
  if (days < 0) return { glyph: '▲', text: `${-days}d late`, cls: 'due-late' };
  if (days === 0) return { glyph: '●', text: 'today', cls: 'due-today' };
  if (days === 1) return { glyph: '', text: 'tomorrow', cls: 'due-tomorrow' };
  return { glyph: '', text: `in ${days}d`, cls: '' };
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

// dueToken writes a due date the way a person types it and parse.go reads it back to the same day:
// @25/11 for a coming date, @25/11/2026 for today or a past one (a bare day and month means the next
// such day, so it would jump a year).
function dueToken(due, now = new Date()) {
  const at = parseISODate(due);
  if (!at) return '@' + due;
  const short = `${at.getDate()}/${at.getMonth() + 1}`;
  if (dayDiff(at, now) > 0 && parseDate(short, now) === due) return '@' + short;
  return `@${short}/${at.getFullYear()}`;
}

function formatLine(t) {
  const parts = [t.text];
  if (t.group) parts.push('#' + t.group.replaceAll(' ', '_'));
  if (t.due) parts.push(dueToken(t.due));
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
    if (e instanceof ApiError && e.status === 423) signedOut(e.message);
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
  unreachableWhy = e instanceof ApiError ? 'server unreachable' : 'offline';
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

const SVG_NS = 'http://www.w3.org/2000/svg';

function icon(d, className) {
  const svg = document.createElementNS(SVG_NS, 'svg');
  svg.setAttribute('viewBox', '0 0 16 16');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  if (className) svg.setAttribute('class', className);
  const path = document.createElementNS(SVG_NS, 'path');
  path.setAttribute('d', d);
  svg.append(path);
  return svg;
}

const chevron = () => icon('M6 3.5 10.5 8 6 12.5', 'go');
const glyph = (g) => h('span', { 'aria-hidden': 'true', text: `${g} ` });

function renderNet() {
  const meta = $('sync-meta');
  const note = $('net-note');
  const waiting = queue.length;
  meta.classList.toggle('is-off', !reachable);
  if (!reachable) meta.textContent = server && server.at ? `offline · ${fmtClock(server.at)}` : 'offline';
  else if (waiting && auth === 'in') meta.textContent = 'sending';
  else meta.textContent = server && server.at ? `synced ${fmtClock(server.at)}` : '';
  if (reachable && !waiting) {
    note.hidden = true;
    return;
  }
  const parts = [];
  if (!reachable) {
    parts.push(unreachableWhy || 'offline');
    if (server && server.at) parts.push(`showing the list from ${fmtClock(server.at)}`);
  }
  if (waiting) parts.push(waiting === 1 ? '1 change waiting to be sent' : `${waiting} changes waiting to be sent`);
  if (reachable && auth === 'out') parts.push('sign in to send them');
  note.textContent = `○ ${parts.join(' · ')}`;
  note.hidden = false;
}

function renderTally(open, now) {
  let late = 0;
  let today = 0;
  for (const t of open) {
    const d = dueDays(t.due, now);
    if (d === null) continue;
    if (d < 0) late++;
    else if (d === 0) today++;
  }
  fill($('tally'),
    h('span', { class: late ? 'is-late' : 'is-zero' }, h('b', {}, glyph('▲'), String(late)), ' late'),
    h('span', { class: today ? 'is-today' : 'is-zero' }, h('b', {}, glyph('●'), String(today)), ' today'),
    h('span', { class: open.length ? '' : 'is-zero' }, glyph('□'), `${open.length} open`));
}

function dueNode(d) {
  return h('span', { class: cls('due', d.cls) }, d.glyph ? glyph(d.glyph) : null, d.text);
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
  if (done) side.append(h('span', { class: 'due', text: doneLabel(t.done, now) }));
  else {
    const d = dueLabel(t.due, now);
    if (d) side.append(dueNode(d));
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
      h('p', { class: 'empty-title', text: 'ALL DONE' }),
      h('p', { text: 'Nothing open. Type the next thing below.' }));
  }
  return h('div', { class: 'empty' },
    h('p', { class: 'empty-title', text: 'NOTHING TO DO YET' }),
    h('p', {}, 'Type a task below. ', h('span', { class: 'tok-g', text: '#group' }), ' files it, ',
      h('span', { class: 'tok-d', text: '@tomorrow' }), ' dates it.'));
}

function skeleton() {
  return h('div', { class: 'skeleton', 'aria-label': 'Loading the list' }, h('span'), h('span'));
}

function sortGroup(list) {
  return list.map((t, i) => [t, i]).sort(([a, i], [b, j]) => {
    if (!a.due !== !b.due) return a.due ? -1 : 1;
    if (a.due !== b.due) return a.due < b.due ? -1 : 1;
    return i - j;
  }).map(([t]) => t);
}

function render() {
  renderNet();
  const focused = document.activeElement && document.activeElement.dataset ? document.activeElement.dataset.key : null;
  if (!server) {
    $('tasks').replaceChildren(skeleton());
    $('done').hidden = true;
    fill($('tally'));
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
  let n = 0;
  for (const [g, list] of byGroup) {
    if (!list.length) continue;
    const sorted = sortGroup(list);
    const attn = sorted.some((t) => { const d = dueDays(t.due, now); return d !== null && d <= 0; });
    const headId = `g-${n++}`;
    frag.append(h('section', { class: cls('box group', attn && 'is-attn'), 'aria-labelledby': headId },
      h('h2', { class: 'box-h group-head' }, h('span', { id: headId, class: 'group-name', text: g }), h('span', { class: 'n', text: String(list.length) })),
      h('ul', { class: 'rows' }, sorted.map((t) => taskRow(t, pending, now)))));
  }
  $('tasks').replaceChildren(frag);

  $('done').hidden = done.length === 0;
  $('done-count').textContent = String(done.length);
  $('done-list').replaceChildren(...done.map((t) => taskRow(t, pending, now)));
  renderTally(open.filter((t) => !settling.has(t.id)), now);
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

// summary describes a quick-add line the way the server will read it: "→ WORK · ● today · mon 24 nov".
function summary(line, now = new Date()) {
  const groups = server ? server.groups : [];
  const p = parseLine(line, groups, now);
  const group = p.group || (server ? server.defaultGroup : 'Tasks');
  const parts = [h('span', { 'aria-hidden': 'true', text: '→' }), h('b', { text: group.toUpperCase() })];
  const at = parseISODate(p.due);
  if (at) {
    const d = dueLabel(p.due, now);
    parts.push(' · ');
    if (d.cls === 'due-late' || d.cls === 'due-today') parts.push(h('span', { class: d.cls === 'due-late' ? 'is-late' : 'is-today', text: d.text }), ' · ');
    else if (d.cls === 'due-tomorrow') parts.push('tomorrow · ');
    parts.push(`${d.cls ? '' : 'due '}${dayWordFmt.format(at).toLowerCase()}`);
  } else {
    parts.push(' · no date');
  }
  const loose = line.split(/\s+/).filter((w) => w.length > 1 && w[0] === '@' && !parseDate(w.slice(1), now));
  if (loose.length) parts.push(' · ', h('span', { class: 'is-late', text: `${loose[0]} is not a date` }));
  return parts;
}

function tokenCount(line, now = new Date()) {
  return line.split(/\s+/).filter((w) => w.length > 1 && (w[0] === '#' || (w[0] === '@' && parseDate(w.slice(1), now)))).length;
}

// ---------- adding ----------

function renderChips() {
  const input = $('add-input');
  const value = input.value;
  const words = value.split(/\s+/).map((w) => w.toLowerCase());
  const groups = server ? server.groups.filter((g) => g !== server.defaultGroup) : [];
  const key = (prefix, name, token) => h('button', {
    type: 'button',
    class: 'key chip',
    'aria-pressed': String(words.includes(token.toLowerCase())),
    on: { mousedown: (e) => e.preventDefault(), click: () => insertToken(prefix, token) },
  }, h('span', {}, h('i', { text: prefix }), name));
  fill($('keys'),
    groups.map((g) => key('#', g, '#' + g.replaceAll(' ', '_'))),
    groups.length ? h('span', { class: 'sep', 'aria-hidden': 'true' }) : null,
    key('@', 'today', '@today'),
    key('@', 'tomorrow', '@tomorrow'));
  const trimmed = value.trim();
  if (trimmed) fill($('add-parse'), summary(trimmed));
  else fill($('add-parse'), h('span', { 'aria-hidden': 'true', text: '→' }), '#group files it · @date: today, tomorrow, 25/12');
  $('add-form').querySelector('.add-btn').classList.toggle('is-ready', Boolean(trimmed));
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

function renderEditParse() {
  const line = $('edit-line').value.replace(/\s+/g, ' ').trim();
  const n = tokenCount(line);
  $('edit-tokens').textContent = n === 1 ? '1 token' : `${n} tokens`;
  fill($('edit-parse'), line ? summary(line) : null);
}

function openEdit(id) {
  const t = findTask(id);
  if (!t) return;
  editingId = id;
  const form = $('form-edit');
  formError(form, '');
  const area = $('edit-line');
  area.value = formatLine(t);
  const created = validTime(t.created);
  $('edit-meta').textContent = created ? `added ${dayMonthWordFmt.format(created).toLowerCase()}` : '';
  renderEditParse();
  $('edit-sheet').showModal();
  area.focus();
  area.setSelectionRange(area.value.length, area.value.length);
}

function closeSheet(dialog) {
  if (dialog.open) dialog.close();
}

function sameTask(t, line) {
  const p = parseLine(line, server ? server.groups : []);
  return p.text === t.text && (p.group || '') === (t.group || '') && (p.due || '') === (t.due || '');
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
    if (!sameTask(t, line)) mutate({ op: 'patch', id: t.id, body: { line } });
  });
  area.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      form.requestSubmit();
    }
  });
  area.addEventListener('input', renderEditParse);
  $('edit-cancel').addEventListener('click', () => closeSheet(sheet));
  $('edit-delete').addEventListener('click', async () => {
    const t = findTask(editingId);
    if (!t) return closeSheet(sheet);
    await confirmSheet({
      title: 'Delete this task?',
      text: t.text,
      ok: 'Delete task',
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

// confirmSheet asks before something that cannot be undone, or for the password before a sensitive
// change. action(password, name) runs on confirm; when it throws, its message shows in the sheet and
// the sheet stays open. name, when given, adds an editable name field holding it.
let confirmState = null;

function confirmSheet({ title, text, ok, password = false, name = null, tone = 'danger', action }) {
  const sheet = $('confirm-sheet');
  const form = $('form-confirm');
  $('confirm-title').textContent = title;
  $('confirm-text').textContent = text || '';
  $('confirm-text').hidden = !text;
  $('confirm-ok').textContent = ok;
  $('confirm-ok').className = tone === 'primary' ? 'btn btn-gold' : 'btn btn-danger';
  $('confirm-password-field').hidden = !password;
  $('confirm-name-field').hidden = name === null;
  form.elements.name.value = name || '';
  form.elements.password.value = '';
  formError(form, '');
  return new Promise((resolve) => {
    confirmState = { resolve, action, password, named: name !== null, done: false };
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
    const name = form.elements.name.value.trim();
    if (st.named && !name) {
      formError(form, 'Give it a name you will recognize.');
      return;
    }
    if (st.password && !pw) {
      formError(form, 'Enter your password.');
      return;
    }
    const button = $('confirm-ok');
    busy(button, true);
    try {
      await st.action(pw, name);
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

// ---------- passkeys ----------
// The server sends binary WebAuthn fields as base64url and wants them back the same way, unpadded.

function b64urlEncode(data) {
  const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function b64urlDecode(s) {
  const b64 = String(s).replace(/-/g, '+').replace(/_/g, '/');
  const bin = atob(b64 + '='.repeat((4 - (b64.length % 4)) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

const decodeCreds = (list) => (Array.isArray(list) ? list.map((c) => ({ ...c, id: b64urlDecode(c.id) })) : undefined);

function getOptions(pk) {
  const o = { ...pk, challenge: b64urlDecode(pk.challenge) };
  if (pk.allowCredentials) o.allowCredentials = decodeCreds(pk.allowCredentials);
  return o;
}

function createOptions(pk) {
  const o = { ...pk, challenge: b64urlDecode(pk.challenge), user: { ...pk.user, id: b64urlDecode(pk.user.id) } };
  if (pk.excludeCredentials) o.excludeCredentials = decodeCreds(pk.excludeCredentials);
  return o;
}

const passkeysSupported = () => typeof window.PublicKeyCredential === 'function' && Boolean(navigator.credentials);
const isApple = () => /iPhone|iPad/.test(navigator.userAgent) || (/Macintosh/.test(navigator.userAgent) && navigator.maxTouchPoints > 1);
const isCancel = (e) => e && (e.name === 'NotAllowedError' || e.name === 'AbortError');

function webauthnMessage(e) {
  if (e && e.name === 'InvalidStateError') return 'This device already has a passkey for this list.';
  if (e && e.name === 'SecurityError') return 'Passkeys need the app on its own HTTPS address.';
  if (e && e.name === 'NotSupportedError') return 'This device cannot make a passkey.';
  return (e && e.message) || 'The passkey did not work.';
}

function passkeyDeviceName() {
  const ua = navigator.userAgent;
  if (/iPhone/.test(ua)) return 'iPhone';
  if (/iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return 'iPad';
  if (/Macintosh|Mac OS X/.test(ua)) return 'Mac';
  if (/Android/.test(ua)) return 'Android';
  if (/Windows/.test(ua)) return 'Windows';
  if (/Linux/.test(ua)) return 'Linux';
  return 'This device';
}

async function finishPasskeySignIn(cred) {
  const r = cred.response;
  const res = await api('POST', '/api/login/passkey', {
    id: cred.id,
    clientDataJSON: b64urlEncode(r.clientDataJSON),
    authenticatorData: b64urlEncode(r.authenticatorData),
    signature: b64urlEncode(r.signature),
    userHandle: r.userHandle ? b64urlEncode(r.userHandle) : '',
  });
  if (!res || res.next !== 'done') throw new Error('Unexpected answer from the server.');
  await afterSignIn(res.csrf);
}

function passkeyError(message) {
  const el = $('passkey-error');
  el.textContent = message || '';
  el.hidden = !message;
}

// Conditional UI: while the sign-in screen is up, a background request lets Safari offer the passkey
// in the password field's suggestions. Its challenge lasts 5 minutes, so it is renewed before that.
let conditional = null;
let conditionalTimer = 0;
let conditionalOK = null;

function stopConditional() {
  clearTimeout(conditionalTimer);
  if (conditional) conditional.abort();
  conditional = null;
}

async function startConditional() {
  stopConditional();
  if (!passkeysSupported() || current !== 'signin' || auth === 'in') return;
  if (conditionalOK === null) {
    try {
      conditionalOK = Boolean(await PublicKeyCredential.isConditionalMediationAvailable?.());
    } catch {
      conditionalOK = false;
    }
  }
  if (!conditionalOK || current !== 'signin' || auth === 'in') return;
  const ctl = new AbortController();
  conditional = ctl;
  conditionalTimer = setTimeout(startConditional, 4 * 60_000);
  let cred;
  try {
    const opts = await api('POST', '/api/login/passkey/options', {});
    if (ctl.signal.aborted) return;
    cred = await navigator.credentials.get({ publicKey: getOptions(opts.publicKey), mediation: 'conditional', signal: ctl.signal });
  } catch {
    return;
  }
  if (ctl.signal.aborted || !cred) return;
  stopConditional();
  passkeyError('');
  try {
    await finishPasskeySignIn(cred);
  } catch (e) {
    passkeyError(e.message);
  }
}

async function passkeySignIn() {
  stopConditional();
  const button = $('passkey-signin');
  busy(button, true);
  passkeyError('');
  formError($('form-password'), '');
  try {
    const opts = await api('POST', '/api/login/passkey/options', {});
    const cred = await navigator.credentials.get({ publicKey: getOptions(opts.publicKey), mediation: 'optional' });
    if (cred) await finishPasskeySignIn(cred);
  } catch (e) {
    if (!isCancel(e)) passkeyError(e instanceof ApiError || e instanceof NetworkError ? e.message : webauthnMessage(e));
  } finally {
    busy(button, false);
    if (auth !== 'in') startConditional();
  }
}

function setupPasskeySignIn() {
  if (!passkeysSupported()) return;
  $('passkey-block').hidden = false;
  $('passkey-label').textContent = isApple() ? 'Sign in with Face ID' : 'Sign in with a passkey';
  const device = passkeyDeviceName();
  $('signin-device').textContent = device === 'This device' ? 'this device' : `this ${device}`;
  $('signin-username').setAttribute('autocomplete', 'username webauthn');
  $('signin-password').setAttribute('autocomplete', 'current-password webauthn');
  $('passkey-signin').addEventListener('click', passkeySignIn);
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
  passkeyError('');
  $('passkey-block').hidden = !passkeysSupported();
  signInStep('password');
  readyEnter(pwForm.elements.password);
  show('signin', 'signin-title');
  startConditional();
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
  stopConditional();
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
  input.setAttribute('placeholder', on ? 'recovery code' : '6-digit code');
  $('mfa-toggle').textContent = on ? 'Use a 6-digit code' : 'Use a recovery code';
  readyEnter(input);
}

function readyEnter(input) {
  input.form.querySelector('.enter').classList.toggle('is-ready', Boolean(input.value.trim()));
}

function signInStep(step) {
  $('signin-box-title').textContent = step === 'mfa' ? '2-STEP CODE' : 'SIGN IN';
  $('signin-foot').textContent = step === 'mfa'
    ? 'the code from your authenticator app, or a recovery code'
    : 'a 6-digit code follows if 2-step is on';
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
    stopConditional();
    passkeyError('');
    const button = pwForm.querySelector('button[type="submit"]');
    busy(button, true);
    formError(pwForm, '');
    try {
      const r = await api('POST', '/api/login', { password });
      pwForm.elements.password.value = '';
      readyEnter(pwForm.elements.password);
      if (r && r.next === 'mfa') {
        pwForm.hidden = true;
        $('passkey-block').hidden = true;
        mfaForm.hidden = false;
        signInStep('mfa');
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
      startConditional();
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

  const pwInput = $('signin-password');
  pwInput.addEventListener('input', () => readyEnter(pwInput));
  codeInput.addEventListener('input', () => {
    readyEnter(codeInput);
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
    signInStep('password');
    $('passkey-block').hidden = !passkeysSupported();
    formError(pwForm, '');
    pwForm.elements.password.focus();
    startConditional();
  });
  setupPasskeySignIn();
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
// The main screen is boxed lists of rows, as the dashboard draws its panels; password, passkeys,
// two-step verification, adding a computer and the full activity log open as detail screens.

const known = { passkeys: null, sessions: null };
let sub = null;

const SUB_TITLES = {
  password: 'PASSWORD',
  passkeys: 'PASSKEYS',
  mfa: '2-STEP',
  device: 'ADD A COMPUTER',
  activity: 'ACTIVITY',
};

function openSettings() {
  show('settings', 'settings-title');
  window.scrollTo(0, 0);
  renderSignInRows();
  refreshMe().then(renderSignInRows);
  loadPasskeys();
  loadSessions();
  loadDevices();
  loadAudit();
}

function closeSettings() {
  enterList();
  $('open-settings').focus({ preventScroll: true });
}

function openSub(name, from) {
  sub = { name, from };
  for (const key of Object.keys(SUB_TITLES)) $(`sub-${key}`).hidden = key !== name;
  const title = name === 'passkeys' && isApple() ? 'FACE ID' : SUB_TITLES[name];
  fill($('sub-title'), h('span', { 'aria-hidden': 'true', text: '◆' }), ` ${title}`);
  if (name === 'password') {
    const form = $('form-change-password');
    form.reset();
    formError(form, '');
    formOk(form, '');
  }
  if (name === 'device') {
    const form = $('form-add-device');
    form.reset();
    form.hidden = false;
    formError(form, '');
    $('device-token').replaceChildren();
    $('device-token').hidden = true;
  }
  if (name === 'mfa') {
    mfaFlow = null;
    renderMfa();
  }
  show('sub', 'sub-title');
  window.scrollTo(0, 0);
}

function closeSub() {
  const from = sub && sub.from;
  sub = null;
  mfaFlow = null;
  $('device-token').replaceChildren();
  $('device-token').hidden = true;
  show('settings');
  renderSignInRows();
  if (from) $(from)?.focus({ preventScroll: true });
}

function rowNote(text) { return h('li', { class: 'srow-note', text }); }

function rowError(message, retry) {
  return h('li', { class: 'srow-error', role: 'alert' },
    h('span', { text: message }),
    h('button', { type: 'button', class: 'mini', text: 'retry', on: { click: retry } }));
}

async function loadInto(panel, path, draw, retry) {
  if (!panel.children.length) fill(panel, rowNote('loading'));
  try {
    const data = await call('GET', path);
    draw(data || {});
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) return;
    fill(panel, rowError(e.message, retry));
  }
}

function srow({ title, sub: small, value, cls: extra }) {
  return h('li', { class: cls('srow', extra) },
    h('span', { class: 'k' }, title, small ? h('small', { text: small }) : null),
    h('span', { class: 'v' }, value));
}

function plural(n, one, many) { return `${n} ${n === 1 ? one : many}`; }

function renderSignInRows() {
  const keys = known.passkeys;
  $('row-passkeys-n').textContent = keys ? String(keys.length) : '';
  $('row-passkeys-sub').textContent = keys ? (keys.length ? keys.map((k) => k.name || 'passkey').join(', ') : 'none yet') : '';
  const v = $('row-mfa-v');
  if (mfa) {
    v.textContent = mfa.on ? 'on' : 'off';
    v.className = mfa.on ? 'on' : '';
    $('row-mfa-sub').textContent = mfa.on ? plural(Number(mfa.recoveryLeft) || 0, 'recovery code left', 'recovery codes left') : 'a code after the password';
  } else {
    v.textContent = '';
    $('row-mfa-sub').textContent = reachable ? '' : 'not available offline';
  }
  const parts = [];
  if (mfa) parts.push(h('span', { class: mfa.on ? '' : 'is-zero' }, h('b', { class: mfa.on ? 'is-on' : '' }, glyph(mfa.on ? '●' : '○')), mfa.on ? '2-step on' : '2-step off'));
  if (keys) parts.push(h('span', { class: keys.length ? '' : 'is-zero' }, glyph('□'), plural(keys.length, 'passkey', 'passkeys')));
  if (known.sessions) parts.push(h('span', {}, glyph('○'), plural(known.sessions.length, 'session', 'sessions')));
  fill($('settings-tally'), parts);
}

function deviceParts(ua) {
  const s = String(ua || '');
  if (/cockpit/i.test(s)) return { os: 'cockpit', browser: '' };
  const os = /iPhone/.test(s) ? 'iPhone' : /iPad/.test(s) ? 'iPad' : /Android/.test(s) ? 'Android'
    : /Macintosh|Mac OS X/.test(s) ? 'Mac' : /Windows/.test(s) ? 'Windows' : /Linux/.test(s) ? 'Linux' : '';
  const browser = /Edg\//.test(s) ? 'Edge' : /CriOS|Chrome\//.test(s) ? 'Chrome' : /FxiOS|Firefox\//.test(s) ? 'Firefox'
    : /Safari\//.test(s) ? 'Safari' : /AppleWebKit/.test(s) && /iPhone|iPad/.test(s) ? 'home screen app' : '';
  return { os, browser, raw: s };
}

function deviceName(ua) {
  const { os, browser, raw } = deviceParts(ua);
  if (!raw) return 'Unknown device';
  if (os && browser) return browser === 'home screen app' ? `${os} app` : `${browser} on ${os}`;
  return os || browser || raw.slice(0, 60);
}

function endOthers() {
  return confirmSheet({
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
  });
}

function loadSessions() {
  const panel = $('sessions-panel');
  loadInto(panel, '/api/sessions', (data) => {
    const sessions = Array.isArray(data.sessions) ? data.sessions : [];
    sessions.sort((a, b) => (b.current ? 1 : 0) - (a.current ? 1 : 0));
    known.sessions = sessions;
    $('sessions-n').textContent = String(sessions.length);
    renderSignInRows();
    if (!sessions.length) {
      fill(panel, rowNote('no sessions'));
      return;
    }
    const rows = sessions.map((s) => {
      const { os, browser } = deviceParts(s.agent);
      const name = s.current && os ? `This ${os}` : deviceName(s.agent);
      const small = s.current
        ? [browser, 'now'].filter(Boolean).join(' · ')
        : [`active ${agoShort(s.lastUsed)}`, s.ip].filter(Boolean).join(' · ');
      const value = s.current
        ? h('span', { class: 'here' }, glyph('●'), 'here')
        : h('button', {
          type: 'button',
          class: 'mini',
          text: 'end',
          'aria-label': `End session: ${name}`,
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
      return srow({ title: name, sub: small, value });
    });
    if (sessions.some((s) => !s.current)) {
      rows.push(h('li', {}, h('button', { type: 'button', class: 'srow danger', id: 'end-others', on: { click: endOthers } },
        h('span', { class: 'k', text: 'End all other sessions' }), h('span', { class: 'v' }))));
    }
    fill(panel, rows);
  }, loadSessions);
}

function loadDevices() {
  const panel = $('devices-panel');
  loadInto(panel, '/api/devices', (data) => {
    const devices = Array.isArray(data.devices) ? data.devices : [];
    $('devices-n').textContent = String(devices.length);
    if (!devices.length) {
      fill(panel);
      return;
    }
    fill(panel, devices.map((d) => srow({
      title: d.name || 'Unnamed',
      sub: d.lastUsed ? `synced ${agoShort(d.lastUsed)}` : `never synced · added ${fmtDay(d.created)}`,
      value: h('button', {
        type: 'button',
        class: 'mini danger',
        text: 'revoke',
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
      }),
    })));
  }, loadDevices);
}

function showDeviceToken(name, token) {
  const box = $('device-token');
  const cmd = `cockpit todo connect ${location.origin}`;
  const copyToken = h('button', { type: 'button', class: 'btn btn-gold', text: 'Copy token' });
  copyToken.addEventListener('click', () => copyText(token, copyToken));
  const copyCmd = h('button', { type: 'button', class: 'btn btn-line', text: 'Copy command' });
  copyCmd.addEventListener('click', () => copyText(cmd, copyCmd));
  fill(box,
    h('p', { class: 'status-line' }, h('span', { class: 'on', 'aria-hidden': 'true', text: '●' }), `TOKEN FOR ${name.toUpperCase()}`),
    h('p', { class: 'callout', text: 'Copy it now: it is shown only once.' }),
    h('div', { class: 'box field-box' }, h('p', { class: 'box-h' }, h('span', { text: 'TOKEN' })), h('p', { class: 'secret', text: token })),
    copyToken,
    h('p', { class: 'hint', text: 'On that computer, run this, then paste the token when it asks:' }),
    h('div', { class: 'box field-box' }, h('p', { class: 'box-h' }, h('span', { text: 'COMMAND' })), h('p', { class: 'cmd', text: cmd })),
    h('div', { class: 'btn-row' }, copyCmd,
      h('button', { type: 'button', class: 'btn btn-line', text: 'Done', on: { click: closeSub } })),
  );
  $('form-add-device').hidden = true;
  box.hidden = false;
}

// auditTitle names an audit event in words (the server writes keys such as login_ok).
function auditTitle(ev) {
  const d = ev.details && typeof ev.details === 'object' ? ev.details : {};
  const count = Number(d.count) || 0;
  switch (ev.event) {
    case 'login_ok': return `signed in${d.with ? ` · ${d.with}` : ''}`;
    case 'login_fail': return `sign-in refused${d.reason ? ` · ${d.reason}` : ''}`;
    case 'locked': return 'locked after wrong passwords';
    case 'logout': return 'signed out';
    case 'password_changed': return 'password changed';
    case 'reauth_fail': return 'wrong password in settings';
    case 'sessions_revoked': return count === 1 ? 'session ended' : `${count || ''} sessions ended`.trim();
    case 'mfa_on': return '2-step turned on';
    case 'mfa_off': return '2-step turned off';
    case 'device_added': return `computer added${d.name ? ` · ${d.name}` : ''}`;
    case 'device_revoked': return `computer revoked${d.name ? ` · ${d.name}` : ''}`;
    case 'passkey_added': return `passkey added${d.passkey ? ` · ${d.passkey}` : ''}`;
    case 'passkey_removed': return `passkey removed${d.passkey ? ` · ${d.passkey}` : ''}`;
    default: return String(ev.event || 'event').replace(/[_.]+/g, ' ');
  }
}

function auditRow(ev, now) {
  const { os, browser } = deviceParts(ev.agent);
  const agent = ev.agent === 'cockpit-todo-server' ? 'server command line' : (browser && browser !== 'home screen app' ? `${browser} on ${os}` : os || browser);
  const at = validTime(ev.at);
  return srow({
    title: auditTitle(ev),
    sub: agent || (ev.ip !== 'server' ? ev.ip : ''),
    value: h('time', { datetime: ev.at || '', text: at ? (dayDiff(at, now) === 0 ? timeFmt.format(at) : fmtDay(ev.at)) : '' }),
    cls: 'log',
  });
}

function loadAudit() {
  const preview = $('activity-preview');
  const full = $('audit-panel');
  loadInto(preview, '/api/audit', (data) => {
    const events = Array.isArray(data.events) ? data.events : [];
    const now = new Date();
    const today = events.filter((ev) => { const at = validTime(ev.at); return at && dayDiff(at, now) === 0; }).length;
    $('activity-n').textContent = today ? `${today} today` : String(events.length);
    $('audit-n').textContent = String(events.length);
    if (!events.length) {
      fill(preview, rowNote('no activity yet'));
      fill(full, rowNote('no activity yet'));
      return;
    }
    fill(preview, events.slice(0, 3).map((ev) => auditRow(ev, now)));
    fill(full, events.map((ev) => auditRow(ev, now)));
  }, loadAudit);
}

function loadPasskeys() {
  const panel = $('passkeys-panel');
  loadInto(panel, '/api/passkeys', (data) => {
    const keys = Array.isArray(data.passkeys) ? data.passkeys : [];
    known.passkeys = keys;
    $('passkeys-n').textContent = String(keys.length);
    renderSignInRows();
    if (!keys.length) {
      fill(panel, rowNote('No passkeys yet.'));
      return;
    }
    fill(panel, keys.map((k) => srow({
      title: k.name || 'Passkey',
      sub: `added ${fmtDay(k.created)} · ${k.lastUsed ? `used ${agoShort(k.lastUsed)}` : 'never used'}`,
      value: h('button', {
        type: 'button',
        class: 'mini danger',
        text: 'remove',
        'aria-label': `Remove passkey ${k.name || ''}`.trim(),
        on: {
          click: () => confirmSheet({
            title: `Remove ${k.name || 'this passkey'}?`,
            text: 'It stops signing in to this list at once. The passkey itself stays in Passwords until you delete it there.',
            ok: 'Remove',
            action: async () => {
              await call('DELETE', `/api/passkeys/${encodeURIComponent(k.id)}`);
              toast('Passkey removed.');
              loadPasskeys();
              loadAudit();
            },
          }),
        },
      }),
    })));
  }, loadPasskeys);
}

async function addPasskey(password, name) {
  const opts = await call('POST', '/api/passkeys/options', { password });
  let cred;
  try {
    cred = await navigator.credentials.create({ publicKey: createOptions(opts.publicKey) });
  } catch (e) {
    if (isCancel(e)) throw new Error('Cancelled. Try again when you are ready.');
    throw new Error(webauthnMessage(e));
  }
  if (!cred) throw new Error('Cancelled. Try again when you are ready.');
  const r = cred.response;
  const transports = typeof r.getTransports === 'function' ? r.getTransports() : [];
  await call('POST', '/api/passkeys', {
    name,
    id: cred.id,
    clientDataJSON: b64urlEncode(r.clientDataJSON),
    attestationObject: b64urlEncode(r.attestationObject),
    transports,
  });
  toast(isApple() ? 'Face ID added. Use it the next time you sign in.' : 'Passkey added. Use it the next time you sign in.');
  loadPasskeys();
  loadAudit();
}

function setupPasskeySettings() {
  const button = $('add-passkey');
  if (!passkeysSupported()) {
    button.hidden = true;
    $('passkey-add-hint').textContent = 'This browser cannot make passkeys. Open the list in Safari on your iPhone to add Face ID.';
    return;
  }
  const label = isApple() ? 'Add Face ID' : 'Add a passkey';
  $('add-passkey-label').textContent = label;
  button.addEventListener('click', () => confirmSheet({
    title: label,
    text: 'Enter your password to add a passkey for this device.',
    ok: 'Continue',
    tone: 'primary',
    password: true,
    name: passkeyDeviceName(),
    action: addPasskey,
  }));
}

function groupSecret(secret) {
  return (String(secret).replace(/\s+/g, '').match(/.{1,4}/g) || []).join(' ');
}

function fieldBox(label, child) {
  return h('div', { class: 'box field-box' }, h('p', { class: 'box-h' }, h('span', { text: label })), child);
}

function renderMfa() {
  const panel = $('mfa-panel');

  if (mfaFlow && mfaFlow.stage === 'codes') {
    const codes = mfaFlow.codes;
    const copyAll = h('button', { type: 'button', class: 'btn btn-gold', text: 'Copy all' });
    copyAll.addEventListener('click', () => copyText(codes.join('\n'), copyAll));
    fill(panel,
      h('p', { class: 'status-line' }, h('span', { class: 'on', 'aria-hidden': 'true', text: '●' }), '2-STEP IS ON'),
      h('p', { class: 'callout', text: 'Save these recovery codes now, in your password manager or on paper. Each one signs you in once without the authenticator. They are not shown again.' }),
      h('div', { class: 'box' },
        h('p', { class: 'box-h' }, h('span', { text: 'RECOVERY CODES' }), h('span', { class: 'n', text: String(codes.length) })),
        h('ul', { class: 'codes', 'aria-label': 'Recovery codes' }, codes.map((c) => h('li', { text: c })))),
      h('div', { class: 'btn-row' }, copyAll,
        h('button', {
          type: 'button',
          class: 'btn btn-line',
          text: "I've saved them",
          on: { click: async () => { mfaFlow = null; await refreshMe(); renderMfa(); loadAudit(); } },
        })),
    );
    return;
  }

  if (mfaFlow && mfaFlow.stage === 'setup') {
    const copyKey = h('button', { type: 'button', class: 'btn btn-line', text: 'Copy key' });
    copyKey.addEventListener('click', () => copyText(String(mfaFlow.secret).replace(/\s+/g, ''), copyKey));
    const uri = String(mfaFlow.uri || '');
    const addLink = /^otpauth:\/\//i.test(uri) ? h('a', { class: 'btn btn-gold', href: uri, text: 'Add to Passwords' }) : null;
    const form = h('form', { method: 'post', class: 'stack', novalidate: true },
      h('label', { class: 'box field-box' },
        h('span', { class: 'box-h' }, h('span', { text: 'CODE IT SHOWS' })),
        h('input', { type: 'text', name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', pattern: '[0-9]*', maxlength: '6', enterkeyhint: 'done', autocapitalize: 'off', spellcheck: 'false', placeholder: '6 digits' })),
      h('p', { class: 'form-error', role: 'alert', hidden: true }),
      h('div', { class: 'btn-row' },
        h('button', { type: 'submit', class: 'btn btn-gold', text: 'Turn on' }),
        h('button', { type: 'button', class: 'btn btn-line', text: 'Cancel', on: { click: () => { mfaFlow = null; renderMfa(); } } })));
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
      h('p', { class: 'hint', text: 'Add this key to Passwords or your authenticator app. On this iPhone, Add to Passwords does it for you.' }),
      fieldBox('SETUP KEY', h('p', { class: 'secret', 'aria-label': 'Setup key', text: groupSecret(mfaFlow.secret) })),
      h('div', { class: 'btn-row' }, addLink, copyKey),
      form,
    );
    return;
  }

  if (!mfa) {
    fill(panel, h('p', { class: 'hint', text: reachable ? 'loading' : 'Not available offline.' }));
    return;
  }

  const statusBox = (on, extra) => h('div', { class: 'box' },
    h('p', { class: 'box-h' }, h('span', { text: 'TWO-STEP VERIFICATION' })),
    h('ul', { class: 'srows' },
      srow({ title: 'Status', sub: on ? 'a code after the password' : 'the password alone signs in', value: h('span', { class: on ? 'on' : '', text: on ? 'on' : 'off' }) }),
      extra));

  if (mfa.on) {
    const left = Number(mfa.recoveryLeft) || 0;
    fill(panel,
      statusBox(true, srow({ title: 'Recovery codes', sub: 'each signs in once without the phone', value: `${left} left` })),
      left <= 2 ? h('p', { class: 'callout', text: 'Few recovery codes left. Turning two-step verification off and on again gives a new set.' }) : null,
      h('div', { class: 'box box-plain' }, h('ul', { class: 'srows' }, h('li', {}, h('button', {
        type: 'button',
        class: 'srow danger',
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
      }, h('span', { class: 'k', text: 'Turn off' }), h('span', { class: 'v' }))))),
    );
    return;
  }

  const start = h('button', { type: 'button', class: 'btn btn-gold', text: mfa.pending ? 'Start again' : 'Turn on' });
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
    statusBox(false, null),
    h('p', { class: 'hint', text: mfa.pending ? 'A setup was started and not finished.' : 'Ask for a 6-digit code from an authenticator app after the password.' }),
    start,
  );
}

function wireSettings() {
  setupPasskeySettings();
  $('open-settings').addEventListener('click', openSettings);
  $('close-settings').addEventListener('click', closeSettings);
  $('close-sub').addEventListener('click', closeSub);
  $('sign-out').addEventListener('click', signOut);
  $('row-password').addEventListener('click', () => openSub('password', 'row-password'));
  $('row-passkeys').addEventListener('click', () => openSub('passkeys', 'row-passkeys'));
  $('row-mfa').addEventListener('click', () => openSub('mfa', 'row-mfa'));
  $('row-add-device').addEventListener('click', () => openSub('device', 'row-add-device'));
  $('row-activity').addEventListener('click', () => openSub('activity', 'row-activity'));

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
