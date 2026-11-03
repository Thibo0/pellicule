package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	_ "modernc.org/sqlite"
)

// Schéma v2 (Pellicule 0.5, chiffrement de bout en bout). Le serveur ne stocke que des données
// chiffrées : noms de fichiers, métadonnées et noms d'albums sont des boîtes AES-GCM ouvertes par le client.
const schemaVersion = 2

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  auth_hash TEXT NOT NULL,             -- bcrypt de la clé d'authentification (jamais le mot de passe)
  salt BLOB NOT NULL,                  -- sel PBKDF2
  wrapped_mk BLOB NOT NULL,            -- clé maître chiffrée par la clé dérivée du mot de passe
  recovery_mk BLOB NOT NULL,           -- clé maître chiffrée par la clé de secours
  recovery_auth TEXT NOT NULL,         -- SHA-256 de la preuve de possession de la clé de secours
  pub_key BLOB NOT NULL,               -- clé publique ECDH P-256 (import FolderSync)
  wrapped_priv BLOB NOT NULL,          -- clé privée chiffrée par la clé maître
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,              -- SHA-256 du jeton
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS invites (
  token TEXT PRIMARY KEY,              -- SHA-256 du jeton
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_by INTEGER REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  cert_fp TEXT NOT NULL UNIQUE,        -- empreinte du certificat client (clé dans le Keystore du téléphone)
  created_at INTEGER NOT NULL,
  last_seen INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS pair_codes (
  code TEXT PRIMARY KEY,               -- SHA-256 du code
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS assets (
  id INTEGER PRIMARY KEY,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,                  -- image | video (filtres)
  taken_at INTEGER NOT NULL,           -- date de prise de vue (tri, pagination)
  wrapped_fk BLOB NOT NULL,            -- clé du fichier chiffrée par la clé maître (ou scellée par l'import)
  fk_sealed INTEGER NOT NULL DEFAULT 0,
  meta BLOB NOT NULL,                  -- nom, type, dimensions… chiffrés par la clé du fichier
  dedup_tag TEXT,                      -- HMAC(clé maître, contenu)
  device_tag TEXT NOT NULL DEFAULT '', -- HMAC(clé maître, nom|taille)
  orig_size INTEGER NOT NULL,          -- taille chiffrée attendue de l'original
  uploaded INTEGER NOT NULL DEFAULT 0, -- octets de l'original déjà reçus
  complete INTEGER NOT NULL DEFAULT 0,
  has_thumb INTEGER NOT NULL DEFAULT 0,
  has_preview INTEGER NOT NULL DEFAULT 0,
  favorite INTEGER NOT NULL DEFAULT 0,
  trashed_at INTEGER,
  created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS assets_dedup ON assets(owner_id, dedup_tag) WHERE dedup_tag IS NOT NULL;
CREATE INDEX IF NOT EXISTS assets_timeline ON assets(owner_id, complete, trashed_at, taken_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS assets_device ON assets(owner_id, device_tag);
CREATE TABLE IF NOT EXISTS albums (
  id INTEGER PRIMARY KEY,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name BLOB NOT NULL,                  -- chiffré par la clé maître
  cover_asset_id INTEGER REFERENCES assets(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS album_assets (
  album_id INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
  asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
  added_at INTEGER NOT NULL,
  PRIMARY KEY (album_id, asset_id)
);
CREATE INDEX IF NOT EXISTS album_assets_asset ON album_assets(asset_id);
CREATE TABLE IF NOT EXISTS shares (
  id INTEGER PRIMARY KEY,
  token TEXT NOT NULL UNIQUE,          -- SHA-256 du jeton de l'URL
  album_id INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
  link BLOB NOT NULL,                  -- lien complet (avec la clé de partage) chiffré par la clé maître
  name BLOB NOT NULL,                  -- nom de l'album chiffré par la clé de partage
  allow_download INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  expires_at INTEGER
);
CREATE TABLE IF NOT EXISTS share_keys (
  share_id INTEGER NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
  asset_id INTEGER NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
  wrapped_fk BLOB NOT NULL,            -- clé du fichier chiffrée par la clé de partage
  PRIMARY KEY (share_id, asset_id)
);
CREATE TABLE IF NOT EXISTS import_seen (
  path TEXT PRIMARY KEY,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL
);
-- import commencé mais pas terminé : si le serveur plante dessus (mémoire…), le fichier est mis de côté
CREATE TABLE IF NOT EXISTS import_running (
  path TEXT PRIMARY KEY,
  attempts INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value BLOB NOT NULL
);
`

// execer : *sql.DB ou *sql.Tx (les mêmes fonctions servent dans et hors d'une transaction).
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// inTx : tout ou rien. La base est ouverte avec _txlock=immediate : la transaction prend le verrou
// d'écriture dès le début (pas d'échec « database is locked » à mi-chemin).
func (a *App) inTx(fn func(tx *sql.Tx) error) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	// même si fn panique (net/http rattrape la panique) : sans cela, le verrou d'écriture resterait pris
	defer tx.Rollback() // sans effet après Commit
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// mustExec : pour les suppressions dont dépend la sécurité (révocation, déconnexion…) : l'erreur remonte.
func mustExec(db execer, q string, args ...any) error {
	_, err := db.Exec(q, args...)
	return err
}

func openDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	var v, tables int
	db.QueryRow(`PRAGMA user_version`).Scan(&v)
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='users'`).Scan(&tables)
	if tables > 0 && v < schemaVersion {
		db.Close()
		return nil, errors.New("base créée par Pellicule 0.4 (photos non chiffrées), incompatible avec le chiffrement " +
			"de bout en bout : déplacer " + path + " et le dossier des photos, puis relancer pour repartir de zéro")
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion))
	return db, nil
}

// serverSecret : secret aléatoire propre à l'installation (faux sels pour les comptes inconnus).
func serverSecret(db *sql.DB) []byte {
	var v []byte
	if db.QueryRow(`SELECT value FROM settings WHERE key='secret'`).Scan(&v) == nil && len(v) == 32 {
		return v
	}
	db.Exec(`INSERT OR IGNORE INTO settings(key, value) VALUES ('secret', ?)`, randomBytes(32))
	db.QueryRow(`SELECT value FROM settings WHERE key='secret'`).Scan(&v)
	return v
}

// Asset : ce que le client reçoit. Les []byte sont des boîtes chiffrées (base64 en JSON) qu'il ouvre lui-même.
type Asset struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	TakenAt    int64  `json:"takenAt"`
	WrappedFK  []byte `json:"fk"`
	Sealed     bool   `json:"sealed,omitempty"` // clé scellée par l'import, à rechiffrer par le client
	Meta       []byte `json:"meta"`
	OrigSize   int64  `json:"size"`
	HasThumb   bool   `json:"thumb"`
	HasPreview bool   `json:"preview"`
	Favorite   bool   `json:"favorite"`
	TrashedAt  *int64 `json:"trashedAt,omitempty"`
}

const assetCols = `a.id, a.kind, a.taken_at, a.wrapped_fk, a.fk_sealed, a.meta, a.orig_size, a.has_thumb, a.has_preview, a.favorite, a.trashed_at`

type scanner interface{ Scan(...any) error }

func scanAsset(s scanner) (Asset, error) {
	var a Asset
	var trashed sql.NullInt64
	err := s.Scan(&a.ID, &a.Kind, &a.TakenAt, &a.WrappedFK, &a.Sealed, &a.Meta, &a.OrigSize, &a.HasThumb, &a.HasPreview, &a.Favorite, &trashed)
	if trashed.Valid {
		a.TrashedAt = &trashed.Int64
	}
	return a, err
}

func queryAssets(db *sql.DB, q string, args ...any) ([]Asset, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		a, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// placeholders renvoie "?,?,?" et les ids convertis en []any.
func placeholders(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// userError : erreur à montrer telle quelle (400) ; les autres erreurs d'une transaction sont du serveur (500).
type userError struct{ error }

// txOK répond à l'échec d'une transaction (conflict : erreur à renvoyer en 409) ; true si elle a réussi.
func txOK(w http.ResponseWriter, err, conflict error) bool {
	var ue userError
	switch {
	case err == nil:
		return true
	case conflict != nil && errors.Is(err, conflict):
		httpError(w, http.StatusConflict, err.Error())
	case errors.As(err, &ue):
		httpError(w, http.StatusBadRequest, ue.Error())
	default:
		serverError(w, err)
	}
	return false
}
