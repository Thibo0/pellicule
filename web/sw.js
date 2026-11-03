// Service worker : garde l'interface en cache (app installable) et déchiffre les vidéos en flux.
// Une vidéo est servie à la balise <video> depuis /vault/<clé> : le worker télécharge seulement les blocs
// chiffrés de la plage demandée (Range), les déchiffre et les renvoie. On peut donc avancer dans une
// longue vidéo sans tout télécharger. Les clés ne sont gardées qu'en mémoire.
importScripts('/crypto.js');

const CACHE = 'pellicule-v6';
const SHELL = ['/', '/app.js', '/vault.js', '/crypto.js', '/qr.js', '/style.css', '/icon.svg', '/manifest.webmanifest'];
const WINDOW = 4 * PC.CHUNK_SIZE; // une réponse couvre au plus 4 Mio : le lecteur redemande la suite

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(c => c.addAll(SHELL)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))).then(() => self.clients.claim()));
});

const keys = new Map(); // clé → { fk: CryptoKey, url, mime, plainSize, encSize, hdr }

self.addEventListener('message', e => {
  if (e.data?.type === 'vault-key') store(e.data.key, e.data.entry);
});

async function store(key, entry) {
  if (!entry) return null;
  const v = { ...entry, fk: await PC.importAes(new Uint8Array(entry.fk)) };
  keys.set(key, v);
  if (keys.size > 50) keys.delete(keys.keys().next().value);
  return v;
}

// le worker a pu être arrêté entre deux requêtes : on redemande la clé à la page qui lit la vidéo
async function lookup(key, clientId) {
  if (keys.has(key)) return keys.get(key);
  const clients = clientId ? [await self.clients.get(clientId)] : await self.clients.matchAll({ type: 'window' });
  for (const c of clients.filter(Boolean)) {
    const entry = await new Promise(res => {
      const ch = new MessageChannel();
      ch.port1.onmessage = m => res(m.data);
      c.postMessage({ type: 'vault-need', key }, [ch.port2]);
      setTimeout(() => res(null), 4000);
    });
    if (entry) return store(key, entry);
  }
  return null;
}

async function fetchRange(url, start, end) {
  const r = await fetch(url, { headers: { Range: `bytes=${start}-${end}` }, credentials: 'same-origin' });
  if (r.status !== 206 && r.status !== 200) throw new Error('HTTP ' + r.status);
  const b = new Uint8Array(await r.arrayBuffer());
  return r.status === 200 ? b.subarray(start, end + 1) : b;
}

async function serveVault(request, key, clientId) {
  const v = await lookup(key, clientId);
  if (!v) return new Response('clé inconnue', { status: 404 });
  const size = v.plainSize;
  const m = /bytes=(\d*)-(\d*)/.exec(request.headers.get('Range') || '');
  let start = m && m[1] ? +m[1] : 0;
  let end = m && m[2] ? +m[2] : size - 1;
  if (m && !m[1] && m[2]) { start = Math.max(0, size - +m[2]); end = size - 1; } // bytes=-N
  if (start >= size) return new Response(null, { status: 416, headers: { 'Content-Range': `bytes */${size}` } });
  end = Math.min(end, size - 1, start + WINDOW - 1);

  if (!v.hdr) v.hdr = PC.parseHeader(await fetchRange(v.url, 0, PC.STREAM_HDR - 1)).hdr;
  const CS = PC.CHUNK_SIZE, full = CS + PC.TAG_SIZE;
  const nChunks = Math.max(1, Math.ceil(size / CS));
  const c0 = Math.floor(start / CS), c1 = Math.floor(end / CS);
  const encStart = PC.STREAM_HDR + c0 * full;
  const encEnd = Math.min(v.encSize - 1, PC.STREAM_HDR + (c1 + 1) * full - 1);
  const data = await fetchRange(v.url, encStart, encEnd);
  const parts = [];
  for (let c = c0; c <= c1; c++) {
    const off = (c - c0) * full;
    parts.push(await PC.decryptChunk(v.fk, v.hdr, c, data.subarray(off, Math.min(data.length, off + full)), c === nChunks - 1));
  }
  const plain = PC.concat(...parts).subarray(start - c0 * CS, end - c0 * CS + 1);
  // réponse fabriquée ici : elle ne porte pas les en-têtes du serveur, on les pose nous-mêmes
  const mime = /^(video|audio)\/[\w.+-]+$/i.test(v.mime || '') ? v.mime : 'video/mp4';
  return new Response(plain, { status: 206, headers: {
    'Content-Type': mime, 'Content-Length': String(plain.length), 'Accept-Ranges': 'bytes',
    'Content-Range': `bytes ${start}-${end}/${size}`, 'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff', 'Content-Security-Policy': "default-src 'none'; sandbox",
  } });
}

self.addEventListener('fetch', e => {
  const url = new URL(e.request.url);
  if (url.origin !== location.origin || e.request.method !== 'GET') return;
  if (url.pathname.startsWith('/vault/')) {
    e.respondWith(serveVault(e.request, url.pathname.slice(7), e.clientId).catch(err => new Response(String(err), { status: 500 })));
    return;
  }
  if (url.pathname.startsWith('/api/')) return; // données : jamais en cache
  // interface : réseau d'abord (toujours la dernière version), cache si hors ligne
  e.respondWith(
    fetch(e.request)
      .then(r => {
        if (r.ok && SHELL.includes(url.pathname)) {
          const copy = r.clone();
          caches.open(CACHE).then(c => c.put(e.request, copy));
        }
        return r;
      })
      .catch(() => caches.match(e.request).then(r => r || caches.match('/')))
  );
});
