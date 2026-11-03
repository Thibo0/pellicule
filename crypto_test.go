package main

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// go test -run TestVecteurs -update   régénère testdata/vectors.json (à faire seulement si le format change).
var update = flag.Bool("update", false, "régénérer testdata/vectors.json")

const vectorsPath = "testdata/vectors.json"

func h(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func hx(b []byte) string { return hex.EncodeToString(b) }

// pattern : données de test déterministes, reproductibles en JS et en Kotlin.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + 7) & 0xff)
	}
	return b
}

type vectors struct {
	Password struct {
		Password, Salt, AuthKey, WrapKey string
	}
	Recovery struct {
		Key, Formatted, WrapKey string
	}
	Seal []struct {
		Key, Nonce, Plain, AAD, Box string
	}
	Stream []struct {
		Key, Prefix string
		Length      int
		CipherSHA   string // SHA-256 du chiffré complet
		Cipher      string `json:",omitempty"` // chiffré complet (petits cas seulement)
		ContentHash string
		DedupTag    string
	}
	FileKeys struct {
		MasterKey, ContentHash, FK, Prefix string
	}
	DeviceTag struct {
		MasterKey, Name string
		Size            int64
		Tag             string
	}
	Sealed struct {
		RecipientPKCS8, RecipientPub, EphPriv, Nonce, Plain, Box string
	}
}

func buildVectors(t *testing.T) vectors {
	var v vectors
	mk := h("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")

	v.Password.Password = "correct horse ⚡ batterie"
	v.Password.Salt = "a0a1a2a3a4a5a6a7a8a9aaabacadaeaf"
	ak, wk := PasswordKeys(v.Password.Password, h(v.Password.Salt))
	v.Password.AuthKey, v.Password.WrapKey = hx(ak), hx(wk)

	rk := h("f0e1d2c3b4a5968778695a4b3c2d1e0f00112233445566778899aabbccddeeff")
	v.Recovery.Key, v.Recovery.Formatted, v.Recovery.WrapKey = hx(rk), FormatRecoveryKey(rk), hx(RecoveryWrapKey(rk))

	for _, c := range []struct {
		plain []byte
		aad   string
	}{
		{mk, AADMasterKey}, {[]byte(`{"name":"IMG_0001.HEIC","mime":"image/heic"}`), AADMeta}, {nil, AADThumb},
	} {
		nonce := h("101112131415161718191a1b")
		v.Seal = append(v.Seal, struct{ Key, Nonce, Plain, AAD, Box string }{
			hx(wk), hx(nonce), hx(c.plain), c.aad, hx(sealWithNonce(wk, nonce, c.plain, c.aad)),
		})
	}

	fk := h("202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f")
	prefix := h("4142434445464748")
	for _, n := range []int{0, 1, 1000, ChunkSize, 2*ChunkSize + 12345} {
		var out bytes.Buffer
		if err := encryptStreamPrefix(fk, prefix, bytes.NewReader(pattern(n)), &out); err != nil {
			t.Fatal(err)
		}
		if int64(out.Len()) != StreamSize(int64(n)) {
			t.Fatalf("StreamSize(%d) = %d, réel %d", n, StreamSize(int64(n)), out.Len())
		}
		sum := sha256.Sum256(out.Bytes())
		ch := NewContentHasher()
		ch.Write(pattern(n))
		hash := ch.Sum()
		e := struct {
			Key, Prefix string
			Length      int
			CipherSHA   string
			Cipher      string `json:",omitempty"`
			ContentHash string
			DedupTag    string
		}{hx(fk), hx(prefix), n, hx(sum[:]), "", hx(hash), hx(DedupTag(mk, hash))}
		if n <= 1000 {
			e.Cipher = hx(out.Bytes())
		}
		v.Stream = append(v.Stream, e)
	}

	ch := h(v.Stream[len(v.Stream)-1].ContentHash)
	fkd, pfx := FileKeys(mk, ch)
	v.FileKeys.MasterKey, v.FileKeys.ContentHash, v.FileKeys.FK, v.FileKeys.Prefix = hx(mk), hx(ch), hx(fkd), hx(pfx)

	v.DeviceTag.MasterKey, v.DeviceTag.Name, v.DeviceTag.Size = hx(mk), "IMG_20260101_120000.jpg", 4242424
	v.DeviceTag.Tag = hx(DeviceTag(mk, v.DeviceTag.Name, v.DeviceTag.Size))

	recip, _ := ecdh.P256().NewPrivateKey(h("c9afa9d845ba75166b5c215767b1d6934e50c3db36e89b127b8a622b120f6721"))
	eph, _ := ecdh.P256().NewPrivateKey(h("519b423d715f8b581f4fa8ee59f4771a5b44c8130b4e3eacca54a56dda72b464"))
	pkcs8, err := x509.MarshalPKCS8PrivateKey(recip)
	if err != nil {
		t.Fatal(err)
	}
	nonce := h("505152535455565758595a5b")
	box, err := sealToPublicWith(eph, nonce, recip.PublicKey().Bytes(), fk)
	if err != nil {
		t.Fatal(err)
	}
	v.Sealed.RecipientPKCS8 = base64.StdEncoding.EncodeToString(pkcs8)
	v.Sealed.RecipientPub, v.Sealed.EphPriv, v.Sealed.Nonce = hx(recip.PublicKey().Bytes()), hx(eph.Bytes()), hx(nonce)
	v.Sealed.Plain, v.Sealed.Box = hx(fk), hx(box)
	return v
}

