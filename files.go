package main

// Fichiers autonomes : à côté de chaque photo chiffrée, un petit fichier « <id>.k » porte ce qu'il faut pour
// la relire (sa clé chiffrée par la clé maître, ses métadonnées chiffrées, sa date) ; chaque compte a son
// « u<id>/compte.json » (clé maître chiffrée par la CLÉ DE SECOURS seulement, paire de clés) et son
// « u<id>/albums.json » (noms chiffrés). Si la VM est perdue, « pellicule rebuild » reconstruit la base à
// partir du disque des photos, et chacun reprend son compte avec sa clé de secours.
//
// Rien de ce qui est écrit ici ne permet de deviner un mot de passe : ni la clé maître chiffrée par le mot de
// passe, ni le hash d'authentification, ni sessions, ni téléphones. Quelqu'un qui lit le disque de la
// Freebox n'a que des données chiffrées par des clés aléatoires de 256 bits.

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const filesFormat = 1

type accountFile struct {
	Format       int    `json:"format"`
	Username     string `json:"username"`
	Admin        bool   `json:"admin"`
	RecoveryMK   []byte `json:"recoveryMK"`
	RecoveryAuth string `json:"recoveryAuth"` // SHA-256 de la preuve (comme dans la base)
	Pub          []byte `json:"pub"`
	WrappedPriv  []byte `json:"wrappedPriv"`
	CreatedAt    int64  `json:"createdAt"`
}

type assetFile struct {
	Format    int    `json:"format"`
	Kind      string `json:"kind"`
	TakenAt   int64  `json:"takenAt"`
	WrappedFK []byte `json:"fk"`
	Sealed    bool   `json:"sealed,omitempty"`
	Meta      []byte `json:"meta"`
	DedupTag  string `json:"dedup,omitempty"`
	DeviceTag string `json:"device,omitempty"`
	OrigSize  int64  `json:"size"`
	Favorite  bool   `json:"favorite,omitempty"`
	TrashedAt *int64 `json:"trashedAt,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

type albumFile struct {
	ID        int64   `json:"id"`
	Name      []byte  `json:"name"`
	Cover     *int64  `json:"cover,omitempty"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
	Assets    []int64 `json:"assets"`
}

type albumsFile struct {
	Format int         `json:"format"`
	Albums []albumFile `json:"albums"`
}

func (a *App) accountPath(id int64) string {
	return filepath.Join(a.libDir, userDir(id), "compte.json")
}
func (a *App) albumsPath(id int64) string { return filepath.Join(a.libDir, userDir(id), "albums.json") }

func writeJSONFile(p string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(p, data)
}

// writeAccountFile : à la création du compte (la clé de secours et la paire de clés ne changent jamais).
func (a *App) writeAccountFile(id int64) error {
	f := accountFile{Format: filesFormat}
	err := a.db.QueryRow(`SELECT username, is_admin, recovery_mk, recovery_auth, pub_key, wrapped_priv, created_at FROM users WHERE id=?`, id).
		Scan(&f.Username, &f.Admin, &f.RecoveryMK, &f.RecoveryAuth, &f.Pub, &f.WrappedPriv, &f.CreatedAt)
	if err == nil {
		err = writeJSONFile(a.accountPath(id), f)
	}
	if err != nil {
		log.Printf("compte.json de #%d : %v", id, err)
		a.files.failed.Add(1)
	}
	return err
}

// writeAssetFiles : après chaque création ou modification d'un élément (clé rechiffrée, favori, corbeille).
func (a *App) writeAssetFiles(ids ...int64) {
	for _, id := range ids {
		a.syncAssetFile(id, false)
	}
}

// assetFileData : contenu attendu du fichier de clé (nil si l'élément n'existe plus).
func (a *App) assetFileData(id int64) (owner int64, data []byte, err error) {
	f := assetFile{Format: filesFormat}
	var dedup sql.NullString
	var trashed sql.NullInt64
	err = a.db.QueryRow(`SELECT owner_id, kind, taken_at, wrapped_fk, fk_sealed, meta, dedup_tag, device_tag, orig_size, favorite, trashed_at, created_at
		FROM assets WHERE id=?`, id).Scan(&owner, &f.Kind, &f.TakenAt, &f.WrappedFK, &f.Sealed, &f.Meta, &dedup, &f.DeviceTag,
		&f.OrigSize, &f.Favorite, &trashed, &f.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, err
	}
	f.DedupTag = dedup.String
	if trashed.Valid {
		f.TrashedAt = &trashed.Int64
	}
	data, err = json.Marshal(f)
	return owner, data, err
}

