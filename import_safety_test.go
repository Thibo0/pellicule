package main

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSignatureSelonLExtension(t *testing.T) {
	ok := map[string]string{
		".jpg":  "\xff\xd8\xff\xe0",
		".png":  "\x89PNG\r\n\x1a\n",
		".gif":  "GIF89a",
		".webp": "RIFF\x00\x00\x00\x00WEBPVP8 ",
		".heic": "\x00\x00\x00\x18ftypheic",
		".dng":  "II*\x00",
		".mp4":  "\x00\x00\x00\x20ftypisom",
		".mov":  "\x00\x00\x00\x08wide",
		".mkv":  "\x1a\x45\xdf\xa3",
		".avi":  "RIFF\x00\x00\x00\x00AVI LIST",
		".mts":  "G" + strings.Repeat("\x00", 187) + "G",
	}
	for ext, h := range ok {
		if !headerMatches(ext, []byte(h)) {
			t.Errorf("%s légitime refusé", ext)
		}
	}
	bad := map[string]string{
		".jpg":  `<svg xmlns="http://www.w3.org/2000/svg"><image href="text:/etc/passwd"/></svg>`,
		".png":  "push graphic-context\nviewbox 0 0 640 480\nimage over 0,0 0,0 'text:/etc/passwd'",
		".mp4":  "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:10.0,\nfile:///etc/passwd\n",
		".webp": "RIFF\x00\x00\x00\x00AVI ",
		".heic": "",
		".mts":  "G",
	}
	for ext, h := range bad {
		if headerMatches(ext, []byte(h)) {
			t.Errorf("faux %s accepté", ext)
		}
	}
}

func dropImport(t *testing.T, app *App, name, content string) string {
	dir := filepath.Join(app.libDir, importDir, "alice")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	os.WriteFile(p, []byte(content), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	return p
}

func countAssets(app *App) (n int) {
	app.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&n)
	return
}

func TestImportRefuseUnFauxFichier(t *testing.T) {
	app, _, _ := setupAdmin(t)
	p := dropImport(t, app, "photo.jpg", "push graphic-context\nimage over 0,0 0,0 'text:/etc/passwd'\n")
	app.scanImports()
	if countAssets(app) != 0 {
		t.Fatal("faux .jpg importé")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("faux .jpg resté dans le dossier d'import (retenté sans fin)")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(p), refusedDir, "photo.jpg")); err != nil {
		t.Fatal("faux .jpg pas mis de côté :", err)
	}
	if n := app.refusedCount("alice"); n != 1 {
		t.Fatalf("%d fichier(s) refusé(s) affiché(s)", n)
	}
	// même nom une seconde fois : pas d'écrasement
	dropImport(t, app, "photo.jpg", "encore faux")
	app.scanImports()
	if n := app.refusedCount("alice"); n != 2 {
		t.Fatalf("%d fichier(s) refusé(s) après le second", n)
	}
}

func TestImportFichierPoison(t *testing.T) {
	app, _, _ := setupAdmin(t)
	// un premier essai interrompu (arrêt normal pendant une mise à jour) : le fichier est retenté
	dropImport(t, app, "a.jpg", "\xff\xd8\xff\xe0 photo")
	app.db.Exec(`INSERT INTO import_running(path, attempts) VALUES ('Import/alice/a.jpg', 1)`)
	app.scanImports()
	if countAssets(app) != 1 {
		t.Fatal("fichier non retenté après un seul arrêt")
	}
	var left int
	app.db.QueryRow(`SELECT COUNT(*) FROM import_running`).Scan(&left)
	if left != 0 {
		t.Fatal("import terminé encore marqué en cours")
	}
	// deux essais interrompus (le serveur plante dessus) : mis de côté, pas de boucle de plantages
	p := dropImport(t, app, "b.jpg", "\xff\xd8\xff\xe0 poison")
	app.db.Exec(`INSERT INTO import_running(path, attempts) VALUES ('Import/alice/b.jpg', 2)`)
	app.scanImports()
	if countAssets(app) != 1 {
		t.Fatal("fichier poison retraité")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("fichier poison resté dans le dossier d'import")
	}
	if app.refusedCount("alice") != 1 {
		t.Fatal("fichier poison pas mis de côté")
	}
}

