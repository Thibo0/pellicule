package main

// Import automatique : chaque utilisateur a un dossier <library>/Import/<utilisateur> que le téléphone
// remplit tout seul (FolderSync en Wi-Fi, etc.). Les fichiers y sont indexés sur place, sans être
// déplacés : l'appli de synchro les retrouve là où elle les a mis et ne les renvoie pas.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	importDir      = "Import"
	importInterval = 3 * time.Minute
	importSettle   = 2 * time.Minute // un fichier modifié récemment est peut-être encore en cours de copie
)

type importState struct {
	mu       sync.Mutex
	lastScan time.Time
	lastNew  int
	running  bool
}

func (a *App) importRel(username string) string {
	return filepath.ToSlash(filepath.Join(importDir, username))
}

func (a *App) importer() {
	for {
		a.scanImports()
		select {
		case <-a.scanKick:
		case <-time.After(importInterval):
		}
	}
}

func (a *App) scanImports() {
	a.imp.mu.Lock()
	if a.imp.running {
		a.imp.mu.Unlock()
		return
	}
	a.imp.running = true
	a.imp.mu.Unlock()

	type user struct {
		id   int64
		name string
	}
	var users []user
	rows, err := a.db.Query(`SELECT id, username FROM users`)
	if err == nil {
		for rows.Next() {
			var u user
			rows.Scan(&u.id, &u.name)
			users = append(users, u)
		}
		rows.Close()
	}

	added := 0
	for _, u := range users {
		root := filepath.Join(a.libDir, importDir, u.name)
		if err := os.MkdirAll(root, 0o755); err != nil {
			log.Printf("import : %v", err)
			continue
		}
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() && p != root {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			kind, mime, ok := mediaType(d.Name())
			if !ok {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Size() == 0 || time.Since(info.ModTime()) < importSettle {
				return nil
			}
			rel, err := filepath.Rel(a.libDir, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			size, mtime := info.Size(), info.ModTime().UnixMilli()

			var seenSize, seenMtime int64
			if a.db.QueryRow(`SELECT size, mtime FROM import_seen WHERE path=?`, rel).Scan(&seenSize, &seenMtime) == nil &&
				seenSize == size && seenMtime == mtime {
				return nil
			}
			created, err := a.importFile(u.id, p, rel, kind, mime, size, info.ModTime())
			if err != nil {
				log.Printf("import de %s : %v", rel, err)
				return nil
			}
			if created {
				added++
			}
			// on garde la trace même après suppression dans Pellicule : un fichier renvoyé à l'identique reste ignoré
			a.db.Exec(`INSERT INTO import_seen(path, size, mtime) VALUES (?,?,?)
				ON CONFLICT(path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime`, rel, size, mtime)
			return nil
		})
	}

	if added > 0 {
		log.Printf("import : %d nouveau(x) fichier(s)", added)
		a.wake()
	}
	a.imp.mu.Lock()
	a.imp.lastScan = time.Now()
	a.imp.lastNew = added
	a.imp.running = false
	a.imp.mu.Unlock()
}

// importFile indexe un fichier du dossier d'import ; false si c'est un doublon.
func (a *App) importFile(ownerID int64, path, rel, kind, mime string, size int64, mtime time.Time) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return false, err
	}
	sum := hex.EncodeToString(h.Sum(nil))

	var existing int64
	err = a.db.QueryRow(`SELECT id FROM assets WHERE owner_id=? AND sha256=?`, ownerID, sum).Scan(&existing)
	if err == nil {
		return false, nil
	}

	taken := quickTakenAt(path, mime)
	if taken.IsZero() {
		taken = wallClock(mtime)
	}
	name := filepath.Base(path)
	_, err = a.db.Exec(`INSERT INTO assets(owner_id, kind, filename, path, mime, size, sha256, device_key, taken_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, ownerID, kind, name, rel, mime, size, sum,
		fmt.Sprintf("%s|%d", strings.ToLower(name), size), taken.UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func isImported(rel string) bool {
	return strings.HasPrefix(filepath.ToSlash(rel), importDir+"/")
}

func (a *App) handleImportScan(w http.ResponseWriter, r *http.Request, u *User) {
	select {
	case a.scanKick <- struct{}{}:
	default:
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) importStatus(u *User) map[string]any {
	a.imp.mu.Lock()
	defer a.imp.mu.Unlock()
	var last any
	if !a.imp.lastScan.IsZero() {
		last = a.imp.lastScan.UnixMilli()
	}
	var count int64
	a.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE owner_id=? AND path LIKE ?`, u.ID, importDir+"/"+u.Username+"/%").Scan(&count)
	return map[string]any{
		"folder":   filepath.Base(a.libDir) + "/" + a.importRel(u.Username),
		"lastScan": last,
		"running":  a.imp.running,
		"count":    count,
	}
}

var errReserved = errors.New("ce nom est réservé")
