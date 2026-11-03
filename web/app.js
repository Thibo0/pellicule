'use strict';

/* =========================================================
   Pellicule — interface web (sans framework, sans build)
   ========================================================= */

const $ = (s, el = document) => el.querySelector(s);
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const app = $('#app');
const NATIVE = window.PelliculeAndroid || null; // présent quand la page tourne dans l'app Android
const state = { user: null, version: '' };

/* ---------- icônes ---------- */
const ICONS = {
  photos: '<rect x="3" y="3" width="18" height="18" rx="3"/><circle cx="9" cy="9" r="2"/><path d="m21 15-4.5-4.5L6 21"/>',
  album: '<rect x="3" y="7" width="14" height="14" rx="2"/><path d="M7 3h12a2 2 0 0 1 2 2v12"/>',
  heart: '<path d="M12 20s-7.5-4.6-7.5-10.2A4.3 4.3 0 0 1 12 7.2a4.3 4.3 0 0 1 7.5 2.6C19.5 15.4 12 20 12 20z"/>',
  video: '<rect x="2.5" y="6" width="13.5" height="12" rx="2.5"/><path d="m16 10.5 5.5-3v9l-5.5-3"/>',
  trash: '<path d="M4 7h16M10 11v6M14 11v6M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12M9 7V4h6v3"/>',
  upload: '<path d="M12 15V4M7 9l5-5 5 5M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3"/>',
  cloud: '<path d="M7 18.5a4.5 4.5 0 0 1-.6-9 6 6 0 0 1 11.5 1.6A3.8 3.8 0 0 1 17.5 18.5"/><path d="M12 12.5v8M9 15.5l3-3 3 3"/>',
  settings: '<path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/>',
  search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  x: '<path d="M18 6 6 18M6 6l12 12"/>',
  check: '<path d="m5 12.5 4.5 4.5L19 7"/>',
  left: '<path d="m15 18-6-6 6-6"/>',
  right: '<path d="m9 18 6-6-6-6"/>',
  download: '<path d="M12 4v11M7 10l5 5 5-5M4 20h16"/>',
  link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 7.5h.01"/>',
  restore: '<path d="M3.5 12a8.5 8.5 0 1 0 2.6-6.1L3.5 8.5"/><path d="M3.5 3.5v5h5"/>',
  logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
  play: '<path d="M7 4.5v15l12.5-7.5z"/>',
  calendar: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 10h18M8 3v4M16 3v4"/>',
  file: '<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/>',
  camera: '<path d="M4 8h3l2-3h6l2 3h3a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V9a1 1 0 0 1 1-1z"/><circle cx="12" cy="13.5" r="3.5"/>',
  minus: '<circle cx="12" cy="12" r="9"/><path d="M8 12h8"/>',
  star: '<path d="m12 3.5 2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.9l-5.2 2.7 1-5.8-4.3-4.1 5.9-.9z"/>',
  copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/>',
  folder: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
};
const icon = (n, cls = '') => `<svg class="i ${cls}" viewBox="0 0 24 24" aria-hidden="true">${ICONS[n]}</svg>`;
const logoMark = `<span class="logo-mark">${icon('camera')}</span>`;

/* ---------- API ---------- */
async function api(path, opts = {}) {
  const o = { method: opts.method || 'GET', headers: { 'X-Pellicule': '1' }, credentials: 'same-origin' };
  if (opts.body !== undefined) {
    o.headers['Content-Type'] = 'application/json';
    o.body = JSON.stringify(opts.body);
  }
  let r;
  try { r = await fetch(path, o); } catch { throw new Error('Serveur injoignable'); }
  if (r.status === 401 && !opts.noRedirect) {
    state.user = null;
    boot();
    throw new Error('non connecté');
  }
  const data = (r.headers.get('content-type') || '').includes('json') ? await r.json() : null;
  if (!r.ok) throw new Error(data?.error || `Erreur ${r.status}`);
  return data;
}
const post = (path, body) => api(path, { method: 'POST', body });

