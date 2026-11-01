package main

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Album struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CoverID   *int64 `json:"coverId"`
	Count     int    `json:"count"`
	StartAt   *int64 `json:"startAt"`
	EndAt     *int64 `json:"endAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Shared    bool   `json:"shared"`
}

// La couverture par défaut est la photo la plus récente de l'album.
const albumSelect = `SELECT al.id, al.name,
	COALESCE(al.cover_asset_id, (SELECT x.id FROM album_assets aa JOIN assets x ON x.id=aa.asset_id
		WHERE aa.album_id=al.id AND x.trashed_at IS NULL ORDER BY x.taken_at DESC LIMIT 1)),
	(SELECT COUNT(*) FROM album_assets aa JOIN assets x ON x.id=aa.asset_id WHERE aa.album_id=al.id AND x.trashed_at IS NULL),
	(SELECT MIN(x.taken_at) FROM album_assets aa JOIN assets x ON x.id=aa.asset_id WHERE aa.album_id=al.id AND x.trashed_at IS NULL),
	(SELECT MAX(x.taken_at) FROM album_assets aa JOIN assets x ON x.id=aa.asset_id WHERE aa.album_id=al.id AND x.trashed_at IS NULL),
	al.updated_at,
	EXISTS(SELECT 1 FROM shares s WHERE s.album_id=al.id)
	FROM albums al`

func scanAlbum(s scanner) (Album, error) {
	var al Album
	var cover, start, end sql.NullInt64
	err := s.Scan(&al.ID, &al.Name, &cover, &al.Count, &start, &end, &al.UpdatedAt, &al.Shared)
	if cover.Valid {
		al.CoverID = &cover.Int64
	}
	if start.Valid {
		al.StartAt = &start.Int64
	}
	if end.Valid {
		al.EndAt = &end.Int64
	}
	return al, err
}

func (a *App) ownedAlbum(u *User, id int64) (Album, error) {
	return scanAlbum(a.db.QueryRow(albumSelect+` WHERE al.id=? AND al.owner_id=?`, id, u.ID))
}

func (a *App) albumAssets(albumID int64) ([]Asset, error) {
	return queryAssets(a.db, `SELECT `+assetCols+` FROM album_assets aa JOIN assets a ON a.id=aa.asset_id
		WHERE aa.album_id=? AND a.trashed_at IS NULL ORDER BY a.taken_at DESC, a.id DESC`, albumID)
}

func (a *App) handleAlbums(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(albumSelect+` WHERE al.owner_id=? ORDER BY al.updated_at DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []Album{}
	for rows.Next() {
		al, err := scanAlbum(rows)
		if err != nil {
			serverError(w, err)
			return
		}
		out = append(out, al)
	}
	writeJSON(w, out)
}

