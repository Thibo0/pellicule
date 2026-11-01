package main

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS assets (
  id INTEGER PRIMARY KEY,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  filename TEXT NOT NULL,
  path TEXT NOT NULL,
  mime TEXT NOT NULL,
  size INTEGER NOT NULL,
  sha256 TEXT NOT NULL,
  device_key TEXT NOT NULL DEFAULT '',
  taken_at INTEGER NOT NULL,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  duration REAL NOT NULL DEFAULT 0,
  favorite INTEGER NOT NULL DEFAULT 0,
  trashed_at INTEGER,
  thumb_status INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  UNIQUE(owner_id, sha256)
);
CREATE INDEX IF NOT EXISTS assets_timeline ON assets(owner_id, trashed_at, taken_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS assets_device ON assets(owner_id, device_key);
CREATE INDEX IF NOT EXISTS assets_pending ON assets(thumb_status);
CREATE TABLE IF NOT EXISTS albums (
  id INTEGER PRIMARY KEY,
  owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
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
CREATE TABLE IF NOT EXISTS import_seen (
  path TEXT PRIMARY KEY,
  size INTEGER NOT NULL,
  mtime INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS shares (
  token TEXT PRIMARY KEY,
  album_id INTEGER NOT NULL REFERENCES albums(id) ON DELETE CASCADE,
  allow_download INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  expires_at INTEGER
);
`

func openDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return db, nil
}

// Asset est la forme publique d'une photo/vidéo.
type Asset struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"`
	Filename  string  `json:"filename"`
	Path      string  `json:"-"`
	Mime      string  `json:"mime"`
	Size      int64   `json:"size"`
	TakenAt   int64   `json:"takenAt"`
	Width     int     `json:"width"`
	Height    int     `json:"height"`
	Duration  float64 `json:"duration"`
	Favorite  bool    `json:"favorite"`
	TrashedAt *int64  `json:"trashedAt,omitempty"`
	Thumb     int     `json:"thumb"` // 0 en attente, 1 prête, 2 échec
}

const assetCols = `a.id, a.kind, a.filename, a.path, a.mime, a.size, a.taken_at, a.width, a.height, a.duration, a.favorite, a.trashed_at, a.thumb_status`

type scanner interface{ Scan(...any) error }

func scanAsset(s scanner) (Asset, error) {
	var a Asset
	var trashed sql.NullInt64
	err := s.Scan(&a.ID, &a.Kind, &a.Filename, &a.Path, &a.Mime, &a.Size, &a.TakenAt, &a.Width, &a.Height, &a.Duration, &a.Favorite, &trashed, &a.Thumb)
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
