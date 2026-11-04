package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newPendingAsset crée une photo dont l'original reste à envoyer ; renvoie son id, son flux chiffré et sa clé.
func newPendingAsset(t *testing.T, c *client, content []byte) (string, []byte, []byte) {
	t.Helper()
	fk := randomBytes(KeySize)
	var enc bytes.Buffer
	EncryptStream(fk, bytes.NewReader(content), &enc)
	h := NewContentHasher()
	h.Write(content)
	code, out, raw := c.do("POST", "/api/assets", map[string]any{"kind": "image", "takenAt": 1,
		"fk": Seal(c.mk, fk, AADFileKey), "meta": Seal(fk, []byte("{}"), AADMeta),
		"dedupTag": strings.Repeat("cd", 32), "size": enc.Len()})
	if code != 200 {
		t.Fatalf("création : %d %s", code, raw)
	}
	return itoa(int64(out["id"].(float64))), enc.Bytes(), fk
}

func partFile(t *testing.T, app *App) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(app.libDir, "u*", "*", "*.o.part"))
	if len(m) != 1 {
		t.Fatalf("fichier .part : %v", m)
	}
	return m[0]
}

// Scénario de la revue : le téléphone sort du Wi-Fi en plein envoi, la vieille requête reste bloquée côté serveur,
// le téléphone revient et renvoie le même morceau. Avant, la vieille requête finissait par échouer et tronquait le
// fichier que la nouvelle venait de compléter : original indéchiffrable, marqué comme sauvegardé.
func TestEnvoiConcurrentNeCorrompPasLOriginal(t *testing.T) {
	app, _, c := setupAdmin(t)
	content := bytes.Repeat([]byte("photo "), 300_000) // ≈ 1,8 Mo : deux blocs chiffrés
	id, enc, fk := newPendingAsset(t, c, content)

	// 1. requête bloquée : quelques octets, puis plus rien
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.CloseWithError(errors.New("fin du test")) }) // sinon srv.Close attend la requête bloquée
	req, _ := http.NewRequest("PUT", c.base+"/api/assets/"+id+"/original?offset=0", pr)
	req.Header.Set("X-Pellicule", "1")
	req.ContentLength = int64(len(enc))
	done := make(chan struct{})
	go func() {
		if resp, err := c.http.Do(req); err == nil {
			resp.Body.Close()
		}
		close(done)
	}()
	pw.Write(enc[:100_000])
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m, _ := filepath.Glob(filepath.Join(app.libDir, "u*", "*", "*.o.part")); len(m) == 1 {
			if st, err := os.Stat(m[0]); err == nil && st.Size() > 0 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("la première requête n'a rien écrit")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 2. le téléphone revient pendant que la vieille requête est encore là : refusé, pas de double écriture
	second, _ := http.NewRequest("PUT", c.base+"/api/assets/"+id+"/original?offset=0", bytes.NewReader(enc))
	second.Header.Set("X-Pellicule", "1")
	quick := *c.http
	quick.Timeout = 10 * time.Second
	resp, err := quick.Do(second)
	if err != nil {
		t.Fatalf("deuxième envoi simultané bloqué : %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("deuxième envoi simultané accepté : %d", resp.StatusCode)
	}

	// 3. la vieille requête finit par mourir ; l'envoi reprend et se termine
	pw.CloseWithError(errors.New("Wi-Fi perdu"))
	<-done
	_, out, _ := c.do("POST", "/api/assets", map[string]any{"kind": "image", "takenAt": 1,
		"fk": Seal(c.mk, fk, AADFileKey), "meta": Seal(fk, []byte("{}"), AADMeta),
		"dedupTag": strings.Repeat("cd", 32), "size": len(enc)})
	off := int64(out["uploaded"].(float64))
	if code, _, raw := c.do("PUT", "/api/assets/"+id+"/original?offset="+itoa(off), enc[off:]); code != 200 {
		t.Fatalf("reprise : %d %s", code, raw)
	}
	if code, _, raw := c.do("POST", "/api/assets/"+id+"/complete", nil); code != 200 {
		t.Fatalf("fin d'envoi : %d %s", code, raw)
	}
	assertOriginal(t, app, fk, content)
}

// Si le fichier a perdu des octets que la base croyait reçus, le serveur ne comble pas le trou avec des zéros :
// il fait reprendre le client plus tôt, et « complete » refuse un fichier de la mauvaise taille.
func TestFichierPartielPlusCourtQueLaBase(t *testing.T) {
	app, _, c := setupAdmin(t)
	content := bytes.Repeat([]byte("video "), 400_000)
	id, enc, fk := newPendingAsset(t, c, content)
	if code, _, _ := c.do("PUT", "/api/assets/"+id+"/original?offset=0", enc[:1_500_000]); code != 200 {
		t.Fatal("premier morceau refusé")
	}
	part := partFile(t, app)
	os.Truncate(part, 1_200_000) // octets perdus après coup

	code, _, _ := c.do("PUT", "/api/assets/"+id+"/original?offset=1500000", enc[1_500_000:])
	if code != http.StatusConflict {
		t.Fatalf("trou comblé avec des zéros : %d", code)
	}
	var uploaded int64
	app.db.QueryRow(`SELECT uploaded FROM assets WHERE id=?`, id).Scan(&uploaded)
	if uploaded != alignUpload(1_200_000) || uploaded > 1_200_000 {
		t.Fatalf("position après correction : %d", uploaded)
	}
	if code, _, raw := c.do("PUT", "/api/assets/"+id+"/original?offset="+itoa(uploaded), enc[uploaded:]); code != 200 {
		t.Fatalf("reprise : %d %s", code, raw)
	}
	if code, _, raw := c.do("POST", "/api/assets/"+id+"/complete", nil); code != 200 {
		t.Fatalf("fin d'envoi : %d %s", code, raw)
	}
	assertOriginal(t, app, fk, content)
}

func TestFinDEnvoiVerifieLaTailleDuFichier(t *testing.T) {
	app, _, c := setupAdmin(t)
	id, enc, _ := newPendingAsset(t, c, []byte("contenu court"))
	c.do("PUT", "/api/assets/"+id+"/original?offset=0", enc)
	os.Truncate(partFile(t, app), int64(len(enc)-3))
	if code, _, _ := c.do("POST", "/api/assets/"+id+"/complete", nil); code != http.StatusConflict {
		t.Fatal("fichier tronqué accepté comme complet")
	}
	var uploaded int64
	app.db.QueryRow(`SELECT uploaded FROM assets WHERE id=?`, id).Scan(&uploaded)
	if uploaded != 0 {
		t.Fatalf("l'envoi devrait repartir de zéro : %d", uploaded)
	}
}

func assertOriginal(t *testing.T, app *App, fk, want []byte) {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(app.libDir, "u*", "*", "*.o"))
	if len(m) != 1 {
		t.Fatalf("original : %v", m)
	}
	f, _ := os.Open(m[0])
	defer f.Close()
	var got bytes.Buffer
	if err := DecryptStream(fk, f, &got); err != nil {
		t.Fatalf("original indéchiffrable : %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("original différent")
	}
}

// Une grosse sélection (favoris, corbeille…) écrit ses fichiers de clé en tâche de fond, sans en perdre.
func TestGrosseSelectionEcritLesFichiersEnFond(t *testing.T) {
	app, _, c := setupAdmin(t)
	id, _ := c.upload("IMG_F.jpg", []byte("favori parmi beaucoup"))
	k := app.blobPath(1, id, "k")
	os.Remove(k)
	ids := []int64{id}
	for i := int64(1); i <= syncWriteMax; i++ {
		ids = append(ids, 1_000_000+i) // éléments inexistants : ignorés
	}
	app.db.Exec(`UPDATE assets SET favorite=1 WHERE id=?`, id)
	app.writeAssetFiles(ids...)
	deadline := time.Now().Add(10 * time.Second)
	for app.files.pendingWrites() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("file d'écriture jamais vidée")
		}
		time.Sleep(10 * time.Millisecond)
	}
	b, err := os.ReadFile(k)
	if err != nil || !strings.Contains(string(b), `"favorite":true`) {
		t.Fatalf("fichier de clé : %v %s", err, b)
	}
}
