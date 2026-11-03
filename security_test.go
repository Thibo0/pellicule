package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------- banc d'essai : un vrai serveur HTTPS et un client qui fait la crypto comme l'app ----------

func newTestApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	dir := t.TempDir()
	db, err := openDB(filepath.Join(dir, "pellicule.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	lib := filepath.Join(dir, "library")
	os.MkdirAll(filepath.Join(lib, ".tmp"), 0o755)
	app := &App{db: db, dataDir: dir, libDir: lib, scanKick: make(chan struct{}, 1),
		limiter: newLoginLimiter(), secure: true, secret: serverSecret(db)}
	cert, fp, err := loadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	app.certFP = fp
	srv := httptest.NewUnstartedServer(app.routes())
	srv.TLS = tlsConfig(cert)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return app, srv
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
	mk   []byte
	priv *ecdh.PrivateKey
}

// newClient épingle l'empreinte du serveur (comme l'app Android) ; cert = certificat client optionnel (téléphone).
func newClient(t *testing.T, srv *httptest.Server, fp string, cert *tls.Certificate) *client {
	jar, _ := cookiejar.New(nil)
	cfg := &tls.Config{
		InsecureSkipVerify: true, // remplacé par la vérification d'empreinte
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || certFingerprint(raw[0]) != fp {
				return x509.UnknownAuthorityError{}
			}
			return nil
		},
	}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}
	return &client{t: t, base: srv.URL, http: &http.Client{Jar: jar, Transport: &http.Transport{TLSClientConfig: cfg}}}
}

func (c *client) do(method, path string, body any) (int, map[string]any, []byte) {
	c.t.Helper()
	var rd io.Reader = bytes.NewReader(nil)
	ct := "application/json"
	switch b := body.(type) {
	case nil:
	case []byte:
		rd, ct = bytes.NewReader(b), "application/octet-stream"
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("X-Pellicule", "1")
	req.Header.Set("Content-Type", ct)
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s : %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return resp.StatusCode, out, raw
}

func b64field(m map[string]any, k string) []byte {
	var b []byte
	j, _ := json.Marshal(m[k])
	json.Unmarshal(j, &b)
	return b
}

// register crée toutes les clés du compte côté client, comme le navigateur.
func (c *client) register(path, username, password string, extra map[string]any) (int, string) {
	c.mk = randomBytes(KeySize)
	salt := randomBytes(SaltSize)
	authKey, wrapKey := PasswordKeys(password, salt)
	rec := randomBytes(KeySize)
	c.priv, _ = ecdh.P256().GenerateKey(rand.Reader)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(c.priv)
	body := map[string]any{
		"username": username, "authKey": authKey, "salt": salt,
		"wrappedMK":    Seal(wrapKey, c.mk, AADMasterKey),
		"recoveryMK":   Seal(RecoveryWrapKey(rec), c.mk, AADMasterKey),
		"recoveryAuth": hkdfKey(rec, LabelRecAuth),
		"pub":          c.priv.PublicKey().Bytes(), "wrappedPriv": Seal(c.mk, pkcs8, AADPrivKey),
	}
	for k, v := range extra {
		body[k] = v
	}
	code, out, _ := c.do("POST", path, body)
	if code != 200 {
		return code, ""
	}
	_ = out
	return code, FormatRecoveryKey(rec)
}

func (c *client) login(username, password string) int {
	_, pre, _ := c.do("POST", "/api/prelogin", map[string]string{"username": username})
	authKey, wrapKey := PasswordKeys(password, b64field(pre, "salt"))
	code, out, _ := c.do("POST", "/api/login", map[string]any{"username": username, "authKey": authKey})
	if code == 200 {
		keys := out["keys"].(map[string]any)
		mk, err := Open(wrapKey, b64field(keys, "wrappedMK"), AADMasterKey)
		if err != nil {
			c.t.Fatal("clé maître illisible après connexion")
		}
		c.mk = mk
	}
	return code
}

// upload chiffre et envoie une photo en plusieurs morceaux ; renvoie son id.
func (c *client) upload(name string, content []byte) (int64, string) {
	c.t.Helper()
	fk := randomBytes(KeySize)
	var enc bytes.Buffer
	EncryptStream(fk, bytes.NewReader(content), &enc)
	h := NewContentHasher()
	h.Write(content)
	meta, _ := json.Marshal(Meta{Name: name, Mime: "image/jpeg", Size: int64(len(content))})
	code, out, raw := c.do("POST", "/api/assets", map[string]any{
		"kind": "image", "takenAt": time.Now().UnixMilli(), "fk": Seal(c.mk, fk, AADFileKey),
		"meta": Seal(fk, meta, AADMeta), "dedupTag": hex.EncodeToString(DedupTag(c.mk, h.Sum())),
		"deviceTag": hex.EncodeToString(DeviceTag(c.mk, name, int64(len(content)))), "size": enc.Len(),
	})
	if code != 200 {
		c.t.Fatalf("création : %d %s", code, raw)
	}
	id := int64(out["id"].(float64))
	if out["status"] == "duplicate" {
		return id, "duplicate"
	}
	data := enc.Bytes()
	for off := 0; off < len(data); off += 700_000 {
		end := min(off+700_000, len(data))
		if code, _, raw := c.do("PUT", "/api/assets/"+itoa(id)+"/original?offset="+itoa(int64(off)), data[off:end]); code != 200 {
			c.t.Fatalf("envoi : %d %s", code, raw)
		}
	}
	c.do("PUT", "/api/assets/"+itoa(id)+"/thumb", Seal(fk, []byte("miniature-jpeg"), AADThumb))
	if code, _, raw := c.do("POST", "/api/assets/"+itoa(id)+"/complete", nil); code != 200 {
		c.t.Fatalf("fin d'envoi : %d %s", code, raw)
	}
	return id, out["status"].(string)
}

func itoa(n int64) string { return big.NewInt(n).String() }

// phoneCert : certificat client auto-signé, comme celui que génère le Keystore Android.
func phoneCert(t *testing.T) tls.Certificate {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: DeviceCertName, // même nom que le certificat créé par l'app (Keys.kt)
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(20, 0, 0),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func setupAdmin(t *testing.T) (*App, *httptest.Server, *client) {
	app, srv := newTestApp(t)
	c := newClient(t, srv, app.certFP, nil)
	if code, _ := c.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode()}); code != 200 {
		t.Fatalf("setup : %d", code)
	}
	return app, srv, c
}

// ---------- transport ----------

func TestCertificatStable(t *testing.T) {
	dir := t.TempDir()
	_, fp1, err := loadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, _ := loadOrCreateCert(dir)
	if fp1 != fp2 || len(fp1) != 64 {
		t.Fatalf("empreinte instable ou invalide : %q / %q", fp1, fp2)
	}
}

func TestEpinglageRefuseUnAutreServeur(t *testing.T) {
	_, srv := newTestApp(t)
	c := newClient(t, srv, strings.Repeat("0", 64), nil)
	if _, err := c.http.Get(srv.URL + "/api/me"); err == nil {
		t.Fatal("un client épinglé a accepté un certificat inconnu")
	}
}

// ---------- comptes ----------

