package main

// Photos et vidéos. Tout arrive déjà chiffré par le client : le serveur range des blobs opaques.
//
// Envoi en trois temps, reprenable :
//   1. POST /api/assets              clé du fichier (chiffrée), métadonnées (chiffrées), empreintes HMAC
//   2. PUT  /api/assets/{id}/original?offset=N   l'original chiffré, par morceaux, dans l'ordre
//      PUT  /api/assets/{id}/thumb | /preview    miniature et aperçu chiffrés
//   3. POST /api/assets/{id}/complete
// Sur le disque : <library>/u<id>/<id/1000>/<id>.o (original), .m (aperçu), .s (miniature).

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxOriginal  = 64 << 30 // 64 Gio
	maxPart      = 32 << 20 // un morceau d'envoi
	maxThumb     = 2 << 20
	maxPreview   = 16 << 20
	maxMeta      = 64 << 10
	staleUploads = 24 * time.Hour
)

func userDir(id int64) string { return fmt.Sprintf("u%d", id) }

// blobPath : variant "o" (original), "m" (aperçu), "s" (miniature), "o.part" (envoi en cours).
func (a *App) blobPath(owner, id int64, variant string) string {
	return filepath.Join(a.libDir, userDir(owner), strconv.FormatInt(id/1000, 10), fmt.Sprintf("%d.%s", id, variant))
}

