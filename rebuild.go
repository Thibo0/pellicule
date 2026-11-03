package main

// pellicule rebuild : la VM est perdue mais le disque des photos est intact. On reconstruit la base à partir
// des fichiers autonomes (voir files.go). Les comptes reviennent SANS mot de passe : chacun les reprend avec
// sa clé de secours (dans l'app : « J'ai oublié mon mot de passe »), qui ouvre la clé maître. Sans clé de
// secours, les photos d'un compte restent illisibles : c'est voulu, c'est ce qui rend le disque inattaquable.
//
// Le disque est accessible à d'autres (partage Windows) : rien n'est cru sur parole. La liste des comptes et
// de leurs administrateurs est affichée et doit être confirmée ; aucun téléphone, session ni invitation n'est
// recréé ; la clé publique du compte est revérifiée par l'app à la reprise.

import (
	"bufio"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type rebuildAccount struct {
	id     int64
	f      accountFile
	assets map[int64]assetFile
	albums []albumFile
	have   map[int64][3]bool // original, aperçu, miniature présents
	bad    int
	dup    int
}

var (
	userDirRe  = regexp.MustCompile(`^u([1-9][0-9]{0,17})$`)
	keyFileRe  = regexp.MustCompile(`^([1-9][0-9]{0,17})\.k$`)
	hexTagRe   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	maxKeyFile = int64(64 << 10)
)

func readSmallJSON(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || st.Size() > maxKeyFile {
		return errors.New("fichier invalide")
	}
	return json.NewDecoder(f).Decode(v)
}

func (f *accountFile) valid() bool {
	if f.Format != filesFormat || !legacyUsernameOK(f.Username) || len(f.RecoveryMK) != boxSize ||
		!hexTagRe.MatchString(f.RecoveryAuth) || len(f.WrappedPriv) < NonceSize+TagSize+32 || len(f.WrappedPriv) > 1024 {
		return false
	}
	_, err := ecdh.P256().NewPublicKey(f.Pub)
	return err == nil
}

func (f *assetFile) valid() bool {
	fkOK := (!f.Sealed && len(f.WrappedFK) == boxSize) || (f.Sealed && len(f.WrappedFK) > boxSize && len(f.WrappedFK) < 512)
	return f.Format == filesFormat && (f.Kind == "image" || f.Kind == "video") && fkOK &&
		len(f.Meta) >= NonceSize+TagSize && len(f.Meta) <= 16<<10 && f.OrigSize > 0 &&
		(f.DedupTag == "" || validTag(f.DedupTag)) && (f.DeviceTag == "" || validTag(f.DeviceTag))
}

// scanLibrary lit les fichiers autonomes de la photothèque.
func scanLibrary(lib string) ([]*rebuildAccount, error) {
	entries, err := os.ReadDir(lib)
	if err != nil {
		return nil, err
	}
	var out []*rebuildAccount
	for _, e := range entries {
		m := userDirRe.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		id, _ := strconv.ParseInt(m[1], 10, 64)
		acc := &rebuildAccount{id: id, assets: map[int64]assetFile{}, have: map[int64][3]bool{}}
		dir := filepath.Join(lib, e.Name())
		if readSmallJSON(filepath.Join(dir, "compte.json"), &acc.f) != nil || !acc.f.valid() {
			fmt.Printf("  %s : compte.json absent ou invalide, dossier ignoré\n", e.Name())
			continue
		}
		buckets, _ := os.ReadDir(dir)
		for _, b := range buckets {
			if !b.IsDir() {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(dir, b.Name()))
			for _, kf := range files {
				km := keyFileRe.FindStringSubmatch(kf.Name())
				if km == nil {
					continue
				}
				aid, _ := strconv.ParseInt(km[1], 10, 64)
				if strconv.FormatInt(aid/1000, 10) != b.Name() {
					acc.bad++
					continue
				}
				var af assetFile
				base := filepath.Join(dir, b.Name(), km[1])
				if readSmallJSON(base+".k", &af) != nil || !af.valid() {
					acc.bad++
					continue
				}
				st, err := os.Lstat(base + ".o")
				if err != nil || !st.Mode().IsRegular() || st.Size() != af.OrigSize {
					continue // envoi inachevé, ou original supprimé : pas de photo à reprendre
				}
				_, errM := os.Lstat(base + ".m")
				_, errS := os.Lstat(base + ".s")
				acc.assets[aid] = af
				acc.have[aid] = [3]bool{true, errM == nil, errS == nil}
			}
		}
		var al albumsFile
		if readSmallJSONLarge(filepath.Join(dir, "albums.json"), &al) == nil && al.Format == filesFormat {
			acc.albums = al.Albums
		}
		out = append(out, acc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

// albums.json peut être plus gros qu'un fichier de clé (beaucoup de photos dans les albums)
func readSmallJSONLarge(p string, v any) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return errors.New("fichier invalide")
	}
	return json.NewDecoder(f).Decode(v)
}

// rebuild : confirmation demandée sur in (« RECONSTRUIRE »), compte rendu sur out.
func (a *App) rebuild(in io.Reader, out io.Writer) error {
	if a.userCount() > 0 {
		return errors.New("la base contient déjà des comptes : rien à reconstruire")
	}
	accounts, err := scanLibrary(a.libDir)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return fmt.Errorf("aucun compte Pellicule trouvé dans %s", a.libDir)
	}
	// un même n° de photo ou d'album dans deux comptes (fichiers copiés) : on garde le premier, on le signale
	assetSeen, albumSeen := map[int64]bool{}, map[int64]bool{}
	for _, acc := range accounts {
		for id := range acc.assets {
			if assetSeen[id] {
				delete(acc.assets, id)
				acc.dup++
			}
			assetSeen[id] = true
		}
		kept := acc.albums[:0]
		for _, al := range acc.albums {
			if albumSeen[al.ID] {
				acc.dup++
				continue
			}
			albumSeen[al.ID] = true
			kept = append(kept, al)
		}
		acc.albums = kept
	}
	seen := map[string]bool{}
	fmt.Fprintf(out, "\nComptes trouvés sur le disque (%s) :\n", a.libDir)
	for _, acc := range accounts {
		name := foldUsername(acc.f.Username)
		if seen[name] {
			return fmt.Errorf("le nom %q apparaît deux fois : fichiers modifiés ? reconstruction refusée", acc.f.Username)
		}
		seen[name] = true
		role := ""
		if acc.f.Admin {
			role = "  (administrateur)"
		}
		if validUsername(acc.f.Username) != nil {
			role += "  ⚠ nom hors des règles actuelles : vérifie qu'il ne ressemble pas à un autre"
		}
		fmt.Fprintf(out, "  - %-20s %6d photo(s) et vidéo(s), %d album(s)%s\n", acc.f.Username, len(acc.assets), len(acc.albums), role)
		if acc.bad > 0 {
			fmt.Fprintf(out, "      %d fichier(s) illisible(s) ignoré(s)\n", acc.bad)
		}
		if acc.dup > 0 {
			fmt.Fprintf(out, "      %d élément(s) déjà présent(s) dans un autre compte ignoré(s) (fichiers copiés ?)\n", acc.dup)
		}
	}
	fmt.Fprint(out, `
Vérifie cette liste : chaque nom doit être quelqu'un de la maison, et l'administrateur la bonne personne.
Un compte que tu ne connais pas = quelqu'un a déposé des fichiers sur le disque : arrête-toi là.

Après la reconstruction, chaque personne reprend son compte dans l'app avec SA CLÉ DE SECOURS
(« Mot de passe oublié / serveur réinstallé ») et choisit un nouveau mot de passe. Sans clé de secours, les photos
de ce compte restent illisibles : personne ne peut les récupérer.

Tape RECONSTRUIRE pour continuer : `)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if strings.TrimSpace(line) != "RECONSTRUIRE" {
		return errors.New("annulé")
	}

	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	total := 0
	for _, acc := range accounts {
		f := acc.f
		// ni mot de passe ni clé maître chiffrée par mot de passe : connexion impossible avant la reprise
		if _, err := tx.Exec(`INSERT INTO users(id, username, auth_hash, salt, wrapped_mk, recovery_mk, recovery_auth, pub_key, wrapped_priv, is_admin, created_at)
			VALUES (?,?,'',?,x'',?,?,?,?,?,?)`, acc.id, strings.TrimSpace(f.Username), randomBytes(SaltSize), f.RecoveryMK, f.RecoveryAuth,
			f.Pub, f.WrappedPriv, f.Admin, f.CreatedAt); err != nil {
			return err
		}
		ids := make([]int64, 0, len(acc.assets))
		for id := range acc.assets {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			af, h := acc.assets[id], acc.have[id]
			var dedup any
			if af.DedupTag != "" {
				dedup = af.DedupTag
			}
			var trashed any
			if af.TrashedAt != nil {
				trashed = *af.TrashedAt
			}
			if _, err := tx.Exec(`INSERT INTO assets(id, owner_id, kind, taken_at, wrapped_fk, fk_sealed, meta, dedup_tag, device_tag, orig_size,
				uploaded, complete, has_thumb, has_preview, favorite, trashed_at, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,1,?,?,?,?,?)`,
				id, acc.id, af.Kind, af.TakenAt, af.WrappedFK, af.Sealed, af.Meta, dedup, af.DeviceTag, af.OrigSize,
				af.OrigSize, h[2], h[1], af.Favorite, trashed, af.CreatedAt); err != nil {
				return fmt.Errorf("élément #%d : %w", id, err)
			}
		}
		total += len(ids)
		for _, al := range acc.albums {
			if len(al.Name) < NonceSize+TagSize || len(al.Name) > 4096 {
				continue
			}
			var cover any
			if al.Cover != nil {
				if _, ok := acc.assets[*al.Cover]; ok {
					cover = *al.Cover
				}
			}
			if _, err := tx.Exec(`INSERT INTO albums(id, owner_id, name, cover_asset_id, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
				al.ID, acc.id, al.Name, cover, al.CreatedAt, al.UpdatedAt); err != nil {
				return fmt.Errorf("album #%d : %w", al.ID, err)
			}
			for i, aid := range al.Assets {
				if _, ok := acc.assets[aid]; !ok {
					continue
				}
				if _, err := tx.Exec(`INSERT OR IGNORE INTO album_assets(album_id, asset_id, added_at) VALUES (?,?,?)`,
					al.ID, aid, al.UpdatedAt+int64(i)); err != nil {
					return err
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\nBase reconstruite : %d compte(s), %d photo(s) et vidéo(s).\n", len(accounts), total)
	return nil
}

// needsRecovery : compte reconstruit, pas encore repris avec la clé de secours.
func needsRecovery(hash string) bool { return hash == "" }