func TestVecteurs(t *testing.T) {
	got := buildVectors(t)
	data, _ := json.MarshalIndent(got, "", "  ")
	if *update {
		os.MkdirAll(filepath.Dir(vectorsPath), 0o755)
		if err := os.WriteFile(vectorsPath, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatalf("%v (lancer : go test -run TestVecteurs -update)", err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(data)) {
		t.Fatal("le format chiffré a changé : les vecteurs ne correspondent plus (les clients JS/Kotlin seraient incompatibles)")
	}
}

func TestSealOpen(t *testing.T) {
	k := randomBytes(KeySize)
	box := Seal(k, []byte("secret"), AADMeta)
	if p, err := Open(k, box, AADMeta); err != nil || string(p) != "secret" {
		t.Fatal("aller-retour impossible")
	}
	if _, err := Open(k, box, AADThumb); err == nil {
		t.Fatal("une boîte a été acceptée avec une autre étiquette")
	}
	box[len(box)-1] ^= 1
	if _, err := Open(k, box, AADMeta); err == nil {
		t.Fatal("une boîte altérée a été acceptée")
	}
}

func TestStreamAllerRetourEtTroncature(t *testing.T) {
	k := randomBytes(KeySize)
	for _, n := range []int{0, 5, ChunkSize - 1, ChunkSize, ChunkSize + 1, 3 * ChunkSize} {
		var enc, dec bytes.Buffer
		EncryptStream(k, bytes.NewReader(pattern(n)), &enc)
		if err := DecryptStream(k, bytes.NewReader(enc.Bytes()), &dec); err != nil || !bytes.Equal(dec.Bytes(), pattern(n)) {
			t.Fatalf("n=%d : aller-retour impossible (%v)", n, err)
		}
		if n > ChunkSize {
			// couper le dernier bloc : le bloc précédent n'a pas le drapeau « dernier », le fichier doit être refusé
			cut := enc.Bytes()[:StreamHdrSize+ChunkSize+TagSize]
			if err := DecryptStream(k, bytes.NewReader(cut), &bytes.Buffer{}); err == nil {
				t.Fatalf("n=%d : un fichier tronqué a été accepté", n)
			}
		}
	}
}

func TestSealToPublic(t *testing.T) {
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	box, err := SealToPublic(priv.PublicKey().Bytes(), []byte("clé de fichier"))
	if err != nil {
		t.Fatal(err)
	}
	if p, err := OpenFromPublic(priv, box); err != nil || string(p) != "clé de fichier" {
		t.Fatal("aller-retour impossible")
	}
	other, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := OpenFromPublic(other, box); err == nil {
		t.Fatal("un autre compte a ouvert l'enveloppe")
	}
}

func TestCleDeSecours(t *testing.T) {
	k := randomBytes(KeySize)
	s := FormatRecoveryKey(k)
	back, err := ParseRecoveryKey(" " + s + " ")
	if err != nil || !bytes.Equal(back, k) {
		t.Fatalf("relecture impossible : %q", s)
	}
}