func validTag(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// --- 1. création ---

func (a *App) handleCreateAsset(w http.ResponseWriter, r *http.Request, u *User) {
	var b struct {
		Kind      string `json:"kind"`
		TakenAt   int64  `json:"takenAt"`
		FK        []byte `json:"fk"`
		Meta      []byte `json:"meta"`
		DedupTag  string `json:"dedupTag"`
		DeviceTag string `json:"deviceTag"`
		Size      int64  `json:"size"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if (b.Kind != "image" && b.Kind != "video") || len(b.FK) != boxSize || len(b.Meta) < NonceSize+TagSize ||
		len(b.Meta) > maxMeta || !validTag(b.DedupTag) || (b.DeviceTag != "" && !validTag(b.DeviceTag)) ||
		b.Size < StreamHdrSize+TagSize || b.Size > maxOriginal {
		httpError(w, http.StatusBadRequest, "requête invalide")
		return
	}
	if free := diskFree(a.libDir); free > 0 && free < b.Size+512<<20 {
		httpError(w, http.StatusInsufficientStorage, "disque plein")
		return
	}

	var id, uploaded int64
	var complete bool
	var trashed sql.NullInt64
	err := a.db.QueryRow(`SELECT id, uploaded, complete, trashed_at FROM assets WHERE owner_id=? AND dedup_tag=?`, u.ID, b.DedupTag).
		Scan(&id, &uploaded, &complete, &trashed)
	switch {
	case err == nil && complete:
		if trashed.Valid { // renvoyer une photo mise à la corbeille la restaure
			a.db.Exec(`UPDATE assets SET trashed_at=NULL WHERE id=?`, id)
			a.writeAssetFiles(id)
		}
		writeJSON(w, map[string]any{"status": "duplicate", "id": id})
		return
	case err == nil: // envoi interrompu : on reprend où il en était
		writeJSON(w, map[string]any{"status": "resume", "id": id, "uploaded": uploaded})
		return
	case !errors.Is(err, sql.ErrNoRows):
		serverError(w, err)
		return
	}

	if b.TakenAt <= 0 {
		b.TakenAt = time.Now().UnixMilli()
	}
	res, err := a.db.Exec(`INSERT INTO assets(owner_id, kind, taken_at, wrapped_fk, meta, dedup_tag, device_tag, orig_size, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, u.ID, b.Kind, b.TakenAt, b.FK, b.Meta, b.DedupTag, b.DeviceTag, b.Size, time.Now().UnixMilli())
	if err != nil {
		serverError(w, err)
		return
	}
	id, _ = res.LastInsertId()
	a.writeAssetFiles(id)
	writeJSON(w, map[string]any{"status": "created", "id": id, "uploaded": 0})
}

// --- 2. contenu ---

type assetState struct {
	owner, size, uploaded int64
	complete              bool
}

func (a *App) ownedState(w http.ResponseWriter, r *http.Request, u *User) (int64, assetState, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return 0, assetState{}, false
	}
	var s assetState
	err := a.db.QueryRow(`SELECT owner_id, orig_size, uploaded, complete FROM assets WHERE id=? AND owner_id=?`, id, u.ID).
		Scan(&s.owner, &s.size, &s.uploaded, &s.complete)
	if err != nil {
		httpError(w, http.StatusNotFound, "introuvable")
		return 0, s, false
	}
	return id, s, true
}

// uploadLocks : un seul envoi à la fois par photo. Sans ça, une requête restée bloquée (téléphone sorti du
// Wi-Fi) pouvait, en échouant après coup, tronquer le fichier qu'une requête plus récente venait de compléter.
type uploadLocks struct {
	mu   sync.Mutex
	busy map[int64]bool
}

func (l *uploadLocks) try(id int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.busy[id] {
		return false
	}
	if l.busy == nil {
		l.busy = map[int64]bool{}
	}
	l.busy[id] = true
	return true
}

func (l *uploadLocks) release(id int64) {
	l.mu.Lock()
	delete(l.busy, id)
	l.mu.Unlock()
}

// uploadIdle : une requête d'envoi qui ne reçoit plus rien pendant ce délai est abandonnée (et libère la photo).
const uploadIdle = 60 * time.Second

// idleReader repousse l'échéance de lecture à chaque lecture : la connexion peut être lente, pas muette.
type idleReader struct {
	r  io.Reader
	rc *http.ResponseController
}

func (i idleReader) Read(p []byte) (int, error) {
	i.rc.SetReadDeadline(time.Now().Add(uploadIdle)) // ErrNotSupported (tests) : on garde l'échéance du serveur
	return i.r.Read(p)
}

func (a *App) handlePutOriginal(w http.ResponseWriter, r *http.Request, u *User) {
	id, _, ok := a.ownedState(w, r, u)
	if !ok {
		return
	}
	if !a.uploads.try(id) {
		httpError(w, http.StatusConflict, "envoi déjà en cours")
		return
	}
	defer a.uploads.release(id)
	_, s, ok := a.ownedState(w, r, u) // relu sous le verrou
	if !ok {
		return
	}
	if s.complete {
		httpError(w, http.StatusConflict, "déjà envoyé")
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset != s.uploaded {
		w.Header().Set("X-Uploaded", strconv.FormatInt(s.uploaded, 10))
		httpError(w, http.StatusConflict, "position incorrecte")
		return
	}
	limit := min(int64(maxPart), s.size-s.uploaded)
	p := a.blobPath(u.ID, id, "o.part")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		serverError(w, err)
		return
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		serverError(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		serverError(w, err)
		return
	}
	if st.Size() < offset {
		// le fichier a perdu des octets que la base croyait reçus : on ne comble jamais le trou avec des zéros,
		// on fait reprendre le client là où le fichier s'arrête vraiment (en début de bloc chiffré).
		back := alignUpload(st.Size())
		f.Truncate(back)
		a.db.Exec(`UPDATE assets SET uploaded=? WHERE id=? AND uploaded=?`, back, id, s.uploaded)
		w.Header().Set("X-Uploaded", strconv.FormatInt(back, 10))
		httpError(w, http.StatusConflict, "position incorrecte")
		return
	}
	// au-delà de la position validée : reste d'un morceau à moitié écrit, simplement réécrit
	if err := f.Truncate(offset); err != nil {
		serverError(w, err)
		return
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		serverError(w, err)
		return
	}
	body := idleReader{io.LimitReader(r.Body, limit+1), http.NewResponseController(w)}
	n, err := io.Copy(f, body)
	if n > limit {
		f.Truncate(offset)
		httpError(w, http.StatusRequestEntityTooLarge, "morceau trop grand")
		return
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		f.Truncate(offset)
		httpError(w, http.StatusBadRequest, "envoi interrompu")
		return
	}
	res, err := a.db.Exec(`UPDATE assets SET uploaded=? WHERE id=? AND uploaded=?`, offset+n, id, offset)
	if c, _ := res.RowsAffected(); err != nil || c != 1 {
		if err == nil {
			err = fmt.Errorf("asset %d : position modifiée pendant l'envoi", id)
		}
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"uploaded": offset + n})
}

// alignUpload ramène une position au dernier début de bloc chiffré complet.
func alignUpload(n int64) int64 {
	if n < StreamHdrSize {
		return 0
	}
	full := int64(ChunkSize + TagSize)
	return StreamHdrSize + (n-StreamHdrSize)/full*full
}

func (a *App) putSmallBlob(variant, column string, max int64) userHandler {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		id, _, ok := a.ownedState(w, r, u)
		if !ok {
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, max+1))
		if err != nil || int64(len(data)) > max || len(data) < NonceSize+TagSize {
			httpError(w, http.StatusBadRequest, "fichier invalide")
			return
		}
		if err := writeFileAtomic(a.blobPath(u.ID, id, variant), data); err != nil {
			serverError(w, err)
			return
		}
		a.db.Exec(`UPDATE assets SET `+column+`=1 WHERE id=?`, id)
		writeJSON(w, map[string]any{"ok": true})
	}
}

func writeFileAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// --- 3. fin d'envoi ---

func (a *App) handleCompleteAsset(w http.ResponseWriter, r *http.Request, u *User) {
	id, s, ok := a.ownedState(w, r, u)
	if !ok {
		return
	}
	if s.complete {
		writeJSON(w, map[string]any{"ok": true})
		return
	}
	if s.uploaded != s.size {
		httpError(w, http.StatusConflict, fmt.Sprintf("envoi incomplet (%d / %d octets)", s.uploaded, s.size))
		return
	}
	if !a.uploads.try(id) {
		httpError(w, http.StatusConflict, "envoi déjà en cours")
		return
	}
	defer a.uploads.release(id)
	part, final := a.blobPath(u.ID, id, "o.part"), a.blobPath(u.ID, id, "o")
	if st, err := os.Stat(part); err != nil || st.Size() != s.size {
		a.db.Exec(`UPDATE assets SET uploaded=0 WHERE id=? AND complete=0`, id)
		os.Remove(part)
		httpError(w, http.StatusConflict, "fichier reçu incomplet : envoi à refaire")
		return
	}
	if !hasStreamMagic(part) {
		httpError(w, http.StatusBadRequest, "fichier chiffré invalide")
		return
	}
	if err := os.Rename(part, final); err != nil {
		serverError(w, err)
		return
	}
	a.db.Exec(`UPDATE assets SET complete=1 WHERE id=?`, id)
	writeJSON(w, map[string]any{"ok": true})
}

func hasStreamMagic(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 4)
	_, err = io.ReadFull(f, b)
	return err == nil && string(b) == string(streamMagic)
}

