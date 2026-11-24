// Caches the app shell only, never /api/*. Every shell file is fetched from the network first (so a new
// version arrives on the next open) and falls back to the cache when the network fails or is slow.
// Bump VERSION when a shell file changes.
const VERSION = 'todo-shell-v3';
const SHELL = [
  './',
  'app.js',
  'app.css',
  'manifest.webmanifest',
  'icon-192.png',
  'icon-512.png',
  'apple-touch-icon.png',
];
const NETWORK_TIMEOUT_MS = 4000;

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(VERSION)
      .then((cache) => cache.addAll(SHELL.map((path) => new Request(path, { cache: 'reload' }))))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

const scope = new URL(self.registration.scope);
const shellURLs = new Set(SHELL.map((path) => new URL(path, scope).href));

function withTimeout(promise, ms) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timeout')), ms);
    promise.then((value) => { clearTimeout(timer); resolve(value); }, (err) => { clearTimeout(timer); reject(err); });
  });
}

async function networkFirst(request, key) {
  const cache = await caches.open(VERSION);
  try {
    const response = await withTimeout(fetch(request, { cache: 'no-cache' }), NETWORK_TIMEOUT_MS);
    if (response.ok && response.type === 'basic') await cache.put(key, response.clone());
    return response;
  } catch (err) {
    const cached = await cache.match(key);
    if (cached) return cached;
    throw err;
  }
}

self.addEventListener('fetch', (event) => {
  const { request } = event;
  if (request.method !== 'GET') return;
  const url = new URL(request.url);
  if (url.origin !== scope.origin || url.pathname.startsWith('/api/')) return;

  if (request.mode === 'navigate') {
    event.respondWith(networkFirst(request, new URL('./', scope).href));
    return;
  }
  const key = url.origin + url.pathname;
  if (shellURLs.has(key)) event.respondWith(networkFirst(request, key));
});
