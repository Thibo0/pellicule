'use strict';
/* =========================================================
   Coffre : tout ce qui touche aux clés et au chiffrement dans la page.
   Le serveur ne reçoit et ne renvoie que des données chiffrées ; c'est ici qu'elles
   sont ouvertes (miniatures, aperçus, vidéos, téléchargements) et fermées (envois).
   S'appuie sur PC (crypto.js).
   ========================================================= */

const Vault = (() => {
  const enc = new TextEncoder();
  const ALBUM_AAD = PC.L.album;
  const LINK_AAD = 'pellicule/share-link';
  let keys = null; // { username, aes, dedup, hkdf, priv, pub }

  /* ---------- requêtes ---------- */
  async function req(path, { method = 'GET', body, raw } = {}) {
    const o = { method, headers: { 'X-Pellicule': '1' }, credentials: 'same-origin' };
    if (raw !== undefined) { o.body = raw; o.headers['Content-Type'] = 'application/octet-stream'; }
    else if (body !== undefined) { o.body = JSON.stringify(body); o.headers['Content-Type'] = 'application/json'; }
    let r;
    try { r = await fetch(path, o); } catch { throw new Error('Serveur injoignable'); }
    const data = (r.headers.get('content-type') || '').includes('json') ? await r.json() : null;
    if (!r.ok) { const e = new Error(data?.error || `Erreur ${r.status}`); e.status = r.status; e.data = data; throw e; }
    return data;
  }
  // Go encode les []byte en base64 standard ; on accepte les deux alphabets
  const bytes = s => (s ? PC.fromB64(s) : new Uint8Array(0));
  const b64std = b => { const s = PC.toB64(b).replace(/-/g, '+').replace(/_/g, '/'); return s + '='.repeat((4 - s.length % 4) % 4); };

  /* ---------- stockage local des clés (IndexedDB, clés NON extractibles) ---------- */
  const DB = 'pellicule-vault';
  function idb(mode, fn) {
    return new Promise((resolve, reject) => {
      const open = indexedDB.open(DB, 1);
      open.onupgradeneeded = () => open.result.createObjectStore('keys');
      open.onerror = () => reject(open.error);
      open.onsuccess = () => {
        const tx = open.result.transaction('keys', mode);
        const r = fn(tx.objectStore('keys'));
        tx.oncomplete = () => { open.result.close(); resolve(r?.result); };
        tx.onerror = () => { open.result.close(); reject(tx.error); };
      };
    });
  }
  const saveKeys = k => idb('readwrite', s => s.put(k, 'current')).catch(() => {});
  const loadKeys = () => idb('readonly', s => s.get('current')).catch(() => null);
  const clearKeys = () => idb('readwrite', s => s.delete('current')).catch(() => {});

  async function restore(username) {
    if (keys?.username === username) return true;
    const k = await loadKeys();
    if (k && k.username === username && k.aes) { keys = k; return true; }
    // dans l'app Android, la clé maître est gardée par le Keystore du téléphone ; elle passe par un canal
    // que l'app n'ouvre que pour l'origine exacte du serveur épinglé
    if (window.PelliculeKeys) {
      const mk = await new Promise(resolve => {
        window.PelliculeKeys.onmessage = e => resolve(e.data);
        window.PelliculeKeys.postMessage('mk');
        setTimeout(() => resolve(''), 5000);
      });
      if (mk) {
        await unlockWithMK(PC.fromB64(mk), await req('/api/keys'), username);
        return true;
      }
    }
    return false;
  }

  // unlockWithMK : la clé maître devient deux CryptoKey non extractibles ; ses octets sont effacés.
  async function unlockWithMK(mkRaw, bundle, username) {
    const mk = await PC.importMasterKey(mkRaw);
    mkRaw.fill(0);
    const pkcs8 = await PC.open(mk.aes, bytes(bundle.wrappedPriv), PC.L.priv);
    // la clé publique vient du serveur (et, après une reconstruction, d'un fichier du disque des photos) :
    // remplacée, elle ferait chiffrer les photos importées pour quelqu'un d'autre
    const pub = await PC.publicFromPrivate(pkcs8);
    if (PC.toHex(pub) !== PC.toHex(bytes(bundle.pub))) {
      pkcs8.fill(0);
      throw new Error('Les clés de ton compte ont été modifiées sur le serveur : ne continue pas et préviens la personne qui l’a installé.');
    }
    const priv = await PC.importPrivate(pkcs8);
    pkcs8.fill(0);
    keys = { username, ...mk, priv, pub: bytes(bundle.pub) };
    await saveKeys(keys);
  }

  async function openMasterKey(wrapKey, wrappedMK) {
    try { return await PC.open(wrapKey, bytes(wrappedMK), PC.L.mk); }
    catch { throw new Error('Mot de passe incorrect'); }
  }

  /* ---------- comptes ---------- */
  async function login(username, password) {
    const { salt } = await req('/api/prelogin', { method: 'POST', body: { username } });
    const pk = await PC.passwordKeys(password, bytes(salt));
    const r = await req('/api/login', { method: 'POST', body: { username, authKey: b64std(pk.authKey) } });
    await unlockWithMK(await openMasterKey(pk.wrapKey, r.keys.wrappedMK), r.keys, username);
  }

  // unlock : session valide mais clés absentes de ce navigateur (données effacées…) : on redemande le mot de passe.
  async function unlock(username, password) {
    const { salt } = await req('/api/prelogin', { method: 'POST', body: { username } });
    const pk = await PC.passwordKeys(password, bytes(salt));
    const bundle = await req('/api/keys');
    await unlockWithMK(await openMasterKey(pk.wrapKey, bundle.wrappedMK), bundle, username);
  }

  // createAccount : toutes les clés naissent ici. Renvoie la clé de secours à afficher une seule fois.
  async function createAccount(path, username, password, extra = {}) {
    const mkRaw = PC.randomBytes(32), rec = PC.randomBytes(32), salt = PC.randomBytes(16);
    const pk = await PC.passwordKeys(password, salt);
    const acct = await PC.generateAccountKeys();
    const body = {
      ...extra, username,
      authKey: b64std(pk.authKey), salt: b64std(salt),
      wrappedMK: b64std(await PC.seal(pk.wrapKey, mkRaw, PC.L.mk)),
      recoveryMK: b64std(await PC.seal(await PC.recoveryWrapKey(rec), mkRaw, PC.L.mk)),
      recoveryAuth: b64std(await PC.recoveryAuth(rec)),
      pub: b64std(acct.pub),
      wrappedPriv: b64std(await PC.seal(mkRaw, acct.pkcs8, PC.L.priv)),
    };
    acct.pkcs8.fill(0);
    await req(path, { method: 'POST', body });
    await unlockWithMK(mkRaw, { wrappedPriv: body.wrappedPriv, pub: body.pub }, username.trim());
    const s = PC.formatRecovery(rec);
    rec.fill(0);
    return s;
  }

  // rewrapFor : nouveau mot de passe → nouvelle clé d'authentification et clé maître rechiffrée.
  async function rewrapFor(password, mkRaw) {
    const salt = PC.randomBytes(16);
    const pk = await PC.passwordKeys(password, salt);
    return { authKey: b64std(pk.authKey), salt: b64std(salt), wrappedMK: b64std(await PC.seal(pk.wrapKey, mkRaw, PC.L.mk)) };
  }

  async function changePassword(current, next, logoutDevices = false) {
    const { salt } = await req('/api/prelogin', { method: 'POST', body: { username: keys.username } });
    const cur = await PC.passwordKeys(current, bytes(salt));
    const bundle = await req('/api/keys');
    const mkRaw = await openMasterKey(cur.wrapKey, bundle.wrappedMK);
    try {
      await req('/api/me/password', { method: 'POST', body: { currentAuthKey: b64std(cur.authKey), logoutDevices, ...await rewrapFor(next, mkRaw) } });
    } finally { mkRaw.fill(0); }
  }

  async function recover(username, recoveryText, newPassword) {
    const rec = PC.parseRecovery(recoveryText);
    const st = await req('/api/recover/start', { method: 'POST', body: { username } });
    let mkRaw;
    try { mkRaw = await PC.open(await PC.recoveryWrapKey(rec), bytes(st.recoveryMK), PC.L.mk); }
    catch { throw new Error('Clé de secours incorrecte'); }
    await req('/api/recover', { method: 'POST', body: { username, recoveryAuth: b64std(await PC.recoveryAuth(rec)), ...await rewrapFor(newPassword, mkRaw.slice()) } });
    await unlockWithMK(mkRaw, await req('/api/keys'), username);
  }

  async function forget() { keys = null; cache.clear(); urls.clear(); await clearKeys(); }

  /* ---------- ouverture des éléments ---------- */
  // src : { owner: true } ou { share: { token, sk } } (sk = CryptoKey de la clé de partage)
  const cache = new Map(); // `${scope}:${id}` → { fk, meta }
  const scopeOf = src => (src.share ? 's' + src.share.token.slice(0, 12) : 'o');
  const pendingRewrap = [];
  let rewrapTimer = null;

  async function openFK(a, src) {
    const box = bytes(a.fk);
    if (src.share) return PC.open(src.share.sk, box, PC.L.shareFk);
    if (a.sealed) {
      // clé scellée par l'import pour notre clé publique : on la rechiffre avec la clé maître
      const fk = await PC.openFromPublic(keys.priv, keys.pub, box);
      pendingRewrap.push({ id: a.id, fk: b64std(await PC.seal(keys.aes, fk, PC.L.fk)) });
      clearTimeout(rewrapTimer);
      rewrapTimer = setTimeout(flushRewrap, 1500);
      return fk;
    }
    return PC.open(keys.aes, box, PC.L.fk);
  }
  async function flushRewrap() {
    const batch = pendingRewrap.splice(0, 500);
    if (!batch.length) return;
    try { await req('/api/assets/keys', { method: 'POST', body: { keys: batch } }); } catch { pendingRewrap.push(...batch); }
    if (pendingRewrap.length) rewrapTimer = setTimeout(flushRewrap, 1500);
  }

  async function hydrateOne(a, src) {
    const k = scopeOf(src) + ':' + a.id + (a.meta ? '' : ':cle');
    let c = cache.get(k);
    if (!c) {
      try {
        const fk = await openFK(a, src);
        // les couvertures d'album arrivent sans métadonnées : seule la clé compte
        const meta = a.meta ? await PC.openJSON(fk, bytes(a.meta), PC.L.meta) : {};
        c = { fk, meta };
      } catch {
        c = { fk: null, meta: { name: '(illisible)' } };
      }
      if (!a.meta && c.fk) return Object.assign(a, { src, fkRaw: c.fk, broken: false, filename: '', mime: '' });
      cache.set(k, c);
    }
    const m = c.meta;
    return Object.assign(a, {
      src, fkRaw: c.fk, broken: !c.fk, filename: m.name || 'photo', mime: m.mime || '',
      width: m.w || 0, height: m.h || 0, duration: m.dur || 0, plainSize: m.size || PC.plainSize(a.size),
    });
  }
  const hydrate = (items, src) => Promise.all(items.map(a => hydrateOne(a, src)));

  /* ---------- miniatures et aperçus (URL blob:, cache borné) ---------- */
  const urls = new Map(); // clé → Promise<string>
  const MAX_URLS = 1200;
  let active = 0;
  const waiters = [];
  const slot = () => (active < 6 ? (active++, Promise.resolve()) : new Promise(r => waiters.push(r)));
  const release = () => { const w = waiters.shift(); w ? w() : active--; };

  function blobBase(a) {
    return a.src.share ? `/api/s/${a.src.share.token}/assets/${a.id}` : `/api/assets/${a.id}`;
  }

  // imageURL : variant "thumb" | "preview" ; null si l'élément n'en a pas (format non lisible, etc.)
  function imageURL(a, variant) {
    if (!a.fkRaw || !(variant === 'thumb' ? a.thumb : a.preview)) return Promise.resolve(null);
    const k = scopeOf(a.src) + ':' + a.id + ':' + variant;
    let p = urls.get(k);
    if (p) { urls.delete(k); urls.set(k, p); return p; } // LRU
    p = (async () => {
      await slot();
      try {
        const r = await fetch(`${blobBase(a)}/${variant}`, { credentials: 'same-origin' });
        if (!r.ok) throw new Error(`Erreur ${r.status}`);
        const plain = await PC.open(a.fkRaw, new Uint8Array(await r.arrayBuffer()), variant === 'thumb' ? PC.L.thumb : PC.L.preview);
        return URL.createObjectURL(new Blob([plain], { type: 'image/jpeg' }));
      } finally { release(); }
    })();
    p.catch(() => urls.delete(k));
    urls.set(k, p);
    while (urls.size > MAX_URLS) {
      const [old, op] = urls.entries().next().value;
      urls.delete(old);
      op.then(u => u && setTimeout(() => URL.revokeObjectURL(u), 60000)).catch(() => {});
    }
    return p;
  }

  /* ---------- originaux ---------- */
  const CRC = (() => {
    const t = new Uint32Array(256);
    for (let n = 0; n < 256; n++) { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; t[n] = c >>> 0; }
    return t;
  })();
  const crcUpdate = (crc, b) => { for (let i = 0; i < b.length; i++) crc = CRC[(crc ^ b[i]) & 255] ^ (crc >>> 8); return crc; };

  // decryptOriginal : télécharge et déchiffre en flux (blocs de 1 Mio) ; renvoie { blob, crc }.
  async function decryptOriginal(a, onProgress) {
    if (!a.fkRaw) throw new Error('Fichier illisible');
    const r = await fetch(`${blobBase(a)}/original`, { credentials: 'same-origin' });
    if (!r.ok) throw new Error(r.status === 403 ? 'Téléchargement désactivé' : `Erreur ${r.status}`);
    const reader = r.body.getReader();
    const fk = await PC.importAes(a.fkRaw);
    const full = PC.CHUNK_SIZE + PC.TAG_SIZE;
    const nChunks = Math.max(1, Math.ceil((a.size - PC.STREAM_HDR) / full));
    let buf = new Uint8Array(0), hdr = null, idx = 0, got = 0, crc = 0xffffffff;
    const parts = [];
    // déchiffre tous les blocs complets reçus, sauf le dernier (traité en fin de flux, avec son drapeau)
    const take = async () => {
      if (!hdr && buf.length >= PC.STREAM_HDR) { hdr = PC.parseHeader(buf.slice(0, PC.STREAM_HDR)).hdr; buf = buf.slice(PC.STREAM_HDR); }
      if (!hdr) return;
      let off = 0;
      while (idx < nChunks - 1 && buf.length - off >= full) {
        const plain = await PC.decryptChunk(fk, hdr, idx++, buf.subarray(off, off + full), false);
        crc = crcUpdate(crc, plain);
        parts.push(new Blob([plain]));
        off += full;
      }
      if (off) buf = buf.slice(off);
    };
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      got += value.length;
      const nb = new Uint8Array(buf.length + value.length);
      nb.set(buf); nb.set(value, buf.length);
      buf = nb;
      await take();
      onProgress?.(got, a.size);
    }
    if (!hdr || idx !== nChunks - 1) throw new Error('Fichier tronqué');
    const plain = await PC.decryptChunk(fk, hdr, idx, buf, true);
    crc = crcUpdate(crc, plain);
    parts.push(plain);
    return { blob: new Blob(parts, { type: safeMime(a.mime) }), crc: (crc ^ 0xffffffff) >>> 0 };
  }

  function saveBlob(blob, name) {
    const u = URL.createObjectURL(blob);
    const el = document.createElement('a');
    el.href = u; el.download = name;
    document.body.appendChild(el); el.click(); el.remove();
    setTimeout(() => URL.revokeObjectURL(u), 120000);
  }

  // ZIP sans compression (photos et vidéos le sont déjà), écrit dans un Blob (le navigateur le garde sur disque).
  function zipBlob(entries) {
    const parts = [], central = [];
    let offset = 0;
    const dosTime = d => ((d.getUTCHours() << 11) | (d.getUTCMinutes() << 5) | (d.getUTCSeconds() >> 1)) & 0xffff;
    const dosDate = d => (((d.getUTCFullYear() - 1980) << 9) | ((d.getUTCMonth() + 1) << 5) | d.getUTCDate()) & 0xffff;
    for (const e of entries) {
      const name = enc.encode(e.name), d = new Date(e.date || Date.now()), size = e.blob.size;
      if (offset + size > 0xfffffff0) throw new Error('Sélection trop lourde pour un seul ZIP (4 Go max) : télécharge en plusieurs fois');
      const h = new DataView(new ArrayBuffer(30));
      h.setUint32(0, 0x04034b50, true); h.setUint16(4, 20, true); h.setUint16(6, 0x0800, true); h.setUint16(8, 0, true);
      h.setUint16(10, dosTime(d), true); h.setUint16(12, dosDate(d), true); h.setUint32(14, e.crc, true);
      h.setUint32(18, size, true); h.setUint32(22, size, true); h.setUint16(26, name.length, true); h.setUint16(28, 0, true);
      const c = new DataView(new ArrayBuffer(46));
      c.setUint32(0, 0x02014b50, true); c.setUint16(4, 20, true); c.setUint16(6, 20, true); c.setUint16(8, 0x0800, true);
      c.setUint16(10, 0, true); c.setUint16(12, dosTime(d), true); c.setUint16(14, dosDate(d), true); c.setUint32(16, e.crc, true);
      c.setUint32(20, size, true); c.setUint32(24, size, true); c.setUint16(28, name.length, true); c.setUint32(42, offset, true);
      parts.push(h.buffer, name, e.blob);
      central.push(c.buffer, name);
      offset += 30 + name.length + size;
    }
    const cdSize = central.reduce((n, p) => n + p.byteLength, 0);
    const end = new DataView(new ArrayBuffer(22));
    end.setUint32(0, 0x06054b50, true); end.setUint16(8, entries.length, true); end.setUint16(10, entries.length, true);
    end.setUint32(12, cdSize, true); end.setUint32(16, offset, true);
    return new Blob([...parts, ...central, end.buffer], { type: 'application/zip' });
  }

  async function download(items, zipName, onProgress) {
    if (items.length === 1) {
      const { blob } = await decryptOriginal(items[0], (d, t) => onProgress?.(d, t));
      return saveBlob(blob, items[0].filename);
    }
    const total = items.reduce((n, a) => n + a.size, 0);
    let before = 0;
    const entries = [], used = new Map();
    for (const a of items) {
      const { blob, crc } = await decryptOriginal(a, d => onProgress?.(before + d, total));
      before += a.size;
      let n = a.filename;
      const c = used.get(n.toLowerCase()) || 0;
      used.set(n.toLowerCase(), c + 1);
      if (c) { const dot = n.lastIndexOf('.'); n = dot > 0 ? `${n.slice(0, dot)} (${c + 1})${n.slice(dot)}` : `${n} (${c + 1})`; }
      entries.push({ name: n, blob, crc, date: a.takenAt });
    }
    saveBlob(zipBlob(entries), zipName + '.zip');
  }

  /* ---------- vidéos : déchiffrement en flux par le service worker ---------- */
  async function videoURL(a) {
    // dans l'app Android : l'app déchiffre elle-même, par plages (le service worker n'y est pas disponible)
    if (window.PelliculeAndroid?.registerVideo && a.fkRaw && !a.src.share) {
      const key = 'o-' + a.id;
      const e = videoEntry(a);
      window.PelliculeAndroid.registerVideo(key, JSON.stringify({ ...e, fk: PC.toB64(e.fk), url: location.origin + e.url }));
      return `/vault/${key}`;
    }
    const sw = navigator.serviceWorker?.controller;
    if (sw && a.fkRaw) {
      const key = scopeOf(a.src) + '-' + a.id;
      sw.postMessage({ type: 'vault-key', key, entry: videoEntry(a) });
      return `/vault/${key}`;
    }
    // pas de service worker (navigation privée de Firefox…) : on déchiffre tout avant de lire
    const { blob } = await decryptOriginal(a);
    return URL.createObjectURL(blob);
  }
  const videoEntry = a => ({ fk: a.fkRaw, url: `${blobBase(a)}/original`, mime: /^video\//.test(safeMime(a.mime)) ? a.mime : 'video/mp4', plainSize: a.plainSize, encSize: a.size });
  // le service worker a été arrêté et a perdu ses clés : il les redemande à la page
  const videoKeys = new Map();
  function rememberVideo(a) { videoKeys.set(scopeOf(a.src) + '-' + a.id, a); }
  navigator.serviceWorker?.addEventListener('message', e => {
    if (e.data?.type !== 'vault-need') return;
    const a = videoKeys.get(e.data.key);
    e.ports[0]?.postMessage(a ? videoEntry(a) : null);
  });

  /* ---------- envoi : tout est chiffré avant de quitter la page ---------- */
  // Type d'un contenu déchiffré : il vient des métadonnées, donc de celui qui a envoyé le fichier (lien de
  // partage d'un tiers…). Jamais text/html ni SVG : une URL blob: ou /vault/ hérite de notre origine.
  const safeMime = m => (/^(image\/(jpeg|png|gif|webp|heic|heif|avif|tiff|x-adobe-dng)|video\/[\w.+-]+|audio\/[\w.+-]+)$/i.test(m || '') ? m : 'application/octet-stream');

  const MIME = { jpg: 'image/jpeg', jpeg: 'image/jpeg', png: 'image/png', gif: 'image/gif', webp: 'image/webp', heic: 'image/heic',
    heif: 'image/heif', avif: 'image/avif', tif: 'image/tiff', tiff: 'image/tiff', dng: 'image/x-adobe-dng', mp4: 'video/mp4',
    m4v: 'video/mp4', mov: 'video/quicktime', webm: 'video/webm', '3gp': 'video/3gpp', mkv: 'video/x-matroska', avi: 'video/x-msvideo', mts: 'video/mp2t' };
  const mimeOf = f => f.type || MIME[(f.name.split('.').pop() || '').toLowerCase()] || 'application/octet-stream';

  // date EXIF (DateTimeOriginal) d'un JPEG, en « heure murale » stockée comme UTC (comme le serveur et l'app)
  async function exifDate(file) {
    if (!/jpe?g$/i.test(file.name) && file.type !== 'image/jpeg') return 0;
    const b = new DataView(await file.slice(0, 128 * 1024).arrayBuffer());
    if (b.byteLength < 4 || b.getUint16(0) !== 0xffd8) return 0;
    let o = 2;
    while (o + 4 < b.byteLength) {
      const marker = b.getUint16(o), len = b.getUint16(o + 2);
      if (marker === 0xffe1 && b.getUint32(o + 4) === 0x45786966) {
        const t = o + 10, le = b.getUint16(t) === 0x4949;
        const u16 = p => b.getUint16(t + p, le), u32 = p => b.getUint32(t + p, le);
        const find = (ifd, tag) => {
          if (t + ifd + 2 > b.byteLength) return null;
          const n = u16(ifd);
          for (let i = 0; i < n; i++) { const e = ifd + 2 + i * 12; if (t + e + 12 > b.byteLength) return null; if (u16(e) === tag) return e; }
          return null;
        };
        let e = find(u32(4), 0x8769); // pointeur vers le sous-IFD Exif
        e = e !== null ? find(u32(e + 8), 0x9003) : null; // DateTimeOriginal
        if (e === null) return 0;
        const p = t + u32(e + 8);
        let s = '';
        for (let i = 0; i < 19 && p + i < b.byteLength; i++) s += String.fromCharCode(b.getUint8(p + i));
        const m = s.match(/^(\d{4}):(\d\d):(\d\d) (\d\d):(\d\d):(\d\d)/);
        return m ? Date.UTC(+m[1], m[2] - 1, +m[3], +m[4], +m[5], +m[6]) : 0;
      }
      if ((marker & 0xff00) !== 0xff00) break;
      o += 2 + len;
    }
    return 0;
  }
  const wallClock = ms => { const d = new Date(ms); return Date.UTC(d.getFullYear(), d.getMonth(), d.getDate(), d.getHours(), d.getMinutes(), d.getSeconds()); };

  // Miniature carrée 400 px + aperçu 1600 px, calculés ici (le serveur ne voit jamais l'image).
  async function canvasJpeg(source, sw, sh, size, square) {
    let sx = 0, sy = 0, w = sw, h = sh;
    if (square) { const side = Math.min(sw, sh); sx = (sw - side) / 2; sy = (sh - side) / 2; w = h = side; }
    const scale = Math.min(1, size / Math.max(w, h));
    const cw = Math.max(1, Math.round(w * scale)), ch = Math.max(1, Math.round(h * scale));
    const c = document.createElement('canvas');
    c.width = cw; c.height = ch;
    c.getContext('2d').drawImage(source, sx, sy, w, h, 0, 0, cw, ch);
    const blob = await new Promise(r => c.toBlob(r, 'image/jpeg', 0.82));
    return new Uint8Array(await blob.arrayBuffer());
  }
  async function makeThumbs(file, kind) {
    if (kind === 'video') {
      const v = document.createElement('video');
      v.muted = true; v.playsInline = true; v.preload = 'metadata';
      const u = URL.createObjectURL(file);
      try {
        v.src = u;
        await new Promise((res, rej) => { v.onloadedmetadata = res; v.onerror = rej; setTimeout(rej, 15000); });
        v.currentTime = Math.min(1, (v.duration || 0) / 2);
        await new Promise((res, rej) => { v.onseeked = res; v.onerror = rej; setTimeout(rej, 15000); });
        const w = v.videoWidth, h = v.videoHeight;
        return { w, h, dur: v.duration || 0, thumb: await canvasJpeg(v, w, h, 400, true), preview: await canvasJpeg(v, w, h, 1600, false) };
      } catch { return { dur: v.duration || 0 }; } finally { URL.revokeObjectURL(u); }
    }
    try {
      const bmp = await createImageBitmap(file, { imageOrientation: 'from-image' });
      const r = { w: bmp.width, h: bmp.height, thumb: await canvasJpeg(bmp, bmp.width, bmp.height, 400, true),
        preview: await canvasJpeg(bmp, bmp.width, bmp.height, 1600, false) };
      bmp.close();
      return r;
    } catch { return {}; } // HEIC hors Safari, RAW… : pas de miniature, l'original reste sauvegardé
  }

  async function contentHash(file, onProgress) {
    const digests = [];
    for (let o = 0; o < file.size || (o === 0 && !digests.length); o += PC.CHUNK_SIZE) {
      digests.push(await PC.chunkDigest(new Uint8Array(await file.slice(o, o + PC.CHUNK_SIZE).arrayBuffer())));
      onProgress?.(Math.min(file.size, o + PC.CHUNK_SIZE));
      if (!file.size) break;
    }
    return PC.hashOfChunkDigests(digests);
  }

  const deviceTagOf = async f => PC.toHex(await PC.deviceTag(keys, f.name, f.size));

  async function existingTags(files) {
    const tags = await Promise.all(files.map(deviceTagOf));
    const found = new Set();
    for (let k = 0; k < tags.length; k += 2000) {
      const r = await req('/api/assets/check', { method: 'POST', body: { tags: tags.slice(k, k + 2000) } });
      r.existing.forEach(t => found.add(t));
    }
    return files.filter((f, i) => found.has(tags[i]));
  }

  const PART_CHUNKS = 8; // 8 Mio par requête

  // uploadFile → "created" | "duplicate". onBytes(n) : octets du fichier traités depuis le dernier appel.
  async function uploadFile(file, onBytes) {
    const kind = mimeOf(file).startsWith('video/') ? 'video' : 'image';
    const hash = await contentHash(file);
    const dedupTag = PC.toHex(await PC.dedupTag(keys, hash));
    const { fk, prefix } = await PC.fileKeys(keys, hash);
    const fkKey = await PC.importAes(fk);
    const th = await makeThumbs(file, kind);
    const taken = (await exifDate(file).catch(() => 0)) || wallClock(file.lastModified || Date.now());
    const meta = { name: file.name, mime: mimeOf(file), size: file.size, w: th.w || 0, h: th.h || 0, dur: th.dur || 0 };
    const encSize = PC.streamSize(file.size);

    const r = await req('/api/assets', { method: 'POST', body: {
      kind, takenAt: taken, size: encSize, dedupTag, deviceTag: await deviceTagOf(file),
      fk: b64std(await PC.seal(keys.aes, fk, PC.L.fk)), meta: b64std(await PC.sealJSON(fkKey, meta, PC.L.meta)),
    } });
    if (r.status === 'duplicate') { onBytes(file.size); return 'duplicate'; }
    const base = `/api/assets/${r.id}`;
    if (th.thumb) await req(`${base}/thumb`, { method: 'PUT', raw: await PC.seal(fkKey, th.thumb, PC.L.thumb) });
    if (th.preview) await req(`${base}/preview`, { method: 'PUT', raw: await PC.seal(fkKey, th.preview, PC.L.preview) });

    // flux chiffré, envoyé par morceaux à partir de ce que le serveur a déjà (reprise identique : clé dérivée du contenu)
    const hdr = PC.streamHeader(prefix);
    const full = PC.CHUNK_SIZE + PC.TAG_SIZE;
    const nChunks = Math.max(1, Math.ceil(file.size / PC.CHUNK_SIZE));
    let uploaded = r.uploaded || 0;
    let chunk = Math.max(0, Math.floor((uploaded - PC.STREAM_HDR) / full));
    onBytes(Math.min(file.size, chunk * PC.CHUNK_SIZE));
    while (uploaded < encSize) {
      const startOff = chunk === 0 ? 0 : PC.STREAM_HDR + chunk * full;
      const pieces = chunk === 0 ? [hdr] : [];
      const end = Math.min(nChunks, chunk + PART_CHUNKS);
      for (let c = chunk; c < end; c++) {
        const plain = new Uint8Array(await file.slice(c * PC.CHUNK_SIZE, (c + 1) * PC.CHUNK_SIZE).arrayBuffer());
        pieces.push(await PC.encryptChunk(fkKey, hdr, c, plain, c === nChunks - 1));
      }
      const data = PC.concat(...pieces).subarray(uploaded - startOff);
      try {
        const res = await req(`${base}/original?offset=${uploaded}`, { method: 'PUT', raw: data });
        uploaded = res.uploaded;
      } catch (e) {
        if (e.status !== 409) throw e;
        const again = await req('/api/assets', { method: 'POST', body: { kind, takenAt: taken, size: encSize, dedupTag,
          fk: b64std(await PC.seal(keys.aes, fk, PC.L.fk)), meta: b64std(await PC.sealJSON(fkKey, meta, PC.L.meta)) } });
        if (again.status === 'duplicate') return 'duplicate';
        uploaded = again.uploaded;
        chunk = Math.max(0, Math.floor((uploaded - PC.STREAM_HDR) / full));
        continue;
      }
      onBytes(Math.min(file.size, end * PC.CHUNK_SIZE) - Math.min(file.size, chunk * PC.CHUNK_SIZE));
      chunk = end;
    }
    await req(`${base}/complete`, { method: 'POST' });
    return 'created';
  }

  /* ---------- albums ---------- */
  const sealName = async name => b64std(await PC.seal(keys.aes, enc.encode(name), ALBUM_AAD));
  async function openName(box, key = keys.aes) {
    try { return new TextDecoder().decode(await PC.open(key, bytes(box), ALBUM_AAD)); } catch { return '(nom illisible)'; }
  }
  const hydrateAlbum = async al => Object.assign(al, { name: await openName(al.name) });

  // Liens de partage : la clé du lien vit dans le fragment (#), jamais envoyé au serveur.
  async function shareKeyOf(share) {
    const link = new TextDecoder().decode(await PC.open(keys.aes, bytes(share.link), LINK_AAD));
    return { link, sk: PC.fromB64(link.split('#')[1]) };
  }
  async function wrapForShare(sk, items) {
    return Promise.all(items.filter(a => a.fkRaw).map(async a => ({ asset: a.id, fk: b64std(await PC.seal(sk, a.fkRaw, PC.L.shareFk)) })));
  }
  async function createShare(albumId, albumName, items, allowDownload, days) {
    const sk = PC.randomBytes(32);
    const token = PC.toHex(PC.randomBytes(24));
    const link = `${location.origin}/s/${token}#${PC.toB64(sk)}`;
    await req(`/api/albums/${albumId}/shares`, { method: 'POST', body: {
      token, allowDownload, days,
      link: b64std(await PC.seal(keys.aes, enc.encode(link), LINK_AAD)),
      name: b64std(await PC.seal(sk, enc.encode(albumName), ALBUM_AAD)),
      keys: await wrapForShare(sk, items),
    } });
    return link;
  }
  // après un ajout de photos ou un renommage : chaque lien existant reçoit les clés / le nom
  async function syncShares(albumId, items, newName) {
    const shares = await req(`/api/albums/${albumId}/shares`);
    const names = {};
    for (const s of shares) {
      const { sk } = await shareKeyOf(s);
      if (items?.length) await req(`/api/shares/${s.id}/keys`, { method: 'POST', body: { keys: await wrapForShare(sk, items) } });
      if (newName) names[s.id] = b64std(await PC.seal(sk, enc.encode(newName), ALBUM_AAD));
    }
    return names;
  }

  async function shareFromURL(token) {
    const frag = location.hash.slice(1);
    if (!frag) throw new Error('Lien incomplet : la clé de l’album manque après le « # »');
    let sk;
    try { sk = await PC.importAes(PC.fromB64(frag)); } catch { throw new Error('Lien invalide'); }
    return { token, sk };
  }

  return {
    req, bytes, b64std, get keys() { return keys; }, restore, login, unlock, createAccount, changePassword, recover, forget,
    hydrate, imageURL, decryptOriginal, download, saveBlob, videoURL, rememberVideo, existingTags, uploadFile,
    sealName, openName, hydrateAlbum, shareKeyOf, createShare, syncShares, shareFromURL, flushRewrap,
  };
})();