// ffprobe ne doit pas suivre une playlist HLS (qui peut désigner des fichiers locaux). ffmpeg ≥ 6 ignore déjà une
// playlist sans extension .m3u8, pas la 5.1 de Debian 12 : on teste donc la liste blanche avec un vrai .m3u8.
func TestFFprobeRefuseLesPlaylists(t *testing.T) {
	ffprobe, err1 := exec.LookPath("ffprobe")
	ffmpeg, err2 := exec.LookPath("ffmpeg")
	if err1 != nil || err2 != nil {
		needTool(t, "ffmpeg")
	}
	dir := t.TempDir()
	if _, err := run(time.Minute, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=64x64:duration=1",
		"-f", "mpegts", filepath.Join(dir, "cible.ts")); err != nil {
		t.Skip("ffmpeg ne sait pas produire une vidéo de test :", err)
	}
	pl := filepath.Join(dir, "piege.m3u8")
	os.WriteFile(pl, []byte("#EXTM3U\n#EXT-X-TARGETDURATION:10\n#EXTINF:10.0,\ncible.ts\n#EXT-X-ENDLIST\n"), 0o644)
	// témoin : sans liste blanche de formats, ffprobe suit la playlist
	if _, err := run(time.Minute, ffprobe, "-v", "quiet", "-protocol_whitelist", "file", "-show_format", pl); err != nil {
		t.Fatal("témoin : playlist non ouverte même sans protection :", err)
	}
	if _, err := run(time.Minute, ffprobe, "-v", "quiet", "-protocol_whitelist", "file", "-format_whitelist", videoFormats, "-show_format", pl); err == nil {
		t.Fatal("playlist ouverte malgré -format_whitelist")
	}
	// une vraie vidéo reste lisible avec la liste blanche
	mp4 := filepath.Join(dir, "vraie.mp4")
	if _, err := run(time.Minute, ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=64x64:duration=1", "-pix_fmt", "yuv420p", mp4); err != nil {
		t.Fatal(err)
	}
	app := &App{tools: tools{ffmpeg: ffmpeg, ffprobe: ffprobe}}
	if m := app.probe(mp4, "video", "video/mp4"); m.width != 64 {
		t.Fatalf("vraie vidéo illisible avec la liste blanche (largeur %d)", m.width)
	}
	if !signatureOK(mp4) {
		t.Fatal("vraie vidéo refusée par la signature")
	}
	small, preview := filepath.Join(dir, "s.jpg"), filepath.Join(dir, "m.jpg")
	if err := app.makeThumbs(mp4, "video", small, preview); err != nil {
		t.Fatal("miniature de la vraie vidéo :", err)
	}
}

// libvips : avec VIPS_BLOCK_UNTRUSTED (posé par run), un format que seul ImageMagick sait lire est refusé,
// et une vraie photo donne toujours ses miniatures.
func TestVipsBloqueLesDecodeursNonSurs(t *testing.T) {
	header, err1 := exec.LookPath("vipsheader")
	thumb, err2 := exec.LookPath("vipsthumbnail")
	if err1 != nil || err2 != nil {
		needTool(t, "libvips")
	}
	dir := t.TempDir()
	// BMP 1×1 : pas de décodeur natif dans libvips, seulement magickload
	bmp := filepath.Join(dir, "image.bmp")
	os.WriteFile(bmp, []byte("BM\x3a\x00\x00\x00\x00\x00\x00\x00\x36\x00\x00\x00\x28\x00\x00\x00\x01\x00\x00\x00\x01\x00\x00\x00"+
		"\x01\x00\x18\x00\x00\x00\x00\x00\x04\x00\x00\x00\x13\x0b\x00\x00\x13\x0b\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"+
		"\x00\x00\xff\x00"), 0o644)
	if err := exec.Command(header, bmp).Run(); err != nil {
		t.Skip("libvips sans ImageMagick ici : rien à bloquer")
	}
	if _, err := run(time.Minute, header, bmp); err == nil {
		t.Fatal("décodeur ImageMagick utilisé malgré VIPS_BLOCK_UNTRUSTED")
	}

	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil)
	src := filepath.Join(dir, "photo.jpg")
	os.WriteFile(src, b.Bytes(), 0o644)
	app := &App{tools: tools{vipsthumbnail: thumb, vipsheader: header}}
	if m := app.probe(src, "image", "image/jpeg"); m.width != 800 {
		t.Fatalf("vraie photo mal lue (largeur %d)", m.width)
	}
	small, preview := filepath.Join(dir, "s.jpg"), filepath.Join(dir, "m.jpg")
	if err := app.makeThumbs(src, "image", small, preview); err != nil {
		t.Fatal("miniatures d'une vraie photo :", err)
	}
}

// needTool : en CI (PELLICULE_OUTILS=1), les outils sont installés exprès : leur absence est une erreur, pas un saut.
func needTool(t *testing.T, name string) {
	if os.Getenv("PELLICULE_OUTILS") == "1" {
		t.Fatal(name + " absent")
	}
	t.Skip(name + " absent")
}
