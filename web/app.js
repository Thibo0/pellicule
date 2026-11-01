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
  }

  thumb(a) { return this.o.thumbUrl ? this.o.thumbUrl(a) : `/api/assets/${a.id}/thumb`; }

  load() {
    if (this.loading) return this.loading;
    if (this.done) return Promise.resolve();
    this.loading = (async () => {
      this.moreEl.innerHTML = '<div class="spinner"></div>';
      try {
        const r = await this.o.load(this.next);
        this.next = r.next;
        this.done = !r.next;
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
    const pending = a.thumb === 0 ? '<div class="pending"><div class="spinner"></div></div>' : '';
    return `<div class="tile${this.sel.has(a.id) ? ' sel' : ''}" data-i="${i}" tabindex="0" role="button" aria-label="${esc(a.filename)}">${pending}
      <img src="${this.thumb(a)}" alt="" loading="lazy" decoding="async" draggable="false">
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
  }

  bind() {
    const r = this.root;
    r.addEventListener('load', e => {
      if (e.target.tagName !== 'IMG') return;
      e.target.classList.add('ok');
      e.target.parentNode.querySelector('.pending')?.remove();
    }, true);
    // miniature pas encore prête : on réessaie tant que le serveur la prépare
    r.addEventListener('error', e => {
      const img = e.target;
      if (img.tagName !== 'IMG') return;
      const n = +(img.dataset.retry || 0);
      if (n < 60) {
        img.dataset.retry = n + 1;
        setTimeout(() => { if (img.isConnected) img.src = img.src.split('?')[0] + '?r=' + n; }, 3000);
      }
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
    if (el) el.outerHTML = this.tile(a, i);
  }
  destroy() { this.io.disconnect(); }
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

function downloadItems(items) {
  // dans l'app Android : chaque original va dans la galerie du téléphone (pas de ZIP)
  if (NATIVE?.download) {
    return NATIVE.download(JSON.stringify(items.map(a => ({ url: `${location.origin}/api/assets/${a.id}/original?download=1`, name: a.filename }))));
  }
  if (items.length === 1) return downloadUrl(`/api/assets/${items[0].id}/original?download=1`);
  downloadUrl('/api/download?ids=' + items.map(a => a.id).join(','));
}
function downloadUrl(url) {
  const a = document.createElement('a');
  a.href = url;
  a.download = '';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

async function albumPicker(items, exceptId) {
  const ids = items.map(a => a.id);
  const albums = (await api('/api/albums')).filter(al => al.id !== exceptId);
  const choice = await modal(`<h2>Ajouter à un album</h2><p>${countLabel(ids.length)}</p><div class="list">
      <button data-new><span class="th">${icon('plus')}</span><div><b>Nouvel album</b></div></button>
      ${albums.map(al => `<button data-id="${al.id}"><span class="th">${al.coverId ? `<img src="/api/assets/${al.coverId}/thumb" alt="">` : icon('album')}</span>
        <div><b>${esc(al.name)}</b><small>${countLabel(al.count)}</small></div></button>`).join('')}
    </div><div class="foot"><button class="btn" data-close>Annuler</button></div>`,
  (m, close) => {
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
    const r = await post('/api/albums', { name, ids });
    toast(`Ajouté à « ${name} »`, { action: { label: 'Voir', run: () => { location.hash = '#/albums/' + r.id; } } });
  } else {
    await post(`/api/albums/${choice}/add`, { ids });
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
  const base = share ? `/api/s/${share.token}/assets/` : '/api/assets/';
  const preview = a => `${base}${a.id}/preview`;
  const original = a => `${base}${a.id}/original`;
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
      ${a.kind === 'video'
        ? `<video src="${original(a)}" poster="${preview(a)}" controls playsinline autoplay preload="metadata"></video>`
        : `<div class="loading"><div class="spinner"></div></div><img src="${preview(a)}" alt="${esc(a.filename)}" draggable="false">`}
    </div>${infoOpen ? `<aside class="info"><h3><span class="grow">Informations</span><button class="icon-btn" data-a="info">${icon('x')}</button></h3><div id="info-body"></div></aside>` : ''}`;
    el.classList.toggle('chrome-off', !chrome);

    const img = el.querySelector('.stage img');
    if (img) {
      const done = () => el.querySelector('.loading')?.remove();
      if (img.complete) done();
      img.onload = done;
      img.onerror = () => {
        done();
        if (a.thumb !== 1) setTimeout(() => { if (img.isConnected) img.src = preview(a) + '?r=' + Date.now(); }, 3000);
      };
      setupZoom(img);
    }
    if (infoOpen) fillInfo(a);
    // précharge les voisines
    for (const k of [i + 1, i - 1]) {
      const n = g.items[k];
      if (n && n.kind === 'image') new Image().src = preview(n);
    }
  }

  async function fillInfo(a) {
    const box = el.querySelector('#info-body');
    const rows = [
      [icon('calendar'), cap(DF.full.format(a.takenAt)), DF.time.format(a.takenAt)],
      [icon(a.kind === 'video' ? 'video' : 'file'), a.filename,
        [a.width ? `${a.width} × ${a.height}` : '', fmtSize(a.size), a.kind === 'video' && a.duration ? fmtDuration(a.duration) : ''].filter(Boolean).join(' · ')],
    ];
    box.innerHTML = rows.map(([ic, t, s]) => `<div class="row">${ic}<div>${esc(t)}<small>${esc(s)}</small></div></div>`).join('');
    if (share) return;
    try {
      const d = await api(`/api/assets/${a.id}`);
      if (!box.isConnected) return;
      box.insertAdjacentHTML('beforeend', `<div class="row">${icon('folder')}<div><small>${esc(d.path)}</small></div></div>`);
      if (d.albums.length) {
        box.insertAdjacentHTML('beforeend', `<div class="row">${icon('album')}<div>${d.albums.map(al => `<a class="pill" href="#/albums/${al.id}" data-a="goto">${esc(al.name)}</a>`).join('')}</div></div>`);
      }
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
      case 'dl': return share ? downloadUrl(original(a) + '?download=1') : downloadItems([a]);
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
  send(f) {
    this.active++;
    this.current = f.name;
    const fd = new FormData();
    fd.append('mtime', String(f.lastModified || 0));
    fd.append('file', f, f.name);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/upload');
    xhr.setRequestHeader('X-Pellicule', '1');
    let sent = 0;
    xhr.upload.onprogress = e => { this.bytesDone += e.loaded - sent; sent = e.loaded; this.render(); };
    const end = () => {
      this.active--;
      let res = null;
      try { res = JSON.parse(xhr.responseText); } catch { /* réponse vide */ }
      if (xhr.status === 0 && !f.retried) {
        // coupure réseau : on retente une fois
        f.retried = true;
        this.bytesDone -= sent;
        this.q.push(f);
      } else {
        this.bytesDone += f.size - sent;
        this.done++;
        if (xhr.status === 200 && res) res.status === 'created' ? this.created++ : this.dup++;
        else if (xhr.status === 401) { this.errors.push('Session expirée : reconnecte-toi.'); this.q = []; }
        else this.errors.push(`${f.name} : ${res?.error || 'échec de l’envoi'}`);
      }
      this.render();
      this.pump();
    };
    xhr.onload = end;
    xhr.onerror = end;
    xhr.send(fd);
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
  const key = f => `${f.name}|${f.size}`;
  const existing = new Set();
  try {
    for (let k = 0; k < media.length; k += 2000) {
      const r = await post('/api/assets/check', { keys: media.slice(k, k + 2000).map(key) });
      r.existing.forEach(x => existing.add(x));
    }
  } catch (e) {
    if (e.message === 'non connecté') return;
  }
  const todo = media.filter(f => !existing.has(key(f)));
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

let usageTimer = null;
async function refreshUsage() {
  clearTimeout(usageTimer);
  try {
    const s = await api('/api/stats');
    const el = $('#usage');
    if (!el) return;
    el.innerHTML = `<b>${s.photos.toLocaleString('fr-FR')}</b> photos · <b>${s.videos.toLocaleString('fr-FR')}</b> vidéos<br>${fmtSize(s.bytes)} utilisés${s.diskFree >= 0 ? ` · ${fmtSize(s.diskFree)} libres` : ''}${s.pending ? `<br>Préparation de ${s.pending} miniature${s.pending > 1 ? 's' : ''}…` : ''}`;
    if (s.pending) usageTimer = setTimeout(refreshUsage, 8000);
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
    pickAlbum = (await api('/api/albums/' + pick)).album;
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
  if (p.q) qs.set('q', p.q);
  if (p.year) qs.set('year', p.year);

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
    load: cursor => api('/api/timeline?' + qs + (cursor ? '&cursor=' + cursor : '')),
    empty: empties[mode],
    open: i => openViewer(g, i),
    onCancel: () => { location.hash = '#/albums/' + pick; },
  });
  cleanup = () => g.destroy();

  let actions;
  if (pick) {
    actions = [{ label: 'Ajouter', run: async items => {
      await post(`/api/albums/${pick}/add`, { ids: items.map(a => a.id) });
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
  const albums = await api('/api/albums');
  if (stale()) return;
  $('#sub').textContent = plural(albums.length, 'album', 'albums');
  if (!albums.length) {
    $('#albums').outerHTML = emptyState('album', 'Aucun album', 'Regroupe tes photos par voyage, fête ou personne.', `<button class="btn primary" id="empty-new">${icon('plus')}Créer un album</button>`);
    $('#empty-new').onclick = newAlbum;
    return;
  }
  $('#albums').innerHTML = `<a class="album-card new" href="#" id="new-card"><div class="cover">${icon('plus')}</div><div class="name">Nouvel album</div><div class="meta">&nbsp;</div></a>`
    + albums.map(al => `<a class="album-card" href="#/albums/${al.id}">
      <div class="cover">${al.coverId ? `<img src="/api/assets/${al.coverId}/thumb" alt="" loading="lazy">` : icon('album')}
        ${al.shared ? `<span class="shared" title="Partagé par lien">${icon('link')}</span>` : ''}</div>
      <div class="name">${esc(al.name)}</div>
      <div class="meta">${[countLabel(al.count), fmtRange(al.startAt, al.endAt)].filter(Boolean).join(' · ')}</div></a>`).join('');
  $('#new-card').onclick = e => { e.preventDefault(); newAlbum(); };
}

async function newAlbum() {
  const name = await promptModal('Nouvel album', { label: 'Nom', placeholder: 'Vacances d’été', ok: 'Créer' });
  if (!name) return;
  const r = await post('/api/albums', { name });
  location.hash = `#/?pick=${r.id}`;
}

/* ---------- vue : un album ---------- */
async function viewAlbum(id, stale) {
  setActive('albums');
  const r = await api('/api/albums/' + id);
  if (stale()) return;
  const al = r.album;
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
    await api('/api/albums/' + id, { method: 'PATCH', body: { name } });
    al.name = name;
    toast('Album renommé');
  };
  $('#share').onclick = () => shareModal(id);
  $('#zip')?.addEventListener('click', () => (NATIVE ? downloadItems(g.items) : downloadUrl(`/api/albums/${id}/zip`)));
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

async function shareModal(albumId) {
  await modal(`<h2>Partager l’album</h2>
    <p>Toute personne qui a le lien peut voir l’album, sans compte.</p>
    <div id="links"></div>
    <label class="switch"><input type="checkbox" id="dl" checked> Autoriser le téléchargement</label>
    <label class="lbl" for="exp">Expiration</label>
    <select class="field" id="exp"><option value="0">Jamais</option><option value="1">1 jour</option><option value="7">7 jours</option><option value="30">30 jours</option></select>
    <div class="foot"><button class="btn" data-c>Fermer</button><button class="btn primary" data-create>${icon('link')}Créer un lien</button></div>`,
  (m, close) => {
    const list = m.querySelector('#links');
    const load = async () => {
      const shares = await api(`/api/albums/${albumId}/shares`);
      list.innerHTML = shares.map(s => {
        const url = `${location.origin}/s/${s.token}`;
        const meta = [s.allowDownload ? 'téléchargement autorisé' : 'lecture seule', s.expiresAt ? `expire le ${DF.short.format(s.expiresAt)}` : 'sans expiration'].join(' · ');
        return `<div class="share-link"><div style="flex:1;min-width:0"><input class="field" readonly value="${esc(url)}"><small>${esc(meta)}</small></div>
          <button class="icon-btn" data-copy="${esc(url)}" title="Copier">${icon('copy')}</button>
          <button class="icon-btn" data-del="${esc(s.token)}" title="Supprimer le lien">${icon('trash')}</button></div>`;
      }).join('');
    };
    m.onclick = async e => {
      const b = e.target.closest('button');
      if (!b) return;
      if ('c' in b.dataset) return close();
      if ('create' in b.dataset) {
        await post(`/api/albums/${albumId}/shares`, { allowDownload: m.querySelector('#dl').checked, days: +m.querySelector('#exp').value });
        await load();
        const first = list.querySelector('[data-copy]');
        if (first) { await copyText(first.dataset.copy, list.querySelector('input')); toast('Lien créé et copié'); }
      } else if (b.dataset.copy) {
        await copyText(b.dataset.copy, b.parentNode.querySelector('input'));
        toast('Lien copié');
      } else if (b.dataset.del) {
        await api('/api/shares/' + b.dataset.del, { method: 'DELETE' });
        await load();
        toast('Lien supprimé : il ne fonctionne plus');
      }
    };
    load();
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
  const s = await api('/api/stats');
  if (stale()) return;
  const u = state.user;
  view().innerHTML = `<div class="settings">
    <div class="page-head"><div><h1>Réglages</h1></div></div>
    ${NATIVE ? `<div class="card"><h3>Cette app</h3><p>Dossiers sauvegardés, vidéos, batterie et état de la sauvegarde.</p>
      <button class="btn primary" id="nat-settings">${icon('settings')}Réglages de la sauvegarde</button></div>` : ''}
    <div class="card"><h3>Mon compte</h3>
      <p>Connecté en tant que <b>${esc(u.username)}</b>${u.isAdmin ? '<span class="tag">admin</span>' : ''}</p>
      <form id="pw" class="form-row">
        <div><label class="lbl">Mot de passe actuel</label><input class="field" type="password" name="current" autocomplete="current-password" required></div>
        <div><label class="lbl">Nouveau mot de passe</label><input class="field" type="password" name="new" minlength="8" autocomplete="new-password" required></div>
        <button class="btn">Changer</button>
      </form>
      <div style="margin-top:18px"><button class="btn danger" id="logout">${icon('logout')}Se déconnecter</button></div>
    </div>
    ${u.isAdmin ? `<div class="card"><h3>Comptes</h3><p>Chaque compte a sa propre photothèque, privée.</p>
      <table class="users" id="users"></table>
      <form id="new-user" class="form-row" style="margin-top:6px">
        <div><label class="lbl">Nom d'utilisateur</label><input class="field" name="username" autocapitalize="none" required></div>
        <div><label class="lbl">Mot de passe (8+)</label><input class="field" name="password" type="password" minlength="8" autocomplete="new-password" required></div>
        <button class="btn primary">${icon('plus')}Ajouter</button>
      </form></div>` : ''}
    <div class="card"><h3>Serveur</h3>
      <p>${[plural(s.photos, 'photo', 'photos'), plural(s.videos, 'vidéo', 'vidéos'), `${fmtSize(s.bytes)} utilisés`, s.diskFree >= 0 ? `${fmtSize(s.diskFree)} libres` : ''].filter(Boolean).join(' · ')}</p>
      <p>Miniatures HEIC / RAW (libvips) : <b>${s.tools.vips ? 'oui' : 'non installé'}</b> · Vidéos (ffmpeg) : <b>${s.tools.ffmpeg ? 'oui' : 'non installé'}</b>${s.pending ? ` · ${s.pending} miniature(s) en préparation` : ''}</p>
      <p>Pellicule ${esc(state.version)}</p>
    </div></div>`;

  $('#pw').onsubmit = async e => {
    e.preventDefault();
    const f = e.target;
    try {
      await post('/api/me/password', { current: f.current.value, new: f.new.value });
      f.reset();
      toast('Mot de passe modifié');
    } catch (err) { toast(err.message); }
  };
  $('#nat-settings')?.addEventListener('click', () => NATIVE.openSettings());
  $('#logout').onclick = async () => {
    if (NATIVE) {
      if (await confirmModal('Se déconnecter ?', 'La sauvegarde automatique de ce téléphone s’arrêtera jusqu’à la prochaine connexion.', 'Se déconnecter', true)) {
        await post('/api/logout', {}).catch(() => {});
        NATIVE.logout();
      }
      return;
    }
    await post('/api/logout', {});
    state.user = null;
    boot();
  };

  if (!u.isAdmin) return;
  const loadUsers = async () => {
    const users = await api('/api/users');
    $('#users').innerHTML = users.map(x => `<tr><td><b>${esc(x.username)}</b>${x.isAdmin ? '<span class="tag">admin</span>' : ''}</td>
      <td>${countLabel(x.count)} · ${fmtSize(x.bytes)}</td>
      <td><button class="btn" data-pw="${x.id}" data-name="${esc(x.username)}">Mot de passe</button>
      ${x.id === u.id ? '' : `<button class="icon-btn" data-del="${x.id}" data-name="${esc(x.username)}" title="Supprimer">${icon('trash')}</button>`}</td></tr>`).join('');
  };
  $('#users').onclick = async e => {
    const b = e.target.closest('button');
    if (!b) return;
    try {
      if (b.dataset.pw) {
        const pw = await promptModal(`Nouveau mot de passe pour ${b.dataset.name}`, { type: 'password', ok: 'Enregistrer', text: '8 caractères minimum. Ses sessions ouvertes seront déconnectées.' });
        if (!pw) return;
        await post(`/api/users/${b.dataset.pw}/password`, { password: pw });
        toast('Mot de passe modifié');
      } else if (b.dataset.del) {
        if (!await confirmModal(`Supprimer ${b.dataset.name} ?`, 'Le compte, ses albums et ses liens de partage sont supprimés. Ses fichiers originaux restent sur le disque.', 'Supprimer', true)) return;
        await api('/api/users/' + b.dataset.del, { method: 'DELETE' });
        loadUsers();
      }
    } catch (err) { toast(err.message); }
  };
  $('#new-user').onsubmit = async e => {
    e.preventDefault();
    const f = e.target;
    try {
      await post('/api/users', { username: f.username.value.trim(), password: f.password.value });
      f.reset();
      toast('Compte créé');
      loadUsers();
    } catch (err) { toast(err.message); }
  };
  loadUsers();
}

/* =========================================================
   Connexion / première configuration
   ========================================================= */
function renderAuth(needsSetup) {
  app.className = 'auth';
  app.innerHTML = `<form class="auth-card" id="auth">
    <div class="logo">${logoMark}<span>Pellicule</span></div>
    <h1>${needsSetup ? 'Bienvenue' : 'Connexion'}</h1>
    <p>${needsSetup ? 'Crée le compte administrateur pour commencer.' : 'Tes photos, chez toi.'}</p>
    <label class="lbl" for="u">Nom d'utilisateur</label>
    <input class="field" id="u" name="username" autocomplete="username" autocapitalize="none" spellcheck="false" required>
    <label class="lbl" for="p">Mot de passe${needsSetup ? ' (8 caractères minimum)' : ''}</label>
    <input class="field" id="p" type="password" name="password" autocomplete="${needsSetup ? 'new-password' : 'current-password'}" ${needsSetup ? 'minlength="8"' : ''} required>
    <button class="btn primary">${needsSetup ? 'Créer le compte' : 'Se connecter'}</button>
    <div class="err" id="err"></div>
  </form>`;
  $('#u').focus();
  $('#auth').onsubmit = async e => {
    e.preventDefault();
    const f = e.target;
    const btn = f.querySelector('button');
    btn.disabled = true;
    $('#err').textContent = '';
    try {
      await api(needsSetup ? '/api/setup' : '/api/login', { method: 'POST', body: { username: f.username.value.trim(), password: f.password.value }, noRedirect: true });
      boot();
    } catch (err) {
      $('#err').textContent = err.message;
      btn.disabled = false;
    }
  };
}

/* =========================================================
   Page publique d'un album partagé
   ========================================================= */
async function bootShare(token) {
  let r;
  try {
    r = await api('/api/s/' + token, { noRedirect: true });
  } catch (e) {
    app.className = 'auth';
    app.innerHTML = `<div class="auth-card"><div class="logo">${logoMark}<span>Pellicule</span></div><h1>Lien indisponible</h1><p>${esc(e.message)}</p></div>`;
    return;
  }
  const al = r.album;
  document.title = `${al.name} · Pellicule`;
  app.className = '';
  app.innerHTML = `<div class="share-head">
      <div class="logo">${logoMark}<span>Pellicule</span></div>
      <div class="page-head" style="margin:0 0 10px"><div><h1>${esc(al.name)}</h1>
        <div class="sub">${[countLabel(al.count), fmtRange(al.startAt, al.endAt), r.owner ? `partagé par ${r.owner}` : ''].filter(Boolean).map(esc).join(' · ')}</div></div>
        <div class="actions">${r.allowDownload && al.count ? `<a class="btn primary" href="/api/s/${token}/zip" download>${icon('download')}Tout télécharger</a>` : ''}</div></div>
    </div><div class="share-wrap" id="gal"></div>`;
  const g = new Gallery($('#gal'), {
    public: true,
    groupBy: a => dKey(a.takenAt),
    groupLabel: a => dayLabel(a.takenAt),
    thumbUrl: a => `/api/s/${token}/assets/${a.id}/thumb`,
    load: async () => ({ items: r.items, next: '' }),
    empty: emptyState('album', 'Album vide', 'Rien à voir ici pour l’instant.'),
    open: i => openViewer(g, i, { share: { token, allowDownload: r.allowDownload } }),
  });
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
    app.className = 'auth';
    app.innerHTML = `<div class="auth-card"><div class="logo">${logoMark}<span>Pellicule</span></div><h1>Serveur injoignable</h1><p>${esc(e.message)}</p><button class="btn primary" onclick="location.reload()">Réessayer</button></div>`;
    return;
  }
  state.version = me.version;
  if (!me.user) return renderAuth(me.needsSetup);
  state.user = me.user;
  renderShell();
  route();
  refreshUsage();
}

if ('serviceWorker' in navigator && window.isSecureContext) {
  navigator.serviceWorker.register('/sw.js').catch(() => {});
}
boot();