func TestInstallationExigeLeCode(t *testing.T) {
	app, srv := newTestApp(t)
	c := newClient(t, srv, app.certFP, nil)
	if code, _ := c.register("/api/setup", "pirate", "motdepasse1", map[string]any{"setupCode": "AAAAA-AAAAA"}); code != http.StatusForbidden {
		t.Fatalf("compte admin créé sans le code d'installation : %d", code)
	}
	if code, _ := c.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode()}); code != 200 {
		t.Fatalf("setup légitime refusé : %d", code)
	}
	if code, _ := c.register("/api/setup", "bob", "motdepasse1", map[string]any{"setupCode": app.setupCode()}); code != http.StatusConflict {
		t.Fatalf("second setup accepté : %d", code)
	}
}

func TestLeServeurNeVoitJamaisLeMotDePasse(t *testing.T) {
	app, _, _ := setupAdmin(t)
	var dump strings.Builder
	rows, _ := app.db.Query(`SELECT auth_hash, hex(salt), hex(wrapped_mk) FROM users`)
	for rows.Next() {
		var a, b, c string
		rows.Scan(&a, &b, &c)
		dump.WriteString(a + b + c)
	}
	rows.Close()
	if strings.Contains(dump.String(), "motdepasse1") || !strings.HasPrefix(dump.String(), "$2") {
		t.Fatal("mot de passe en clair ou non haché")
	}
}

func TestConnexionSessionsEtEnTetes(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	c := newClient(t, srv, app.certFP, nil)
	if c.login("alice", "mauvais") != http.StatusUnauthorized {
		t.Fatal("mauvais mot de passe accepté")
	}
	if c.login("Alice", "motdepasse1") != 200 {
		t.Fatal("connexion refusée")
	}
	var stored string
	app.db.QueryRow(`SELECT token FROM sessions LIMIT 1`).Scan(&stored)
	for _, ck := range c.http.Jar.Cookies(mustURL(srv.URL)) {
		if ck.Value == stored {
			t.Fatal("jeton de session stocké en clair")
		}
	}
	resp, _ := c.http.Get(srv.URL + "/")
	for _, h := range []string{"Content-Security-Policy", "Strict-Transport-Security", "X-Frame-Options"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("en-tête %s manquant", h)
		}
	}
}

func TestPreloginNeRevelePasLesComptes(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	c := newClient(t, srv, app.certFP, nil)
	_, a, _ := c.do("POST", "/api/prelogin", map[string]string{"username": "inconnu"})
	_, b, _ := c.do("POST", "/api/prelogin", map[string]string{"username": "inconnu"})
	if len(b64field(a, "salt")) != SaltSize || !bytes.Equal(b64field(a, "salt"), b64field(b, "salt")) {
		t.Fatal("le faux sel n'est pas stable : on distinguerait les comptes inconnus")
	}
}

func TestChangementDeMotDePasse(t *testing.T) {
	app, srv, a := setupAdmin(t)
	b := newClient(t, srv, app.certFP, nil)
	b.login("alice", "motdepasse1")

	_, pre, _ := a.do("POST", "/api/prelogin", map[string]string{"username": "alice"})
	cur, _ := PasswordKeys("motdepasse1", b64field(pre, "salt"))
	salt := randomBytes(SaltSize)
	ak, wk := PasswordKeys("motdepasse2", salt)
	code, _, raw := a.do("POST", "/api/me/password", map[string]any{"currentAuthKey": cur, "authKey": ak, "salt": salt, "wrappedMK": Seal(wk, a.mk, AADMasterKey)})
	if code != 200 {
		t.Fatalf("changement : %d %s", code, raw)
	}
	if _, me, _ := b.do("GET", "/api/me", nil); me["user"] != nil {
		t.Fatal("l'autre navigateur est resté connecté")
	}
	c := newClient(t, srv, app.certFP, nil)
	if c.login("alice", "motdepasse2") != 200 || !bytes.Equal(c.mk, a.mk) {
		t.Fatal("nouvelle connexion impossible ou clé maître différente")
	}
}

func TestRecuperationParCleDeSecours(t *testing.T) {
	app, srv := newTestApp(t)
	a := newClient(t, srv, app.certFP, nil)
	_, recovery := a.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode()})

	// mot de passe oublié : avec la clé de secours, on retrouve la même clé maître
	c := newClient(t, srv, app.certFP, nil)
	_, st, _ := c.do("POST", "/api/recover/start", map[string]string{"username": "alice"})
	rec, _ := ParseRecoveryKey(recovery)
	mk, err := Open(RecoveryWrapKey(rec), b64field(st, "recoveryMK"), AADMasterKey)
	if err != nil || !bytes.Equal(mk, a.mk) {
		t.Fatal("clé maître non récupérée")
	}
	salt := randomBytes(SaltSize)
	ak, wk := PasswordKeys("nouveau-mdp", salt)
	body := map[string]any{"username": "alice", "authKey": ak, "salt": salt, "wrappedMK": Seal(wk, mk, AADMasterKey),
		"recoveryAuth": randomBytes(KeySize)}
	if code, _, _ := c.do("POST", "/api/recover", body); code != http.StatusUnauthorized {
		t.Fatal("récupération acceptée avec une fausse preuve")
	}
	body["recoveryAuth"] = hkdfKey(rec, LabelRecAuth)
	if code, _, raw := c.do("POST", "/api/recover", body); code != 200 {
		t.Fatalf("récupération refusée : %s", raw)
	}
	d := newClient(t, srv, app.certFP, nil)
	if d.login("alice", "nouveau-mdp") != 200 {
		t.Fatal("nouveau mot de passe inutilisable")
	}
}

func TestInvitation(t *testing.T) {
	app, srv, admin := setupAdmin(t)
	_, inv, _ := admin.do("POST", "/api/invites", map[string]any{"isAdmin": false})
	token := inv["token"].(string)
	bob := newClient(t, srv, app.certFP, nil)
	if code, _ := bob.register("/api/register", "bob", "motdepasse1", map[string]any{"token": "faux"}); code != http.StatusNotFound {
		t.Fatal("inscription sans invitation valide")
	}
	if code, _ := bob.register("/api/register", "bob", "motdepasse1", map[string]any{"token": token}); code != 200 {
		t.Fatal("inscription refusée")
	}
	eve := newClient(t, srv, app.certFP, nil)
	if code, _ := eve.register("/api/register", "eve", "motdepasse1", map[string]any{"token": token}); code == 200 {
		t.Fatal("invitation réutilisée")
	}
	if code, _, _ := bob.do("POST", "/api/invites", map[string]any{}); code != http.StatusForbidden {
		t.Fatal("un non-admin a créé une invitation")
	}
}

// ---------- photos ----------