func (a *App) handleCreateAlbum(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		Name string  `json:"name"`
		IDs  []int64 `json:"ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 200 {
		httpError(w, http.StatusBadRequest, "nom d'album invalide")
		return
	}
	now := time.Now().UnixMilli()
	res, err := a.db.Exec(`INSERT INTO albums(owner_id, name, created_at, updated_at) VALUES (?,?,?,?)`, u.ID, name, now, now)
	if err != nil {
		serverError(w, err)
		return
	}
	id, _ := res.LastInsertId()
	if len(body.IDs) > 0 {
		if err := a.addToAlbum(u, id, body.IDs); err != nil {
			serverError(w, err)
			return
		}
	}
	writeJSON(w, map[string]any{"id": id})
}

func (a *App) handleAlbum(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	al, err := a.ownedAlbum(u, id)
	if err != nil {
		httpError(w, http.StatusNotFound, "album introuvable")
		return
	}
	items, err := a.albumAssets(id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"album": al, "items": items})
}

func (a *App) handleUpdateAlbum(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.ownedAlbum(u, id); err != nil {
		httpError(w, http.StatusNotFound, "album introuvable")
		return
	}
	var body struct {
		Name    *string `json:"name"`
		CoverID *int64  `json:"coverId"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	now := time.Now().UnixMilli()
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" || len(name) > 200 {
			httpError(w, http.StatusBadRequest, "nom d'album invalide")
			return
		}
		a.db.Exec(`UPDATE albums SET name=?, updated_at=? WHERE id=?`, name, now, id)
	}
	if body.CoverID != nil {
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM album_assets WHERE album_id=? AND asset_id=?`, id, *body.CoverID).Scan(&n)
		if n == 0 {
			httpError(w, http.StatusBadRequest, "cette photo n'est pas dans l'album")
			return
		}
		a.db.Exec(`UPDATE albums SET cover_asset_id=?, updated_at=? WHERE id=?`, *body.CoverID, now, id)
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleDeleteAlbum(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	// Supprimer un album ne supprime pas les photos.
	a.db.Exec(`DELETE FROM albums WHERE id=? AND owner_id=?`, id, u.ID)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) addToAlbum(u *User, albumID int64, ids []int64) error {
	ph, args := placeholders(ids)
	now := time.Now().UnixMilli()
	_, err := a.db.Exec(`INSERT OR IGNORE INTO album_assets(album_id, asset_id, added_at)
		SELECT ?, id, ? FROM assets WHERE owner_id=? AND id IN (`+ph+`)`, append([]any{albumID, now, u.ID}, args...)...)
	if err == nil {
		a.db.Exec(`UPDATE albums SET updated_at=? WHERE id=?`, now, albumID)
	}
	return err
}

func (a *App) handleAlbumAdd(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.ownedAlbum(u, id); err != nil {
		httpError(w, http.StatusNotFound, "album introuvable")
		return
	}
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	if err := a.addToAlbum(u, id, ids); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleAlbumRemove(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.ownedAlbum(u, id); err != nil {
		httpError(w, http.StatusNotFound, "album introuvable")
		return
	}
	ids, ok := readIDs(w, r)
	if !ok {
		return
	}
	ph, args := placeholders(ids)
	a.db.Exec(`DELETE FROM album_assets WHERE album_id=? AND asset_id IN (`+ph+`)`, append([]any{id}, args...)...)
	a.db.Exec(`UPDATE albums SET cover_asset_id=NULL WHERE id=? AND cover_asset_id IN (`+ph+`)`, append([]any{id}, args...)...)
	a.db.Exec(`UPDATE albums SET updated_at=? WHERE id=?`, time.Now().UnixMilli(), id)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleAlbumZip(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	al, err := a.ownedAlbum(u, id)
	if err != nil {
		httpError(w, http.StatusNotFound, "album introuvable")
		return
	}
	items, err := a.albumAssets(id)
	if err != nil {
		serverError(w, err)
		return
	}
	a.writeZip(w, al.Name, items)
}

// --- liens de partage ---

type Share struct {
	Token         string `json:"token"`
	AllowDownload bool   `json:"allowDownload"`
	CreatedAt     int64  `json:"createdAt"`
	ExpiresAt     *int64 `json:"expiresAt"`
}

func (a *App) handleAlbumShares(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.ownedAlbum(u, id); err != nil {
		httpError(w, http.StatusNotFound, "album introuvable")
		return
	}
	rows, err := a.db.Query(`SELECT token, allow_download, created_at, expires_at FROM shares WHERE album_id=? ORDER BY created_at DESC`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var s Share
		var exp sql.NullInt64
		rows.Scan(&s.Token, &s.AllowDownload, &s.CreatedAt, &exp)
		if exp.Valid {
			s.ExpiresAt = &exp.Int64
		}
		out = append(out, s)
	}
	writeJSON(w, out)
}

func (a *App) handleCreateShare(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := a.ownedAlbum(u, id); err != nil {
		httpError(w, http.StatusNotFound, "album introuvable")
		return
	}
	var body struct {
		AllowDownload bool `json:"allowDownload"`
		Days          int  `json:"days"` // 0 = sans expiration
	}
	if !readJSON(w, r, &body) {
		return
	}
	token := randomToken(16)
	var exp any
	if body.Days > 0 {
		exp = time.Now().Add(time.Duration(body.Days) * 24 * time.Hour).UnixMilli()
	}
	_, err := a.db.Exec(`INSERT INTO shares(token, album_id, allow_download, created_at, expires_at) VALUES (?,?,?,?,?)`,
		token, id, body.AllowDownload, time.Now().UnixMilli(), exp)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"token": token})
}

func (a *App) handleDeleteShare(w http.ResponseWriter, r *http.Request, u *User) {
	a.db.Exec(`DELETE FROM shares WHERE token=? AND album_id IN (SELECT id FROM albums WHERE owner_id=?)`, r.PathValue("token"), u.ID)
	writeJSON(w, map[string]any{"ok": true})
}

// publicShare valide un jeton de partage et renvoie l'album associé.
func (a *App) publicShare(token string) (Album, bool, error) {
	var albumID int64
	var allow bool
	var exp sql.NullInt64
	err := a.db.QueryRow(`SELECT album_id, allow_download, expires_at FROM shares WHERE token=?`, token).Scan(&albumID, &allow, &exp)
	if err != nil {
		return Album{}, false, err
	}
	if exp.Valid && exp.Int64 < time.Now().UnixMilli() {
		return Album{}, false, errors.New("lien expiré")
	}
	al, err := scanAlbum(a.db.QueryRow(albumSelect+` WHERE al.id=?`, albumID))
	return al, allow, err
}

func (a *App) handlePublicAlbum(w http.ResponseWriter, r *http.Request) {
	al, allow, err := a.publicShare(r.PathValue("token"))
	if err != nil {
		httpError(w, http.StatusNotFound, "lien invalide ou expiré")
		return
	}
	items, err := a.albumAssets(al.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	var owner string
	a.db.QueryRow(`SELECT u.username FROM albums al JOIN users u ON u.id=al.owner_id WHERE al.id=?`, al.ID).Scan(&owner)
	al.Shared = false
	writeJSON(w, map[string]any{"album": al, "items": items, "allowDownload": allow, "owner": owner})
}

func (a *App) servePublic(variant string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		al, allow, err := a.publicShare(r.PathValue("token"))
		if err != nil {
			httpError(w, http.StatusNotFound, "lien invalide ou expiré")
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			httpError(w, http.StatusBadRequest, "identifiant invalide")
			return
		}
		as, err := scanAsset(a.db.QueryRow(`SELECT `+assetCols+` FROM album_assets aa JOIN assets a ON a.id=aa.asset_id
			WHERE aa.album_id=? AND a.id=? AND a.trashed_at IS NULL`, al.ID, id))
		if err != nil {
			httpError(w, http.StatusNotFound, "introuvable")
			return
		}
		// les vidéos se lisent depuis l'original : autorisé même sans téléchargement
		a.serveAsset(w, r, as, variant, allow || (as.Kind == "video" && r.URL.Query().Get("download") != "1"))
	}
}

func (a *App) handlePublicZip(w http.ResponseWriter, r *http.Request) {
	al, allow, err := a.publicShare(r.PathValue("token"))
	if err != nil {
		httpError(w, http.StatusNotFound, "lien invalide ou expiré")
		return
	}
	if !allow {
		httpError(w, http.StatusForbidden, "téléchargement désactivé")
		return
	}
	items, err := a.albumAssets(al.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	a.writeZip(w, al.Name, items)
}
