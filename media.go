package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "image/gif"
	_ "image/png"

	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

var mediaTypes = map[string][2]string{
	".jpg": {"image", "image/jpeg"}, ".jpeg": {"image", "image/jpeg"}, ".png": {"image", "image/png"},
	".gif": {"image", "image/gif"}, ".webp": {"image", "image/webp"}, ".heic": {"image", "image/heic"},
	".heif": {"image", "image/heif"}, ".avif": {"image", "image/avif"}, ".tif": {"image", "image/tiff"},
	".tiff": {"image", "image/tiff"}, ".dng": {"image", "image/x-adobe-dng"},
	".mp4": {"video", "video/mp4"}, ".m4v": {"video", "video/mp4"}, ".mov": {"video", "video/quicktime"},
	".webm": {"video", "video/webm"}, ".3gp": {"video", "video/3gpp"}, ".mkv": {"video", "video/x-matroska"},
	".avi": {"video", "video/x-msvideo"}, ".mts": {"video", "video/mp2t"},
}

func mediaType(name string) (kind, mime string, ok bool) {
	t, ok := mediaTypes[strings.ToLower(filepath.Ext(name))]
	return t[0], t[1], ok
}

// signatureOK : le contenu commence bien comme le format annoncé par l'extension. libvips et ffmpeg choisissent
// leur décodeur d'après le contenu : sans ce contrôle, un faux .jpg déposé dans Import/ pourrait passer par un
// décodeur exotique (ImageMagick, playlist HLS…) capable de lire d'autres fichiers de la machine.
func signatureOK(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := make([]byte, 200)
	n, _ := f.Read(h)
	return headerMatches(strings.ToLower(filepath.Ext(path)), h[:n])
}

func headerMatches(ext string, h []byte) bool {
	at := func(off int, s string) bool { return len(h) >= off+len(s) && string(h[off:off+len(s)]) == s }
	// MP4, MOV, HEIC, AVIF… (ISO BMFF / QuickTime) : boîte « ftyp », ou boîte QuickTime ancienne en tête
	isoBox := func() bool {
		for _, b := range []string{"ftyp", "moov", "mdat", "wide", "free", "skip", "pnot"} {
			if at(4, b) {
				return true
			}
		}
		return false
	}
	switch ext {
	case ".jpg", ".jpeg":
		return at(0, "\xff\xd8\xff")
	case ".png":
		return at(0, "\x89PNG\r\n\x1a\n")
	case ".gif":
		return at(0, "GIF87a") || at(0, "GIF89a")
	case ".webp":
		return at(0, "RIFF") && at(8, "WEBP")
	case ".heic", ".heif", ".avif":
		return at(4, "ftyp")
	case ".tif", ".tiff", ".dng":
		return at(0, "II*\x00") || at(0, "MM\x00*")
	case ".mp4", ".m4v", ".mov", ".3gp":
		return isoBox()
	case ".webm", ".mkv":
		return at(0, "\x1a\x45\xdf\xa3")
	case ".avi":
		return at(0, "RIFF") && at(8, "AVI ")
	case ".mts":
		// MPEG-TS (paquets de 188 octets) ou M2TS d'AVCHD (4 octets d'horodatage + 188)
		return at(0, "G") && at(188, "G") || at(4, "G") && at(196, "G")
	}
	return false
}

// formats que ffmpeg et ffprobe ont le droit d'ouvrir (noms de démultiplexeurs) : pas de playlist HLS, concat…
const videoFormats = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,avi,mpegts"

// wallClock convertit un instant en "heure murale" stockée comme UTC :
// les dates EXIF n'ont pas de fuseau, on aligne tout le reste dessus.
func wallClock(t time.Time) time.Time {
	l := t.In(time.Local)
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), l.Second(), 0, time.UTC)
}

func parseExifDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if len(s) < 19 {
		return time.Time{}
	}
	t, err := time.Parse("2006:01:02 15:04:05", s[:19])
	if err != nil || t.Year() < 1900 {
		return time.Time{}
	}
	return t
}

func readExif(path string) *exif.Exif {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	x, err := exif.Decode(bufio.NewReader(f))
	if err != nil {
		return nil
	}
	return x
}

func exifDate(x *exif.Exif) time.Time {
	for _, name := range []exif.FieldName{exif.DateTimeOriginal, exif.DateTimeDigitized, exif.DateTime} {
		if tag, err := x.Get(name); err == nil {
			if s, err := tag.StringVal(); err == nil {
				if t := parseExifDate(s); !t.IsZero() {
					return t
				}
			}
		}
	}
	return time.Time{}
}

func exifOrientation(x *exif.Exif) int {
	if x == nil {
		return 1
	}
	if tag, err := x.Get(exif.Orientation); err == nil {
		if v, err := tag.Int(0); err == nil {
			return v
		}
	}
	return 1
}

// --- outils externes (libvips / ffmpeg), facultatifs mais recommandés ---

type tools struct {
	vipsthumbnail, vipsheader, ffmpeg, ffprobe string
	vipsTooOld                                 string // version de libvips écartée (voir detectTools)
}