func TestPhotoChiffreeDeBoutEnBout(t *testing.T) {
	app, srv, c := setupAdmin(t)
	content := bytes.Repeat([]byte("\xff\xd8\xff\xe0 JFIF photo de vacances secrète "), 60000) // ~2,6 Mo
	id, status := c.upload("IMG_VACANCES.jpg", content)
	if status != "created" {
		t.Fatal(status)
	}

	// le disque ne contient que du chiffré : ni en-tête JPEG, ni texte, ni nom de fichier
	err := filepath.Walk(app.libDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		if bytes.Contains(b, []byte("JFIF")) || bytes.Contains(b, []byte("secrète")) || strings.Contains(p, "VACANCES") {
			t.Errorf("donnée en clair dans %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	rows, _ := app.db.Query(`SELECT meta, wrapped_fk FROM assets`)
	for rows.Next() {
		var m, f []byte
		rows.Scan(&m, &f)
		dump.Write(m)
		dump.Write(f)
	}
	rows.Close()
	if bytes.Contains(dump.Bytes(), []byte("VACANCES")) {
		t.Fatal("nom de fichier en clair dans la base")
	}

	// relecture : le client déchiffre la clé, les métadonnées et l'original
	_, tl, _ := c.do("GET", "/api/timeline", nil)
	item := tl["items"].([]any)[0].(map[string]any)
	fk, err := Open(c.mk, b64field(item, "fk"), AADFileKey)
	if err != nil {
		t.Fatal(err)
	}
	metaJSON, _ := Open(fk, b64field(item, "meta"), AADMeta)
	if !strings.Contains(string(metaJSON), "IMG_VACANCES.jpg") {
		t.Fatal("métadonnées illisibles")
	}
	_, _, enc := c.do("GET", "/api/assets/"+itoa(id)+"/original", nil)
	var plain bytes.Buffer
	if err := DecryptStream(fk, bytes.NewReader(enc), &plain); err != nil || !bytes.Equal(plain.Bytes(), content) {
		t.Fatal("original différent après aller-retour")
	}

	// lecture partielle (vidéo) : le serveur sert la plage demandée du chiffré
	req, _ := http.NewRequest("GET", srv.URL+"/api/assets/"+itoa(id)+"/original", nil)
	req.Header.Set("Range", "bytes=16-1048607")
	resp, _ := c.http.Do(req)
	part, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || len(part) != ChunkSize+TagSize {
		t.Fatalf("Range : %d, %d octets", resp.StatusCode, len(part))
	}

	// doublon détecté sans que le serveur voie le contenu
	if _, st := c.upload("copie.jpg", content); st != "duplicate" {
		t.Fatal("doublon non détecté")
	}
}

func TestIsolationEntreComptes(t *testing.T) {
	app, srv, alice := setupAdmin(t)
	id, _ := alice.upload("a.jpg", []byte("photo d'alice"))
	_, inv, _ := alice.do("POST", "/api/invites", map[string]any{})
	bob := newClient(t, srv, app.certFP, nil)
	bob.register("/api/register", "bob", "motdepasse1", map[string]any{"token": inv["token"]})

	for _, p := range []string{"/api/assets/" + itoa(id), "/api/assets/" + itoa(id) + "/original", "/api/assets/" + itoa(id) + "/thumb"} {
		if code, _, _ := bob.do("GET", p, nil); code != http.StatusNotFound {
			t.Errorf("bob accède à %s : %d", p, code)
		}
	}
	if code, _, _ := bob.do("PUT", "/api/assets/"+itoa(id)+"/thumb", Seal(randomBytes(32), []byte("x"), AADThumb)); code != http.StatusNotFound {
		t.Error("bob a remplacé la miniature d'alice")
	}
	bob.do("POST", "/api/assets/trash", map[string]any{"ids": []int64{id}})
	var trashed sql.NullInt64
	app.db.QueryRow(`SELECT trashed_at FROM assets WHERE id=?`, id).Scan(&trashed)
	if trashed.Valid {
		t.Error("bob a mis la photo d'alice à la corbeille")
	}
}

func TestEnvoiReprenableEtPositionVerifiee(t *testing.T) {
	_, _, c := setupAdmin(t)
	fk := randomBytes(KeySize)
	var enc bytes.Buffer
	EncryptStream(fk, bytes.NewReader([]byte("contenu")), &enc)
	body := map[string]any{"kind": "image", "takenAt": 1, "fk": Seal(c.mk, fk, AADFileKey), "meta": Seal(fk, []byte("{}"), AADMeta),
		"dedupTag": strings.Repeat("ab", 32), "size": enc.Len()}
	_, out, _ := c.do("POST", "/api/assets", body)
	id := itoa(int64(out["id"].(float64)))
	if code, _, _ := c.do("PUT", "/api/assets/"+id+"/original?offset=5", enc.Bytes()); code != http.StatusConflict {
		t.Fatal("morceau accepté à une mauvaise position")
	}
	c.do("PUT", "/api/assets/"+id+"/original?offset=0", enc.Bytes()[:10])
	if code, _, _ := c.do("POST", "/api/assets/"+id+"/complete", nil); code != http.StatusConflict {
		t.Fatal("envoi incomplet accepté")
	}
	_, out, _ = c.do("POST", "/api/assets", body)
	if out["status"] != "resume" || out["uploaded"].(float64) != 10 {
		t.Fatalf("reprise non proposée : %v", out)
	}
	c.do("PUT", "/api/assets/"+id+"/original?offset=10", enc.Bytes()[10:])
	if code, _, _ := c.do("POST", "/api/assets/"+id+"/complete", nil); code != 200 {
		t.Fatal("fin d'envoi refusée")
	}
}

// ---------- partage ----------

func TestLienDePartage(t *testing.T) {
	app, srv, c := setupAdmin(t)
	id1, _ := c.upload("partagee.jpg", []byte("photo partagée"))
	id2, _ := c.upload("privee.jpg", []byte("photo privée"))
	_, al, _ := c.do("POST", "/api/albums", map[string]any{"name": Seal(c.mk, []byte("Vacances"), AADAlbumName), "ids": []int64{id1}})
	albumID := itoa(int64(al["id"].(float64)))

	sk := randomBytes(KeySize)
	token := randomToken(24)
	_, tl, _ := c.do("GET", "/api/timeline", nil)
	var keys []map[string]any
	for _, it := range tl["items"].([]any) {
		m := it.(map[string]any)
		fk, _ := Open(c.mk, b64field(m, "fk"), AADFileKey)
		keys = append(keys, map[string]any{"asset": int64(m["id"].(float64)), "fk": Seal(sk, fk, AADShareKey)}) // tente aussi la photo privée
	}
	code, _, raw := c.do("POST", "/api/albums/"+albumID+"/shares", map[string]any{"token": token,
		"link": Seal(c.mk, []byte("/s/"+token+"#"+hex.EncodeToString(sk)), AADShareKey), "name": Seal(sk, []byte("Vacances"), AADAlbumName),
		"keys": keys, "allowDownload": false})
	if code != 200 {
		t.Fatalf("partage : %s", raw)
	}

	anon := newClient(t, srv, app.certFP, nil)
	_, pub, _ := anon.do("GET", "/api/s/"+token, nil)
	items := pub["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("le lien expose %d photo(s), 1 attendue", len(items))
	}
	m := items[0].(map[string]any)
	fk, err := Open(sk, b64field(m, "fk"), AADShareKey)
	if err != nil {
		t.Fatal("clé de partage inutilisable")
	}
	if name, _ := Open(sk, b64field(pub, "name"), AADAlbumName); string(name) != "Vacances" {
		t.Fatal("nom d'album illisible pour le visiteur")
	}
	_, _, thumb := anon.do("GET", "/api/s/"+token+"/assets/"+itoa(id1)+"/thumb", nil)
	if p, err := Open(fk, thumb, AADThumb); err != nil || string(p) != "miniature-jpeg" {
		t.Fatal("miniature partagée illisible")
	}
	if code, _, _ := anon.do("GET", "/api/s/"+token+"/assets/"+itoa(id2)+"/thumb", nil); code != http.StatusNotFound {
		t.Fatal("une photo hors de l'album est accessible par le lien")
	}
	if code, _, _ := anon.do("GET", "/api/s/"+token+"/assets/"+itoa(id1)+"/original", nil); code != http.StatusForbidden {
		t.Fatal("original téléchargeable alors que c'est interdit")
	}
	var stored string
	app.db.QueryRow(`SELECT token FROM shares`).Scan(&stored)
	if stored == token {
		t.Fatal("jeton de partage stocké en clair")
	}
}

// ---------- téléphones ----------

func TestAppairageEtRevocation(t *testing.T) {
	app, srv, browser := setupAdmin(t)
	cert := phoneCert(t)
	phone := newClient(t, srv, app.certFP, &cert)

	if code, _, _ := phone.do("GET", "/api/keys", nil); code != http.StatusUnauthorized {
		t.Fatal("téléphone non appairé accepté")
	}
	if code, _, _ := phone.do("POST", "/api/devices/pair", map[string]string{"code": "AAAAA-AAAAA", "name": "x"}); code != http.StatusUnauthorized {
		t.Fatal("appairage avec un faux code")
	}
	_, pc, _ := browser.do("POST", "/api/devices/pair-code", nil)
	if pc["fingerprint"] != app.certFP {
		t.Fatal("le QR code ne porte pas l'empreinte du serveur")
	}
	if code, _, raw := phone.do("POST", "/api/devices/pair", map[string]any{"code": pc["code"], "name": "Pixel"}); code != 200 {
		t.Fatalf("appairage : %s", raw)
	}
	if code, _, _ := phone.do("POST", "/api/devices/pair", map[string]any{"code": pc["code"], "name": "Pixel"}); code == 200 {
		t.Fatal("code d'appairage réutilisable")
	}

	// le téléphone est reconnu par son certificat, sans cookie ni mot de passe
	code, keys, _ := phone.do("GET", "/api/keys", nil)
	if code != 200 {
		t.Fatal("téléphone appairé refusé")
	}
	_, pre, _ := phone.do("POST", "/api/prelogin", map[string]string{"username": "alice"})
	_, wk := PasswordKeys("motdepasse1", b64field(pre, "salt"))
	if mk, err := Open(wk, b64field(keys, "wrappedMK"), AADMasterKey); err != nil || !bytes.Equal(mk, browser.mk) {
		t.Fatal("le téléphone ne retrouve pas la clé maître")
	}
	// un téléphone (potentiellement volé) ne peut ni appairer d'autres appareils ni partager
	if code, _, _ := phone.do("POST", "/api/devices/pair-code", nil); code != http.StatusForbidden {
		t.Fatal("un téléphone peut générer des codes d'appairage")
	}

	_, devs, _ := browser.do("GET", "/api/devices", nil)
	_ = devs
	var devID int64
	app.db.QueryRow(`SELECT id FROM devices`).Scan(&devID)
	browser.do("DELETE", "/api/devices/"+itoa(devID), nil)
	if code, _, _ := phone.do("GET", "/api/keys", nil); code != http.StatusUnauthorized {
		t.Fatal("appareil révoqué toujours accepté")
	}
}

// ---------- import FolderSync ----------

func TestImportChiffreEtEfface(t *testing.T) {
	app, _, c := setupAdmin(t)
	dir := filepath.Join(app.libDir, importDir, "alice")
	os.MkdirAll(dir, 0o755)
	src := filepath.Join(dir, "IMG_IMPORT.jpg")
	content := []byte("\xff\xd8\xff\xe0 JFIF photo importée")
	os.WriteFile(src, content, 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(src, old, old)

	app.scanImports()
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("le fichier en clair est resté dans le dossier d'import")
	}
	_, tl, _ := c.do("GET", "/api/timeline?sealed=1", nil)
	items := tl["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("%d élément(s) importé(s)", len(items))
	}
	m := items[0].(map[string]any)
	fk, err := OpenFromPublic(c.priv, b64field(m, "fk"))
	if err != nil {
		t.Fatal("clé scellée illisible pour le propriétaire")
	}
	id := int64(m["id"].(float64))
	_, _, enc := c.do("GET", "/api/assets/"+itoa(id)+"/original", nil)
	var plain bytes.Buffer
	if DecryptStream(fk, bytes.NewReader(enc), &plain) != nil || !bytes.Equal(plain.Bytes(), content) {
		t.Fatal("original importé illisible")
	}
	// rechiffrement par la clé maître
	c.do("POST", "/api/assets/keys", map[string]any{"keys": []map[string]any{{"id": id, "fk": Seal(c.mk, fk, AADFileKey)}}})
	_, tl, _ = c.do("GET", "/api/timeline?sealed=1", nil)
	if len(tl["items"].([]any)) != 0 {
		t.Fatal("clé toujours scellée après rechiffrement")
	}

	// FolderSync renvoie le même fichier : ignoré et effacé
	os.WriteFile(src, content, 0o644)
	os.Chtimes(src, old, old)
	app.scanImports()
	var n int
	app.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&n)
	if n != 1 {
		t.Fatalf("fichier réimporté (%d éléments)", n)
	}
}

func TestLimiteParCompte(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	c := newClient(t, srv, app.certFP, nil)
	for i := 0; i < 10; i++ {
		app.limiter.reset("127.0.0.1") // simule un attaquant qui change d'IP à chaque essai
		c.login("alice", "mauvais")
	}
	app.limiter.reset("127.0.0.1")
	if code := c.login("alice", "motdepasse1"); code != http.StatusTooManyRequests {
		t.Fatalf("compte non protégé après 10 échecs : %d", code)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func TestImportIgnoreLesLiensSymboliques(t *testing.T) {
	app, _, c := setupAdmin(t)
	dir := filepath.Join(app.libDir, importDir, "alice")
	os.MkdirAll(dir, 0o755)
	secret := filepath.Join(t.TempDir(), "secret.jpg")
	os.WriteFile(secret, []byte("contenu hors du dossier d'import"), 0o600)
	link := filepath.Join(dir, "piege.jpg")
	if err := os.Symlink(secret, link); err != nil {
		t.Skip("liens symboliques indisponibles ici :", err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(secret, old, old)
	app.scanImports()
	_, tl, _ := c.do("GET", "/api/timeline?sealed=1", nil)
	if n := len(tl["items"].([]any)); n != 0 {
		t.Fatalf("lien symbolique importé (%d élément)", n)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatal("la cible du lien a été touchée")
	}
}

// ---------- tout depuis l'app (sans navigateur) ----------

func TestInstallationEtConnexionDepuisLApp(t *testing.T) {
	app, srv := newTestApp(t)
	cert := phoneCert(t)
	phone := newClient(t, srv, app.certFP, &cert)
	if code, _ := phone.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode(), "deviceName": "Pixel"}); code != 200 {
		t.Fatalf("installation depuis l'app : %d", code)
	}
	// le téléphone est appairé d'office : il s'authentifie par son certificat, sans cookie
	phone.http.Jar = nil
	if code, me, _ := phone.do("GET", "/api/me", nil); code != 200 || me["user"] == nil {
		t.Fatal("téléphone non reconnu après l'installation")
	}

	// un second téléphone se connecte avec le mot de passe, sans navigateur
	cert2 := phoneCert(t)
	phone2 := newClient(t, srv, app.certFP, &cert2)
	salt := func(c *client) []byte {
		_, r, _ := c.do("POST", "/api/prelogin", map[string]string{"username": "alice"})
		return b64field(r, "salt")
	}
	bad, _ := PasswordKeys("mauvais", salt(phone2))
	if code, _, _ := phone2.do("POST", "/api/devices/login", map[string]any{"username": "alice", "authKey": bad, "name": "x"}); code != http.StatusUnauthorized {
		t.Fatal("connexion de l'app avec un mauvais mot de passe acceptée")
	}
	auth, wrap := PasswordKeys("motdepasse1", salt(phone2))
	code, r, raw := phone2.do("POST", "/api/devices/login", map[string]any{"username": "alice", "authKey": auth, "name": "Tablette"})
	if code != 200 {
		t.Fatalf("connexion de l'app : %s", raw)
	}
	keys := r["keys"].(map[string]any)
	if _, err := Open(wrap, b64field(keys, "wrappedMK"), AADMasterKey); err != nil {
		t.Fatal("clé maître illisible avec le mot de passe")
	}
	phone2.http.Jar = nil
	if code, me, _ := phone2.do("GET", "/api/me", nil); code != 200 || me["user"] == nil {
		t.Fatal("second téléphone non reconnu")
	}
	// sans certificat client, la connexion directe est refusée (elle n'ouvre pas de session navigateur)
	web := newClient(t, srv, app.certFP, nil)
	if code, _, _ := web.do("POST", "/api/devices/login", map[string]any{"username": "alice", "authKey": auth}); code != http.StatusBadRequest {
		t.Fatal("connexion directe acceptée sans certificat d'appareil")
	}
}

// recoverWith : reprise d'un compte avec la clé de secours (nouveau mot de passe) ; renvoie le code HTTP.
func (c *client) recoverWith(username, recovery, newPassword string, extra map[string]any) (int, map[string]any) {
	c.t.Helper()
	_, st, _ := c.do("POST", "/api/recover/start", map[string]string{"username": username})
	rec, _ := ParseRecoveryKey(recovery)
	mk, err := Open(RecoveryWrapKey(rec), b64field(st, "recoveryMK"), AADMasterKey)
	if err != nil {
		return 0, nil
	}
	salt := randomBytes(SaltSize)
	ak, wk := PasswordKeys(newPassword, salt)
	body := map[string]any{"username": username, "authKey": ak, "salt": salt, "wrappedMK": Seal(wk, mk, AADMasterKey),
		"recoveryAuth": hkdfKey(rec, LabelRecAuth)}
	for k, v := range extra {
		body[k] = v
	}
	code, out, _ := c.do("POST", "/api/recover", body)
	if code == 200 {
		c.mk = mk
	}
	return code, out
}

func TestFichiersAutonomesSansSecretDeMotDePasse(t *testing.T) {
	app, _, c := setupAdmin(t)
	c.upload("IMG_1.jpg", bytes.Repeat([]byte("photo"), 1000))
	var hash string
	var wrapped []byte
	app.db.QueryRow(`SELECT auth_hash, wrapped_mk FROM users WHERE username='alice'`).Scan(&hash, &wrapped)
	err := filepath.Walk(app.libDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, _ := os.ReadFile(p)
		if bytes.Contains(data, []byte(hash)) || bytes.Contains(data, wrapped) ||
			bytes.Contains(data, []byte(base64.StdEncoding.EncodeToString(wrapped))) || bytes.Contains(data, []byte("wrappedMK")) {
			t.Errorf("%s contient de quoi deviner le mot de passe", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(app.libDir, "u1", "compte.json")); err != nil {
		t.Fatal("compte.json absent")
	}
	if _, err := os.Stat(filepath.Join(app.libDir, ".sauvegardes")); err == nil {
		t.Fatal("copie de la base sur le disque des photos")
	}
}

func TestReconstructionDepuisLeDisque(t *testing.T) {
	app, srv := newTestApp(t)
	alice := newClient(t, srv, app.certFP, nil)
	_, recovery := alice.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode()})
	content := bytes.Repeat([]byte("vacances"), 200_000)
	id1, _ := alice.upload("IMG_1.jpg", content)
	id2, _ := alice.upload("IMG_2.jpg", []byte("deuxieme photo"))
	id3, _ := alice.upload("IMG_3.jpg", []byte("photo jetée"))
	alice.do("POST", "/api/assets/favorite", map[string]any{"ids": []int64{id2}, "favorite": true})
	alice.do("POST", "/api/assets/trash", map[string]any{"ids": []int64{id3}})
	_, al, _ := alice.do("POST", "/api/albums", map[string]any{"name": Seal(alice.mk, []byte("Été"), "pellicule/album"), "ids": []int64{id1}})
	albumID := int64(al["id"].(float64))
	// un envoi inachevé ne doit pas revenir
	alice.do("POST", "/api/assets", map[string]any{"kind": "image", "takenAt": 1, "fk": Seal(alice.mk, randomBytes(KeySize), AADFileKey),
		"meta": Seal(randomBytes(KeySize), []byte("{}"), AADMeta), "dedupTag": hex.EncodeToString(randomBytes(32)), "size": 5000})
	bob := newClient(t, srv, app.certFP, nil)
	token, _ := app.createInvite(nil, false)
	_, bobRecovery := bob.register("/api/register", "bob", "motdepasse2", map[string]any{"token": token})
	bob.upload("BOB.jpg", []byte("photo de bob"))

	// la VM est perdue : base neuve, même disque des photos
	dir := t.TempDir()
	db2, err := openDB(filepath.Join(dir, "pellicule.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	app2 := &App{db: db2, dataDir: dir, libDir: app.libDir, scanKick: make(chan struct{}, 1),
		limiter: newLoginLimiter(), secure: true, secret: serverSecret(db2)}
	var out bytes.Buffer
	if err := app2.rebuild(strings.NewReader("oui\n"), &out); err == nil || app2.userCount() != 0 {
		t.Fatal("reconstruction sans confirmation explicite")
	}
	if !strings.Contains(out.String(), "alice") || !strings.Contains(out.String(), "(administrateur)") || !strings.Contains(out.String(), "bob") {
		t.Fatalf("liste des comptes non affichée :\n%s", out.String())
	}
	if err := app2.rebuild(strings.NewReader("RECONSTRUIRE\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := app2.rebuild(strings.NewReader("RECONSTRUIRE\n"), io.Discard); err == nil {
		t.Fatal("seconde reconstruction par-dessus une base existante")
	}
	var devices, sessions, admins int
	db2.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&devices)
	db2.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions)
	db2.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin=1`).Scan(&admins)
	if devices != 0 || sessions != 0 || admins != 1 {
		t.Fatalf("appareils %d, sessions %d, admins %d", devices, sessions, admins)
	}

	cert, _, _ := loadOrCreateCert(dir)
	app2.certFP = certFingerprint(cert.Certificate[0])
	srv2 := httptest.NewUnstartedServer(app2.routes())
	srv2.TLS = tlsConfig(cert)
	srv2.StartTLS()
	t.Cleanup(srv2.Close)

	// l'ancien mot de passe ne sert plus à rien : il faut la clé de secours
	c := newClient(t, srv2, app2.certFP, nil)
	if code := c.login("alice", "motdepasse1"); code != http.StatusConflict {
		t.Fatalf("connexion par mot de passe après reconstruction : %d", code)
	}
	if code, _ := c.recoverWith("alice", bobRecovery, "nouveau-mdp-1", nil); code == 200 {
		t.Fatal("compte repris avec la clé de secours d'un autre")
	}
	if code, _ := c.recoverWith("alice", recovery, "nouveau-mdp-1", nil); code != 200 {
		t.Fatalf("reprise par clé de secours : %d", code)
	}
	if !bytes.Equal(c.mk, alice.mk) {
		t.Fatal("clé maître différente")
	}
	_, tl, _ := c.do("GET", "/api/timeline", nil)
	items := tl["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("%d éléments dans la photothèque (attendu 2 : la photo jetée est dans la corbeille)", len(items))
	}
	var fav bool
	db2.QueryRow(`SELECT favorite FROM assets WHERE id=?`, id2).Scan(&fav)
	var trashed sql.NullInt64
	db2.QueryRow(`SELECT trashed_at FROM assets WHERE id=?`, id3).Scan(&trashed)
	if !fav || !trashed.Valid {
		t.Fatal("favori ou corbeille perdus")
	}
	var item map[string]any
	for _, it := range items {
		if int64(it.(map[string]any)["id"].(float64)) == id1 {
			item = it.(map[string]any)
		}
	}
	fk, err := Open(c.mk, b64field(item, "fk"), AADFileKey)
	if err != nil {
		t.Fatal(err)
	}
	_, _, enc := c.do("GET", "/api/assets/"+itoa(id1)+"/original", nil)
	var plain bytes.Buffer
	if err := DecryptStream(fk, bytes.NewReader(enc), &plain); err != nil || !bytes.Equal(plain.Bytes(), content) {
		t.Fatal("original illisible après reconstruction")
	}
	_, alb, _ := c.do("GET", "/api/albums/"+itoa(albumID), nil)
	if alb["items"] == nil || len(alb["items"].([]any)) != 1 {
		t.Fatal("album perdu")
	}
	// bob, de son côté
	b := newClient(t, srv2, app2.certFP, nil)
	if code, _ := b.recoverWith("bob", bobRecovery, "nouveau-mdp-2", nil); code != 200 {
		t.Fatal("bob ne peut pas reprendre son compte")
	}
	if b.login("bob", "nouveau-mdp-2") != 200 {
		t.Fatal("nouveau mot de passe de bob inutilisable")
	}
}

func TestReconstructionRefuseLesFichiersDouteux(t *testing.T) {
	app, _, c := setupAdmin(t)
	c.upload("IMG_1.jpg", []byte("photo"))
	newApp := func() *App {
		dir := t.TempDir()
		db, _ := openDB(filepath.Join(dir, "pellicule.db"))
		t.Cleanup(func() { db.Close() })
		return &App{db: db, dataDir: dir, libDir: app.libDir, limiter: newLoginLimiter(), secret: serverSecret(db)}
	}
	// un compte.json invalide est ignoré (et signalé), il ne crée pas de compte
	os.MkdirAll(filepath.Join(app.libDir, "u7"), 0o755)
	os.WriteFile(filepath.Join(app.libDir, "u7", "compte.json"), []byte(`{"format":1,"username":"mallory","admin":true}`), 0o644)
	a2 := newApp()
	if err := a2.rebuild(strings.NewReader("RECONSTRUIRE\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	var n int
	a2.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username='mallory'`).Scan(&n)
	if n != 0 {
		t.Fatal("compte invalide accepté")
	}
	// deux dossiers pour le même nom : refus
	data, _ := os.ReadFile(filepath.Join(app.libDir, "u1", "compte.json"))
	os.WriteFile(filepath.Join(app.libDir, "u7", "compte.json"), data, 0o644)
	if err := newApp().rebuild(strings.NewReader("RECONSTRUIRE\n"), io.Discard); err == nil {
		t.Fatal("deux comptes du même nom acceptés")
	}
}

func TestRepriseDepuisLAppAppaireLeTelephone(t *testing.T) {
	app, srv := newTestApp(t)
	a := newClient(t, srv, app.certFP, nil)
	_, recovery := a.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode()})
	pc := phoneCert(t)
	phone := newClient(t, srv, app.certFP, &pc)
	code, out := phone.recoverWith("alice", recovery, "nouveau-mdp", map[string]any{"deviceName": "Pixel"})
	if code != 200 || out["keys"] == nil {
		t.Fatalf("reprise depuis l'app : %d", code)
	}
	keys := out["keys"].(map[string]any)
	if pub := b64field(keys, "pub"); !bytes.Equal(pub, a.priv.PublicKey().Bytes()) {
		t.Fatal("clé publique non renvoyée")
	}
	phone.http.Jar = nil
	if code, me, _ := phone.do("GET", "/api/me", nil); code != 200 || me["user"] == nil {
		t.Fatal("téléphone non appairé après reprise")
	}
}

// ---------- correctifs de l'audit ----------

func TestLimiteResisteAuxRequetesSimultanees(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	verifies := 0 // réponses 401 = vérifications bcrypt réellement faites
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := newClient(t, srv, app.certFP, nil)
			if c.login("alice", "mauvais") == http.StatusUnauthorized {
				mu.Lock()
				verifies++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if verifies > 10 {
		t.Fatalf("%d mots de passe vérifiés en parallèle (10 au plus)", verifies)
	}
}

func TestTelephoneNePeutPasInviterUnAdmin(t *testing.T) {
	app, srv := newTestApp(t)
	cert := phoneCert(t)
	phone := newClient(t, srv, app.certFP, &cert)
	phone.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode(), "deviceName": "Pixel"})
	phone.http.Jar = nil // uniquement le certificat du téléphone
	_, r, raw := phone.do("POST", "/api/invites", map[string]any{"isAdmin": true})
	if r["token"] == nil {
		t.Fatalf("invitation refusée au téléphone : %s", raw)
	}
	var admin bool
	app.db.QueryRow(`SELECT is_admin FROM invites WHERE token=?`, tokenHash(r["token"].(string))).Scan(&admin)
	if admin {
		t.Fatal("un téléphone a créé une invitation administrateur")
	}
}

