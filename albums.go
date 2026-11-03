package main

// Albums et liens de partage.
//
// Le nom d'un album est chiffré par la clé maître du propriétaire. Un lien de partage porte sa propre
// clé dans le fragment de l'URL (/s/<jeton>#<clé>), que le navigateur n'envoie jamais au serveur :
// pour chaque photo partagée, le client dépose ici la clé du fichier chiffrée par cette clé de partage.
// Le serveur connaît donc le jeton (pour retrouver l'album) mais ne peut rien déchiffrer.

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type Album struct {
	ID        int64  `json:"id"`
	Name      []byte `json:"name"` // chiffré
	CoverID   *int64 `json:"coverId"`
	CoverFK   []byte `json:"coverFk,omitempty"`
	CoverSeal bool   `json:"coverSealed,omitempty"` // clé de la couverture encore scellée par l'import
	Count     int    `json:"count"`
	StartAt   *int64 `json:"startAt"`
	EndAt     *int64 `json:"endAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Shared    bool   `json:"shared"`
}

// La couverture par défaut est la photo la plus récente de l'album.
const albumSelect = `SELECT al.id, al.name, c.id, c.wrapped_fk, COALESCE(c.fk_sealed, 0),
	(SELECT COUNT(*) FROM album_assets aa JOIN assets x ON x.id=aa.asset_id WHERE aa.album_id=al.id AND x.trashed_at IS NULL AND x.complete=1),
	(SELECT MIN(x.taken_at) FROM album_assets aa JOIN assets x ON x.id=aa.asset_id WHERE aa.album_id=al.id AND x.trashed_at IS NULL AND x.complete=1),
	(SELECT MAX(x.taken_at) FROM album_assets aa JOIN assets x ON x.id=aa.asset_id WHERE aa.album_id=al.id AND x.trashed_at IS NULL AND x.complete=1),
	al.updated_at,
	EXISTS(SELECT 1 FROM shares s WHERE s.album_id=al.id)
	FROM albums al
	LEFT JOIN assets c ON c.id = COALESCE(al.cover_asset_id, (SELECT x.id FROM album_assets aa JOIN assets x ON x.id=aa.asset_id
		WHERE aa.album_id=al.id AND x.trashed_at IS NULL AND x.complete=1 ORDER BY x.taken_at DESC LIMIT 1))`

func scanAlbum(s scanner) (Album, error) {
	var al Album
	var cover, start, end sql.NullInt64
	err := s.Scan(&al.ID, &al.Name, &cover, &al.CoverFK, &al.CoverSeal, &al.Count, &start, &end, &al.UpdatedAt, &al.Shared)
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
		WHERE aa.album_id=? AND a.trashed_at IS NULL AND a.complete=1 ORDER BY a.taken_at DESC, a.id DESC`, albumID)
}

