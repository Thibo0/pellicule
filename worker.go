package main

import (
	"os"
	"path/filepath"
	"time"
)

// janitor : corbeille vidée après 30 jours, envois abandonnés, jetons expirés, fichiers temporaires.
func (a *App) janitor() {
	for round := 0; ; round++ {
		if round%4 == 0 { // au démarrage, puis une fois par jour
			a.syncFiles()
		}
		now := time.Now()
		a.purgeWhere(`trashed_at IS NOT NULL AND trashed_at < ?`, now.Add(-30*24*time.Hour).UnixMilli())
		a.purgeWhere(`complete = 0 AND created_at < ?`, now.Add(-staleUploads).UnixMilli())
		ms := now.UnixMilli()
		a.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, ms)
		a.db.Exec(`DELETE FROM invites WHERE expires_at < ?`, ms)
		a.db.Exec(`DELETE FROM pair_codes WHERE expires_at < ?`, ms)
		a.db.Exec(`DELETE FROM shares WHERE expires_at IS NOT NULL AND expires_at < ?`, ms)

		tmpDir := filepath.Join(a.libDir, ".tmp")
		if entries, err := os.ReadDir(tmpDir); err == nil {
			for _, e := range entries {
				if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
					os.RemoveAll(filepath.Join(tmpDir, e.Name()))
				}
			}
		}
		time.Sleep(6 * time.Hour)
	}
}