func TestSecoursDeconnecteLesTelephones(t *testing.T) {
	app, srv := newTestApp(t)
	cert := phoneCert(t)
	phone := newClient(t, srv, app.certFP, &cert)
	_, recovery := phone.register("/api/setup", "alice", "motdepasse1", map[string]any{"setupCode": app.setupCode(), "deviceName": "Pixel"})
	c := newClient(t, srv, app.certFP, nil)
	rec, _ := ParseRecoveryKey(recovery)
	salt := randomBytes(SaltSize)
	ak, wk := PasswordKeys("nouveau-mdp", salt)
	body := map[string]any{"username": "alice", "authKey": ak, "salt": salt, "wrappedMK": Seal(wk, phone.mk, AADMasterKey),
		"recoveryAuth": hkdfKey(rec, LabelRecAuth)}
	if code, _, raw := c.do("POST", "/api/recover", body); code != 200 {
		t.Fatalf("secours : %s", raw)
	}
	phone.http.Jar = nil
	if _, me, _ := phone.do("GET", "/api/me", nil); me["user"] != nil {
		t.Fatal("téléphone toujours appairé après l'utilisation de la clé de secours")
	}
}

func TestIPDuClientDerriereUnProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:4000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 192.168.1.20")
	defer func() { trustedProxy = "" }()
	trustedProxy = ""
	if clientIP(r) != "10.0.0.5" {
		t.Fatal("X-Forwarded-For lu sans proxy déclaré")
	}
	trustedProxy = "10.0.0.5"
	if clientIP(r) != "192.168.1.20" {
		t.Fatalf("IP réelle non lue derrière le proxy déclaré : %s", clientIP(r))
	}
	r.RemoteAddr = "10.0.0.9:4000"
	if clientIP(r) != "10.0.0.9" {
		t.Fatal("X-Forwarded-For accepté d'un autre que le proxy")
	}
}

