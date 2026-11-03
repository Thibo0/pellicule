// Vérifie que web/crypto.js produit exactement les mêmes octets que crypto.go.
// Lancer : node --test web/crypto.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const here = new URL('.', import.meta.url);
const ctx = { crypto: globalThis.crypto, TextEncoder, TextDecoder, atob, btoa, Uint8Array, DataView, Math, JSON, Error, Promise, Array, String };
ctx.globalThis = ctx;
vm.createContext(ctx);
vm.runInContext(readFileSync(new URL('crypto.js', here), 'utf8'), ctx);
const PC = ctx.PC;
const V = JSON.parse(readFileSync(new URL('../testdata/vectors.json', here), 'utf8'));
const { fromHex: h, toHex: hx } = PC;
const pattern = n => { const b = new Uint8Array(n); for (let i = 0; i < n; i++) b[i] = (i * 31 + 7) & 0xff; return b; };
const MK = h('000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f');

test('mot de passe → clés', async () => {
  const k = await PC.passwordKeys(V.Password.Password, h(V.Password.Salt));
  assert.equal(hx(k.authKey), V.Password.AuthKey);
  assert.equal(hx(k.wrapRaw), V.Password.WrapKey);
});

test('clé de secours', async () => {
  assert.equal(PC.formatRecovery(h(V.Recovery.Key)), V.Recovery.Formatted);
  assert.equal(hx(PC.parseRecovery(V.Recovery.Formatted.toLowerCase().replace(/-/g, ' '))), V.Recovery.Key);
});

test('boîtes AES-GCM', async () => {
  for (const s of V.Seal) {
    const box = await PC.seal(h(s.Key), h(s.Plain), s.AAD, h(s.Nonce));
    assert.equal(hx(box), s.Box);
    assert.equal(hx(await PC.open(h(s.Key), h(s.Box), s.AAD)), s.Plain);
    await assert.rejects(PC.open(h(s.Key), h(s.Box), 'autre'));
  }
});

test('flux par blocs, empreintes', async () => {
  const mk = await PC.importMasterKey(MK);
  for (const s of V.Stream) {
    const plain = pattern(s.Length);
    const enc = await PC.encryptStreamBytes(h(s.Key), plain, h(s.Prefix));
    assert.equal(enc.length, PC.streamSize(s.Length));
    assert.equal(PC.plainSize(enc.length), s.Length);
    assert.equal(hx(new Uint8Array(await crypto.subtle.digest('SHA-256', enc))), s.CipherSHA, `chiffré n=${s.Length}`);
    if (s.Cipher) assert.equal(hx(await PC.decryptStreamBytes(h(s.Key), h(s.Cipher))), hx(plain));
    else assert.deepEqual(await PC.decryptStreamBytes(h(s.Key), enc), plain);

    const ch = new PC.ContentHasher();
    for (let o = 0; o < plain.length || o === 0; o += 333333) { await ch.update(plain.subarray(o, o + 333333)); if (!plain.length) break; }
    const hash = await ch.digest();
    assert.equal(hx(hash), s.ContentHash);
    assert.equal(hx(await PC.dedupTag(mk, hash)), s.DedupTag);
  }
});

test('troncature refusée', async () => {
  const s = V.Stream.find(x => x.Length > 2 * PC.CHUNK_SIZE);
  const enc = await PC.encryptStreamBytes(h(s.Key), pattern(s.Length), h(s.Prefix));
  await assert.rejects(PC.decryptStreamBytes(h(s.Key), enc.subarray(0, PC.STREAM_HDR + 2 * (PC.CHUNK_SIZE + PC.TAG_SIZE))));
});

test('clé de fichier dérivée du contenu', async () => {
  const mk = await PC.importMasterKey(h(V.FileKeys.MasterKey));
  const k = await PC.fileKeys(mk, h(V.FileKeys.ContentHash));
  assert.equal(hx(k.fk), V.FileKeys.FK);
  assert.equal(hx(k.prefix), V.FileKeys.Prefix);
});

test('empreinte d\'appareil', async () => {
  const mk = await PC.importMasterKey(h(V.DeviceTag.MasterKey));
  assert.equal(hx(await PC.deviceTag(mk, V.DeviceTag.Name, V.DeviceTag.Size)), V.DeviceTag.Tag);
});

test('enveloppe de l\'import (clé publique)', async () => {
  const priv = await PC.importPrivate(Uint8Array.from(atob(V.Sealed.RecipientPKCS8), c => c.charCodeAt(0)));
  const fk = await PC.openFromPublic(priv, h(V.Sealed.RecipientPub), h(V.Sealed.Box));
  assert.equal(hx(fk), V.Sealed.Plain);
});
