///offline-оболочка: при потере сети страница и сканер продолжают работать
const C = 'hg-v2';
const CORE = ['/', '/jsQR.js', '/zxing.js'];

self.addEventListener('install', e => {
  e.waitUntil(caches.open(C).then(c => c.addAll(CORE)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', e => {
  e.waitUntil(
    caches.keys()
      .then(ks => Promise.all(ks.filter(k => k !== C).map(k => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', e => {
  const r = e.request;
  if (r.method !== 'GET') return; // POST-запросы (API) мимо кэша
  if (new URL(r.url).pathname === '/reset') return; // сброс — всегда напрямую по сети
  const same = new URL(r.url).origin === location.origin;
  if (r.mode === 'navigate') {
    // свежая версия при сети, кэш — при её отсутствии (свайп-обновление тоже)
    e.respondWith(
      fetch(r)
        .then(resp => {
          const c = resp.clone();
          caches.open(C).then(x => x.put('/', c));
          return resp;
        })
        .catch(() => caches.match('/'))
    );
    return;
  }
  if (same && (r.url.endsWith('/jsQR.js') || r.url.endsWith('/zxing.js'))) {
    e.respondWith(
      fetch(r)
        .then(resp => {
          const c = resp.clone();
          caches.open(C).then(x => x.put(r, c));
          return resp;
        })
        .catch(() => caches.match(r))
    );
  }
});
