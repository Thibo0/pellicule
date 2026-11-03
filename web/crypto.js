'use strict';
// Noyau cryptographique de Pellicule (format v1), identique à crypto.go et Crypto.kt.
// Tout le chiffrement/déchiffrement des photos se fait ici, dans le navigateur : le serveur ne voit
// que des données chiffrées. Script classique (pas de module) : chargé par la page et par sw.js.
// Vérifié contre testdata/vectors.json (node --test web/crypto.test.mjs).

(function (root) {
  const subtle = root.crypto.subtle;
  const enc = new TextEncoder();
  const dec = new TextDecoder();

  const KEY_SIZE = 32, NONCE_SIZE = 12, TAG_SIZE = 16;
  const CHUNK_SIZE = 1 << 20;
  const STREAM_HDR = 16;
  const PBKDF2_ITER = 600000;
  const L = {
    auth: 'pellicule auth', wrap: 'pellicule wrap', recovery: 'pellicule recovery', dedup: 'pellicule dedup',
    seal: 'pellicule seal', mk: 'pellicule/mk', fk: 'pellicule/fk', priv: 'pellicule/priv', meta: 'pellicule/meta',
    thumb: 'pellicule/thumb', preview: 'pellicule/preview', album: 'pellicule/album', shareFk: 'pellicule/share-fk',
    sealedFk: 'pellicule/fk-sealed', deviceNs: 'device|', recAuth: 'pellicule recovery auth',
    fileKey: 'pellicule file key|', filePrefix: 'pellicule file prefix|',
  };

  // --- encodages ---
  const toHex = b => Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
  const fromHex = s => new Uint8Array((s.match(/../g) || []).map(x => parseInt(x, 16)));
  function toB64(b) {
    let s = '';
    for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode.apply(null, b.subarray(i, i + 0x8000));
    return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }
  function fromB64(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    const bin = atob(s + '='.repeat((4 - s.length % 4) % 4));
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }
  const concat = (...parts) => {
    const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
    let o = 0;
    for (const p of parts) { out.set(p, o); o += p.length; }
    return out;
  };
  const randomBytes = n => root.crypto.getRandomValues(new Uint8Array(n));

  // --- clés ---
  const importAes = (raw, extractable = false) => subtle.importKey('raw', raw, 'AES-GCM', extractable, ['encrypt', 'decrypt']);
  const importHkdf = raw => subtle.importKey('raw', raw, 'HKDF', false, ['deriveBits', 'deriveKey']);
  async function hkdfBits(keyOrRaw, info, salt = new Uint8Array(0)) {
    const k = keyOrRaw instanceof Uint8Array ? await importHkdf(keyOrRaw) : keyOrRaw;
    return new Uint8Array(await subtle.deriveBits({ name: 'HKDF', hash: 'SHA-256', salt, info: enc.encode(info) }, k, KEY_SIZE * 8));
  }

  // Clé maître : importée en deux CryptoKey NON extractibles (chiffrement + dérivation).
  // Une fois importée, ses octets ne peuvent plus être lus par la page (ni par un script injecté).
  async function importMasterKey(raw) {
    const [aes, hkdf] = await Promise.all([importAes(raw), importHkdf(raw)]);
    const dedup = await subtle.importKey('raw', await hkdfBits(hkdf, L.dedup), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
    return { aes, dedup, hkdf };
  }

  // Clé et préfixe de nonce d'un fichier, dérivés de son contenu (voir FileKeys dans crypto.go).
  async function fileKeys(mk, contentHash) {
    const h = toHex(contentHash);
    const [fk, prefix] = await Promise.all([hkdfBits(mk.hkdf, L.fileKey + h), hkdfBits(mk.hkdf, L.filePrefix + h)]);
    return { fk, prefix: prefix.subarray(0, 8) };
  }

  // --- boîtes AES-GCM : nonce (12) || chiffré || tag (16) ---
  async function seal(key, plain, aad, nonce = randomBytes(NONCE_SIZE)) {
    const k = key instanceof Uint8Array ? await importAes(key) : key;
    const ct = new Uint8Array(await subtle.encrypt({ name: 'AES-GCM', iv: nonce, additionalData: enc.encode(aad) }, k, plain));
    return concat(nonce, ct);
  }
  async function open(key, box, aad) {
    const k = key instanceof Uint8Array ? await importAes(key) : key;
    if (box.length < NONCE_SIZE + TAG_SIZE) throw new Error('données chiffrées invalides');
    try {
      return new Uint8Array(await subtle.decrypt({ name: 'AES-GCM', iv: box.subarray(0, NONCE_SIZE), additionalData: enc.encode(aad) }, k, box.subarray(NONCE_SIZE)));
    } catch {
      throw new Error('déchiffrement impossible (clé incorrecte ou données altérées)');
    }
  }
  const sealJSON = (key, obj, aad) => seal(key, enc.encode(JSON.stringify(obj)), aad);
  const openJSON = async (key, box, aad) => JSON.parse(dec.decode(await open(key, box, aad)));

  // --- mot de passe → clé d'authentification (envoyée au serveur) + clé de chiffrement (reste ici) ---
  async function passwordKeys(password, salt) {
    const base = await subtle.importKey('raw', enc.encode(password.normalize('NFC')), 'PBKDF2', false, ['deriveBits']);
    const k = new Uint8Array(await subtle.deriveBits({ name: 'PBKDF2', hash: 'SHA-256', salt, iterations: PBKDF2_ITER }, base, KEY_SIZE * 8));
    const [authKey, wrapRaw] = await Promise.all([hkdfBits(k, L.auth), hkdfBits(k, L.wrap)]);
    return { authKey, wrapRaw, wrapKey: await importAes(wrapRaw) };
  }

  // --- clé de secours : base32 par groupes de 4 ---
  const B32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  function formatRecovery(k) {
    let bits = 0, val = 0, s = '';
    for (const b of k) {
      val = (val << 8) | b; bits += 8;
      while (bits >= 5) { s += B32[(val >>> (bits - 5)) & 31]; bits -= 5; }
    }
    if (bits > 0) s += B32[(val << (5 - bits)) & 31];
    return s.match(/.{1,4}/g).join('-');
  }
  function parseRecovery(s) {
    s = s.toUpperCase().replace(/[\s-]/g, '');
    let bits = 0, val = 0;
    const out = [];
    for (const c of s) {
      const i = B32.indexOf(c);
      if (i < 0) throw new Error('clé de secours invalide');
      val = (val << 5) | i; bits += 5;
      if (bits >= 8) { out.push((val >>> (bits - 8)) & 255); bits -= 8; }
    }
    if (out.length !== KEY_SIZE) throw new Error('clé de secours invalide');
    return new Uint8Array(out);
  }
  const recoveryWrapKey = async rec => importAes(await hkdfBits(rec, L.recovery));
  const recoveryAuth = rec => hkdfBits(rec, L.recAuth);

  // --- empreintes de dédoublonnage ---
  // SHA-256 de la suite des SHA-256 de chaque bloc de 1 Mio du clair.
  class ContentHasher {
    constructor() { this.parts = []; this.buf = new Uint8Array(CHUNK_SIZE); this.filled = 0; }
    async update(p) {
      while (p.length) {
        const n = Math.min(p.length, CHUNK_SIZE - this.filled);
        this.buf.set(p.subarray(0, n), this.filled);
        this.filled += n; p = p.subarray(n);
        if (this.filled === CHUNK_SIZE) await this._flush();
      }
    }
    async _flush() {
      this.parts.push(new Uint8Array(await subtle.digest('SHA-256', this.buf.subarray(0, this.filled))));
      this.filled = 0;
    }
    async digest() {
      if (this.filled > 0 || !this.parts.length) await this._flush();
      return new Uint8Array(await subtle.digest('SHA-256', concat(...this.parts)));
    }
  }
  // un bloc de 1 Mio exactement = un appel : évite la copie quand on lit déjà par blocs
  const chunkDigest = async chunk => new Uint8Array(await subtle.digest('SHA-256', chunk));
  const hashOfChunkDigests = async digests => new Uint8Array(await subtle.digest('SHA-256', concat(...digests)));

  const hmac = async (mk, msg) => new Uint8Array(await subtle.sign('HMAC', mk.dedup, msg));
  const dedupTag = (mk, contentHash) => hmac(mk, contentHash);
  const deviceTag = (mk, name, size) => hmac(mk, enc.encode(L.deviceNs + name.toLowerCase() + '|' + size));

  // --- flux par blocs (originaux) ---
  function streamHeader(prefix) {
    const h = new Uint8Array(STREAM_HDR);
    h.set(enc.encode('PLC1'));
    new DataView(h.buffer).setUint32(4, CHUNK_SIZE);
    h.set(prefix, 8);
    return h;
  }
  function chunkNonce(prefix, i) {
    const n = new Uint8Array(NONCE_SIZE);
    n.set(prefix);
    new DataView(n.buffer).setUint32(8, i);
    return n;
  }
  const chunkAAD = (hdr, last) => concat(hdr, new Uint8Array([last ? 1 : 0]));
  async function encryptChunk(fk, hdr, i, plain, last) {
    const k = fk instanceof Uint8Array ? await importAes(fk) : fk;
    return new Uint8Array(await subtle.encrypt({ name: 'AES-GCM', iv: chunkNonce(hdr.subarray(8), i), additionalData: chunkAAD(hdr, last) }, k, plain));
  }
  async function decryptChunk(fk, hdr, i, cipher, last) {
    const k = fk instanceof Uint8Array ? await importAes(fk) : fk;
    try {
      return new Uint8Array(await subtle.decrypt({ name: 'AES-GCM', iv: chunkNonce(hdr.subarray(8), i), additionalData: chunkAAD(hdr, last) }, k, cipher));
    } catch {
      throw new Error('bloc altéré ou clé incorrecte');
    }
  }
  function parseHeader(hdr) {
    if (hdr.length < STREAM_HDR || dec.decode(hdr.subarray(0, 4)) !== 'PLC1') throw new Error('fichier chiffré invalide');
    return { hdr: hdr.subarray(0, STREAM_HDR), chunkSize: new DataView(hdr.buffer, hdr.byteOffset).getUint32(4) };
  }
  const streamSize = n => STREAM_HDR + n + Math.max(1, Math.ceil(n / CHUNK_SIZE)) * TAG_SIZE;
  const plainSize = encSize => {
    const body = encSize - STREAM_HDR, full = CHUNK_SIZE + TAG_SIZE;
    const chunks = Math.max(1, Math.ceil(body / full));
    return body - chunks * TAG_SIZE;
  };

  // chiffre un tableau d'octets entier (petits fichiers, tests)
  async function encryptStreamBytes(fk, plain, prefix = randomBytes(8)) {
    const hdr = streamHeader(prefix);
    const k = await importAes(fk);
    const parts = [hdr];
    const n = Math.max(1, Math.ceil(plain.length / CHUNK_SIZE));
    for (let i = 0; i < n; i++) parts.push(await encryptChunk(k, hdr, i, plain.subarray(i * CHUNK_SIZE, (i + 1) * CHUNK_SIZE), i === n - 1));
    return concat(...parts);
  }
  async function decryptStreamBytes(fk, data) {
    const { hdr, chunkSize } = parseHeader(data);
    const k = fk instanceof Uint8Array ? await importAes(fk) : fk;
    const full = chunkSize + TAG_SIZE, body = data.subarray(STREAM_HDR);
    const n = Math.max(1, Math.ceil(body.length / full));
    const parts = [];
    for (let i = 0; i < n; i++) parts.push(await decryptChunk(k, hdr, i, body.subarray(i * full, (i + 1) * full), i === n - 1));
    return concat(...parts);
  }

  // --- paire de clés du compte (ECDH P-256) et enveloppes de l'import ---
  async function generateAccountKeys() {
    const kp = await subtle.generateKey({ name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
    return {
      pub: new Uint8Array(await subtle.exportKey('raw', kp.publicKey)),
      pkcs8: new Uint8Array(await subtle.exportKey('pkcs8', kp.privateKey)),
    };
  }
  const importPrivate = pkcs8 => subtle.importKey('pkcs8', pkcs8, { name: 'ECDH', namedCurve: 'P-256' }, false, ['deriveBits']);
  // clé publique (format brut 04‖x‖y) recalculée depuis la clé privée
  async function publicFromPrivate(pkcs8) {
    const k = await subtle.importKey('pkcs8', pkcs8, { name: 'ECDH', namedCurve: 'P-256' }, true, ['deriveBits']);
    const jwk = await subtle.exportKey('jwk', k);
    return concat(new Uint8Array([4]), fromB64(jwk.x), fromB64(jwk.y));
  }
  async function openFromPublic(priv, myPub, box) {
    if (box.length < 65 + NONCE_SIZE + TAG_SIZE) throw new Error('enveloppe invalide');
    const ephPub = await subtle.importKey('raw', box.subarray(0, 65), { name: 'ECDH', namedCurve: 'P-256' }, false, []);
    const shared = new Uint8Array(await subtle.deriveBits({ name: 'ECDH', public: ephPub }, priv, 256));
    const key = await hkdfBits(shared, L.seal, concat(box.subarray(0, 65), myPub));
    return open(key, box.subarray(65), L.sealedFk);
  }

  root.PC = {
    KEY_SIZE, CHUNK_SIZE, STREAM_HDR, TAG_SIZE, L,
    toHex, fromHex, toB64, fromB64, concat, randomBytes,
    importAes, importMasterKey, seal, open, sealJSON, openJSON,
    publicFromPrivate, passwordKeys, formatRecovery, parseRecovery, recoveryWrapKey, recoveryAuth, fileKeys,
    ContentHasher, chunkDigest, hashOfChunkDigests, dedupTag, deviceTag,
    streamHeader, encryptChunk, decryptChunk, parseHeader, streamSize, plainSize,
    encryptStreamBytes, decryptStreamBytes,
    generateAccountKeys, importPrivate, openFromPublic,
  };
})(typeof self !== 'undefined' ? self : globalThis);
