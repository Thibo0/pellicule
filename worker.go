package main

import (
	"log"
	"os"
	"path/filepath"
	"time"
)

func (a *App) wake() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// worker traite les photos une par une : avec 1 Go de RAM, pas de parallélisme.
func (a *App) worker() {
	for {
		var id int64
		var path, kind, mime string
		err := a.db.QueryRow(`SELECT id, path, kind, mime FROM assets WHERE thumb_status=0 ORDER BY id DESC LIMIT 1`).Scan(&id, &path, &kind, &mime)
		if err != nil {
			select {
			case <-a.kick:
			case <-time.After(time.Minute):
			}
			continue
		}
		a.process(id, filepath.Join(a.libDir, filepath.FromSlash(path)), kind, mime)
	}
}

func (a *App) process(id int64, src, kind, mime string) {
	start := time.Now()
	m := a.probe(src, kind, mime)
	if !m.taken.IsZero() {
		a.db.Exec(`UPDATE assets SET taken_at=? WHERE id=?`, m.taken.UnixMilli(), id)
		src = a.refile(id, src, m.taken)
	}
	a.db.Exec(`UPDATE assets SET width=?, height=?, duration=? WHERE id=?`, m.width, m.height, m.duration, id)

	status := 1
	if err := a.makeThumbs(id, src, kind); err != nil {
		log.Printf("miniature #%d (%s) : %v", id, filepath.Base(src), err)
		status = 2
	}
	a.db.Exec(`UPDATE assets SET thumb_status=? WHERE id=?`, status, id)
	if d := time.Since(start); d > 10*time.Second {
		log.Printf("miniature #%d : %s", id, d.Round(time.Second))
	}
}

// refile range le fichier dans <utilisateur>/<année>/<mois> de sa vraie date de prise de vue
// (à l'envoi, seule la date des JPEG est connue ; HEIC et vidéos sont datés ici).
func (a *App) refile(id int64, src string, taken time.Time) string {
	var username, rel string
	if err := a.db.QueryRow(`SELECT u.username, a.path FROM assets a JOIN users u ON u.id=a.owner_id WHERE a.id=?`, id).Scan(&username, &rel); err != nil {
		return src
	}
	if isImported(rel) {
		return src // laissé à sa place pour l'appli de synchro du téléphone
	}
	wantDir := filepath.Join(username, taken.Format("2006"), taken.Format("01"))
	if filepath.Dir(filepath.FromSlash(rel)) == wantDir {
		return src
	}
	if err := os.MkdirAll(filepath.Join(a.libDir, wantDir), 0o755); err != nil {
		return src
	}
	newRel, err := uniquePath(a.libDir, wantDir, filepath.Base(rel))
	if err != nil {
		return src
	}
	dst := filepath.Join(a.libDir, newRel)
	if err := os.Rename(src, dst); err != nil {
		log.Printf("rangement de %s : %v", rel, err)
		return src
	}
	if _, err := a.db.Exec(`UPDATE assets SET path=? WHERE id=?`, filepath.ToSlash(newRel), id); err != nil {
		os.Rename(dst, src)
		return src
	}
	os.Remove(filepath.Dir(src)) // supprime le dossier s'il est devenu vide
	return dst
}

// janitor : corbeille vidée après 30 jours, sessions expirées, fichiers temporaires orphelins.
func (a *App) janitor() {
	for {
		limit := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
		if items, err := queryAssets(a.db, `SELECT `+assetCols+` FROM assets a WHERE a.trashed_at IS NOT NULL AND a.trashed_at < ?`, limit); err == nil && len(items) > 0 {
			log.Printf("corbeille : suppression définitive de %d élément(s) de plus de 30 jours", len(items))
			a.purge(items)
		}
		a.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UnixMilli())
		a.db.Exec(`DELETE FROM shares WHERE expires_at IS NOT NULL AND expires_at < ?`, time.Now().UnixMilli())

		tmpDir := filepath.Join(a.libDir, ".tmp")
		if entries, err := os.ReadDir(tmpDir); err == nil {
			for _, e := range entries {
				if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
					os.Remove(filepath.Join(tmpDir, e.Name()))
				}
			}
		}
		time.Sleep(6 * time.Hour)
	}
}
