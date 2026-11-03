package main

// Import automatique (FolderSync…) : chaque compte a un dossier <library>/Import/<utilisateur> que le
// téléphone remplit en Wi-Fi. Ces fichiers arrivent en clair sur le partage de la Freebox : le serveur
// les voit donc forcément. Il en fait les miniatures, chiffre le tout avec une clé de fichier neuve,
// scelle cette clé pour la clé PUBLIQUE du compte (il ne peut plus la rouvrir ensuite), puis SUPPRIME
// le fichier en clair. Le client du propriétaire rechiffre ensuite la clé avec sa clé maître.

import (
	"encoding/json"
	"errors"
	"fmt"
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

// Meta : métadonnées d'un fichier, chiffrées par sa clé (même format pour tous les clients).
type Meta struct {
	Name     string  `json:"name"`
	Mime     string  `json:"mime"`
	Width    int     `json:"w,omitempty"`
	Height   int     `json:"h,omitempty"`
	Duration float64 `json:"dur,omitempty"`
	Size     int64   `json:"size"` // taille du clair
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

type importUser struct {
	id   int64
	name string
	pub  []byte
}

func (a *App) scanImports() {
	a.imp.mu.Lock()
	if a.imp.running {
		a.imp.mu.Unlock()
		return
	}
	a.imp.running = true
	a.imp.mu.Unlock()

	var users []importUser
	// compte reconstruit pas encore repris : sa clé publique vient du disque, non vérifiée par l'app (rebuild.go)
	rows, err := a.db.Query(`SELECT id, username, pub_key FROM users WHERE auth_hash <> ''`)
	if err == nil {
		for rows.Next() {
			var u importUser
			rows.Scan(&u.id, &u.name, &u.pub)
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
			// fichiers ordinaires seulement : un lien symbolique déposé sur le partage ferait chiffrer
			// (et livrer au compte) un fichier quelconque de la machine
			if !d.Type().IsRegular() {
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
			// FolderSync renvoie parfois un fichier qu'on a supprimé après import : même chemin et même taille = ignoré
			var seenSize int64
			if a.db.QueryRow(`SELECT size FROM import_seen WHERE path=?`, rel).Scan(&seenSize) == nil && seenSize == info.Size() {
				os.Remove(p)
				return nil
			}
			if !signatureOK(p) {
				a.refuseImport(root, p, rel, "le contenu ne correspond pas à l'extension")
				return nil
			}
			// fichier poison : un import interrompu deux fois (plantage, mémoire épuisée) n'est pas retenté
			var attempts int
			a.db.QueryRow(`SELECT attempts FROM import_running WHERE path=?`, rel).Scan(&attempts)
			if attempts >= maxImportAttempts {
				a.refuseImport(root, p, rel, "le serveur s'est arrêté pendant son traitement")
				return nil
			}
			a.db.Exec(`INSERT INTO import_running(path, attempts) VALUES (?,1)
				ON CONFLICT(path) DO UPDATE SET attempts=attempts+1`, rel)
			err = a.importFile(u, p, kind, mime, info)
			a.db.Exec(`DELETE FROM import_running WHERE path=?`, rel)
			if err != nil {
				log.Printf("import de %s : %v", rel, err)
				return nil
			}
			added++
			a.db.Exec(`INSERT INTO import_seen(path, size, mtime) VALUES (?,?,?)
				ON CONFLICT(path) DO UPDATE SET size=excluded.size, mtime=excluded.mtime`, rel, info.Size(), info.ModTime().UnixMilli())
			return nil
		})
	}

	if added > 0 {
		log.Printf("import : %d fichier(s) chiffré(s)", added)
	}
	a.imp.mu.Lock()
	a.imp.lastScan = time.Now()
	a.imp.lastNew = added
	a.imp.running = false
	a.imp.mu.Unlock()
}

// refusedDir : sous-dossier caché (ignoré par le parcours) où sont mis de côté les fichiers refusés,
// pour que le propriétaire les retrouve sur le partage au lieu qu'ils soient retentés sans fin.
const refusedDir = ".refusés"

// 2 et non 1 : un arrêt normal du service (mise à jour) pendant un import ne doit pas suffire à refuser le fichier
const maxImportAttempts = 2

func (a *App) refuseImport(root, path, rel, why string) {
	dir := filepath.Join(root, refusedDir)
	os.MkdirAll(dir, 0o755)
	base := filepath.Base(path)
	dst := filepath.Join(dir, base)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			break
		}
		ext := filepath.Ext(base)
		dst = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), i, ext))
	}
	if err := os.Rename(path, dst); err != nil {
		log.Printf("import : %s refusé (%s), impossible de le déplacer : %v", rel, why, err)
		return
	}
	a.db.Exec(`DELETE FROM import_running WHERE path=?`, rel)
	log.Printf("import : %s refusé (%s), déplacé dans %s", rel, why, refusedDir)
}