func validName(name []byte) bool { return len(name) >= NonceSize+TagSize && len(name) <= 4096 }

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
		Name []byte  `json:"name"`
		IDs  []int64 `json:"ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !validName(body.Name) || len(body.IDs) > 20000 {
		httpError(w, http.StatusBadRequest, "requête invalide")
		return
	}
	now := time.Now().UnixMilli()
	res, err := a.db.Exec(`INSERT INTO albums(owner_id, name, created_at, updated_at) VALUES (?,?,?,?)`, u.ID, body.Name, now, now)
	if err != nil {
		serverError(w, err)
		return
	}
	id, _ := res.LastInsertId()
	a.writeAlbumsFile(u.ID)
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
		Name    []byte `json:"name"`
		CoverID *int64 `json:"coverId"`
		// noms re-chiffrés pour chaque lien de partage existant (sinon le lien garderait l'ancien nom)
		ShareNames map[string][]byte `json:"shareNames"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	now := time.Now().UnixMilli()
	if body.Name != nil {
		if !validName(body.Name) {
			httpError(w, http.StatusBadRequest, "nom d'album invalide")
			return
		}
		a.db.Exec(`UPDATE albums SET name=?, updated_at=? WHERE id=?`, body.Name, now, id)
		for sid, n := range body.ShareNames {
			if validName(n) {
				a.db.Exec(`UPDATE shares SET name=? WHERE id=? AND album_id=?`, n, sid, id)
			}
		}
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
	a.writeAlbumsFile(u.ID)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleDeleteAlbum(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := mustExec(a.db, `DELETE FROM albums WHERE id=? AND owner_id=?`, id, u.ID); err != nil { // les photos restent
		serverError(w, err)
		return
	}
	a.writeAlbumsFile(u.ID)
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
	a.writeAlbumsFile(u.ID)
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
	with := append([]any{id}, args...)
	err := a.inTx(func(tx *sql.Tx) error {
		if err := mustExec(tx, `DELETE FROM album_assets WHERE album_id=? AND asset_id IN (`+ph+`)`, with...); err != nil {
			return err
		}
		// la clé de partage d'une photo retirée de l'album est détruite : le lien ne peut plus l'ouvrir
		if err := mustExec(tx, `DELETE FROM share_keys WHERE share_id IN (SELECT id FROM shares WHERE album_id=?) AND asset_id IN (`+ph+`)`, with...); err != nil {
			return err
		}
		if err := mustExec(tx, `UPDATE albums SET cover_asset_id=NULL WHERE id=? AND cover_asset_id IN (`+ph+`)`, with...); err != nil {
			return err
		}
		return mustExec(tx, `UPDATE albums SET updated_at=? WHERE id=?`, time.Now().UnixMilli(), id)
	})
	if err != nil {
		serverError(w, err)
		return
	}
	a.writeAlbumsFile(u.ID)
	writeJSON(w, map[string]any{"ok": true})
}

// --- liens de partage ---

type Share struct {
	ID            int64  `json:"id"`
	Link          []byte `json:"link"` // chiffré par la clé maître : seul le propriétaire peut réafficher le lien
	AllowDownload bool   `json:"allowDownload"`
	CreatedAt     int64  `json:"createdAt"`
	ExpiresAt     *int64 `json:"expiresAt"`
	Missing       int    `json:"missing"` // photos de l'album dont la clé n'a pas encore été déposée
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
	rows, err := a.db.Query(`SELECT s.id, s.link, s.allow_download, s.created_at, s.expires_at,
		(SELECT COUNT(*) FROM album_assets aa JOIN assets x ON x.id=aa.asset_id
			WHERE aa.album_id=s.album_id AND x.complete=1 AND x.trashed_at IS NULL
			AND NOT EXISTS(SELECT 1 FROM share_keys k WHERE k.share_id=s.id AND k.asset_id=aa.asset_id))
		FROM shares s WHERE s.album_id=? ORDER BY s.created_at DESC`, id)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var s Share
		var exp sql.NullInt64
		rows.Scan(&s.ID, &s.Link, &s.AllowDownload, &s.CreatedAt, &exp, &s.Missing)
		if exp.Valid {
			s.ExpiresAt = &exp.Int64
		}
		out = append(out, s)
	}
	writeJSON(w, out)
}

type shareKey struct {
	Asset int64  `json:"asset"`
	FK    []byte `json:"fk"`
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
		Token         string     `json:"token"` // choisi par le client (il fait partie du lien qu'il chiffre)
		Link          []byte     `json:"link"`
		Name          []byte     `json:"name"`
		Keys          []shareKey `json:"keys"`
		AllowDownload bool       `json:"allowDownload"`
		Days          int        `json:"days"` // 0 = sans expiration
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.Token) < 32 || len(body.Token) > 128 || !validName(body.Link) || !validName(body.Name) {
		httpError(w, http.StatusBadRequest, "requête invalide")
		return
	}
	if body.Days < 0 || body.Days > 3650 {
		httpError(w, http.StatusBadRequest, "durée invalide (0 à 3650 jours)")
		return
	}
	var exp any
	if body.Days > 0 {
		exp = time.Now().Add(time.Duration(body.Days) * 24 * time.Hour).UnixMilli()
	}
	var sid int64
	err := a.inTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO shares(token, album_id, link, name, allow_download, created_at, expires_at) VALUES (?,?,?,?,?,?,?)`,
			tokenHash(body.Token), id, body.Link, body.Name, body.AllowDownload, time.Now().UnixMilli(), exp)
		if err != nil {
			return err
		}
		sid, _ = res.LastInsertId()
		_, err = storeShareKeys(tx, sid, id, body.Keys)
		return err
	})
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"id": sid})
}

// storeShareKeys n'accepte que des photos qui sont réellement dans l'album partagé.
func storeShareKeys(db execer, shareID, albumID int64, keys []shareKey) (int, error) {
	n := 0
	for _, k := range keys {
		if len(k.FK) != boxSize {
			continue
		}
		res, err := db.Exec(`INSERT OR REPLACE INTO share_keys(share_id, asset_id, wrapped_fk)
			SELECT ?, ?, ? WHERE EXISTS(SELECT 1 FROM album_assets WHERE album_id=? AND asset_id=?)`,
			shareID, k.Asset, k.FK, albumID, k.Asset)
		if err != nil {
			return n, err
		}
		c, _ := res.RowsAffected()
		n += int(c)
	}
	return n, nil
}

// handleShareKeys : après un ajout de photos dans un album partagé, le client dépose leurs clés.
func (a *App) handleShareKeys(w http.ResponseWriter, r *http.Request, u *User) {
	sid, ok := pathID(w, r)
	if !ok {
		return
	}
	var albumID int64
	if a.db.QueryRow(`SELECT s.album_id FROM shares s JOIN albums al ON al.id=s.album_id WHERE s.id=? AND al.owner_id=?`, sid, u.ID).Scan(&albumID) != nil {
		httpError(w, http.StatusNotFound, "lien introuvable")
		return
	}
	var body struct {
		Keys []shareKey `json:"keys"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	var n int
	err := a.inTx(func(tx *sql.Tx) (err error) {
		n, err = storeShareKeys(tx, sid, albumID, body.Keys)
		return err
	})
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"stored": n})
}