func detectTools() tools {
	look := func(n string) string { p, _ := exec.LookPath(n); return p }
	t := tools{vipsthumbnail: look("vipsthumbnail"), vipsheader: look("vipsheader"), ffmpeg: look("ffmpeg"), ffprobe: look("ffprobe")}
	if t.vipsthumbnail != "" || t.vipsheader != "" {
		// VIPS_BLOCK_UNTRUSTED (R16) n'existe qu'à partir de libvips 8.13 : avant, il est ignoré sans erreur et
		// un fichier piégé déposé dans Import/ pourrait atteindre ImageMagick. On n'utilise pas un libvips plus ancien.
		var out []byte
		if v := look("vips"); v != "" {
			out, _ = run(10*time.Second, v, "--version") // « vips-8.14.1 »
		}
		if _, ok := vipsVersionOK(string(out)); !ok && t.vipsthumbnail != "" {
			out, _ = run(10*time.Second, t.vipsthumbnail, "--vips-version") // option commune aux outils libvips
		}
		if ver, ok := vipsVersionOK(string(out)); !ok {
			t.vipsthumbnail, t.vipsheader, t.vipsTooOld = "", "", ver
		}
	}
	return t
}

var vipsVersionRe = regexp.MustCompile(`(\d+)\.(\d+)\.\d+`)

// vipsVersionOK lit la sortie de « vips --version » (« vips-8.14.1 ») ; vrai à partir de 8.13.
func vipsVersionOK(out string) (string, bool) {
	m := vipsVersionRe.FindStringSubmatch(out)
	if m == nil {
		return "inconnue", false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return m[0], major > 8 || (major == 8 && minor >= 13)
}

func (t tools) report() {
	if t.vipsTooOld != "" {
		log.Printf("libvips %s ignoré : il faut la version 8.13 ou plus pour bloquer les décodeurs non sûrs "+
			"(Debian 12 / Ubuntu 24.04). Miniatures JPEG/PNG/WebP seulement, pas de HEIC.", t.vipsTooOld)
	} else if t.vipsthumbnail == "" {
		log.Print("libvips absent : miniatures JPEG/PNG/WebP seulement (HEIC iPhone non pris en charge). Installer libvips-tools.")
	}
	if t.ffmpeg == "" || t.ffprobe == "" {
		log.Print("ffmpeg absent : pas de miniature ni de date pour les vidéos. Installer ffmpeg.")
	}
}

func run(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	// libvips ≥ 8.13 : refuse les décodeurs non sûrs (ImageMagick, Matlab, FITS…) même si le contenu les désigne
	cmd.Env = append(os.Environ(), "VIPS_BLOCK_UNTRUSTED=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return out, fmt.Errorf("%s : %v %s", filepath.Base(name), err, msg)
	}
	return out, nil
}

type meta struct {
	taken    time.Time
	width    int
	height   int
	duration float64
}

func (a *App) probe(path, kind, mime string) meta {
	var m meta
	if kind == "video" {
		if a.tools.ffprobe == "" {
			return m
		}
		out, err := run(time.Minute, a.tools.ffprobe, "-v", "quiet", "-protocol_whitelist", "file", "-format_whitelist", videoFormats, "-print_format", "json", "-show_format", "-show_streams", path)
		if err != nil {
			return m
		}
		var p struct {
			Format struct {
				Duration string            `json:"duration"`
				Tags     map[string]string `json:"tags"`
			} `json:"format"`
			Streams []struct {
				CodecType    string            `json:"codec_type"`
				Width        int               `json:"width"`
				Height       int               `json:"height"`
				Tags         map[string]string `json:"tags"`
				SideDataList []struct {
					Rotation float64 `json:"rotation"`
				} `json:"side_data_list"`
			} `json:"streams"`
		}
		if json.Unmarshal(out, &p) != nil {
			return m
		}
		m.duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
		// iPhone : com.apple.quicktime.creationdate contient le fuseau, plus fiable
		for _, k := range []string{"com.apple.quicktime.creationdate", "creation_time"} {
			if v := p.Format.Tags[k]; v != "" {
				if t, err := time.Parse(time.RFC3339Nano, v); err == nil && t.Year() > 1970 {
					m.taken = wallClock(t)
					break
				}
				if t, err := time.Parse("2006-01-02T15:04:05-0700", v); err == nil {
					m.taken = wallClock(t)
					break
				}
			}
		}
		for _, s := range p.Streams {
			if s.CodecType != "video" {
				continue
			}
			m.width, m.height = s.Width, s.Height
			rot := 0.0
			if r, err := strconv.ParseFloat(s.Tags["rotate"], 64); err == nil {
				rot = r
			}
			for _, sd := range s.SideDataList {
				if sd.Rotation != 0 {
					rot = sd.Rotation
				}
			}
			if int(rot)%180 != 0 {
				m.width, m.height = m.height, m.width
			}
			break
		}
		return m
	}

	if a.tools.vipsheader != "" {
		out, err := run(time.Minute, a.tools.vipsheader, "-a", path)
		if err == nil {
			var orient int
			for _, line := range strings.Split(string(out), "\n") {
				k, v, ok := strings.Cut(line, ": ")
				if !ok {
					continue
				}
				switch strings.TrimSpace(k) {
				case "width":
					m.width, _ = strconv.Atoi(strings.TrimSpace(v))
				case "height":
					m.height, _ = strconv.Atoi(strings.TrimSpace(v))
				case "orientation":
					orient, _ = strconv.Atoi(strings.Fields(v + " 0")[0])
				case "exif-ifd2-DateTimeOriginal":
					m.taken = parseExifDate(v)
				case "exif-ifd0-DateTime":
					if m.taken.IsZero() {
						m.taken = parseExifDate(v)
					}
				}
			}
			if orient >= 5 {
				m.width, m.height = m.height, m.width
			}
			if m.width > 0 {
				return m
			}
		}
	}

	x := readExif(path)
	if x != nil {
		m.taken = exifDate(x)
	}
	if f, err := os.Open(path); err == nil {
		if cfg, _, err := image.DecodeConfig(f); err == nil {
			m.width, m.height = cfg.Width, cfg.Height
			if exifOrientation(x) >= 5 {
				m.width, m.height = m.height, m.width
			}
		}
		f.Close()
	}
	return m
}

// --- miniatures ---

const (
	smallSize   = 400  // carré, pour la grille
	previewSize = 1600 // côté max, pour la visionneuse
)

// makeThumbs écrit la miniature carrée et l'aperçu en JPEG (en clair, dans un dossier temporaire :
// l'import les chiffre aussitôt puis les efface).
func (a *App) makeThumbs(src, kind, small, preview string) error {

	if kind == "video" {
		if a.tools.ffmpeg == "" {
			return errors.New("ffmpeg absent")
		}
		var err error
		for _, at := range []string{"1", "0"} {
			_, err = run(3*time.Minute, a.tools.ffmpeg, "-v", "error", "-y", "-protocol_whitelist", "file", "-format_whitelist", videoFormats, "-ss", at, "-i", src, "-frames:v", "1",
				"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", previewSize, previewSize),
				"-q:v", "4", preview)
			if err == nil {
				if st, e := os.Stat(preview); e == nil && st.Size() > 0 {
					break
				}
				err = errors.New("ffmpeg n'a produit aucune image")
			}
		}
		if err != nil {
			return err
		}
		return a.resize(preview, small, smallSize, true)
	}

	if err := a.resize(src, preview, previewSize, false); err != nil {
		return err
	}
	// la petite miniature part de l'aperçu : bien plus rapide qu'à partir de l'original
	return a.resize(preview, small, smallSize, true)
}

