package main

import (
	"archive/zip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// --- envoi ---

type uploadResult struct {
	Status string `json:"status"` // "created" ou "duplicate"
	Asset  Asset  `json:"asset"`
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request, u *User) {
	mr, err := r.MultipartReader()
	if err != nil {
		httpError(w, http.StatusBadRequest, "envoi multipart attendu")
		return
	}
	var mtime int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			httpError(w, http.StatusBadRequest, "envoi interrompu")
			return
		}
		switch part.FormName() {
		case "mtime":
			b, _ := io.ReadAll(io.LimitReader(part, 32))
			mtime, _ = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		case "file":
			res, err := a.saveUpload(u, part, part.FileName(), mtime)
			part.Close()
			if err != nil {
				var ue userError
				if errors.As(err, &ue) {
					httpError(w, http.StatusUnsupportedMediaType, ue.Error())
				} else {
					serverError(w, err)
				}
				return
			}
			writeJSON(w, res)
			return
		}
		part.Close()
	}
	httpError(w, http.StatusBadRequest, "aucun fichier reçu")
}

type userError string

func (e userError) Error() string { return string(e) }

func (a *App) saveUpload(u *User, src io.Reader, name string, mtimeMs int64) (*uploadResult, error) {
	name = cleanFilename(name)
	kind, mime, ok := mediaType(name)
	if !ok {
		return nil, userError("format non pris en charge : " + name)
	}

	tmp, err := os.CreateTemp(filepath.Join(a.libDir, ".tmp"), "up-*")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // sans effet une fois le fichier déplacé

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, userError("fichier vide : " + name)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	deviceKey := fmt.Sprintf("%s|%d", strings.ToLower(name), size)

	if existing, err := scanAsset(a.db.QueryRow(`SELECT `+assetCols+` FROM assets a WHERE owner_id=? AND sha256=?`, u.ID, sum)); err == nil {
		if existing.TrashedAt != nil {
			// renvoyer une photo mise à la corbeille la restaure
			a.db.Exec(`UPDATE assets SET trashed_at=NULL WHERE id=?`, existing.ID)
			existing.TrashedAt = nil
		}
		return &uploadResult{Status: "duplicate", Asset: existing}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// date provisoire (affinée par le worker) : EXIF du JPEG, sinon date du fichier côté appareil
	taken := quickTakenAt(tmpPath, mime)
	if taken.IsZero() && mtimeMs > 0 {
		taken = wallClock(time.UnixMilli(mtimeMs))
	}
	if taken.IsZero() {
		taken = wallClock(time.Now())
	}

	relDir := filepath.Join(u.Username, taken.Format("2006"), taken.Format("01"))
	if err := os.MkdirAll(filepath.Join(a.libDir, relDir), 0o755); err != nil {
		return nil, err
	}
	rel, err := uniquePath(a.libDir, relDir, name)
	if err != nil {
		return nil, err
	}
	os.Chmod(tmpPath, 0o644)
	if err := os.Rename(tmpPath, filepath.Join(a.libDir, rel)); err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	res, err := a.db.Exec(`INSERT INTO assets(owner_id, kind, filename, path, mime, size, sha256, device_key, taken_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, u.ID, kind, name, filepath.ToSlash(rel), mime, size, sum, deviceKey, taken.UnixMilli(), now)
	if err != nil {
		os.Remove(filepath.Join(a.libDir, rel))
		return nil, err
	}
	id, _ := res.LastInsertId()
	a.wake()

	asset, err := scanAsset(a.db.QueryRow(`SELECT `+assetCols+` FROM assets a WHERE id=?`, id))
	return &uploadResult{Status: "created", Asset: asset}, err
}

func cleanFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimLeft(strings.TrimSpace(name), ".")
	if name == "" {
		name = "photo"
	}
	if len(name) > 180 {
		ext := filepath.Ext(name)
		name = name[:180-len(ext)] + ext
	}
	return name
}

// uniquePath choisit un nom libre dans le dossier : IMG_1.jpg, IMG_1 (2).jpg, …
func uniquePath(root, relDir, name string) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		candidate := name
		if i > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		rel := filepath.Join(relDir, candidate)
		if _, err := os.Lstat(filepath.Join(root, rel)); errors.Is(err, os.ErrNotExist) {
			return rel, nil
		}
	}
	return "", errors.New("impossible de trouver un nom libre pour " + name)
}

// handleCheck indique quels fichiers du téléphone sont déjà sauvegardés (nom + taille).
func (a *App) handleCheck(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		Keys []string `json:"keys"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	known := map[string]bool{}
	rows, err := a.db.Query(`SELECT device_key FROM assets WHERE owner_id=?`, u.ID)
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
	for _, k := range body.Keys {
		if known[strings.ToLower(k)] {
			out = append(out, k)
		}
	}
	writeJSON(w, map[string]any{"existing": out})
}

// --- consultation ---

func (a *App) handleTimeline(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	where := []string{"a.owner_id = ?"}
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
	switch q.Get("kind") {
	case "video", "image":
		where = append(where, "a.kind = ?")
		args = append(args, q.Get("kind"))
	}
	if s := strings.TrimSpace(q.Get("q")); s != "" {
		where = append(where, "a.filename LIKE ? ESCAPE '\\'")
		args = append(args, "%"+likeEscape(s)+"%")
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

func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (a *App) ownedAsset(u *User, id int64) (Asset, error) {
	return scanAsset(a.db.QueryRow(`SELECT `+assetCols+` FROM assets a WHERE a.id=? AND a.owner_id=?`, id, u.ID))
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
	rows, err := a.db.Query(`SELECT al.id, al.name FROM album_assets aa JOIN albums al ON al.id=aa.album_id WHERE aa.asset_id=? ORDER BY al.name`, id)
	if err == nil {
		for rows.Next() {
			var aid int64
			var name string
			rows.Scan(&aid, &name)
			albums = append(albums, map[string]any{"id": aid, "name": name})
		}
		rows.Close()
	}
	writeJSON(w, map[string]any{"asset": as, "albums": albums, "path": as.Path})
}

func (a *App) serveOwned(variant string) userHandler {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		as, err := a.ownedAsset(u, id)
		if err != nil {
			httpError(w, http.StatusNotFound, "introuvable")
			return
		}
		a.serveAsset(w, r, as, variant, true)
	}
}

// serveAsset envoie la miniature ("s"), l'aperçu ("m") ou l'original ("").
func (a *App) serveAsset(w http.ResponseWriter, r *http.Request, as Asset, variant string, allowDownload bool) {
	if variant != "" {
		p := a.thumbPath(as.ID, variant)
		f, err := os.Open(p)
		if err != nil {
			if as.Thumb == 2 {
				w.Header().Set("Cache-Control", "private, max-age=3600")
				serveFallbackIcon(w, as.Kind)
				return
			}
			w.Header().Set("Retry-After", "3")
			httpError(w, http.StatusNotFound, "miniature en préparation")
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "private, max-age=2592000")
		http.ServeContent(w, r, "", time.Time{}, f)
		return
	}
	if !allowDownload {
		httpError(w, http.StatusForbidden, "téléchargement désactivé")
		return
	}
	f, err := os.Open(filepath.Join(a.libDir, filepath.FromSlash(as.Path)))
	if err != nil {
		httpError(w, http.StatusNotFound, "fichier original introuvable")
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", as.Mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(as.Filename))
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func serveFallbackIcon(w http.ResponseWriter, kind string) {
	icon := `<path d="M4 16l4-4 3 3 5-5 4 4" stroke="#9a948c" stroke-width="1.5" fill="none"/><circle cx="8.5" cy="7.5" r="1.5" fill="#9a948c"/>`
	if kind == "video" {
		icon = `<path d="M9 7.5v9l7.5-4.5z" fill="#9a948c"/>`
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect width="24" height="24" fill="#3a3632"/>%s</svg>`, icon)
}

// --- téléchargement d'une sélection en ZIP ---

func (a *App) handleDownloadSelection(w http.ResponseWriter, r *http.Request, u *User) {
	var ids []int64
	for _, s := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > 20000 {
		httpError(w, http.StatusBadRequest, "sélection invalide")
		return
	}
	ph, args := placeholders(ids)
	items, err := queryAssets(a.db, `SELECT `+assetCols+` FROM assets a WHERE a.owner_id=? AND a.id IN (`+ph+`) ORDER BY a.taken_at`, append([]any{u.ID}, args...)...)
	if err != nil {
		serverError(w, err)
		return
	}
	a.writeZip(w, "pellicule-"+time.Now().Format("2006-01-02"), items)
}

func (a *App) writeZip(w http.ResponseWriter, name string, items []Asset) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name+".zip"))
	zw := zip.NewWriter(w)
	used := map[string]int{}
	for _, as := range items {
		f, err := os.Open(filepath.Join(a.libDir, filepath.FromSlash(as.Path)))
		if err != nil {
			continue
		}
		n := as.Filename
		if c := used[strings.ToLower(n)]; c > 0 {
			ext := filepath.Ext(n)
			n = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(n, ext), c+1, ext)
		}
		used[strings.ToLower(as.Filename)]++
		// photos et vidéos sont déjà compressées : on les stocke tels quels (rapide, peu de CPU)
		hdr := &zip.FileHeader{Name: n, Method: zip.Store, Modified: time.UnixMilli(as.TakenAt).UTC()}
		dst, err := zw.CreateHeader(hdr)
		if err == nil {
			_, err = io.Copy(dst, f)
		}
		f.Close()
		if err != nil {
			log.Printf("zip interrompu : %v", err)
			return
		}
	}
	zw.Close()
}

// --- actions ---

func (a *App) handleFavorite(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		IDs      []int64 `json:"ids"`
		Favorite bool    `json:"favorite"`
	}
	if !readJSON(w, r, &body) || len(body.IDs) == 0 {
		return
	}
	ph, args := placeholders(body.IDs)
	_, err := a.db.Exec(`UPDATE assets SET favorite=? WHERE owner_id=? AND id IN (`+ph+`)`, append([]any{body.Favorite, u.ID}, args...)...)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleTrash(w http.ResponseWriter, r *http.Request, u *User) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	ph, args := placeholders(ids)
	_, err := a.db.Exec(`UPDATE assets SET trashed_at=? WHERE owner_id=? AND trashed_at IS NULL AND id IN (`+ph+`)`,
		append([]any{time.Now().UnixMilli(), u.ID}, args...)...)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleRestore(w http.ResponseWriter, r *http.Request, u *User) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	ph, args := placeholders(ids)
	_, err := a.db.Exec(`UPDATE assets SET trashed_at=NULL WHERE owner_id=? AND id IN (`+ph+`)`, append([]any{u.ID}, args...)...)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleDeleteForever(w http.ResponseWriter, r *http.Request, u *User) {
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	ph, args := placeholders(ids)
	items, err := queryAssets(a.db, `SELECT `+assetCols+` FROM assets a WHERE a.owner_id=? AND a.trashed_at IS NOT NULL AND a.id IN (`+ph+`)`, append([]any{u.ID}, args...)...)
	if err != nil {
		serverError(w, err)
		return
	}
	a.purge(items)
	writeJSON(w, map[string]any{"ok": true, "deleted": len(items)})
}

func (a *App) handleEmptyTrash(w http.ResponseWriter, r *http.Request, u *User) {
	items, err := queryAssets(a.db, `SELECT `+assetCols+` FROM assets a WHERE a.owner_id=? AND a.trashed_at IS NOT NULL`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	a.purge(items)
	writeJSON(w, map[string]any{"ok": true, "deleted": len(items)})
}

// purge supprime définitivement fichiers, miniatures et entrées.
func (a *App) purge(items []Asset) {
	for _, as := range items {
		if err := os.Remove(filepath.Join(a.libDir, filepath.FromSlash(as.Path))); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("suppression de %s : %v", as.Path, err)
			continue
		}
		os.Remove(a.thumbPath(as.ID, "s"))
		os.Remove(a.thumbPath(as.ID, "m"))
		a.db.Exec(`DELETE FROM assets WHERE id=?`, as.ID)
	}
}

func (a *App) handleStats(w http.ResponseWriter, r *http.Request, u *User) {
	var photos, videos, bytes, pending, trash int64
	a.db.QueryRow(`SELECT
		COALESCE(SUM(kind='image' AND trashed_at IS NULL),0),
		COALESCE(SUM(kind='video' AND trashed_at IS NULL),0),
		COALESCE(SUM(size),0),
		COALESCE(SUM(thumb_status=0),0),
		COALESCE(SUM(trashed_at IS NOT NULL),0)
		FROM assets WHERE owner_id=?`, u.ID).Scan(&photos, &videos, &bytes, &pending, &trash)
	years := []int{}
	rows, err := a.db.Query(`SELECT DISTINCT CAST(strftime('%Y', taken_at/1000, 'unixepoch') AS INTEGER) y FROM assets
		WHERE owner_id=? AND trashed_at IS NULL ORDER BY y DESC`, u.ID)
	if err == nil {
		for rows.Next() {
			var y int
			rows.Scan(&y)
			years = append(years, y)
		}
		rows.Close()
	}
	free := diskFree(a.libDir)
	writeJSON(w, map[string]any{
		"photos": photos, "videos": videos, "bytes": bytes, "pending": pending, "trash": trash,
		"years": years, "diskFree": free, "tools": a.tools.summary(), "import": a.importStatus(u),
	})
}