func (a *App) handleDeleteShare(w http.ResponseWriter, r *http.Request, u *User) {
	sid, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := mustExec(a.db, `DELETE FROM shares WHERE id=? AND album_id IN (SELECT id FROM albums WHERE owner_id=?)`, sid, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// --- accès public par lien ---

type publicShare struct {
	id, albumID, owner int64
	name               []byte
	allow              bool
}

func (a *App) publicShare(token string) (publicShare, error) {
	var s publicShare
	var exp sql.NullInt64
	err := a.db.QueryRow(`SELECT s.id, s.album_id, al.owner_id, s.name, s.allow_download, s.expires_at
		FROM shares s JOIN albums al ON al.id=s.album_id WHERE s.token=?`, tokenHash(token)).
		Scan(&s.id, &s.albumID, &s.owner, &s.name, &s.allow, &exp)
	if err != nil {
		return s, err
	}
	if exp.Valid && exp.Int64 < time.Now().UnixMilli() {
		return s, errors.New("lien expiré")
	}
	return s, nil
}

// sharedAssets : les photos de l'album, avec la clé de fichier chiffrée pour ce lien (et non pour le propriétaire).
func (a *App) sharedAssets(s publicShare) ([]Asset, error) {
	items, err := queryAssets(a.db, `SELECT a.id, a.kind, a.taken_at, k.wrapped_fk, 0, a.meta, a.orig_size, a.has_thumb, a.has_preview, 0, NULL
		FROM album_assets aa JOIN assets a ON a.id=aa.asset_id JOIN share_keys k ON k.asset_id=a.id AND k.share_id=?
		WHERE aa.album_id=? AND a.trashed_at IS NULL AND a.complete=1 ORDER BY a.taken_at DESC, a.id DESC`, s.id, s.albumID)
	return items, err
}

func (a *App) handlePublicAlbum(w http.ResponseWriter, r *http.Request) {
	s, err := a.publicShare(r.PathValue("token"))
	if err != nil {
		httpError(w, http.StatusNotFound, "lien invalide ou expiré")
		return
	}
	items, err := a.sharedAssets(s)
	if err != nil {
		serverError(w, err)
		return
	}
	var owner string
	a.db.QueryRow(`SELECT username FROM users WHERE id=?`, s.owner).Scan(&owner)
	writeJSON(w, map[string]any{"name": s.name, "items": items, "allowDownload": s.allow, "owner": owner})
}

func (a *App) servePublic(variant string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := a.publicShare(r.PathValue("token"))
		if err != nil {
			httpError(w, http.StatusNotFound, "lien invalide ou expiré")
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			httpError(w, http.StatusBadRequest, "identifiant invalide")
			return
		}
		var kind string
		err = a.db.QueryRow(`SELECT a.kind FROM album_assets aa JOIN assets a ON a.id=aa.asset_id
			JOIN share_keys k ON k.asset_id=a.id AND k.share_id=?
			WHERE aa.album_id=? AND a.id=? AND a.trashed_at IS NULL AND a.complete=1`, s.id, s.albumID, id).Scan(&kind)
		if err != nil {
			httpError(w, http.StatusNotFound, "introuvable")
			return
		}
		// l'original n'est servi que si le téléchargement est permis ; les vidéos se lisent depuis l'original
		if variant == "o" && !s.allow && kind != "video" {
			httpError(w, http.StatusForbidden, "téléchargement désactivé")
			return
		}
		a.serveBlob(w, r, s.owner, id, variant)
	}
}