// ---------- audit 3, étape 1 ----------

func TestRoutesPubliquesLimiteesEnTaille(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	c := newClient(t, srv, app.certFP, nil)
	big := strings.Repeat("a", 200<<10) // 200 Kio
	for _, path := range []string{"/api/prelogin", "/api/login", "/api/recover/start", "/api/recover", "/api/register", "/api/setup"} {
		if code, _, _ := c.do("POST", path, map[string]any{"username": "alice", "pad": big}); code != http.StatusBadRequest {
			t.Fatalf("%s accepte un corps de 200 Kio (%d)", path, code)
		}
	}
}

func TestNomDUtilisateurTropLong(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	c := newClient(t, srv, app.certFP, nil)
	long := strings.Repeat("x", 65)
	if code, _, _ := c.do("POST", "/api/login", map[string]any{"username": long, "authKey": randomBytes(32)}); code != http.StatusBadRequest {
		t.Fatalf("nom de 65 caractères accepté (%d)", code)
	}
	app.limiter.mu.Lock()
	defer app.limiter.mu.Unlock()
	for k := range app.limiter.fails {
		if strings.Contains(k, long) {
			t.Fatal("un nom refusé a quand même créé un compteur dans le limiteur")
		}
	}
}

func TestPurgeDuLimiteur(t *testing.T) {
	l := newLoginLimiter()
	l.mu.Lock()
	l.fails["vieux"] = []time.Time{time.Now().Add(-time.Hour)}
	l.fails["recent"] = []time.Time{time.Now()}
	l.mu.Unlock()
	l.purge()
	if _, ok := l.fails["vieux"]; ok {
		t.Fatal("compteur périmé gardé en mémoire")
	}
	if _, ok := l.fails["recent"]; !ok {
		t.Fatal("compteur récent effacé")
	}
}