// handleCheck : parmi ces empreintes de fichiers du téléphone, lesquelles sont déjà sauvegardées ?
func (a *App) handleCheck(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		Tags []string `json:"tags"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	known := map[string]bool{}
	rows, err := a.db.Query(`SELECT device_tag FROM assets WHERE owner_id=? AND complete=1 AND device_tag<>''`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	for rows.Next() {
		var k string
		rows.Scan(&k)
		known[k] = true
	}
	rows.Close()
	out := []string{}
	for _, k := range body.Tags {
		if known[strings.ToLower(k)] {
			out = append(out, k)
		}
	}
	writeJSON(w, map[string]any{"existing": out})
}

// handleRewrapKeys : l'import a scellé la clé du fichier pour la clé publique du compte ; le client,
// qui seul peut l'ouvrir, la rechiffre avec sa clé maître. Il envoie aussi le dedupTag (calculé depuis
// l'empreinte scellée avec la clé) : une photo importée déjà sauvegardée par l'app est alors supprimée.
func (a *App) handleRewrapKeys(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		Keys []struct {
			ID       int64  `json:"id"`
			FK       []byte `json:"fk"`
			DedupTag string `json:"dedupTag"`
		} `json:"keys"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	n, dupes := 0, 0
	for _, k := range body.Keys {
		if len(k.FK) != boxSize || (k.DedupTag != "" && !validTag(k.DedupTag)) {
			continue
		}
		res, err := a.db.Exec(`UPDATE assets SET wrapped_fk=?, fk_sealed=0 WHERE id=? AND owner_id=? AND fk_sealed=1`, k.FK, k.ID, u.ID)
		if err != nil {
			continue
		}
		if c, _ := res.RowsAffected(); c == 0 {
			continue
		}
		n++
		if k.DedupTag != "" && a.dedupImported(u.ID, k.ID, strings.ToLower(k.DedupTag)) {
			dupes++
			continue
		}
		a.writeAssetFiles(k.ID)
	}
	writeJSON(w, map[string]any{"updated": n, "duplicates": dupes})
}

// dedupImported donne son dedupTag à une photo importée ; si la même photo existe déjà (envoyée par l'app,
// ou importée deux fois), la copie importée est supprimée — sauf si on l'a déjà rangée ou mise en favori.
// Renvoie vrai si elle a été supprimée.
func (a *App) dedupImported(owner, id int64, tag string) bool {
	var other int64
	var complete bool
	var trashed sql.NullInt64
	err := a.db.QueryRow(`SELECT id, complete, trashed_at FROM assets WHERE owner_id=? AND dedup_tag=? AND id<>?`, owner, tag, id).
		Scan(&other, &complete, &trashed)
	if errors.Is(err, sql.ErrNoRows) {
		a.db.Exec(`UPDATE assets SET dedup_tag=? WHERE id=? AND dedup_tag IS NULL`, tag, id)
		return false
	}
	if err != nil || !complete { // envoi de l'app en cours : on garde les deux, sans étiquette pour celle-ci
		return false
	}
	var kept bool
	a.db.QueryRow(`SELECT favorite=1 OR EXISTS(SELECT 1 FROM album_assets WHERE asset_id=?) FROM assets WHERE id=?`, id, id).Scan(&kept)
	if kept {
		return false
	}
	if trashed.Valid { // comme un renvoi depuis l'app : la photo revient de la corbeille
		a.db.Exec(`UPDATE assets SET trashed_at=NULL WHERE id=?`, other)
		a.writeAssetFiles(other)
	}
	a.purge([]purgeItem{{owner, id}})
	return true
}

// --- consultation ---

func (a *App) handleTimeline(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 300
	}
	where := []string{"a.owner_id = ?", "a.complete = 1"}
	args := []any{u.ID}
	order := "a.taken_at DESC, a.id DESC"
	sortCol := "a.taken_at"

	if q.Get("trash") == "1" {
		where = append(where, "a.trashed_at IS NOT NULL")
		order = "a.trashed_at DESC, a.id DESC"
		sortCol = "a.trashed_at"
	} else {
		where = append(where, "a.trashed_at IS NULL")
	}
	if q.Get("favorites") == "1" {
		where = append(where, "a.favorite = 1")
	}
	if q.Get("sealed") == "1" {
		where = append(where, "a.fk_sealed = 1")
	}
	switch q.Get("kind") {
	case "video", "image":
		where = append(where, "a.kind = ?")
		args = append(args, q.Get("kind"))
	}
	if y, err := strconv.Atoi(q.Get("year")); err == nil && y > 1800 {
		from := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		to := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		where = append(where, "a.taken_at >= ? AND a.taken_at < ?")
		args = append(args, from, to)
	}
	if c := q.Get("cursor"); c != "" {
		parts := strings.SplitN(c, "_", 2)
		if len(parts) == 2 {
			t, _ := strconv.ParseInt(parts[0], 10, 64)
			id, _ := strconv.ParseInt(parts[1], 10, 64)
			where = append(where, "("+sortCol+" < ? OR ("+sortCol+" = ? AND a.id < ?))")
			args = append(args, t, t, id)
		}
	}
	args = append(args, limit)
	items, err := queryAssets(a.db, `SELECT `+assetCols+` FROM assets a WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ?`, args...)
	if err != nil {
		serverError(w, err)
		return
	}
	next := ""
	if len(items) == limit {
		last := items[len(items)-1]
		t := last.TakenAt
		if last.TrashedAt != nil && q.Get("trash") == "1" {
			t = *last.TrashedAt
		}
		next = fmt.Sprintf("%d_%d", t, last.ID)
	}
	writeJSON(w, map[string]any{"items": items, "next": next})
}