// refusedCount : nombre de fichiers mis de côté pour ce compte (affiché dans l'app).
func (a *App) refusedCount(username string) int {
	entries, _ := os.ReadDir(filepath.Join(a.libDir, importDir, username, refusedDir))
	return len(entries)
}

// importFile chiffre un fichier du dossier d'import pour la clé publique du compte, puis l'efface.
func (a *App) importFile(u importUser, path, kind, mime string, info fs.FileInfo) error {
	m := a.probe(path, kind, mime)
	taken := m.taken
	if taken.IsZero() {
		taken = wallClock(info.ModTime())
	}

	fk := randomBytes(KeySize)
	sealedFK, err := SealToPublic(u.pub, fk)
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(Meta{Name: filepath.Base(path), Mime: mime, Width: m.width, Height: m.height, Duration: m.duration, Size: info.Size()})
	encSize := StreamSize(info.Size())
	res, err := a.db.Exec(`INSERT INTO assets(owner_id, kind, taken_at, wrapped_fk, fk_sealed, meta, orig_size, created_at)
		VALUES (?,?,?,?,1,?,?,?)`, u.id, kind, taken.UnixMilli(), sealedFK, Seal(fk, meta, AADMeta), encSize, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	a.writeAssetFiles(id)
	fail := func(err error) error {
		a.purge([]purgeItem{{u.id, id}})
		return err
	}

	// original chiffré
	if err := a.encryptFileTo(path, fk, a.blobPath(u.id, id, "o")); err != nil {
		return fail(err)
	}

	// miniatures : générées en clair dans .tmp, chiffrées, effacées
	// sur le disque de la VM (PrivateTmp), pas sur le partage : rien en clair de plus côté Freebox
	tmp, err := os.MkdirTemp("", "pellicule-imp-*")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(tmp)
	small, preview := filepath.Join(tmp, "s.jpg"), filepath.Join(tmp, "m.jpg")
	hasThumb, hasPreview := false, false
	if err := a.makeThumbs(path, kind, small, preview); err != nil {
		log.Printf("miniature de %s : %v", filepath.Base(path), err)
	}
	if b, err := os.ReadFile(small); err == nil && len(b) > 0 {
		hasThumb = writeFileAtomic(a.blobPath(u.id, id, "s"), Seal(fk, b, AADThumb)) == nil
	}
	if b, err := os.ReadFile(preview); err == nil && len(b) > 0 {
		hasPreview = writeFileAtomic(a.blobPath(u.id, id, "m"), Seal(fk, b, AADPreview)) == nil
	}

	if _, err := a.db.Exec(`UPDATE assets SET uploaded=orig_size, complete=1, has_thumb=?, has_preview=? WHERE id=?`, hasThumb, hasPreview, id); err != nil {
		return fail(err)
	}
	// le fichier en clair n'a plus rien à faire sur le partage
	if err := os.Remove(path); err != nil {
		log.Printf("import : impossible d'effacer %s : %v", path, err)
	}
	return nil
}

func (a *App) encryptFileTo(src string, fk []byte, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// le fichier a pu être remplacé par un lien entre le parcours et l'ouverture
	if st, err := in.Stat(); err != nil || !st.Mode().IsRegular() {
		return errors.New("pas un fichier ordinaire")
	}
	if lst, err := os.Lstat(src); err != nil || !lst.Mode().IsRegular() {
		return errors.New("pas un fichier ordinaire")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	err = EncryptStream(fk, in, out)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("chiffrement : %w", err)
	}
	return os.Rename(tmp, dst)
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
	return map[string]any{
		"folder":   filepath.Base(a.libDir) + "/" + a.importRel(u.Username),
		"lastScan": last,
		"running":  a.imp.running,
		"lastNew":  a.imp.lastNew,
		"refused":  a.refusedCount(u.Username),
	}
}

var errReserved = errors.New("ce nom est réservé")