func TestEmpreinteNumerotee(t *testing.T) {
	fp := "533a2ffc2a2770db0dad6afb8a161436d06f6a3f1ebacee25f0b9a3d5255be83"
	out := fingerprintNumbered(fp)
	for i, g := range []string{"533A", "8A16", "CEE2", "BE83"} {
		if !strings.Contains(out, g) {
			t.Fatalf("groupe %d (%s) absent : %q", i, g, out)
		}
	}
	if !strings.Contains(out, "7.") || !strings.Contains(out, "16.") {
		t.Fatal("numéros de groupes absents")
	}
}

func TestReconstructionIgnoreLesDoublons(t *testing.T) {
	app, srv, c := setupAdmin(t)
	id, _ := c.upload("IMG_1.jpg", []byte("photo"))
	bob := newClient(t, srv, app.certFP, nil)
	token, _ := app.createInvite(nil, false)
	bob.register("/api/register", "bob", "motdepasse2", map[string]any{"token": token})
	// la photo d'alice copiée dans le dossier de bob
	for _, v := range []string{"k", "o"} {
		data, _ := os.ReadFile(app.blobPath(1, id, v))
		os.MkdirAll(filepath.Dir(app.blobPath(2, id, v)), 0o755)
		os.WriteFile(app.blobPath(2, id, v), data, 0o644)
	}
	dir := t.TempDir()
	db2, _ := openDB(filepath.Join(dir, "pellicule.db"))
	t.Cleanup(func() { db2.Close() })
	app2 := &App{db: db2, dataDir: dir, libDir: app.libDir, limiter: newLoginLimiter(), secret: serverSecret(db2)}
	var out bytes.Buffer
	if err := app2.rebuild(strings.NewReader("RECONSTRUIRE\n"), &out); err != nil {
		t.Fatalf("un doublon bloque la reconstruction : %v", err)
	}
	var owner int64
	db2.QueryRow(`SELECT owner_id FROM assets WHERE id=?`, id).Scan(&owner)
	if owner != 1 || !strings.Contains(out.String(), "déjà présent") {
		t.Fatalf("doublon mal traité (propriétaire %d) :\n%s", owner, out.String())
	}
}