// syncAssetFile écrit le fichier de clé ; onlyIfDifferent : seulement s'il est absent ou différent de la base
// (écriture ratée sur le partage, ou fichier modifié par quelqu'un qui a accès au partage). Renvoie true si écrit.
func (a *App) syncAssetFile(id int64, onlyIfDifferent bool) bool {
	owner, data, err := a.assetFileData(id)
	if err == nil && data == nil {
		return false
	}
	if err == nil {
		p := a.blobPath(owner, id, "k")
		if onlyIfDifferent {
			if cur, rerr := os.ReadFile(p); rerr == nil && bytes.Equal(cur, data) {
				return false
			}
		}
		if err = os.MkdirAll(filepath.Dir(p), 0o755); err == nil {
			err = writeFileAtomic(p, data)
		}
	}
	if err != nil {
		log.Printf("fichier de clé de #%d : %v", id, err)
		a.files.failed.Add(1)
		return false
	}
	return true
}

// writeAlbumsFile : après chaque modification d'un album du compte.
func (a *App) writeAlbumsFile(owner int64) {
	err := func() error {
		out := albumsFile{Format: filesFormat, Albums: []albumFile{}}
		rows, err := a.db.Query(`SELECT id, name, cover_asset_id, created_at, updated_at FROM albums WHERE owner_id=? ORDER BY id`, owner)
		if err != nil {
			return err
		}
		for rows.Next() {
			var al albumFile
			var cover sql.NullInt64
			if err := rows.Scan(&al.ID, &al.Name, &cover, &al.CreatedAt, &al.UpdatedAt); err != nil {
				rows.Close()
				return err
			}
			if cover.Valid {
				al.Cover = &cover.Int64
			}
			out.Albums = append(out.Albums, al)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range out.Albums {
			al := &out.Albums[i]
			al.Assets = []int64{}
			rs, err := a.db.Query(`SELECT asset_id FROM album_assets WHERE album_id=? ORDER BY added_at, asset_id`, al.ID)
			if err != nil {
				return err
			}
			for rs.Next() {
				var id int64
				rs.Scan(&id)
				al.Assets = append(al.Assets, id)
			}
			rs.Close()
		}
		return writeJSONFile(a.albumsPath(owner), out)
	}()
	if err != nil {
		log.Printf("albums.json de #%d : %v", owner, err)
		a.files.failed.Add(1)
	}
}

// syncFiles : au démarrage puis chaque jour, réécrit les fichiers des comptes et des albums, et les fichiers
// de clé absents ou différents de la base (écriture ratée sur le partage réseau, fichier modifié…).
// filesState : pour les Réglages (administrateur) : la reprise après perte de la VM est-elle à jour ?
type filesState struct {
	last   atomic.Int64 // fin de la dernière vérification complète (ms), 0 = pas encore
	failed atomic.Int64 // écritures ratées depuis le début de cette vérification
}

func (a *App) filesStatus() map[string]any {
	return map[string]any{"lastSync": a.files.last.Load(), "failed": a.files.failed.Load(), "oddNames": a.oddUsernames()}
}

// oddUsernames : comptes créés avant les règles actuelles (ils restent utilisables et reconstructibles ;
// l'administrateur vérifie qu'aucun n'imite un autre nom).
func (a *App) oddUsernames() []string {
	out := []string{}
	rows, err := a.db.Query(`SELECT username FROM users ORDER BY id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil && validUsername(u) != nil {
			out = append(out, u)
		}
	}
	return out
}

func (a *App) syncFiles() {
	if odd := a.oddUsernames(); len(odd) > 0 {
		log.Printf("comptes dont le nom ne suit pas les règles actuelles (à vérifier, ils restent utilisables) : %q", odd)
	}
	a.files.failed.Store(0)
	defer func() { a.files.last.Store(time.Now().UnixMilli()) }()
	var users []int64
	rows, err := a.db.Query(`SELECT id FROM users`)
	if err != nil {
		log.Printf("fichiers autonomes : %v", err)
		return
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		users = append(users, id)
	}
	rows.Close()
	for _, id := range users {
		a.writeAccountFile(id)
		a.writeAlbumsFile(id)
	}
	var ids []int64
	rows, err = a.db.Query(`SELECT id FROM assets WHERE complete=1`)
	if err != nil {
		log.Printf("fichiers autonomes : %v", err)
		return
	}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	fixed := 0
	for _, id := range ids {
		if a.syncAssetFile(id, true) {
			fixed++
		}
	}
	if fixed > 0 {
		log.Printf("fichiers autonomes : %d fichier(s) de clé absent(s) ou différent(s) de la base, réécrit(s)", fixed)
	}
}