// resize écrit un JPEG ; square=true recadre au centre en carré.
func (a *App) resize(src, dst string, size int, square bool) error {
	if a.tools.vipsthumbnail != "" {
		var args []string
		if square {
			args = []string{src, "--size", fmt.Sprintf("%dx%d", size, size), "--smartcrop", "centre"}
		} else {
			args = []string{src, "--size", fmt.Sprintf("%dx%d>", size, size)} // ">" : ne jamais agrandir
		}
		args = append(args, "-o", dst+"[Q=80,strip]")
		if _, err := run(3*time.Minute, a.tools.vipsthumbnail, args...); err == nil {
			return nil
		} else if !goDecodable(src) {
			return err
		}
	}
	return goResize(src, dst, size, square)
}

func goDecodable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	}
	return false
}

// maxGoPixels : au-delà, pas de miniature par le décodeur Go (libvips, lui, lit l'image par morceaux).
const maxGoPixels = 50_000_000

// goResize : repli en Go pur (sans libvips) pour JPEG/PNG/GIF/WebP.
func goResize(src, dst string, size int, square bool) error {
	if !goDecodable(src) {
		return errors.New("format non décodable sans libvips")
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	// les dimensions d'abord : image.Decode alloue tout le bitmap d'un coup (4 octets/pixel en RVBA), et le
	// service est limité à 450 Mo. Sans ce contrôle, une image géante le ferait tuer (deux fois) avant d'être écartée.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxGoPixels {
		return fmt.Errorf("image trop grande pour le décodeur intégré (%d × %d)", cfg.Width, cfg.Height)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var srcRect image.Rectangle
	var tw, th int
	if square {
		side := min(w, h)
		x0, y0 := b.Min.X+(w-side)/2, b.Min.Y+(h-side)/2
		srcRect = image.Rect(x0, y0, x0+side, y0+side)
		tw, th = min(size, side), min(size, side)
	} else {
		srcRect = b
		scale := min(1, float64(size)/float64(max(w, h)))
		tw, th = max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	}
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.CatmullRom.Scale(out, out.Bounds(), img, srcRect, draw.Src, nil)
	// rotation EXIF après réduction : beaucoup moins de pixels à déplacer
	final := applyOrientation(out, exifOrientation(readExif(src)))

	tmp := dst + ".tmp"
	g, err := os.Create(tmp)
	if err != nil {
		return err
	}
	err = jpeg.Encode(g, final, &jpeg.Options{Quality: 80})
	if cerr := g.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func applyOrientation(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := w, h
	if o >= 5 {
		ow, oh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			out.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}