func TestFichierDeCleModifieEstReecrit(t *testing.T) {
	app, _, c := setupAdmin(t)
	id, _ := c.upload("IMG_1.jpg", []byte("photo"))
	p := app.blobPath(1, id, "k")
	good, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(`{"format":1,"kind":"image"}`), 0o644)
	app.syncFiles()
	if now, _ := os.ReadFile(p); !bytes.Equal(now, good) {
		t.Fatal("fichier de clé modifié non réécrit")
	}
	os.Remove(p)
	app.syncFiles()
	if now, _ := os.ReadFile(p); !bytes.Equal(now, good) {
		t.Fatal("fichier de clé absent non réécrit")
	}
}

func TestPasDImportPourUnCompteNonRepris(t *testing.T) {
	app, _, _ := setupAdmin(t)
	app.db.Exec(`UPDATE users SET auth_hash='' WHERE username='alice'`) // comme après rebuild
	dir := filepath.Join(app.libDir, "Import", "alice")
	os.MkdirAll(dir, 0o755)
	f := filepath.Join(dir, "a.jpg")
	os.WriteFile(f, []byte("\xff\xd8\xff pas vraiment une photo"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(f, old, old)
	app.scanImports()
	var n int
	app.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&n)
	if n != 0 {
		t.Fatal("import scellé pour une clé publique non vérifiée")
	}
	if _, err := os.Stat(f); err != nil {
		t.Fatal("fichier en attente supprimé")
	}
	// une fois le compte repris, le fichier est importé
	app.db.Exec(`UPDATE users SET auth_hash='x' WHERE username='alice'`)
	app.scanImports()
	app.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d élément(s) importé(s) après la reprise", n)
	}
}

func TestInscriptionRateeGardeLInvitation(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	token, _ := app.createInvite(nil, false)
	c := newClient(t, srv, app.certFP, nil)
	if code, _ := c.register("/api/register", "alice", "motdepasse2", map[string]any{"token": token}); code != http.StatusBadRequest {
		t.Fatalf("nom déjà pris : %d", code)
	}
	if code, _ := c.register("/api/register", "bob", "motdepasse2", map[string]any{"token": token}); code != 200 {
		t.Fatalf("invitation perdue après un échec : %d", code)
	}
	if code, _ := c.register("/api/register", "carole", "motdepasse3", map[string]any{"token": token}); code != http.StatusNotFound {
		t.Fatalf("invitation réutilisée : %d", code)
	}
	if _, err := os.Stat(filepath.Join(app.libDir, "u2", "compte.json")); err != nil {
		t.Fatal("compte.json non écrit après l'inscription")
	}
}

