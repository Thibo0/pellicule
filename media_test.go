package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngHeader : un PNG qui annonce w × h pixels, sans données (DecodeConfig ne lit que l'en-tête).
func pngHeader(w, h uint32) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8], ihdr[9] = 8, 6 // 8 bits, RVBA
	binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

// Une image géante ne doit pas être décodée en entier par le repli Go (≈ 1,6 Go pour 20 000 × 20 000).
func TestReplieGoRefuseLesImagesGeantes(t *testing.T) {
	src := filepath.Join(t.TempDir(), "geante.png")
	os.WriteFile(src, pngHeader(20000, 20000), 0o644)
	err := goResize(src, filepath.Join(t.TempDir(), "s.jpg"), 256, true)
	if err == nil || !strings.Contains(err.Error(), "trop grande") {
		t.Fatalf("image géante : %v", err)
	}
}

func TestVersionDeLibvips(t *testing.T) {
	for out, want := range map[string]bool{
		"vips-8.14.1":             true,
		"vips-8.13.0-Mon Jan  1":  true,
		"libvips 8.15.1":          true,
		"vips-9.0.0":              true,
		"vips-8.12.1":             false, // Ubuntu 22.04
		"vips-8.10.5-Sat Mar  6":  false, // Debian 11
		"":                        false,
		"commande introuvable 12": false,
	} {
		if _, ok := vipsVersionOK(out); ok != want {
			t.Errorf("%q : %v, attendu %v", out, ok, want)
		}
	}
}