func (a *App) ownedAsset(u *User, id int64) (Asset, error) {
	return scanAsset(a.db.QueryRow(`SELECT `+assetCols+` FROM assets a WHERE a.id=? AND a.owner_id=? AND a.complete=1`, id, u.ID))
}

func (a *App) handleAsset(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	as, err := a.ownedAsset(u, id)
	if err != nil {
		httpError(w, http.StatusNotFound, "introuvable")
		return
	}
	albums := []map[string]any{}
	rows, err := a.db.Query(`SELECT al.id, al.name FROM album_assets aa JOIN albums al ON al.id=aa.album_id WHERE aa.asset_id=? AND al.owner_id=?`, id, u.ID)
	if err == nil {
		for rows.Next() {
			var aid int64
			var name []byte
			rows.Scan(&aid, &name)
			albums = append(albums, map[string]any{"id": aid, "name": name})
		}
		rows.Close()
	}
	writeJSON(w, map[string]any{"asset": as, "albums": albums})
}

func (a *App) serveOwned(variant string) userHandler {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if _, err := a.ownedAsset(u, id); err != nil {
			httpError(w, http.StatusNotFound, "introuvable")
			return
		}
		a.serveBlob(w, r, u.ID, id, variant)
	}
}

// serveBlob envoie un fichier chiffré tel quel (Range pris en charge : le client déchiffre les blocs demandés).
func (a *App) serveBlob(w http.ResponseWriter, r *http.Request, owner, id int64, variant string) {
	f, err := os.Open(a.blobPath(owner, id, variant))
	if err != nil {
		httpError(w, http.StatusNotFound, "fichier introuvable")
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	if variant == "o" {
		// une longue vidéo regardée d'un bout à l'autre dépasse le délai d'écriture du serveur : délai
		// proportionnel à la taille (≈ 32 Kio/s au moins), plafonné à 6 h. Pas plus : les liens de partage
		// publics passent aussi par ici, des connexions lentes ne doivent pas occuper la petite VM indéfiniment.
		http.NewResponseController(w).SetWriteDeadline(time.Now().Add(blobWriteTimeout(st.Size())))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=2592000")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// --- actions ---

func (a *App) handleFavorite(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		IDs      []int64 `json:"ids"`
		Favorite bool    `json:"favorite"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > 20000 {
		httpError(w, http.StatusBadRequest, "sélection vide ou trop grande")
		return
	}
	ph, args := placeholders(body.IDs)
	if _, err := a.db.Exec(`UPDATE assets SET favorite=? WHERE owner_id=? AND id IN (`+ph+`)`, append([]any{body.Favorite, u.ID}, args...)...); err != nil {
		serverError(w, err)
		return
	}
	a.writeAssetFiles(body.IDs...)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleTrash(w http.ResponseWriter, r *http.Request, u *User) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	ph, args := placeholders(ids)
	if _, err := a.db.Exec(`UPDATE assets SET trashed_at=? WHERE owner_id=? AND trashed_at IS NULL AND id IN (`+ph+`)`,
		append([]any{time.Now().UnixMilli(), u.ID}, args...)...); err != nil {
		serverError(w, err)
		return
	}
	a.writeAssetFiles(ids...)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleRestore(w http.ResponseWriter, r *http.Request, u *User) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	ph, args := placeholders(ids)
	if _, err := a.db.Exec(`UPDATE assets SET trashed_at=NULL WHERE owner_id=? AND id IN (`+ph+`)`, append([]any{u.ID}, args...)...); err != nil {
		serverError(w, err)
		return
	}
	a.writeAssetFiles(ids...)
	writeJSON(w, map[string]any{"ok": true})
}

type purgeItem struct{ owner, id int64 }

func (a *App) purgeWhere(where string, args ...any) int {
	rows, err := a.db.Query(`SELECT owner_id, id FROM assets WHERE `+where, args...)
	if err != nil {
		log.Printf("suppression : %v", err)
		return 0
	}
	var items []purgeItem
	for rows.Next() {
		var p purgeItem
		rows.Scan(&p.owner, &p.id)
		items = append(items, p)
	}
	rows.Close()
	a.purge(items)
	return len(items)
}

// purge supprime définitivement fichiers et entrées.
func (a *App) purge(items []purgeItem) {
	for _, p := range items {
		a.purgeOne(p)
	}
}

// purgeOne : sous le verrou des fichiers de clé, pour qu'une écriture en attente ne recrée pas le .k
// entre son effacement et celui de la ligne.
func (a *App) purgeOne(p purgeItem) {
	a.files.wmu.Lock()
	defer a.files.wmu.Unlock()
	failed := false
	for _, v := range []string{"o", "o.part", "m", "s", "k"} {
		if err := os.Remove(a.blobPath(p.owner, p.id, v)); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("suppression de #%d.%s : %v", p.id, v, err)
			failed = true
		}
	}
	if !failed {
		a.db.Exec(`DELETE FROM assets WHERE id=?`, p.id)
	}
}

func (a *App) handleDeleteForever(w http.ResponseWriter, r *http.Request, u *User) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	ph, args := placeholders(ids)
	n := a.purgeWhere(`owner_id=? AND trashed_at IS NOT NULL AND id IN (`+ph+`)`, append([]any{u.ID}, args...)...)
	writeJSON(w, map[string]any{"ok": true, "deleted": n})
}

func (a *App) handleEmptyTrash(w http.ResponseWriter, r *http.Request, u *User) {
	n := a.purgeWhere(`owner_id=? AND trashed_at IS NOT NULL`, u.ID)
	writeJSON(w, map[string]any{"ok": true, "deleted": n})
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request, u *User) {
	var photos, videos, bytes, trash, sealed int64
	a.db.QueryRow(`SELECT
		COALESCE(SUM(kind='image' AND trashed_at IS NULL),0),
		COALESCE(SUM(kind='video' AND trashed_at IS NULL),0),
		COALESCE(SUM(orig_size),0),
		COALESCE(SUM(trashed_at IS NOT NULL),0),
		COALESCE(SUM(fk_sealed),0)
		FROM assets WHERE owner_id=? AND complete=1`, u.ID).Scan(&photos, &videos, &bytes, &trash, &sealed)
	years := []int{}
	rows, err := a.db.Query(`SELECT DISTINCT CAST(strftime('%Y', taken_at/1000, 'unixepoch') AS INTEGER) y FROM assets
		WHERE owner_id=? AND complete=1 AND trashed_at IS NULL ORDER BY y DESC`, u.ID)
	if err == nil {
		for rows.Next() {
			var y int
			rows.Scan(&y)
			years = append(years, y)
		}
		rows.Close()
	}
	writeJSON(w, map[string]any{
		"photos": photos, "videos": videos, "bytes": bytes, "trash": trash, "sealed": sealed,
		"years": years, "diskFree": diskFree(a.libDir), "import": a.importStatus(u),
		"files": adminOnly(u, a.filesStatus()),
	})
}

// adminOnly : information réservée à l'administrateur (nil pour les autres comptes).
func adminOnly(u *User, v any) any {
	if !u.IsAdmin {
		return nil
	}
	return v
}

func blobWriteTimeout(size int64) time.Duration {
	return min(10*time.Minute+time.Duration(size/(32<<10))*time.Second, 6*time.Hour)
}
