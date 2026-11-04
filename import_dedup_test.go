package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// importOne dépose un fichier dans le dossier d'import, lance l'import et renvoie l'élément scellé.
func importOne(t *testing.T, app *App, c *client, name string, content []byte) (int64, []byte) {
	t.Helper()
	dir := filepath.Join(app.libDir, importDir, "alice")
	os.MkdirAll(dir, 0o755)
	src := filepath.Join(dir, name)
	os.WriteFile(src, content, 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(src, old, old)
	app.scanImports()
	_, tl, _ := c.do("GET", "/api/timeline?sealed=1", nil)
	items := tl["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("%d élément(s) scellé(s)", len(items))
	}
	m := items[0].(map[string]any)
	return int64(m["id"].(float64)), b64field(m, "fk")
}

// clientRewrap fait ce que fait le client : ouvre l'enveloppe, rechiffre la clé, calcule le dedupTag.
func clientRewrap(t *testing.T, c *client, id int64, box []byte) map[string]any {
	t.Helper()
	opened, err := OpenFromPublic(c.priv, box)
	if err != nil || len(opened) != 2*KeySize {
		t.Fatalf("enveloppe : %v (%d octets)", err, len(opened))
	}
	_, out, _ := c.do("POST", "/api/assets/keys", map[string]any{"keys": []map[string]any{{
		"id": id, "fk": Seal(c.mk, opened[:KeySize], AADFileKey),
		"dedupTag": hex.EncodeToString(DedupTag(c.mk, opened[KeySize:])),
	}}})
	return out
}

// Photo sauvegardée par l'app, puis arrivée aussi par FolderSync : la copie importée disparaît au rechiffrement.
func TestImportDejaEnvoyeParLAppEstDedoublonne(t *testing.T) {
	app, _, c := setupAdmin(t)
	content := []byte("\xff\xd8\xff\xe0 même photo, deux chemins")
	c.upload("IMG_1.jpg", content)
	id, box := importOne(t, app, c, "IMG_1.jpg", content)
	if out := clientRewrap(t, c, id, box); out["duplicates"].(float64) != 1 {
		t.Fatalf("doublon non détecté : %v", out)
	}
	if n := countAssets(app); n != 1 {
		t.Fatalf("%d éléments au lieu de 1", n)
	}
	if m, _ := filepath.Glob(filepath.Join(app.libDir, "u*", "*", itoa(id)+".*")); len(m) != 0 {
		t.Fatalf("fichiers de la copie importée restés : %v", m)
	}
}

// Dans l'autre sens : importée d'abord, puis envoyée par l'app → reconnue comme déjà sauvegardée.
func TestEnvoiDepuisLAppApresImportEstUnDoublon(t *testing.T) {
	app, _, c := setupAdmin(t)
	content := []byte("\xff\xd8\xff\xe0 importée puis envoyée")
	id, box := importOne(t, app, c, "IMG_2.jpg", content)
	if out := clientRewrap(t, c, id, box); out["duplicates"].(float64) != 0 {
		t.Fatalf("faux doublon : %v", out)
	}
	if _, status := c.upload("IMG_2.jpg", content); status != "duplicate" {
		t.Fatalf("renvoyée depuis l'app : %s", status)
	}
	if n := countAssets(app); n != 1 {
		t.Fatalf("%d éléments au lieu de 1", n)
	}
	// le dedupTag est aussi dans le fichier autonome (.k), pour survivre à une reconstruction
	k, _ := os.ReadFile(app.blobPath(1, id, "k"))
	if !bytes.Contains(k, []byte(`"dedup"`)) {
		t.Fatalf("dedupTag absent du fichier .k")
	}
}