/* ---------- formats ---------- */
const pad = n => String(n).padStart(2, '0');
// Les dates sont stockées en « heure murale » : on les lit toujours en UTC.
const dKey = ms => new Date(ms).toISOString().slice(0, 10);
const todayKey = () => { const n = new Date(); return `${n.getFullYear()}-${pad(n.getMonth() + 1)}-${pad(n.getDate())}`; };
const DF = {
  day: new Intl.DateTimeFormat('fr-FR', { weekday: 'short', day: 'numeric', month: 'long', timeZone: 'UTC' }),
  dayY: new Intl.DateTimeFormat('fr-FR', { weekday: 'short', day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' }),
  full: new Intl.DateTimeFormat('fr-FR', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', timeZone: 'UTC' }),
  time: new Intl.DateTimeFormat('fr-FR', { hour: '2-digit', minute: '2-digit', timeZone: 'UTC' }),
  month: new Intl.DateTimeFormat('fr-FR', { month: 'short', year: 'numeric', timeZone: 'UTC' }),
  short: new Intl.DateTimeFormat('fr-FR', { day: 'numeric', month: 'short', year: 'numeric' }),
};
const cap = s => s.charAt(0).toUpperCase() + s.slice(1);
function dayLabel(ms) {
  const k = dKey(ms), t = todayKey();
  if (k === t) return "Aujourd'hui";
  if (k === dKey(Date.parse(t + 'T00:00:00Z') - 864e5)) return 'Hier';
  return cap((k.slice(0, 4) === t.slice(0, 4) ? DF.day : DF.dayY).format(ms));
}
function fmtSize(b) {
  if (b == null || b < 0) return '—';
  const u = ['o', 'Ko', 'Mo', 'Go', 'To'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return `${b.toLocaleString('fr-FR', { maximumFractionDigits: b < 10 && i > 0 ? 1 : 0 })} ${u[i]}`;
}
function fmtDuration(s) {
  s = Math.round(s || 0);
  const h = Math.floor(s / 3600), m = Math.floor(s / 60) % 60, r = s % 60;
  return h ? `${h}:${pad(m)}:${pad(r)}` : `${m}:${pad(r)}`;
}
const plural = (n, one, many) => `${n.toLocaleString('fr-FR')} ${n > 1 ? many : one}`;
const countLabel = n => plural(n, 'élément', 'éléments');
function fmtRange(a, b) {
  if (!a) return '';
  const x = DF.month.format(a), y = DF.month.format(b);
  return x === y ? x : `${x} – ${y}`;
}

/* ---------- notifications & fenêtres ---------- */
function toast(msg, { action, timeout = 4500 } = {}) {
  const el = document.createElement('div');
  el.className = 'toast';
  el.innerHTML = `<span>${esc(msg)}</span>${action ? `<button>${esc(action.label)}</button>` : ''}`;
  const close = () => el.remove();
  if (action) el.querySelector('button').onclick = () => { close(); action.run(); };
  $('#toasts').appendChild(el);
  setTimeout(close, timeout);
  return { close, set: m => { el.firstChild.textContent = m; } };
}

function modal(html, setup) {
  return new Promise(resolve => {
    const bg = document.createElement('div');
    bg.className = 'modal-bg';
    bg.innerHTML = `<div class="modal" role="dialog" aria-modal="true">${html}</div>`;
    const m = bg.firstChild;
    const onKey = e => { if (e.key === 'Escape') { e.stopPropagation(); close(null); } };
    function close(v) {
      bg.remove();
      document.removeEventListener('keydown', onKey, true);
      resolve(v);
    }
    document.addEventListener('keydown', onKey, true);
    bg.addEventListener('mousedown', e => { if (e.target === bg) close(null); });
    document.body.appendChild(bg);
    setup?.(m, close);
    const first = m.querySelector('input:not([type=checkbox])');
    if (first) { first.focus(); first.select?.(); }
  });
}

function confirmModal(title, text, ok = 'Confirmer', danger = false) {
  return modal(`<h2>${esc(title)}</h2><p>${esc(text)}</p>
    <div class="foot"><button class="btn" data-v="0">Annuler</button>
    <button class="btn ${danger ? 'danger solid' : 'primary'}" data-v="1">${esc(ok)}</button></div>`,
  (m, close) => {
    m.onclick = e => { const b = e.target.closest('[data-v]'); if (b) close(b.dataset.v === '1'); };
    m.querySelector('[data-v="1"]').focus();
  }).then(Boolean);
}

function promptModal(title, { label = '', value = '', ok = 'Valider', type = 'text', placeholder = '', text = '' } = {}) {
  return modal(`<h2>${esc(title)}</h2>${text ? `<p>${esc(text)}</p>` : ''}<form>
    ${label ? `<label class="lbl">${esc(label)}</label>` : ''}
    <input class="field" type="${type}" value="${esc(value)}" placeholder="${esc(placeholder)}" maxlength="200">
    <div class="foot"><button type="button" class="btn" data-c>Annuler</button><button class="btn primary">${esc(ok)}</button></div></form>`,
  (m, close) => {
    const f = m.querySelector('form');
    f.onsubmit = e => { e.preventDefault(); const v = f.querySelector('input').value.trim(); if (v) close(v); };
    m.querySelector('[data-c]').onclick = () => close(null);
  });
}

function copyText(text, input) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
  // HTTP en réseau local : pas d'API presse-papiers, on passe par la sélection
  input.select();
  input.setSelectionRange(0, text.length);
  document.execCommand('copy');
  return Promise.resolve();
}

/* =========================================================
   Galerie : grille groupée par jour, sélection, chargement infini
   ========================================================= */
class Gallery {
  constructor(root, o) {
    this.root = root;
    this.o = o;
    this.items = [];
    this.sel = new Set();
    this.groups = new Map();
    this.next = null;
    this.done = false;
    this.loading = null;
    this.last = -1;
    root.innerHTML = '<div class="g-body"></div><div class="more"></div>';
    this.body = root.firstChild;
    this.moreEl = root.lastChild;
    this.bind();
    this.io = new IntersectionObserver(es => { if (es.some(e => e.isIntersecting)) this.load(); }, { rootMargin: '1500px 0px' });
    this.io.observe(this.moreEl);
    // miniatures : téléchargées et déchiffrées seulement à l'approche de l'écran
    this.thumbIO = new IntersectionObserver(es => es.forEach(e => {
      if (!e.isIntersecting) return;
      this.thumbIO.unobserve(e.target);
      this.paint(e.target);
    }), { rootMargin: '800px 0px' });
  }

  paint(tile) {
    const a = this.items[+tile.dataset.i];
    const img = tile.querySelector('img');
    if (!a || !img) return;
    Vault.imageURL(a, 'thumb').then(u => {
      if (u) img.src = u;
      else { tile.querySelector('.pending')?.remove(); tile.classList.add('noimg'); }
    }).catch(() => tile.classList.add('noimg'));
  }

  load() {
    if (this.loading) return this.loading;
    if (this.done) return Promise.resolve();
    this.loading = (async () => {
      this.moreEl.innerHTML = '<div class="spinner"></div>';
      try {
        const r = await this.o.load(this.next);
        this.next = r.next;
        this.done = !r.next;
        await Vault.hydrate(r.items, this.o.src || { owner: true });
        this.append(r.items);
      } catch (e) {
        if (e.message !== 'non connecté') toast(e.message);
        this.done = true;
      }
      this.moreEl.innerHTML = '';
      if (!this.items.length && this.done) this.body.innerHTML = this.o.empty || '';
      this.o.onLoad?.(this);
      this.loading = null;
      // l'observateur ne se redéclenche pas si la sentinelle reste visible
      if (!this.done && this.moreEl.isConnected && this.moreEl.getBoundingClientRect().top < innerHeight + 1500) this.load();
    })();
    return this.loading;
  }

  tile(a, i) {
    const ph = a.thumb && !a.broken ? '' : `<div class="ph">${icon(a.kind === 'video' ? 'video' : 'file')}<small>${esc(a.filename)}</small></div>`;
    return `<div class="tile${this.sel.has(a.id) ? ' sel' : ''}" data-i="${i}" tabindex="0" role="button" aria-label="${esc(a.filename)}">${ph}
      <img alt="" decoding="async" draggable="false">
      <div class="shade"></div>
      ${this.o.selectable ? `<div class="check">${icon('check')}</div>` : ''}
      ${a.kind === 'video' ? `<div class="badge b-tr">${a.duration ? fmtDuration(a.duration) : ''}${icon('play', 'fill')}</div>` : ''}
      ${a.favorite && !this.o.public ? `<div class="badge b-bl">${icon('heart', 'fill')}</div>` : ''}
    </div>`;
  }

  append(items) {
    if (!this.items.length) this.body.innerHTML = '';
    const start = this.items.length;
    this.items.push(...items);
    const buf = new Map();
    items.forEach((a, j) => {
      const i = start + j;
      const key = this.o.groupBy ? this.o.groupBy(a) : 'all';
      let g = this.groups.get(key);
      if (!g) {
        const sec = document.createElement('section');
        sec.className = 'day';
        sec.innerHTML = (this.o.groupBy ? `<div class="day-head">${this.o.selectable ? `<button class="icon-btn sel-day" data-day="${esc(key)}" title="Sélectionner la journée">${icon('check')}</button>` : ''}<span>${esc(this.o.groupLabel(a))}</span></div>` : '') + '<div class="grid"></div>';
        this.body.appendChild(sec);
        g = { el: sec, grid: sec.lastChild, idx: [] };
        this.groups.set(key, g);
      }
      g.idx.push(i);
      buf.set(g, (buf.get(g) || '') + this.tile(a, i));
    });
    for (const [g, html] of buf) g.grid.insertAdjacentHTML('beforeend', html);
    this.observeTiles(start);
  }
  observeTiles(from = 0) {
    this.root.querySelectorAll('.tile').forEach(t => { if (+t.dataset.i >= from) this.thumbIO.observe(t); });
  }

  bind() {
    const r = this.root;
    r.addEventListener('load', e => {
      if (e.target.tagName !== 'IMG') return;
      e.target.classList.add('ok');
    }, true);
    r.addEventListener('click', e => {
      const day = e.target.closest('.sel-day');
      if (day) return this.toggleDay(day.dataset.day);
      const t = e.target.closest('.tile');
      if (!t) return;
      if (this.longPressed) { this.longPressed = false; return; }
      const i = +t.dataset.i;
      if (this.o.selectable && (e.target.closest('.check') || this.sel.size || this.o.pickMode || e.shiftKey || e.ctrlKey || e.metaKey)) {
        this.toggle(i, e.shiftKey);
      } else {
        this.o.open?.(i);
      }
    });
    r.addEventListener('keydown', e => {
      const t = e.target.closest('.tile');
      if (t && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); t.click(); }
    });
    // appui long (mobile) = sélection
    let timer = null, sx = 0, sy = 0;
    r.addEventListener('pointerdown', e => {
      if (!this.o.selectable || e.pointerType === 'mouse') return;
      const t = e.target.closest('.tile');
      if (!t) return;
      sx = e.clientX; sy = e.clientY;
      timer = setTimeout(() => {
        this.longPressed = true;
        navigator.vibrate?.(15);
        this.toggle(+t.dataset.i);
      }, 450);
    });
    const cancel = () => clearTimeout(timer);
    r.addEventListener('pointerup', cancel);
    r.addEventListener('pointercancel', cancel);
    r.addEventListener('pointermove', e => { if (Math.hypot(e.clientX - sx, e.clientY - sy) > 10) cancel(); });
    r.addEventListener('contextmenu', e => { if (e.target.closest('.tile') && e.pointerType !== 'mouse' && this.o.selectable) e.preventDefault(); });
  }

  setSel(i, on) {
    const id = this.items[i].id;
    on ? this.sel.add(id) : this.sel.delete(id);
    this.root.querySelector(`.tile[data-i="${i}"]`)?.classList.toggle('sel', on);
  }
  toggle(i, range = false) {
    if (range && this.last >= 0) {
      const on = !this.sel.has(this.items[i].id);
      for (let k = Math.min(i, this.last); k <= Math.max(i, this.last); k++) this.setSel(k, on);
    } else {
      this.setSel(i, !this.sel.has(this.items[i].id));
    }
    this.last = i;
    this.changed();
  }
  toggleDay(key) {
    const g = this.groups.get(key);
    if (!g) return;
    const all = g.idx.every(i => this.sel.has(this.items[i].id));
    g.idx.forEach(i => this.setSel(i, !all));
    this.changed();
  }
  clear() {
    this.sel.clear();
    this.root.querySelectorAll('.tile.sel').forEach(el => el.classList.remove('sel'));
    this.changed();
  }
  changed() {
    this.root.classList.toggle('selecting', this.sel.size > 0);
    this.o.onSelect?.(this);
  }
  selected() { return this.items.filter(a => this.sel.has(a.id)); }

  // reconstruit la grille sans certains éléments (corbeille, retrait d'album…)
  remove(ids) {
    const gone = new Set(ids);
    const keep = this.items.filter(a => !gone.has(a.id));
    this.items = [];
    this.groups.clear();
    this.sel.clear();
    this.body.innerHTML = '';
    this.append(keep);
    if (!this.items.length && this.done) this.body.innerHTML = this.o.empty || '';
    this.changed();
  }
  update(a) {
    const i = this.items.findIndex(x => x.id === a.id);
    if (i < 0) return;
    this.items[i] = a;
    const el = this.root.querySelector(`.tile[data-i="${i}"]`);
    if (!el) return;
    el.outerHTML = this.tile(a, i);
    this.paint(this.root.querySelector(`.tile[data-i="${i}"]`));
  }
  destroy() { this.io.disconnect(); this.thumbIO.disconnect(); }
}

/* ---------- barre de sélection ---------- */
function bindSelbar(g, actions, pickLabel) {
  const bar = $('#selbar'), top = $('.top');
  const render = () => {
    const n = g.sel.size;
    const show = n > 0 || g.o.pickMode;
    bar.hidden = !show;
    top.hidden = show;
    if (!show) return;
    const label = n ? `${n.toLocaleString('fr-FR')} sélectionné${n > 1 ? 's' : ''}` : pickLabel;
    bar.innerHTML = `<button class="icon-btn" data-close title="Annuler">${icon('x')}</button><span class="n">${esc(label)}</span><span class="grow"></span>`
      + actions.filter(a => !a.when || a.when(g)).map(a => a.label
        ? `<button class="btn primary" data-k="${actions.indexOf(a)}" ${n ? '' : 'disabled'}>${esc(a.label)}</button>`
        : `<button class="icon-btn" data-k="${actions.indexOf(a)}" title="${esc(a.title)}" aria-label="${esc(a.title)}">${icon(a.icon)}</button>`).join('');
  };
  bar.onclick = async e => {
    const b = e.target.closest('button');
    if (!b) return;
    if ('close' in b.dataset) return g.o.pickMode ? g.o.onCancel() : g.clear();
    try { await actions[+b.dataset.k].run(g.selected(), g); } catch (err) { if (err.message !== 'non connecté') toast(err.message); }
  };
  g.o.onSelect = render;
  render();
}

/* ---------- actions communes ---------- */
async function setFavorite(items, g) {
  const fav = !items.every(a => a.favorite);
  await post('/api/assets/favorite', { ids: items.map(a => a.id), favorite: fav });
  items.forEach(a => { a.favorite = fav; g?.update(a); });
  g?.clear();
  toast(fav ? 'Ajouté aux favoris' : 'Retiré des favoris');
  return fav;
}

async function trashItems(items, g) {
  const ids = items.map(a => a.id);
  await post('/api/assets/trash', { ids });
  g?.remove(ids);
  refreshUsage();
  toast(`${countLabel(ids.length)} dans la corbeille`, {
    action: { label: 'Annuler', run: async () => { await post('/api/assets/restore', { ids }); route(); } },
  });
}

// Les originaux sont déchiffrés ici, dans la page (le serveur ne renvoie que du chiffré).
async function downloadItems(items, zipName = 'Pellicule') {
  items = items.filter(a => !a.broken);
  if (!items.length) return toast('Rien à télécharger');
  // dans l'app Android : l'app déchiffre elle-même et range chaque original dans la galerie du téléphone
  if (NATIVE?.download) {
    return NATIVE.download(JSON.stringify(items.map(a => ({
      url: `${location.origin}/api/${a.src?.share ? `s/${a.src.share.token}/` : ''}assets/${a.id}/original`,
      fk: PC.toB64(a.fkRaw), name: a.filename, mime: a.mime,
    }))));
  }
  const t = toast(items.length > 1 ? `Préparation de ${countLabel(items.length)}…` : 'Téléchargement…', { timeout: 6e5 });
  try {
    await Vault.download(items, zipName, (d, total) => t.set(`${items.length > 1 ? 'ZIP' : 'Téléchargement'} : ${Math.round(d / total * 100)} %`));
  } finally { t.close(); }
}

// couvertures d'albums : clé de la photo de couverture, ouverte comme celle d'une photo normale
function albumCover(al, imgClass = '') {
  if (!al.coverId || !al.coverFk) return icon('album');
  return `<img class="${imgClass}" data-cover="${al.coverId}" alt="">`;
}
async function paintCovers(root, albums) {
  const by = new Map(albums.map(al => [String(al.coverId), al]));
  for (const img of root.querySelectorAll('img[data-cover]')) {
    const al = by.get(img.dataset.cover);
    if (!al) continue;
    const [a] = await Vault.hydrate([{ id: al.coverId, fk: al.coverFk, sealed: al.coverSealed, meta: al.coverMeta, thumb: true }], { owner: true });
    Vault.imageURL(a, 'thumb').then(u => { if (u) img.src = u; }).catch(() => {});
  }
}

// ajout dans un album : si l'album est partagé, chaque lien reçoit les clés des nouvelles photos
async function addToAlbum(albumId, items) {
  await post(`/api/albums/${albumId}/add`, { ids: items.map(a => a.id) });
  await Vault.syncShares(albumId, items);
}

async function albumPicker(items, exceptId) {
  const ids = items.map(a => a.id);
  const albums = await Promise.all((await api('/api/albums')).filter(al => al.id !== exceptId).map(Vault.hydrateAlbum));
  const choice = await modal(`<h2>Ajouter à un album</h2><p>${countLabel(ids.length)}</p><div class="list">
      <button data-new><span class="th">${icon('plus')}</span><div><b>Nouvel album</b></div></button>
      ${albums.map(al => `<button data-id="${al.id}"><span class="th">${albumCover(al)}</span>
        <div><b>${esc(al.name)}</b><small>${countLabel(al.count)}</small></div></button>`).join('')}
    </div><div class="foot"><button class="btn" data-close>Annuler</button></div>`,
  (m, close) => {
    paintCovers(m, albums);
    m.onclick = e => {
      const b = e.target.closest('button');
      if (!b) return;
      if ('close' in b.dataset) return close(null);
      close('new' in b.dataset ? 'new' : +b.dataset.id);
    };
  });
  if (!choice) return false;
  if (choice === 'new') {
    const name = await promptModal('Nouvel album', { label: 'Nom', placeholder: 'Vacances d’été', ok: 'Créer' });
    if (!name) return false;
    const r = await post('/api/albums', { name: await Vault.sealName(name), ids });
    toast(`Ajouté à « ${name} »`, { action: { label: 'Voir', run: () => { location.hash = '#/albums/' + r.id; } } });
  } else {
    await addToAlbum(choice, items);
    const al = albums.find(x => x.id === choice);
    toast(`Ajouté à « ${al.name} »`, { action: { label: 'Voir', run: () => { location.hash = '#/albums/' + choice; } } });
  }
  return true;
}

/* =========================================================
   Visionneuse plein écran
   ========================================================= */
function openViewer(g, index, opts = {}) {
  const share = opts.share;
  // aperçu déchiffré ; à défaut (vieux téléphone, format exotique) la miniature
  const preview = async a => (await Vault.imageURL(a, 'preview')) || Vault.imageURL(a, 'thumb');
  const canDownload = !share || share.allowDownload;
  let i = index, chrome = true, closed = false;
  let infoOpen = false;
  try { infoOpen = !share && localStorage.getItem('pellicule.info') === '1' && innerWidth > 820; } catch { /* stockage indisponible */ }

  const el = document.createElement('div');
  el.className = 'viewer';
  document.body.appendChild(el);
  document.body.style.overflow = 'hidden';
  NATIVE?.setViewer?.(true);
  history.pushState({ viewer: true }, '');

  function close(fromPop) {
    if (closed) return;
    closed = true;
    el.remove();
    document.body.style.overflow = '';
    NATIVE?.setViewer?.(false);
    removeEventListener('keydown', onKey);
    removeEventListener('popstate', onPop);
    if (!fromPop) history.back();
    g.root.querySelector(`.tile[data-i="${i}"]`)?.focus({ preventScroll: false });
  }
  const onPop = () => close(true);
  addEventListener('popstate', onPop);

  async function go(d) {
    let j = i + d;
    if (j >= g.items.length && !g.done) await g.load();
    if (j < 0 || j >= g.items.length) return;
    i = j;
    render();
  }

  function actionsHtml(a) {
    if (share) return canDownload ? `<button class="icon-btn" data-a="dl" title="Télécharger">${icon('download')}</button>` : '';
    if (a.trashedAt) {
      return `<button class="icon-btn" data-a="restore" title="Restaurer">${icon('restore')}</button>
        <button class="icon-btn" data-a="destroy" title="Supprimer définitivement">${icon('trash')}</button>
        <button class="icon-btn" data-a="info" title="Informations (i)">${icon('info')}</button>`;
    }
    return `<button class="icon-btn ${a.favorite ? 'on' : ''}" data-a="fav" title="Favori (f)">${icon('heart', a.favorite ? 'fill' : '')}</button>
      <button class="icon-btn" data-a="album" title="Ajouter à un album">${icon('album')}</button>
      ${opts.album ? `<button class="icon-btn" data-a="cover" title="Définir comme couverture">${icon('star')}</button>
        <button class="icon-btn" data-a="unalbum" title="Retirer de l'album">${icon('minus')}</button>` : ''}
      <button class="icon-btn" data-a="dl" title="Télécharger">${icon('download')}</button>
      <button class="icon-btn" data-a="trash" title="Mettre à la corbeille (Suppr)">${icon('trash')}</button>
      <button class="icon-btn" data-a="info" title="Informations (i)">${icon('info')}</button>`;
  }

  function render() {
    const a = g.items[i];
    if (!a) return close();
    const hasNext = i < g.items.length - 1 || !g.done;
    el.innerHTML = `<div class="stage">
      <div class="vtop">
        <button class="icon-btn" data-a="close" title="Fermer (Échap)">${icon('left')}</button>
        <div class="t">${esc(dayLabel(a.takenAt))}<small>${DF.time.format(a.takenAt)}</small></div>
        <span class="grow"></span>${actionsHtml(a)}
      </div>
      ${i > 0 ? `<button class="icon-btn nav prev" data-a="prev" title="Précédente">${icon('left')}</button>` : ''}
      ${hasNext ? `<button class="icon-btn nav next" data-a="next" title="Suivante">${icon('right')}</button>` : ''}
      <div class="loading"><div class="spinner"></div></div>
      ${a.kind === 'video'
        ? '<video controls playsinline autoplay preload="metadata"></video>'
        : `<img alt="${esc(a.filename)}" draggable="false">`}
    </div>${infoOpen ? `<aside class="info"><h3><span class="grow">Informations</span><button class="icon-btn" data-a="info">${icon('x')}</button></h3><div id="info-body"></div></aside>` : ''}`;
    el.classList.toggle('chrome-off', !chrome);

    const done = () => el.querySelector('.loading')?.remove();
    const fail = msg => { done(); el.querySelector('.stage').insertAdjacentHTML('beforeend', `<div class="vmsg">${icon('info')}<p>${esc(msg)}</p></div>`); };
    const shown = i;
    if (a.broken) fail('Ce fichier ne peut pas être déchiffré.');
    const img = el.querySelector('.stage img');
    if (img && !a.broken) {
      img.onload = done;
      setupZoom(img);
      preview(a).then(u => {
        if (shown !== i) return;
        if (u) img.src = u;
        else fail('Pas d’aperçu pour ce format. Télécharge l’original pour l’ouvrir.');
      }).catch(e => shown === i && fail(e.message));
    }
    const video = el.querySelector('.stage video');
    if (video && !a.broken) {
      video.onloadeddata = done;
      video.onerror = () => fail('Lecture impossible dans ce navigateur. Télécharge la vidéo pour la lire.');
      preview(a).then(u => { if (u && shown === i) video.poster = u; }).catch(() => {});
      Vault.rememberVideo(a);
      Vault.videoURL(a).then(u => { if (shown === i) video.src = u; }).catch(e => shown === i && fail(e.message));
    }
    if (infoOpen) fillInfo(a);
    // précharge les voisines
    for (const k of [i + 1, i - 1]) {
      const n = g.items[k];
      if (n && n.kind === 'image') preview(n).catch(() => {});
    }
  }

  async function fillInfo(a) {
    const box = el.querySelector('#info-body');
    const rows = [
      [icon('calendar'), cap(DF.full.format(a.takenAt)), DF.time.format(a.takenAt)],
      [icon(a.kind === 'video' ? 'video' : 'file'), a.filename,
        [a.width ? `${a.width} × ${a.height}` : '', fmtSize(a.plainSize), a.kind === 'video' && a.duration ? fmtDuration(a.duration) : ''].filter(Boolean).join(' · ')],
    ];
    box.innerHTML = rows.map(([ic, t, s]) => `<div class="row">${ic}<div>${esc(t)}<small>${esc(s)}</small></div></div>`).join('');
    if (share) return;
    try {
      const d = await api(`/api/assets/${a.id}`);
      if (!box.isConnected || !d.albums.length) return;
      const names = await Promise.all(d.albums.map(al => Vault.openName(al.name)));
      box.insertAdjacentHTML('beforeend', `<div class="row">${icon('album')}<div>${d.albums.map((al, k) => `<a class="pill" href="#/albums/${al.id}" data-a="goto">${esc(names[k])}</a>`).join('')}</div></div>`);
    } catch { /* infos facultatives */ }
  }

  function setupZoom(img) {
    let zoomed = false, ox = 50, oy = 50;
    const apply = () => {
      img.classList.toggle('zoomed', zoomed);
      img.style.transformOrigin = `${ox}% ${oy}%`;
      img.style.transform = zoomed ? 'scale(2.5)' : '';
    };
    img.toggleZoom = (x, y) => {
      const r = img.getBoundingClientRect();
      zoomed = !zoomed;
      ox = Math.min(100, Math.max(0, (x - r.left) / r.width * 100));
      oy = Math.min(100, Math.max(0, (y - r.top) / r.height * 100));
      apply();
    };
    img.pan = (dx, dy) => {
      if (!zoomed) return false;
      const r = img.getBoundingClientRect();
      ox = Math.min(100, Math.max(0, ox - dx / r.width * 100 * 1.5));
      oy = Math.min(100, Math.max(0, oy - dy / r.height * 100 * 1.5));
      img.style.transition = 'none';
      apply();
      return true;
    };
    img.isZoomed = () => zoomed;
  }

  async function act(name, a) {
    switch (name) {
      case 'close': return close();
      case 'prev': return go(-1);
      case 'next': return go(1);
      case 'info':
        infoOpen = !infoOpen;
        try { localStorage.setItem('pellicule.info', infoOpen ? '1' : '0'); } catch { /* ignoré */ }
        return render();
      case 'fav':
        await setFavorite([a]);
        g.update(a);
        return render();
      case 'album': return albumPicker([a], opts.album);
      case 'dl': return downloadItems([a]);
      case 'cover':
        await api(`/api/albums/${opts.album}`, { method: 'PATCH', body: { coverId: a.id } });
        return toast('Couverture de l’album modifiée');
      case 'unalbum':
        await post(`/api/albums/${opts.album}/remove`, { ids: [a.id] });
        toast('Retiré de l’album');
        return removeCurrent(a);
      case 'trash':
        await trashItems([a]);
        return removeCurrent(a);
      case 'restore':
        await post('/api/assets/restore', { ids: [a.id] });
        toast('Restauré');
        return removeCurrent(a);
      case 'destroy':
        if (!await confirmModal('Supprimer définitivement ?', 'Le fichier sera effacé du disque. Impossible à annuler.', 'Supprimer', true)) return;
        await post('/api/assets/delete', { ids: [a.id] });
        refreshUsage();
        return removeCurrent(a);
      case 'goto': return close(true);
    }
  }

  function removeCurrent(a) {
    g.remove([a.id]);
    if (!g.items.length) return close();
    i = Math.min(i, g.items.length - 1);
    render();
  }

  el.addEventListener('click', e => {
    const b = e.target.closest('[data-a]');
    if (!b) return;
    if (b.dataset.a === 'goto') { closed = true; el.remove(); document.body.style.overflow = ''; NATIVE?.setViewer?.(false); removeEventListener('keydown', onKey); removeEventListener('popstate', onPop); return; }
    e.stopPropagation();
    act(b.dataset.a, g.items[i]).catch(err => { if (err.message !== 'non connecté') toast(err.message); });
  });

  function onKey(e) {
    if (document.querySelector('.modal-bg')) return;
    if (e.target.tagName === 'INPUT') return;
    const a = g.items[i];
    const map = { ArrowLeft: 'prev', ArrowRight: 'next', Escape: 'close', i: share ? null : 'info', f: share || a.trashedAt ? null : 'fav', Delete: share || a.trashedAt ? null : 'trash' };
    const name = map[e.key];
    if (!name) return;
    e.preventDefault();
    act(name, a).catch(err => toast(err.message));
  }
  addEventListener('keydown', onKey);

  // gestes : balayage gauche/droite, glisser vers le bas pour fermer, double-tap pour zoomer
  let p0 = null, lastTap = 0, tapTimer = null;
  el.addEventListener('pointerdown', e => {
    if (e.target.closest('button, a, video, .info')) return;
    p0 = { x: e.clientX, y: e.clientY, lx: e.clientX, ly: e.clientY, t: Date.now() };
  });
  el.addEventListener('pointermove', e => {
    if (!p0) return;
    const img = el.querySelector('.stage img');
    if (img?.pan?.(e.clientX - p0.lx, e.clientY - p0.ly)) { p0.panned = true; }
    p0.lx = e.clientX; p0.ly = e.clientY;
  });
  el.addEventListener('pointerup', e => {
    if (!p0) return;
    const dx = e.clientX - p0.x, dy = e.clientY - p0.y, dt = Date.now() - p0.t;
    const img = el.querySelector('.stage img');
    if (img) img.style.transition = '';
    const start = p0;
    p0 = null;
    if (start.panned) return;
    const zoomed = img?.isZoomed?.();
    if (!zoomed && Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.2) return go(dx < 0 ? 1 : -1);
    if (!zoomed && dy > 90 && Math.abs(dy) > Math.abs(dx)) return close();
    if (Math.abs(dx) < 10 && Math.abs(dy) < 10 && dt < 300) {
      const now = Date.now();
      if (now - lastTap < 300 && img) {
        clearTimeout(tapTimer);
        lastTap = 0;
        img.toggleZoom(e.clientX, e.clientY);
      } else {
        lastTap = now;
        tapTimer = setTimeout(() => { chrome = !chrome; el.classList.toggle('chrome-off', !chrome); }, 300);
      }
    }
  });

  render();
}

/* =========================================================
   Envoi des fichiers (file d'attente, 2 envois simultanés)
   ========================================================= */
const MEDIA_EXT = /\.(jpe?g|png|gif|webp|heic|heif|avif|tiff?|dng|mp4|m4v|mov|webm|3gp|mkv|avi|mts)$/i;

const uploader = {
  q: [], active: 0, total: 0, done: 0, created: 0, dup: 0, errors: [], bytesTotal: 0, bytesDone: 0, current: '', el: null,

  add(files) {
    if (!files.length) return;
    if (!this.active && !this.q.length) this.reset();
    this.q.push(...files);
    this.total += files.length;
    this.bytesTotal += files.reduce((s, f) => s + f.size, 0);
    this.render();
    this.pump();
  },
  reset() {
    Object.assign(this, { total: 0, done: 0, created: 0, dup: 0, errors: [], bytesTotal: 0, bytesDone: 0, current: '' });
  },
  pump() {
    while (this.active < 2 && this.q.length) this.send(this.q.shift());
    if (!this.active && !this.q.length) this.finish();
  },
  // chaque fichier est chiffré ici, par blocs, avant de partir ; un envoi coupé reprend où il en était
  async send(f) {
    this.active++;
    this.current = f.name;
    let sent = 0, retry = false;
    const onBytes = n => { sent += n; this.bytesDone += n; this.render(); };
    try {
      const st = await Vault.uploadFile(f, onBytes);
      st === 'created' ? this.created++ : this.dup++;
    } catch (e) {
      if (e.status === 401) { this.errors.push('Session expirée : reconnecte-toi.'); this.q = []; }
      else if (e.message === 'Serveur injoignable' && (f.retries || 0) < 3) retry = true;
      else this.errors.push(`${f.name} : ${e.message || 'échec de l’envoi'}`);
    }
    if (retry) {
      // coupure réseau : on reprend un peu plus tard, depuis le dernier morceau reçu par le serveur
      f.retries = (f.retries || 0) + 1;
      this.bytesDone -= sent;
      await new Promise(r => setTimeout(r, 3000 * f.retries));
      this.q.push(f);
    } else {
      this.bytesDone += Math.max(0, f.size - sent);
      this.done++;
    }
    this.active--;
    this.render();
    this.pump();
  },
  finish() {
    this.current = '';
    this.render();
    if (this.created) {
      refreshUsage();
      dispatchEvent(new CustomEvent('pellicule:uploaded'));
    }
  },
  busy() { return this.active > 0 || this.q.length > 0; },
  render() {
    if (!this.total) { this.el?.remove(); this.el = null; return; }
    if (!this.el) {
      this.el = document.createElement('div');
      this.el.className = 'uploader';
      this.el.addEventListener('click', e => {
        if (e.target.closest('[data-close]') && !this.busy()) { this.reset(); this.render(); }
      });
      document.body.appendChild(this.el);
    }
    const pct = this.bytesTotal ? Math.min(100, this.bytesDone / this.bytesTotal * 100) : 0;
    const busy = this.busy();
    this.el.innerHTML = `<div class="h">${icon(busy ? 'cloud' : 'check')}<span class="grow">${busy
      ? `Envoi ${Math.min(this.done + 1, this.total)} / ${this.total}`
      : 'Envoi terminé'}</span>${busy ? '' : `<button class="icon-btn" data-close title="Fermer">${icon('x')}</button>`}</div>
      <div class="line">${busy
        ? `${esc(this.current)} · ${fmtSize(Math.max(0, this.bytesTotal - this.bytesDone))} restants`
        : [`${this.created} ajouté${this.created > 1 ? 's' : ''}`, this.dup ? `${this.dup} déjà présent${this.dup > 1 ? 's' : ''}` : '', this.errors.length ? `${this.errors.length} erreur${this.errors.length > 1 ? 's' : ''}` : ''].filter(Boolean).join(' · ')}</div>
      <div class="bar"><i style="width:${busy ? pct : 100}%"></i></div>
      ${this.errors.length ? `<div class="errs">${this.errors.slice(-20).map(esc).join('<br>')}</div>` : ''}`;
  },
};
addEventListener('beforeunload', e => { if (uploader.busy()) { e.preventDefault(); e.returnValue = ''; } });

// Vérifie d'abord ce qui est déjà sauvegardé (nom + taille), puis envoie le reste.
async function importFiles(files) {
  const media = files.filter(f => MEDIA_EXT.test(f.name) && f.size > 0);
  const ignored = files.length - media.length;
  if (!media.length) {
    if (files.length) toast('Aucune photo ou vidéo reconnue');
    return;
  }
  // empreintes HMAC (nom + taille) : le serveur ne voit pas les noms de fichiers
  let existing = new Set();
  try {
    existing = new Set(await Vault.existingTags(media));
  } catch (e) {
    if (e.status === 401) return boot();
  }
  const todo = media.filter(f => !existing.has(f));
  const parts = [];
  if (existing.size) parts.push(`${existing.size} déjà sauvegardé${existing.size > 1 ? 's' : ''}`);
  if (ignored) parts.push(`${ignored} fichier${ignored > 1 ? 's' : ''} ignoré${ignored > 1 ? 's' : ''}`);
  if (!todo.length) return toast(parts.length ? `Rien de nouveau : ${parts.join(', ')}` : 'Rien à envoyer');
  if (parts.length) toast(`${todo.length} à envoyer (${parts.join(', ')})`);
  uploader.add(todo);
}

async function filesFromDrop(dt) {
  const entries = [...dt.items].map(it => it.webkitGetAsEntry?.()).filter(Boolean);
  if (!entries.length) return [...dt.files];
  const out = [];
  const walk = async e => {
    if (e.isFile) {
      out.push(await new Promise((res, rej) => e.file(res, rej)));
    } else if (e.isDirectory) {
      const reader = e.createReader();
      let batch;
      do {
        batch = await new Promise((res, rej) => reader.readEntries(res, rej));
        for (const x of batch) await walk(x);
      } while (batch.length);
    }
  };
  for (const e of entries) await walk(e).catch(() => {});
  return out;
}

/* =========================================================
   Structure de l'application
   ========================================================= */
const NAV = [
  ['photos', '#/', 'photos', 'Photos'],
  ['albums', '#/albums', 'album', 'Albums'],
  ['favorites', '#/favorites', 'heart', 'Favoris'],
  ['videos', '#/videos', 'video', 'Vidéos'],
  ['trash', '#/trash', 'trash', 'Corbeille'],
  ['backup', '#/backup', 'cloud', 'Sauvegarde'],
  ['settings', '#/settings', 'settings', 'Réglages'],
];
const TABS = ['photos', 'albums', 'backup', 'settings'];

function renderShell() {
  app.className = '';
  app.innerHTML = `<div class="shell">
    <aside class="side">
      <a class="logo" href="#/">${logoMark}<span>Pellicule</span></a>
      ${NAV.map(([k, h, ic, l]) => `<a class="nav-item" data-nav="${k}" href="${h}">${icon(ic)}${l}</a>`).join('')}
      <div class="spacer"></div>
      <div class="usage" id="usage"></div>
    </aside>
    <div class="main">
      <div class="selbar" id="selbar" hidden></div>
      <header class="top">
        <a class="logo" href="#/">${logoMark}<span>Pellicule</span></a>
        <form class="search" id="search" role="search">${icon('search')}<input type="search" name="q" placeholder="Rechercher un fichier" aria-label="Rechercher"></form>
        <button class="btn primary upload-btn" id="up-btn" title="Importer des photos">${icon('upload')}<span>Importer</span></button>
        <input type="file" id="up-input" multiple accept="image/*,video/*,.heic,.heif" hidden>
      </header>
      <main class="content" id="view"></main>
    </div>
  </div>
  <nav class="tabbar">${NAV.filter(n => TABS.includes(n[0])).map(([k, h, ic, l]) => `<a data-nav="${k}" href="${h}">${icon(ic)}${l}</a>`).join('')}</nav>`;

  $('#search').onsubmit = e => {
    e.preventDefault();
    const q = e.target.q.value.trim();
    location.hash = q ? '#/search?q=' + encodeURIComponent(q) : '#/';
  };
  const input = $('#up-input');
  $('#up-btn').hidden = !!NATIVE; // dans l'app, la sauvegarde est automatique
  $('#up-btn').onclick = () => input.click();
  input.onchange = () => { importFiles([...input.files]); input.value = ''; };
}

function setActive(key) {
  document.querySelectorAll('[data-nav]').forEach(a => a.classList.toggle('active', a.dataset.nav === key));
}

async function refreshUsage() {
  try {
    const s = await api('/api/stats');
    const el = $('#usage');
    if (!el) return;
    el.innerHTML = `<b>${s.photos.toLocaleString('fr-FR')}</b> photos · <b>${s.videos.toLocaleString('fr-FR')}</b> vidéos<br>${fmtSize(s.bytes)} utilisés${s.diskFree >= 0 ? ` · ${fmtSize(s.diskFree)} libres` : ''}`;
  } catch { /* hors ligne */ }
}

addEventListener('scroll', () => $('.top')?.classList.toggle('scrolled', scrollY > 4), { passive: true });

// glisser-déposer de fichiers ou de dossiers n'importe où
addEventListener('dragover', e => {
  if (state.user && e.dataTransfer?.types.includes('Files')) {
    e.preventDefault();
    $('#drop')?.classList.add('drag');
  }
});
addEventListener('dragleave', e => { if (!e.relatedTarget) $('#drop')?.classList.remove('drag'); });
addEventListener('drop', async e => {
  if (!state.user || !e.dataTransfer?.types.includes('Files')) return;
  e.preventDefault();
  $('#drop')?.classList.remove('drag');
  importFiles(await filesFromDrop(e.dataTransfer));
});

/* ---------- routeur ---------- */
let cleanup = null;
let routeSeq = 0;
const view = () => $('#view');

async function route() {
  if (!state.user) return;
  cleanup?.();
  cleanup = null;
  const seq = ++routeSeq;
  const sb = $('#selbar');
  sb.hidden = true;
  sb.onclick = null;
  $('.top').hidden = false;
  const [path, qs] = (location.hash.slice(1) || '/').split('?');
  const p = Object.fromEntries(new URLSearchParams(qs || ''));
  $('#search').q.value = path === '/search' ? p.q || '' : '';
  scrollTo(0, 0);
  const stale = () => seq !== routeSeq;
  try {
    let m;
    if (path === '/' || path === '') return await viewPhotos(p, stale);
    if (path === '/favorites') return await viewPhotos({ favorites: '1' }, stale);
    if (path === '/videos') return await viewPhotos({ kind: 'video' }, stale);
    if (path === '/trash') return await viewPhotos({ trash: '1' }, stale);
    if (path === '/search') return await viewPhotos({ q: p.q || '' }, stale);
    if (path === '/albums') return await viewAlbums(stale);
    if ((m = path.match(/^\/albums\/(\d+)$/))) return await viewAlbum(+m[1], stale);
    if (path === '/backup') return viewBackup();
    if (path === '/settings') return await viewSettings(stale);
    location.hash = '#/';
  } catch (e) {
    if (e.message !== 'non connecté' && !stale()) {
      view().innerHTML = `<div class="empty"><div class="big">${icon('info')}</div><h2>Oups</h2><p>${esc(e.message)}</p></div>`;
    }
  }
}
addEventListener('hashchange', route);

function emptyState(ic, title, text, btn = '') {
  return `<div class="empty"><div class="big">${icon(ic)}</div><h2>${esc(title)}</h2><p>${esc(text)}</p>${btn}</div>`;
}

/* ---------- vue : photos / favoris / vidéos / corbeille / recherche ---------- */
async function viewPhotos(p, stale) {
  const mode = p.trash ? 'trash' : p.favorites ? 'favorites' : p.kind === 'video' ? 'videos' : p.q !== undefined ? 'search' : 'photos';
  setActive(mode === 'search' ? 'photos' : mode);
  const pick = +p.pick || 0;
  let pickAlbum = null;
  if (pick) {
    pickAlbum = await Vault.hydrateAlbum((await api('/api/albums/' + pick)).album);
    if (stale()) return;
  }
  const titles = { photos: 'Photos', favorites: 'Favoris', videos: 'Vidéos', trash: 'Corbeille', search: `« ${p.q} »` };
  const v = view();
  v.innerHTML = `<div class="page-head"><div><h1>${esc(pick ? `Ajouter à « ${pickAlbum.name} »` : titles[mode])}</h1><div class="sub" id="sub"></div></div>
    <div class="actions" id="head-actions"></div></div>
    ${pick || mode === 'search' ? '' : `<div class="chips" id="chips"></div>`}
    <div id="gal"></div>`;

  const qs = new URLSearchParams();
  if (p.trash) qs.set('trash', '1');
  if (p.favorites) qs.set('favorites', '1');
  if (p.kind) qs.set('kind', p.kind);
  if (p.year) qs.set('year', p.year);

  // Recherche : les noms de fichiers sont chiffrés, le serveur ne peut pas chercher. On parcourt la
  // photothèque par pages de 2000 et on filtre ici, après déchiffrement.
  const needle = (p.q || '').toLowerCase();
  const load = mode !== 'search'
    ? cursor => api('/api/timeline?' + qs + (cursor ? '&cursor=' + cursor : ''))
    : async cursor => {
      let next = cursor, found = [];
      do {
        const r = await api('/api/timeline?limit=2000' + (next ? '&cursor=' + next : ''));
        await Vault.hydrate(r.items, { owner: true });
        found = found.concat(r.items.filter(a => a.filename.toLowerCase().includes(needle)));
        next = r.next;
      } while (next && found.length < 60 && !stale());
      return { items: found, next };
    };

  const empties = {
    photos: emptyState('photos', 'Aucune photo pour l’instant', 'Importe tes premières photos depuis ton téléphone ou ton ordinateur.', '<a class="btn primary" href="#/backup">Sauvegarder mes photos</a>'),
    favorites: emptyState('heart', 'Pas encore de favoris', 'Touche le cœur sur une photo pour la retrouver ici.'),
    videos: emptyState('video', 'Aucune vidéo', 'Les vidéos importées apparaîtront ici.'),
    trash: emptyState('trash', 'La corbeille est vide', 'Les éléments supprimés y restent 30 jours.'),
    search: emptyState('search', 'Aucun résultat', 'La recherche porte sur le nom des fichiers.'),
  };

  const g = new Gallery($('#gal'), {
    selectable: true,
    pickMode: !!pick,
    groupBy: mode === 'trash' ? null : a => dKey(a.takenAt),
    groupLabel: a => dayLabel(a.takenAt),
    load,
    empty: empties[mode],
    open: i => openViewer(g, i),
    onCancel: () => { location.hash = '#/albums/' + pick; },
  });
  cleanup = () => g.destroy();

  let actions;
  if (pick) {
    actions = [{ label: 'Ajouter', run: async items => {
      await addToAlbum(pick, items);
      toast(`${countLabel(items.length)} ajouté${items.length > 1 ? 's' : ''}`);
      location.hash = '#/albums/' + pick;
    } }];
  } else if (mode === 'trash') {
    actions = [
      { icon: 'restore', title: 'Restaurer', run: async items => {
        await post('/api/assets/restore', { ids: items.map(a => a.id) });
        g.remove(items.map(a => a.id));
        toast(`${countLabel(items.length)} restauré${items.length > 1 ? 's' : ''}`);
        refreshUsage();
      } },
      { icon: 'trash', title: 'Supprimer définitivement', run: async items => {
        if (!await confirmModal('Supprimer définitivement ?', `${countLabel(items.length)} sera effacé du disque. Impossible à annuler.`, 'Supprimer', true)) return;
        await post('/api/assets/delete', { ids: items.map(a => a.id) });
        g.remove(items.map(a => a.id));
        refreshUsage();
      } },
    ];
  } else {
    actions = [
      { icon: 'heart', title: 'Favori', run: async items => {
        const fav = await setFavorite(items, g);
        if (mode === 'favorites' && !fav) g.remove(items.map(a => a.id));
      } },
      { icon: 'album', title: 'Ajouter à un album', run: async items => { if (await albumPicker(items)) g.clear(); } },
      { icon: 'download', title: 'Télécharger', run: async items => downloadItems(items) },
      { icon: 'trash', title: 'Mettre à la corbeille', run: items => trashItems(items, g) },
    ];
  }
  bindSelbar(g, actions, 'Choisis les photos à ajouter');

  if (mode === 'trash') {
    $('#head-actions').innerHTML = `<button class="btn danger" id="empty-trash">${icon('trash')}Vider la corbeille</button>`;
    $('#empty-trash').onclick = async () => {
      if (!await confirmModal('Vider la corbeille ?', 'Tous les éléments seront effacés du disque. Impossible à annuler.', 'Vider', true)) return;
      const r = await post('/api/trash/empty', {});
      toast(`${countLabel(r.deleted)} supprimé${r.deleted > 1 ? 's' : ''}`);
      refreshUsage();
      route();
    };
    $('#sub').textContent = 'Supprimés automatiquement après 30 jours.';
  }

  await g.load();
  if (stale()) return;

  if (!pick && mode !== 'search') {
    const s = await api('/api/stats');
    if (stale()) return;
    const chips = [['#/', 'Tout', mode === 'photos' && !p.year], ['#/favorites', 'Favoris', mode === 'favorites'],
      ['#/videos', 'Vidéos', mode === 'videos'], ['#/trash', `Corbeille${s.trash ? ` (${s.trash})` : ''}`, mode === 'trash']];
    if (mode === 'photos') s.years.forEach(y => chips.push([`#/?year=${y}`, String(y), p.year === String(y)]));
    $('#chips').innerHTML = chips.map(([h, l, on]) => `<a class="chip${on ? ' on' : ''}" href="${h}">${esc(l)}</a>`).join('');
    if (mode === 'photos') {
      $('#sub').textContent = [plural(s.photos, 'photo', 'photos'), plural(s.videos, 'vidéo', 'vidéos')].join(' · ') + (p.year ? ` · année ${p.year}` : '');
    }
  }

  if (mode === 'photos' && !pick && NATIVE) watchNativeBackup(g);
  if (mode === 'photos' && !pick) {
    const onUp = () => {
      if (scrollY < 300 && !g.sel.size) route();
      else toast('Nouvelles photos importées', { action: { label: 'Afficher', run: route } });
    };
    addEventListener('pellicule:uploaded', onUp);
    const prev = cleanup;
    cleanup = () => { prev(); removeEventListener('pellicule:uploaded', onUp); };
  }
}

/* ---------- vue : albums ---------- */
async function viewAlbums(stale) {
  setActive('albums');
  const v = view();
  v.innerHTML = `<div class="page-head"><div><h1>Albums</h1><div class="sub" id="sub"></div></div>
    <div class="actions"><button class="btn primary" id="new-album">${icon('plus')}Nouvel album</button></div></div>
    <div class="albums" id="albums"><div class="spinner"></div></div>`;
  $('#new-album').onclick = newAlbum;
  const albums = await Promise.all((await api('/api/albums')).map(Vault.hydrateAlbum));
  if (stale()) return;
  $('#sub').textContent = plural(albums.length, 'album', 'albums');
  if (!albums.length) {
    $('#albums').outerHTML = emptyState('album', 'Aucun album', 'Regroupe tes photos par voyage, fête ou personne.', `<button class="btn primary" id="empty-new">${icon('plus')}Créer un album</button>`);
    $('#empty-new').onclick = newAlbum;
    return;
  }
  $('#albums').innerHTML = `<a class="album-card new" href="#" id="new-card"><div class="cover">${icon('plus')}</div><div class="name">Nouvel album</div><div class="meta">&nbsp;</div></a>`
    + albums.map(al => `<a class="album-card" href="#/albums/${al.id}">
      <div class="cover">${albumCover(al)}
        ${al.shared ? `<span class="shared" title="Partagé par lien">${icon('link')}</span>` : ''}</div>
      <div class="name">${esc(al.name)}</div>
      <div class="meta">${[countLabel(al.count), fmtRange(al.startAt, al.endAt)].filter(Boolean).join(' · ')}</div></a>`).join('');
  $('#new-card').onclick = e => { e.preventDefault(); newAlbum(); };
  paintCovers($('#albums'), albums);
}

async function newAlbum() {
  const name = await promptModal('Nouvel album', { label: 'Nom', placeholder: 'Vacances d’été', ok: 'Créer' });
  if (!name) return;
  const r = await post('/api/albums', { name: await Vault.sealName(name) });
  location.hash = `#/?pick=${r.id}`;
}

/* ---------- vue : un album ---------- */
async function viewAlbum(id, stale) {
  setActive('albums');
  const r = await api('/api/albums/' + id);
  if (stale()) return;
  const al = await Vault.hydrateAlbum(r.album);
  await Vault.hydrate(r.items, { owner: true });
  if (stale()) return;
  const v = view();
  v.innerHTML = `<a class="back" href="#/albums">${icon('left')}Albums</a>
    <div class="page-head"><div style="flex:1;min-width:240px">
      <input class="album-title" id="title" value="${esc(al.name)}" maxlength="200" aria-label="Nom de l'album">
      <div class="sub">${[countLabel(al.count), fmtRange(al.startAt, al.endAt)].filter(Boolean).join(' · ')}</div></div>
      <div class="actions">
        <a class="btn" href="#/?pick=${id}">${icon('plus')}Ajouter</a>
        <button class="btn" id="share">${icon('link')}Partager</button>
        ${al.count ? `<button class="icon-btn" id="zip" title="Tout télécharger (ZIP)">${icon('download')}</button>` : ''}
        <button class="icon-btn" id="del" title="Supprimer l'album">${icon('trash')}</button>
      </div></div><div id="gal"></div>`;

  const title = $('#title');
  title.onkeydown = e => { if (e.key === 'Enter') title.blur(); if (e.key === 'Escape') { title.value = al.name; title.blur(); } };
  title.onchange = async () => {
    const name = title.value.trim();
    if (!name) { title.value = al.name; return; }
    // chaque lien de partage reçoit aussi le nouveau nom, chiffré avec sa propre clé
    const shareNames = al.shared ? await Vault.syncShares(id, null, name) : {};
    await api('/api/albums/' + id, { method: 'PATCH', body: { name: await Vault.sealName(name), shareNames } });
    al.name = name;
    toast('Album renommé');
  };
  $('#share').onclick = () => shareModal(id, al.name, g.items);
  $('#zip')?.addEventListener('click', () => downloadItems(g.items, al.name).catch(e => toast(e.message)));
  $('#del').onclick = async () => {
    if (!await confirmModal('Supprimer l’album ?', 'Les photos restent dans ta bibliothèque, seul l’album disparaît.', 'Supprimer', true)) return;
    await api('/api/albums/' + id, { method: 'DELETE' });
    location.hash = '#/albums';
  };

  const g = new Gallery($('#gal'), {
    selectable: true,
    groupBy: a => dKey(a.takenAt),
    groupLabel: a => dayLabel(a.takenAt),
    load: async () => ({ items: r.items, next: '' }),
    empty: emptyState('album', 'Album vide', 'Ajoute des photos depuis ta bibliothèque.', `<a class="btn primary" href="#/?pick=${id}">${icon('plus')}Ajouter des photos</a>`),
    open: i => openViewer(g, i, { album: id }),
  });
  cleanup = () => g.destroy();
  bindSelbar(g, [
    { icon: 'heart', title: 'Favori', run: items => setFavorite(items, g) },
    { icon: 'star', title: 'Définir comme couverture', when: g => g.sel.size === 1, run: async items => {
      await api('/api/albums/' + id, { method: 'PATCH', body: { coverId: items[0].id } });
      g.clear();
      toast('Couverture modifiée');
    } },
    { icon: 'album', title: 'Ajouter à un autre album', run: async items => { if (await albumPicker(items, id)) g.clear(); } },
    { icon: 'download', title: 'Télécharger', run: async items => downloadItems(items) },
    { icon: 'minus', title: 'Retirer de l’album', run: async items => {
      await post(`/api/albums/${id}/remove`, { ids: items.map(a => a.id) });
      g.remove(items.map(a => a.id));
      toast(`${countLabel(items.length)} retiré${items.length > 1 ? 's' : ''} de l’album`);
    } },
    { icon: 'trash', title: 'Mettre à la corbeille', run: items => trashItems(items, g) },
  ]);
  g.load();
}

// Un lien de partage contient sa propre clé après le « # » : le serveur ne la voit jamais. Il ne garde
// qu'une copie du lien chiffrée par la clé maître, pour que le propriétaire puisse le réafficher.
async function shareModal(albumId, albumName, items) {
  await modal(`<h2>Partager l’album</h2>
    <p>Toute personne qui a le lien peut voir l’album, sans compte. La clé de déchiffrement est dans le lien lui-même :
    transmets-le uniquement à qui tu veux.</p>
    <div id="links"></div>
    <label class="switch"><input type="checkbox" id="dl" checked> Autoriser le téléchargement</label>
    <label class="lbl" for="exp">Expiration</label>
    <select class="field" id="exp"><option value="0">Jamais</option><option value="1">1 jour</option><option value="7" selected>7 jours</option><option value="30">30 jours</option><option value="365">1 an</option></select>
    <div class="foot"><button class="btn" data-c>Fermer</button><button class="btn primary" data-create>${icon('link')}Créer un lien</button></div>`,
  (m, close) => {
    const list = m.querySelector('#links');
    let synced = false;
    const load = async () => {
      const shares = await api(`/api/albums/${albumId}/shares`);
      // photos ajoutées depuis un autre appareil sans que ce lien ait reçu leur clé : on la dépose maintenant
      if (!synced && shares.some(s => s.missing > 0)) { synced = true; await Vault.syncShares(albumId, items); return load(); }
      const rows = await Promise.all(shares.map(async s => {
        let url = '';
        try { url = (await Vault.shareKeyOf(s)).link; } catch { /* lien illisible */ }
        const meta = [s.allowDownload ? 'téléchargement autorisé' : 'lecture seule', s.expiresAt ? `expire le ${DF.short.format(s.expiresAt)}` : 'sans expiration'].join(' · ');
        return `<div class="share-link"><div style="flex:1;min-width:0"><input class="field" readonly value="${esc(url || 'lien illisible')}"><small>${esc(meta)}</small></div>
          ${url ? `<button class="icon-btn" data-copy="${esc(url)}" title="Copier">${icon('copy')}</button>` : ''}
          <button class="icon-btn" data-del="${s.id}" title="Supprimer le lien">${icon('trash')}</button></div>`;
      }));
      list.innerHTML = rows.join('');
    };
    m.onclick = async e => {
      const b = e.target.closest('button');
      if (!b) return;
      try {
        if ('c' in b.dataset) return close();
        if ('create' in b.dataset) {
          b.disabled = true;
          const link = await Vault.createShare(albumId, albumName, items, m.querySelector('#dl').checked, +m.querySelector('#exp').value);
          await load();
          b.disabled = false;
          const input = [...list.querySelectorAll('input')].find(x => x.value === link);
          await copyText(link, input);
          toast('Lien créé et copié');
        } else if (b.dataset.copy) {
          await copyText(b.dataset.copy, b.parentNode.querySelector('input'));
          toast('Lien copié');
        } else if (b.dataset.del) {
          await api('/api/shares/' + b.dataset.del, { method: 'DELETE' });
          await load();
          toast('Lien supprimé : il ne fonctionne plus');
        }
      } catch (err) { b.disabled = false; toast(err.message); }
    };
    load().catch(err => toast(err.message));
  });
}

/* ---------- vue : sauvegarde ---------- */
function viewBackup() {
  setActive('backup');
  const ios = /iPhone|iPad|iPod/.test(navigator.userAgent);
  const android = /Android/.test(navigator.userAgent);
  view().innerHTML = `<div class="backup">
    <div class="page-head"><div><h1>Sauvegarde</h1><div class="sub">Envoie tes photos et vidéos sur le serveur. Ce qui est déjà sauvegardé est ignoré.</div></div></div>
    <div class="backup-hero" id="drop" ${NATIVE ? 'hidden' : ''}>
      <div class="big">${icon('cloud')}</div>
      <h2>Sauvegarder des photos</h2>
      <p>${ios || android ? 'Choisis les photos et vidéos de ta galerie à envoyer.' : 'Glisse des photos ou des dossiers entiers ici, ou choisis-les.'}</p>
      <div class="row">
        <button class="btn primary" id="pick">${icon('upload')}Choisir des photos</button>
        ${ios || android ? '' : `<button class="btn" id="pick-dir">${icon('folder')}Choisir un dossier</button>`}
      </div>
      <input type="file" id="f" multiple accept="image/*,video/*,.heic,.heif" hidden>
      <input type="file" id="d" webkitdirectory multiple hidden>
    </div>
    <div class="stats" id="stats"></div>
    ${ios ? `<div class="card"><h3>Sur iPhone</h3><ol>
      <li>Dans <b>Safari</b>, touche <b>Partager</b> puis <b>Sur l’écran d’accueil</b> : Pellicule s’ouvre alors comme une app.</li>
      <li>Touche <b>Choisir des photos</b> → <b>Photothèque</b>, sélectionne ce que tu veux (tu peux glisser le doigt pour en sélectionner beaucoup), puis <b>Ajouter</b>.</li>
      <li>Garde l’app ouverte et l’écran allumé pendant l’envoi.</li>
      <li>Refais-le de temps en temps : les photos déjà envoyées sont ignorées.</li></ol></div>` : ''}
    ${NATIVE ? `<div class="card" id="native"><h3>Sauvegarde automatique de ce téléphone</h3>
      <p>Rien à sélectionner : toutes les photos des dossiers choisis partent toutes seules, puis chaque nouvelle photo,
      dès que le téléphone est en Wi-Fi et que le serveur répond (donc à la maison).</p>
      <label class="switch"><input type="checkbox" id="nat-on"> Activée</label>
      <p id="nat-status">…</p>
      <div class="form-row" style="gap:8px"><button class="btn primary" id="nat-now">${icon('cloud')}Sauvegarder maintenant</button>
      <button class="btn" id="nat-set">${icon('settings')}Dossiers et réglages</button></div>
    </div>` : ''}
    ${ios || NATIVE ? '' : `<div class="card" id="auto"><h3>Sauvegarde automatique à la maison (Android)</h3>
      <p>Le téléphone dépose ses photos sur le disque de la Freebox dès qu’il est sur le Wi-Fi de la maison,
      et Pellicule les ajoute toute seule (vérification toutes les 3 minutes). Rien à ouvrir.</p>
      <p id="auto-status">…</p>
      <ol>
        <li>Installe <b>FolderSync</b> depuis le Play Store.</li>
        <li><b>Comptes</b> → <b>+</b> → <b>SMB</b> : serveur <b>mafreebox.freebox.fr</b>, avec l’utilisateur et le mot de passe du partage Windows de la Freebox.</li>
        <li><b>Folderpairs</b> → <b>+</b> : synchronisation <b>vers le dossier distant</b> (envoi seulement), dossier local <b>DCIM/Camera</b>, dossier distant <b id="auto-folder">…</b> sur ce partage.</li>
        <li>Planification : <b>synchro instantanée</b>, ou toutes les heures.</li>
        <li>Connexion : <b>Wi-Fi uniquement</b>, et ajoute le <b>nom du Wi-Fi de la maison</b> dans les SSID autorisés (FolderSync demandera la localisation pour lire ce nom).</li>
        <li>Laisse désactivées la suppression des fichiers source et la synchro des suppressions.</li>
      </ol>
      <p>Captures d’écran, WhatsApp… : ajoute un folderpair par dossier, vers un sous-dossier (par ex. <b>…/Captures</b>).</p>
    </div>`}
    ${android && !NATIVE ? `<div class="card"><h3>Envoi manuel</h3><ol>
      <li>Dans <b>Chrome</b>, menu <b>⋮</b> → <b>Ajouter à l’écran d’accueil</b>.</li>
      <li>Touche <b>Choisir des photos</b>, sélectionne tes photos et vidéos. Les photos déjà envoyées sont ignorées.</li></ol></div>` : ''}
    ${!ios && !android ? `<div class="card"><h3>Importer une grosse photothèque</h3>
      <p>Pour un vieux disque dur ou un export Google Photos / iCloud, glisse le dossier entier sur cette page. Les doublons sont détectés automatiquement, tu peux relancer sans risque.</p></div>` : ''}
  </div>`;

  const f = $('#f'), d = $('#d');
  $('#pick').onclick = () => f.click();
  $('#pick-dir')?.addEventListener('click', () => d.click());
  f.onchange = () => { importFiles([...f.files]); f.value = ''; };
  d.onchange = () => { importFiles([...d.files]); d.value = ''; };
  if (NATIVE) bindNative();

  api('/api/stats').then(s => {
    const el = $('#stats');
    if (!el) return;
    el.innerHTML = [
      [s.photos.toLocaleString('fr-FR'), 'photos'],
      [s.videos.toLocaleString('fr-FR'), 'vidéos'],
      [fmtSize(s.bytes), 'utilisés'],
      [s.diskFree >= 0 ? fmtSize(s.diskFree) : '—', 'libres sur le disque'],
    ].map(([v, k]) => `<div class="stat"><div class="v">${esc(v)}</div><div class="k">${esc(k)}</div></div>`).join('');
    renderAutoStatus(s.import);
  }).catch(() => {});
}

// Dans l'app Android : bandeau d'avancement en haut de la galerie, et la galerie se met à jour toute seule.
function watchNativeBackup(g) {
  const bar = document.createElement('div');
  bar.className = 'native-banner';
  bar.hidden = true;
  $('.page-head').after(bar);
  let lastDone = null;
  const tick = () => {
    let s;
    try { s = JSON.parse(NATIVE.status()); } catch { return; }
    if (s.running && s.progressTotal) {
      const pct = Math.round(s.progressDone / s.progressTotal * 100);
      bar.innerHTML = `${icon('cloud')}<div class="grow"><b>Sauvegarde en cours</b><span>${s.progressDone} / ${s.progressTotal}</span>
        <div class="bar"><i style="width:${pct}%"></i></div></div>`;
      bar.hidden = false;
    } else {
      bar.hidden = true;
    }
    if (lastDone !== null && s.done > lastDone && !g.sel.size && !document.querySelector('.viewer, .modal-bg')) {
      if (scrollY < 300) route();
      else toast('Nouvelles photos sauvegardées', { action: { label: 'Afficher', run: route } });
    }
    lastDone = s.done;
  };
  tick();
  const timer = setInterval(tick, 3000);
  addEventListener('pellicule:native', tick);
  const prev = cleanup;
  cleanup = () => { prev?.(); clearInterval(timer); removeEventListener('pellicule:native', tick); };
}

// Dans l'app Android, la page Sauvegarde pilote la sauvegarde native (pont PelliculeAndroid).
function bindNative() {
  const render = () => {
    let s;
    try { s = JSON.parse(NATIVE.status()); } catch { return; }
    const st = $('#nat-status');
    if (!st) return;
    $('#nat-on').checked = s.enabled;
    const when = s.lastRun ? `Dernier passage : ${dayLabel(s.lastRun - new Date().getTimezoneOffset() * 6e4).toLowerCase()} à ${new Date(s.lastRun).toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' })}` : 'Pas encore lancée';
    st.innerHTML = s.permission
      ? `${esc(plural(s.done, 'élément sauvegardé', 'éléments sauvegardés'))} depuis ce téléphone${s.running ? ' · <b>sauvegarde en cours…</b>' : ''}<br>${esc(when)}${s.lastResult ? ` · ${esc(s.lastResult)}` : ''}`
      : `<b>L’app n’a pas accès aux photos.</b> <button class="btn" id="nat-perm" style="margin-left:6px">Autoriser</button>`;
    $('#nat-perm')?.addEventListener('click', () => NATIVE.requestPermission());
  };
  $('#nat-on').onchange = e => { NATIVE.setEnabled(e.target.checked); render(); };
  $('#nat-now').onclick = () => { NATIVE.backupNow(); toast('Sauvegarde lancée'); setTimeout(render, 800); };
  $('#nat-set').onclick = () => NATIVE.openSettings();
  render();
  const timer = setInterval(render, 2000);
  addEventListener('pellicule:native', render);
  cleanup = () => { clearInterval(timer); removeEventListener('pellicule:native', render); };
}

function renderAutoStatus(imp) {
  const st = $('#auto-status');
  if (!st || !imp) return;
  $('#auto-folder').textContent = imp.folder;
  const last = imp.running ? 'vérification en cours…'
    : imp.lastScan ? `dernière vérification à ${new Date(imp.lastScan).toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' })}` : 'pas encore vérifié';
  st.innerHTML = `Dossier d’import : <b>${esc(imp.folder)}</b><br>${esc(plural(imp.count, 'élément importé', 'éléments importés'))} · ${esc(last)}
    ${imp.refused ? `<br><b>⚠ ${esc(plural(imp.refused, 'fichier refusé', 'fichiers refusés'))}</b> (illisible ou pas une vraie photo/vidéo) : mis de côté dans le dossier caché <b>${esc(imp.folder)}/.refusés</b>, à vérifier puis supprimer.` : ''}
    <br><button class="btn" id="scan-now" style="margin-top:10px">${icon('restore')}Vérifier maintenant</button>`;
  $('#scan-now').onclick = async () => {
    await post('/api/import/scan', {});
    toast('Vérification lancée');
    setTimeout(async () => {
      try { renderAutoStatus((await api('/api/stats')).import); refreshUsage(); } catch { /* ignoré */ }
    }, 3000);
  };
}

/* ---------- vue : réglages ---------- */
async function viewSettings(stale) {
  setActive('settings');
  const [s, me] = await Promise.all([api('/api/stats'), api('/api/me')]);
  if (stale()) return;
  const u = state.user;
  const onPhone = !!u.deviceId; // requête faite avec le certificat d'un téléphone appairé
  view().innerHTML = `<div class="settings">
    <div class="page-head"><div><h1>Réglages</h1></div></div>
    ${NATIVE ? `<div class="card"><h3>Cette app</h3><p>Dossiers sauvegardés, vidéos, batterie et état de la sauvegarde.</p>
      <button class="btn primary" id="nat-settings">${icon('settings')}Réglages de la sauvegarde</button></div>` : ''}
    <div class="card"><h3>Mon compte</h3>
      <p>Connecté en tant que <b>${esc(u.username)}</b>${u.isAdmin ? '<span class="tag">admin</span>' : ''}</p>
      <p class="hint">Tes photos sont chiffrées sur tes appareils avant d’être envoyées. Le serveur ne peut pas les voir,
      et personne ne peut réinitialiser ton mot de passe : garde ta <b>clé de secours</b> en lieu sûr.</p>
      ${onPhone ? '' : `<form id="pw" class="form-row">
        <div><label class="lbl">Mot de passe actuel</label><input class="field" type="password" name="current" autocomplete="current-password" required></div>
        <div><label class="lbl">Nouveau mot de passe</label><input class="field" type="password" name="new" minlength="10" autocomplete="new-password" required></div>
        <button class="btn">Changer</button>
        <label class="switch"><input type="checkbox" name="devices"> Déconnecter aussi mes téléphones (mot de passe compromis : il faudra les appairer de nouveau)</label>
      </form>`}
      <div style="margin-top:18px"><button class="btn danger" id="logout">${icon('logout')}Se déconnecter</button></div>
    </div>
    <div class="card"><h3>Téléphones appairés</h3>
      <p>Un téléphone appairé se connecte avec une clé qui ne quitte jamais sa puce de sécurité. Si tu en perds un, révoque-le ici.</p>
      <table class="users" id="devices"></table>
      ${onPhone ? '' : `<button class="btn primary" id="pair" style="margin-top:10px">${icon('camera')}Appairer un téléphone</button>`}
    </div>
    ${u.isAdmin ? `<div class="card"><h3>Comptes</h3><p>Chaque compte a sa propre photothèque, chiffrée avec ses propres clés.
      Pour ajouter quelqu’un : « Inviter quelqu’un », puis la personne scanne le QR code avec l’app Pellicule
      et choisit elle-même son mot de passe.</p>
      <table class="users" id="users"></table>
      <div class="form-row" style="margin-top:6px"><label class="switch"><input type="checkbox" id="inv-admin"> Administrateur</label>
      <button class="btn primary" id="invite">${icon('plus')}Inviter quelqu’un</button></div>
      <div id="invite-out"></div></div>` : ''}
    <div class="card"><h3>Serveur</h3>
      <p>${[plural(s.photos, 'photo', 'photos'), plural(s.videos, 'vidéo', 'vidéos'), `${fmtSize(s.bytes)} utilisés`, s.diskFree >= 0 ? `${fmtSize(s.diskFree)} libres` : ''].filter(Boolean).join(' · ')}</p>
      ${s.files ? `<p>${s.files.failed > 0
        ? `<b>⚠ ${esc(plural(s.files.failed, 'fichier n’a pas pu être écrit', 'fichiers n’ont pas pu être écrits'))} sur le disque des photos</b>
           (disque de la Freebox débranché ?). Nouvel essai chaque jour et au redémarrage du serveur ; d’ici là, une photo
           concernée ne reviendrait pas si la VM était perdue.`
        : s.files.lastSync ? `Reprise après perte de la VM : fichiers vérifiés le ${esc(new Date(s.files.lastSync).toLocaleString('fr-FR', { dateStyle: 'long', timeStyle: 'short' }))}.`
        : 'Reprise après perte de la VM : première vérification en cours.'}
        <br><span class="hint">Si la VM est perdue, chaque compte se reprend avec sa clé de secours.</span></p>
        ${s.files.oddNames?.length ? `<p><b>⚠ Noms de compte hors des règles actuelles :</b> ${s.files.oddNames.map(esc).join(', ')}.
          <span class="hint">Créés avec une version plus ancienne. Vérifie qu’aucun n’imite le nom de quelqu’un d’autre.</span></p>` : ''}` : ''}
      ${me.certFingerprint ? `<p>Empreinte du certificat :<br><code class="fp">${esc(me.certFingerprint)}</code><br>
        <span class="hint">Elle doit être identique à celle affichée par le serveur (<code>pellicule fingerprint</code>).
        Si ton navigateur signale un jour un autre certificat, ne te connecte pas.</span></p>
      ${window.PelliculeAndroid ? '' : `<p><a class="btn" href="/api/cert" download="pellicule.crt">${icon('download')}Certificat du serveur</a></p>`}
      <p class="hint">À installer comme certificat de confiance sur tes ordinateurs (Windows : double-clic → Installer →
      « Autorités de certification racines de confiance ») : plus d’avertissement, une alerte claire si un faux serveur
      apparaît, et la lecture des longues vidéos sans tout télécharger. L’app Android n’en a pas besoin.</p>` : ''}
      <p>Pellicule ${esc(state.version)}</p>
    </div></div>`;

  $('#pw')?.addEventListener('submit', async e => {
    e.preventDefault();
    const f = e.target, btn = f.querySelector('button');
    btn.disabled = true;
    try {
      const devices = f.devices.checked;
      await Vault.changePassword(f.current.value, f.new.value, devices);
      f.reset();
      toast(devices ? 'Mot de passe modifié. Les autres navigateurs et tes téléphones ont été déconnectés.'
        : 'Mot de passe modifié. Les autres navigateurs ont été déconnectés.');
      if (devices) loadDevices().catch(() => {});
    } catch (err) { toast(err.message); }
    btn.disabled = false;
  });
  $('#nat-settings')?.addEventListener('click', () => NATIVE.openSettings());
  $('#logout').onclick = async () => {
    if (NATIVE) {
      if (await confirmModal('Se déconnecter ?', 'Ce téléphone sera révoqué et oubliera ses clés. Il faudra l’appairer de nouveau.', 'Se déconnecter', true)) {
        await post('/api/devices/self/revoke', {}).catch(() => {});
        await Vault.forget();
        NATIVE.logout();
      }
      return;
    }
    await post('/api/logout', {}).catch(() => {});
    await Vault.forget();
    state.user = null;
    boot();
  };

  const loadDevices = async () => {
    const devs = await api('/api/devices');
    $('#devices').innerHTML = devs.length ? devs.map(d => `<tr><td><b>${esc(d.name)}</b>${d.current ? '<span class="tag">celui-ci</span>' : ''}</td>
      <td>appairé le ${DF.short.format(d.createdAt)} · vu le ${DF.short.format(d.lastSeen)}</td>
      <td>${onPhone ? '' : `<button class="btn danger" data-revoke="${d.id}" data-name="${esc(d.name)}">Révoquer</button>`}</td></tr>`).join('')
      : '<tr><td>Aucun téléphone appairé.</td></tr>';
  };
  $('#devices').onclick = async e => {
    const b = e.target.closest('[data-revoke]');
    if (!b) return;
    if (!await confirmModal(`Révoquer ${b.dataset.name} ?`, 'Ce téléphone ne pourra plus se connecter au serveur. Les photos déjà présentes sur lui y restent.', 'Révoquer', true)) return;
    await api('/api/devices/' + b.dataset.revoke, { method: 'DELETE' }).catch(err => toast(err.message));
    loadDevices();
  };
  $('#pair')?.addEventListener('click', () => pairModal().catch(err => toast(err.message)).then(loadDevices));
  loadDevices().catch(() => {});

  if (!u.isAdmin) return;
  const loadUsers = async () => {
    const users = await api('/api/users');
    $('#users').innerHTML = users.map(x => `<tr><td><b>${esc(x.username)}</b>${x.isAdmin ? '<span class="tag">admin</span>' : ''}</td>
      <td>${countLabel(x.count)} · ${fmtSize(x.bytes)}</td>
      <td>${x.id === u.id || onPhone ? '' : `<button class="icon-btn" data-del="${x.id}" data-name="${esc(x.username)}" title="Supprimer">${icon('trash')}</button>`}</td></tr>`).join('');
  };
  $('#users').onclick = async e => {
    const b = e.target.closest('button');
    if (!b?.dataset.del) return;
    try {
      if (!await confirmModal(`Supprimer ${b.dataset.name} ?`, 'Le compte et toutes ses photos chiffrées sont effacés du serveur. Impossible à annuler.', 'Supprimer', true)) return;
      await api('/api/users/' + b.dataset.del, { method: 'DELETE' });
      loadUsers();
    } catch (err) { toast(err.message); }
  };
  $('#invite').onclick = async () => {
    try {
      const r = await post('/api/invites', { isAdmin: $('#inv-admin').checked });
      const link = `${location.origin}/#invite=${r.token}`;
      // QR code pour l'app : adresse et invitation, plus l'empreinte du certificat si on est dans l'app (voir pinnedFP)
      const payload = 'pellicule:invite?' + new URLSearchParams({ url: location.origin, ...pinnedFP(), token: r.token });
      $('#invite-out').innerHTML = `<div class="qr">${QR.svg(payload)}</div>
        <p class="hint">À scanner avec l’app Pellicule (« Scanner le QR code »). Une seule personne, jusqu’au ${esc(DF.short.format(r.expiresAt))}.
        Sans l’app : ce lien, à ouvrir dans un navigateur.</p>
        <div class="share-link"><div style="flex:1;min-width:0"><input class="field" readonly value="${esc(link)}"></div>
        <button class="icon-btn" title="Copier">${icon('copy')}</button></div>`;
      const input = $('#invite-out input');
      $('#invite-out button').onclick = () => copyText(link, input).then(() => toast('Lien copié'));
    } catch (err) { toast(err.message); }
  };
  loadUsers();
}

// Empreinte à mettre dans un QR code : seulement celle que l'app a épinglée elle-même. Dans un navigateur,
// l'empreinte viendrait d'une page fournie par le serveur, peut-être un faux (R1) : sans elle, l'app scanne le
// QR code puis fait vérifier l'empreinte du serveur qu'elle joint (deux groupes à recopier).
function pinnedFP() {
  const fp = NATIVE?.fingerprint?.() || '';
  return /^[0-9a-f]{64}$/.test(fp) ? { fp } : {};
}

// Appairage : le QR code contient l'adresse du serveur et un code à usage unique valable 5 minutes.
async function pairModal() {
  const r = await post('/api/devices/pair-code', {});
  const payload = 'pellicule:pair?' + new URLSearchParams({ url: location.origin, ...pinnedFP(), code: r.code, user: r.username });
  await modal(`<h2>Appairer un téléphone</h2>
    <p>Dans l’app Pellicule du téléphone, touche <b>Scanner le QR code</b>.</p>
    <div class="qr">${QR.svg(payload)}</div>
    <p class="hint">Sans appareil photo : saisis l’adresse <b>${esc(location.origin)}</b> et le code <b class="code">${esc(r.code)}</b>.
    Valable 5 minutes, une seule fois. L’app te demandera ensuite ton mot de passe.</p>
    <div class="foot"><button class="btn primary" data-c>Terminé</button></div>`,
  (m, close) => {
    m.querySelector('[data-c]').onclick = () => close();
    setTimeout(() => close(), r.expiresAt - Date.now());
  });
}

/* =========================================================
   Connexion, création de compte, clé de secours
   ========================================================= */
const authCard = inner => {
  app.className = 'auth';
  app.innerHTML = `<form class="auth-card" id="auth" autocomplete="on"><div class="logo">${logoMark}<span>Pellicule</span></div>${inner}<div class="err" id="err"></div></form>`;
  return $('#auth');
};

// PBKDF2 (600 000 tours) prend une à deux secondes sur un téléphone : on affiche ce qui se passe.
function bindAuth(form, run, busyLabel = 'Déverrouillage…') {
  form.onsubmit = async e => {
    e.preventDefault();
    const btn = form.querySelector('button.primary');
    const label = btn.textContent;
    btn.disabled = true;
    btn.textContent = busyLabel;
    $('#err').textContent = '';
    try { await run(form); } catch (err) {
      $('#err').textContent = err.status === 401 ? 'Identifiants incorrects' : err.message;
      btn.disabled = false;
      btn.textContent = label;
    }
  };
}

function checkNewPassword(pw, pw2) {
  if (pw.length < 10) throw new Error('Mot de passe trop court (10 caractères minimum)');
  if (pw !== pw2) throw new Error('Les deux mots de passe ne correspondent pas');
}

const newPasswordFields = (hint = true) => `
  <label class="lbl" for="p">Mot de passe (10 caractères minimum)</label>
  <input class="field" id="p" type="password" name="password" autocomplete="new-password" minlength="10" required>
  <label class="lbl" for="p2">Confirmation</label>
  <input class="field" id="p2" type="password" name="password2" autocomplete="new-password" minlength="10" required>
  ${hint ? `<p class="hint">Ce mot de passe chiffre tes photos : personne, pas même l’administrateur, ne pourra le réinitialiser.
  Seule la clé de secours affichée à l’étape suivante permet de le remplacer.</p>` : ''}`;

function renderLogin() {
  const f = authCard(`<h1>Connexion</h1><p>Tes photos, chez toi.</p>
    <label class="lbl" for="u">Nom d'utilisateur</label>
    <input class="field" id="u" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" required>
    <label class="lbl" for="p">Mot de passe</label>
    <input class="field" id="p" type="password" name="password" autocomplete="current-password" required>
    <button class="btn primary">Se connecter</button>
    <a class="link" href="#recover">Mot de passe oublié ?</a>`);
  $('#u').focus();
  bindAuth(f, async f => {
    await Vault.login(f.username.value.trim(), f.password.value);
    boot();
  });
}

// session valide, mais ce navigateur n'a plus les clés (données du site effacées) : on redemande le mot de passe
function renderUnlock(user) {
  const f = authCard(`<h1>Déverrouiller</h1><p>Connecté en tant que <b>${esc(user.username)}</b>. Saisis ton mot de passe pour déchiffrer tes photos sur cet appareil.</p>
    <input type="text" name="username" value="${esc(user.username)}" autocomplete="username" hidden>
    <label class="lbl" for="p">Mot de passe</label>
    <input class="field" id="p" type="password" name="password" autocomplete="current-password" required>
    <button class="btn primary">Déverrouiller</button>
    <button type="button" class="btn" id="other">Changer de compte</button>`);
  $('#p').focus();
  $('#other').onclick = async () => { await post('/api/logout', {}).catch(() => {}); await Vault.forget(); boot(); };
  bindAuth(f, async f => { await Vault.unlock(user.username, f.password.value); boot(); });
}

function renderSetup() {
  const f = authCard(`<h1>Bienvenue</h1><p>Crée le compte administrateur. Le <b>code d’installation</b> a été affiché à la fin de
    l’installation (ou : <code>pellicule setup-code</code> sur le serveur).</p>
    <label class="lbl" for="c">Code d’installation</label>
    <input class="field" id="c" name="code" autocomplete="off" autocapitalize="characters" spellcheck="false" required>
    <label class="lbl" for="u">Nom d'utilisateur</label>
    <input class="field" id="u" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" required>
    ${newPasswordFields()}
    <button class="btn primary">Créer le compte</button>`);
  $('#c').focus();
  bindAuth(f, async f => {
    checkNewPassword(f.password.value, f.password2.value);
    const rec = await Vault.createAccount('/api/setup', f.username.value.trim(), f.password.value, { setupCode: f.code.value.trim() });
    showRecoveryKey(rec);
  }, 'Création des clés…');
}

async function renderRegister(token) {
  let inv;
  try { inv = await api('/api/invites/' + encodeURIComponent(token), { noRedirect: true }); } catch (e) {
    authCard(`<h1>Invitation</h1><p>${esc(e.message)}</p>`);
    return;
  }
  const f = authCard(`<h1>Créer ton compte</h1><p>Tu as été invité${inv.isAdmin ? ' comme administrateur' : ''} sur ce serveur Pellicule.</p>
    <label class="lbl" for="u">Nom d'utilisateur</label>
    <input class="field" id="u" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" required>
    ${newPasswordFields()}
    <button class="btn primary">Créer mon compte</button>`);
  $('#u').focus();
  bindAuth(f, async f => {
    checkNewPassword(f.password.value, f.password2.value);
    const rec = await Vault.createAccount('/api/register', f.username.value.trim(), f.password.value, { token });
    history.replaceState(null, '', '/');
    showRecoveryKey(rec);
  }, 'Création des clés…');
}

// La clé de secours n'est affichée qu'une fois : sans elle, un mot de passe oublié = photos perdues.
function showRecoveryKey(rec) {
  const groups = rec.split('-');
  const f = authCard(`<h1>Ta clé de secours</h1>
    <p>Note-la maintenant, sur papier ou dans un gestionnaire de mots de passe. <b>Elle ne sera plus jamais affichée.</b></p>
    <div class="recovery">${groups.map((g, i) => `<span class="rg"><small>${i + 1}.</small>${esc(g)}</span>`).join(' ')}</div>
    <div class="form-row"><button type="button" class="btn" id="copy-rec">${icon('copy')}Copier</button></div>
    <h2 class="rules">Les règles</h2>
    <ul class="rules">
      <li><b>Mot de passe oublié</b> : cette clé est le seul moyen de retrouver tes photos.</li>
      <li><b>Serveur perdu ou réinstallé</b> : tes photos restent sur le disque, mais il faudra cette clé pour y retrouver accès.
        Sans elle, elles sont perdues pour toujours.</li>
      <li>Personne ne peut la retrouver à ta place, pas même l’administrateur : elle n’est enregistrée nulle part.</li>
      <li>Garde-la ailleurs que sur ce serveur, comme un double de clé.</li>
    </ul>
    <button class="btn primary">Je l’ai notée</button>`);
  $('#copy-rec').onclick = () => navigator.clipboard?.writeText(rec).then(() => toast('Clé copiée'));
  f.onsubmit = e => { e.preventDefault(); verifyRecoveryKey(rec); };
}

// Recopier deux groupes tirés au hasard : une case « j’ai noté » se coche sans rien noter.
function verifyRecoveryKey(rec) {
  const groups = rec.split('-');
  const r = new Uint32Array(2);
  let a, b;
  do { crypto.getRandomValues(r); a = r[0] % groups.length; b = r[1] % groups.length; } while (a === b);
  [a, b] = [Math.min(a, b), Math.max(a, b)];
  const f = authCard(`<h1>Vérification</h1>
    <p>Depuis ce que tu as noté, recopie le groupe n° ${a + 1} puis le groupe n° ${b + 1} de ta clé de secours.</p>
    <input class="field" id="g" autocomplete="off" autocapitalize="characters" spellcheck="false" placeholder="Groupes n° ${a + 1} et n° ${b + 1}" required>
    <button class="btn primary">Valider</button>
    <a class="link" href="#" id="back">Revoir la clé</a>`);
  $('#g').focus();
  $('#back').onclick = e => { e.preventDefault(); showRecoveryKey(rec); };
  f.onsubmit = e => {
    e.preventDefault();
    const typed = f.querySelector('#g').value.toUpperCase().replace(/[^A-Z2-7]/g, '');
    if (typed === groups[a] + groups[b]) boot();
    else $('#err').textContent = 'Ce n’est pas ça. Vérifie ce que tu as noté, ou reviens à la clé.';
  };
}

function renderRecover() {
  const f = authCard(`<h1>Mot de passe oublié</h1><p>Saisis ta clé de secours pour choisir un nouveau mot de passe. Tes autres appareils seront déconnectés.</p>
    <label class="lbl" for="u">Nom d'utilisateur</label>
    <input class="field" id="u" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" required>
    <label class="lbl" for="r">Clé de secours</label>
    <input class="field" id="r" name="recovery" autocomplete="off" autocapitalize="characters" spellcheck="false" placeholder="ABCD-EFGH-…" required>
    ${newPasswordFields(false)}
    <button class="btn primary">Changer le mot de passe</button>
    <a class="link" href="#">Retour à la connexion</a>`);
  $('#u').focus();
  bindAuth(f, async f => {
    checkNewPassword(f.password.value, f.password2.value);
    await Vault.recover(f.username.value.trim(), f.recovery.value, f.password.value);
    history.replaceState(null, '', '/');
    boot();
  }, 'Vérification…');
}

/* =========================================================
   Page publique d'un album partagé (déchiffrée avec la clé du fragment #)
   ========================================================= */
async function bootShare(token) {
  let r, share, name;
  try {
    share = await Vault.shareFromURL(token);
    r = await api('/api/s/' + token, { noRedirect: true });
    name = await Vault.openName(r.name, share.sk);
    if (name === '(nom illisible)') throw new Error('Lien invalide : la clé ne correspond pas à cet album');
  } catch (e) {
    authCard(`<h1>Lien indisponible</h1><p>${esc(e.message)}</p>`);
    return;
  }
  share.allowDownload = r.allowDownload;
  const src = { share };
  await Vault.hydrate(r.items, src);
  const items = r.items.filter(a => !a.broken);
  const start = Math.min(...items.map(a => a.takenAt)), end = Math.max(...items.map(a => a.takenAt));
  document.title = `${name} · Pellicule`;
  app.className = '';
  app.innerHTML = `<div class="share-head">
      <div class="logo">${logoMark}<span>Pellicule</span></div>
      <div class="page-head" style="margin:0 0 10px"><div><h1>${esc(name)}</h1>
        <div class="sub">${[countLabel(items.length), items.length ? fmtRange(start, end) : '', r.owner ? `partagé par ${r.owner}` : ''].filter(Boolean).map(esc).join(' · ')}</div></div>
        <div class="actions">${r.allowDownload && items.length ? `<button class="btn primary" id="zip">${icon('download')}Tout télécharger</button>` : ''}</div></div>
    </div><div class="share-wrap" id="gal"></div>`;
  const g = new Gallery($('#gal'), {
    public: true,
    src,
    groupBy: a => dKey(a.takenAt),
    groupLabel: a => dayLabel(a.takenAt),
    load: async () => ({ items, next: '' }),
    empty: emptyState('album', 'Album vide', 'Rien à voir ici pour l’instant.'),
    open: i => openViewer(g, i, { share }),
  });
  $('#zip')?.addEventListener('click', () => downloadItems(items, name).catch(e => toast(e.message)));
  g.load();
}

/* ---------- démarrage ---------- */
async function boot() {
  const m = location.pathname.match(/^\/s\/([0-9a-f]+)$/);
  if (m) return bootShare(m[1]);
  let me;
  try {
    me = await api('/api/me', { noRedirect: true });
  } catch (e) {
    authCard(`<h1>Serveur injoignable</h1><p>${esc(e.message)}</p><button type="button" class="btn primary" id="retry">Réessayer</button>`);
    $('#retry').onclick = () => location.reload();
    return;
  }
  state.version = me.version;
  const inv = location.hash.match(/^#invite=([0-9a-f]+)$/);
  if (inv && !me.user) return renderRegister(inv[1]);
  if (!me.user) {
    // dans l'app : le serveur ne reconnaît plus ce téléphone (révoqué) → il efface ses clés et se réappaire
    // (l'app le vérifie elle-même par sa propre connexion avant d'effacer quoi que ce soit)
    if (NATIVE?.checkRevoked) {
      authCard('<h1>Vérification…</h1><p>Le serveur ne reconnaît pas ce téléphone.</p>');
      return NATIVE.checkRevoked();
    }
    await Vault.forget(); // session expirée ou déconnectée ailleurs : la clé maître ne reste pas dans ce navigateur
    if (me.needsSetup) return renderSetup();
    if (location.hash === '#recover') return renderRecover();
    return renderLogin();
  }
  if (!await Vault.restore(me.user.username)) return renderUnlock(me.user);
  state.user = me.user;
  if (!/^#\//.test(location.hash)) history.replaceState(null, '', '/#/');
  renderShell();
  route();
  refreshUsage();
  rewrapImported();
}

// Fichiers importés par FolderSync : leur clé est scellée pour la clé publique du compte. Dès qu'un
// appareil déverrouillé passe, il les rechiffre avec la clé maître (le serveur ne peut pas le faire).
async function rewrapImported() {
  try {
    let next = '';
    do {
      const r = await api('/api/timeline?sealed=1&limit=500' + (next ? '&cursor=' + next : ''));
      await Vault.hydrate(r.items, { owner: true });
      next = r.next;
    } while (next);
    await Vault.flushRewrap();
  } catch { /* on réessaiera au prochain démarrage */ }
}

// pages hors connexion (invitation, secours) : navigation par le fragment
addEventListener('hashchange', () => { if (!state.user) boot(); });

// Dans l'app Android, l'interface vient de l'APK et les vidéos sont déchiffrées par l'app : pas de service
// worker (la CSP de l'app l'interdit), et ceux d'anciennes versions sont retirés.
if (window.PelliculeAndroid) {
  navigator.serviceWorker?.getRegistrations?.().then(rs => rs.forEach(r => r.unregister())).catch(() => {});
} else if ('serviceWorker' in navigator && window.isSecureContext) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}
boot();