func TestEtatDesFichiersPourLAdminSeulement(t *testing.T) {
	app, srv, c := setupAdmin(t)
	app.syncFiles()
	_, st, _ := c.do("GET", "/api/stats", nil)
	f, ok := st["files"].(map[string]any)
	if !ok || f["lastSync"].(float64) <= 0 || f["failed"].(float64) != 0 {
		t.Fatalf("état des fichiers absent pour l'admin : %v", st["files"])
	}
	bob := newClient(t, srv, app.certFP, nil)
	token, _ := app.createInvite(nil, false)
	bob.register("/api/register", "bob", "motdepasse2", map[string]any{"token": token})
	if _, st, _ := bob.do("GET", "/api/stats", nil); st["files"] != nil {
		t.Fatal("état des fichiers montré à un non-administrateur")
	}
	// disque des photos inaccessible : l'échec est compté
	os.RemoveAll(app.libDir)
	os.WriteFile(app.libDir, []byte("pas un dossier"), 0o644)
	app.syncFiles()
	if app.files.failed.Load() == 0 {
		t.Fatal("écritures ratées non comptées")
	}
}

func TestTransactionLibereeApresUnePanique(t *testing.T) {
	app, _, _ := setupAdmin(t)
	func() {
		defer func() { recover() }()
		app.inTx(func(tx *sql.Tx) error { panic("boum") })
	}()
	done := make(chan error, 1)
	go func() {
		_, err := app.db.Exec(`INSERT INTO settings(key, value) VALUES ('apres-panique', x'01')`)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("verrou d'écriture gardé après une panique")
	}
}

func TestDelaiDEcritureProportionnel(t *testing.T) {
	if d := blobWriteTimeout(1 << 20); d > 11*time.Minute {
		t.Fatalf("petit fichier : %v", d)
	}
	if d := blobWriteTimeout(1 << 30); d < 30*time.Minute || d > 6*time.Hour {
		t.Fatalf("1 Gio : %v", d)
	}
	if d := blobWriteTimeout(1 << 40); d != 6*time.Hour {
		t.Fatalf("plafond : %v", d)
	}
}

func TestNomsDUtilisateur(t *testing.T) {
	for _, ok := range []string{"alice", "Élodie", "jean-marc", "m.dupont", "zoé_2"} {
		if validUsername(ok) != nil {
			t.Errorf("%q refusé", ok)
		}
	}
	for _, bad := range []string{
		"a", "al ice", "a\u0000b", "bob​", "élodie", // contrôle, invisible, accent combinant (forme décomposée)
		"аdmin", "αlice", "a/b", "..x", ".cache", "Import", "import",
		"aǀice", "ɑlice", "alıce", "ſam", "ａｄｍｉｎ", "ᴀᴅᴍɪɴ", "a×b",
	} {
		if validUsername(bad) == nil {
			t.Errorf("%q accepté", bad)
		}
	}
	app, srv, _ := setupAdmin(t)
	c := newClient(t, srv, app.certFP, nil)
	token, _ := app.createInvite(nil, false)
	if code, _ := c.register("/api/register", "Élodie", "motdepasse2", map[string]any{"token": token}); code != 200 {
		t.Fatalf("Élodie : %d", code)
	}
	token, _ = app.createInvite(nil, false)
	// même nom à la casse près (hors ASCII : COLLATE NOCASE ne le voit pas)
	if code, _ := c.register("/api/register", "élodie", "motdepasse3", map[string]any{"token": token}); code != http.StatusBadRequest {
		t.Fatalf("élodie acceptée à côté d'Élodie : %d", code)
	}
}

func TestSuppressionDuCompteVideSonDossierDImport(t *testing.T) {
	app, srv, _ := setupAdmin(t)
	bob := newClient(t, srv, app.certFP, nil)
	token, _ := app.createInvite(nil, false)
	bob.register("/api/register", "bob", "motdepasse2", map[string]any{"token": token})
	app.db.Exec(`UPDATE users SET username='bob+x' WHERE id=2`) // nom permis avant la 0.5
	dir := filepath.Join(app.libDir, "Import", "bob+x")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "en-attente.jpg"), []byte("x"), 0o644)
	if err := app.deleteUser(2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("Import/bob+x laissé après la suppression du compte")
	}
	if _, err := os.Stat(filepath.Join(app.libDir, "Import", "alice")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestChangementDeMotDePasseDeconnecteLesTelephones(t *testing.T) {
	app, srv, c := setupAdmin(t)
	pc := phoneCert(t)
	phone := newClient(t, srv, app.certFP, &pc)
	_, pre, _ := phone.do("POST", "/api/prelogin", map[string]string{"username": "alice"})
	auth, _ := PasswordKeys("motdepasse1", b64field(pre, "salt"))
	if code, _, raw := phone.do("POST", "/api/devices/login", map[string]any{"username": "alice", "authKey": auth, "name": "Pixel"}); code != 200 {
		t.Fatalf("appairage : %s", raw)
	}
	change := func(from, to string, devices bool) int {
		_, pre, _ := c.do("POST", "/api/prelogin", map[string]string{"username": "alice"})
		cur, _ := PasswordKeys(from, b64field(pre, "salt"))
		salt := randomBytes(SaltSize)
		ak, wk := PasswordKeys(to, salt)
		code, _, _ := c.do("POST", "/api/me/password", map[string]any{"currentAuthKey": cur, "authKey": ak, "salt": salt,
			"wrappedMK": Seal(wk, c.mk, AADMasterKey), "logoutDevices": devices})
		return code
	}
	var n int
	if change("motdepasse1", "motdepasse2", false) != 200 {
		t.Fatal("changement refusé")
	}
	app.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&n)
	if n != 1 {
		t.Fatal("téléphone déconnecté sans qu'on le demande")
	}
	if change("motdepasse2", "motdepasse3", true) != 200 {
		t.Fatal("changement refusé")
	}
	app.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&n)
	if n != 0 {
		t.Fatal("téléphone resté appairé")
	}
}

func TestReconstructionDUnCompteAuNomAncien(t *testing.T) {
	app, _, _ := setupAdmin(t)
	app.db.Exec(`UPDATE users SET username='O''Brien' WHERE id=1`) // permis avant la 0.5
	app.writeAccountFile(1)
	if odd := app.oddUsernames(); len(odd) != 1 {
		t.Fatalf("compte au nom ancien non signalé : %v", odd)
	}
	dir := t.TempDir()
	db2, _ := openDB(filepath.Join(dir, "pellicule.db"))
	t.Cleanup(func() { db2.Close() })
	app2 := &App{db: db2, dataDir: dir, libDir: app.libDir, limiter: newLoginLimiter(), secret: serverSecret(db2)}
	var out bytes.Buffer
	if err := app2.rebuild(strings.NewReader("RECONSTRUIRE\n"), &out); err != nil {
		t.Fatal(err)
	}
	var n int
	db2.QueryRow(`SELECT COUNT(*) FROM users WHERE username='O''Brien'`).Scan(&n)
	if n != 1 || !strings.Contains(out.String(), "hors des règles actuelles") {
		t.Fatalf("compte au nom ancien perdu ou non signalé : %s", out.String())
	}
}
