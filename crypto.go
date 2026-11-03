package main

// Noyau cryptographique de Pellicule (format v1), implémenté à l'identique en Go (ce fichier),
// en JavaScript (web/crypto.js) et en Kotlin (Crypto.kt). Les trois sont vérifiés contre les mêmes
// vecteurs de test (testdata/vectors.json). Le format est décrit dans docs/SECURITE.md.
//
// Le serveur ne s'en sert que pour l'import FolderSync (chiffrer un fichier pour la clé publique
// d'un compte) : il ne détient jamais la clé maître d'un utilisateur.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"strings"
)

const (
	KeySize       = 32
	NonceSize     = 12
	TagSize       = 16
	ChunkSize     = 1 << 20 // 1 Mio de clair par bloc
	StreamHdrSize = 16      // "PLC1" | taille de bloc (u32) | préfixe de nonce (8 octets)
	PBKDF2Iter    = 600_000
	SaltSize      = 16
	ECPubSize     = 65 // point P-256 non compressé
)

var streamMagic = []byte("PLC1")

// Étiquettes (AAD / info HKDF) : une clé chiffrée pour un usage ne peut pas être rejouée pour un autre.
const (
	LabelAuth       = "pellicule auth"
	LabelWrap       = "pellicule wrap"
	LabelRecovery   = "pellicule recovery"
	LabelRecAuth    = "pellicule recovery auth"
	LabelDedup      = "pellicule dedup"
	LabelSealECDH   = "pellicule seal"
	LabelFileKey    = "pellicule file key|"
	LabelFilePrefix = "pellicule file prefix|"
	AADMasterKey    = "pellicule/mk"
	AADFileKey      = "pellicule/fk"
	AADPrivKey      = "pellicule/priv"
	AADMeta         = "pellicule/meta"
	AADThumb        = "pellicule/thumb"
	AADPreview      = "pellicule/preview"
	AADAlbumName    = "pellicule/album"
	AADShareKey     = "pellicule/share-fk"
	AADSealedFK     = "pellicule/fk-sealed"
	AADDeviceTagNS  = "device|"
)

var errDecrypt = errors.New("déchiffrement impossible (clé incorrecte ou données altérées)")

// --- primitives ---

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func gcm(key []byte) cipher.AEAD {
	if len(key) != KeySize {
		panic("clé AES de taille invalide")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return g
}

// sealWithNonce : nonce (12) || AES-256-GCM(clair) || tag (16).
func sealWithNonce(key, nonce, plain []byte, aad string) []byte {
	out := append([]byte{}, nonce...)
	return gcm(key).Seal(out, nonce, plain, []byte(aad))
}

// Seal chiffre un petit message (clé, métadonnées, miniature) avec un nonce aléatoire.
func Seal(key, plain []byte, aad string) []byte {
	return sealWithNonce(key, randomBytes(NonceSize), plain, aad)
}

// Open déchiffre le résultat de Seal.
func Open(key, box []byte, aad string) ([]byte, error) {
	if len(box) < NonceSize+TagSize {
		return nil, errDecrypt
	}
	plain, err := gcm(key).Open(nil, box[:NonceSize], box[NonceSize:], []byte(aad))
	if err != nil {
		return nil, errDecrypt
	}
	return plain, nil
}

func hkdfKey(secret []byte, info string) []byte {
	k, err := hkdf.Key(sha256.New, secret, nil, info, KeySize)
	if err != nil {
		panic(err)
	}
	return k
}

// PasswordKeys dérive du mot de passe la clé d'authentification (envoyée au serveur, qui la hache)
// et la clé qui chiffre la clé maître (qui ne quitte jamais l'appareil).
func PasswordKeys(password string, salt []byte) (authKey, wrapKey []byte) {
	k, err := pbkdf2.Key(sha256.New, password, salt, PBKDF2Iter, KeySize)
	if err != nil {
		panic(err)
	}
	return hkdfKey(k, LabelAuth), hkdfKey(k, LabelWrap)
}

// RecoveryWrapKey : clé qui chiffre la clé maître à partir de la clé de secours (32 octets).
func RecoveryWrapKey(recovery []byte) []byte { return hkdfKey(recovery, LabelRecovery) }

// --- empreintes de dédoublonnage (le serveur ne voit que des HMAC, jamais le hash du contenu) ---

// ContentHasher : SHA-256 de la suite des SHA-256 de chaque bloc de 1 Mio du clair.
// Calculable en flux partout (WebCrypto n'a pas de SHA-256 incrémental).
type ContentHasher struct {
	outer  []byte
	chunk  []byte
	filled int
}

func NewContentHasher() *ContentHasher { return &ContentHasher{chunk: make([]byte, ChunkSize)} }

func (h *ContentHasher) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		c := copy(h.chunk[h.filled:], p)
		h.filled += c
		p = p[c:]
		if h.filled == ChunkSize {
			h.flush()
		}
	}
	return n, nil
}

func (h *ContentHasher) flush() {
	s := sha256.Sum256(h.chunk[:h.filled])
	h.outer = append(h.outer, s[:]...)
	h.filled = 0
}

func (h *ContentHasher) Sum() []byte {
	if h.filled > 0 || len(h.outer) == 0 {
		h.flush()
	}
	s := sha256.Sum256(h.outer)
	return s[:]
}

func dedupMAC(masterKey []byte, msg []byte) []byte {
	m := hmac.New(sha256.New, hkdfKey(masterKey, LabelDedup))
	m.Write(msg)
	return m.Sum(nil)
}

// DedupTag identifie un contenu pour un compte, sans le révéler au serveur.
func DedupTag(masterKey, contentHash []byte) []byte { return dedupMAC(masterKey, contentHash) }

// --- flux chiffré par blocs (originaux : lecture partielle possible, donc streaming vidéo) ---

func streamHeader(prefix []byte) []byte {
	h := make([]byte, StreamHdrSize)
	copy(h, streamMagic)
	binary.BigEndian.PutUint32(h[4:8], ChunkSize)
	copy(h[8:], prefix)
	return h
}

func chunkNonce(prefix []byte, i uint32) []byte {
	n := make([]byte, NonceSize)
	copy(n, prefix)
	binary.BigEndian.PutUint32(n[8:], i)
	return n
}

func chunkAAD(hdr []byte, last bool) []byte {
	aad := append(append([]byte{}, hdr...), 0)
	if last {
		aad[len(aad)-1] = 1
	}
	return aad
}

// EncryptStream chiffre src vers dst. Le drapeau « dernier bloc » dans l'AAD empêche de tronquer le fichier.
func EncryptStream(key []byte, src io.Reader, dst io.Writer) error {
	return encryptStreamPrefix(key, randomBytes(8), src, dst)
}

func encryptStreamPrefix(key, prefix []byte, src io.Reader, dst io.Writer) error {
	g := gcm(key)
	hdr := streamHeader(prefix)
	if _, err := dst.Write(hdr); err != nil {
		return err
	}
	cur := make([]byte, ChunkSize)
	next := make([]byte, ChunkSize)
	n, err := io.ReadFull(src, cur)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return err
	}
	out := make([]byte, 0, ChunkSize+TagSize)
	for i := uint32(0); ; i++ {
		m, err := io.ReadFull(src, next)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		last := m == 0
		out = g.Seal(out[:0], chunkNonce(prefix, i), cur[:n], chunkAAD(hdr, last))
		if _, err := dst.Write(out); err != nil {
			return err
		}
		if last {
			return nil
		}
		cur, next, n = next, cur, m
	}
}

// DecryptStream déchiffre un flux complet (utilisé par les tests et l'outil d'export).
func DecryptStream(key []byte, src io.Reader, dst io.Writer) error {
	g := gcm(key)
	hdr := make([]byte, StreamHdrSize)
	if _, err := io.ReadFull(src, hdr); err != nil || string(hdr[:4]) != string(streamMagic) {
		return errDecrypt
	}
	cs := int(binary.BigEndian.Uint32(hdr[4:8]))
	if cs <= 0 || cs > 64<<20 {
		return errDecrypt
	}
	prefix := hdr[8:]
	cur := make([]byte, cs+TagSize)
	next := make([]byte, cs+TagSize)
	n, err := io.ReadFull(src, cur)
	if err != nil && err != io.ErrUnexpectedEOF {
		return errDecrypt
	}
	for i := uint32(0); ; i++ {
		m, err := io.ReadFull(src, next)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		last := m == 0
		plain, err := g.Open(nil, chunkNonce(prefix, i), cur[:n], chunkAAD(hdr, last))
		if err != nil {
			return errDecrypt
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
		if last {
			return nil
		}
		cur, next, n = next, cur, m
	}
}

// StreamSize : taille chiffrée d'un clair de n octets.
func StreamSize(n int64) int64 {
	chunks := n / ChunkSize
	if n%ChunkSize != 0 || n == 0 {
		chunks++
	}
	return StreamHdrSize + n + chunks*TagSize
}

// --- enveloppe pour une clé publique (import : le serveur chiffre pour un compte sans pouvoir relire) ---

// SealToPublic : clé publique éphémère (65) || Seal(HKDF(ECDH, salt = éph || destinataire), clair).
func SealToPublic(recipientPub []byte, plain []byte) ([]byte, error) {
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return sealToPublicWith(eph, randomBytes(NonceSize), recipientPub, plain)
}

func sealToPublicWith(eph *ecdh.PrivateKey, nonce, recipientPub, plain []byte) ([]byte, error) {
	pub, err := ecdh.P256().NewPublicKey(recipientPub)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return nil, err
	}
	ephPub := eph.PublicKey().Bytes()
	key, err := hkdf.Key(sha256.New, shared, append(append([]byte{}, ephPub...), recipientPub...), LabelSealECDH, KeySize)
	if err != nil {
		return nil, err
	}
	return append(ephPub, sealWithNonce(key, nonce, plain, AADSealedFK)...), nil
}

// OpenFromPublic : réservé aux tests côté serveur (en vrai, seul le client a la clé privée).
func OpenFromPublic(priv *ecdh.PrivateKey, box []byte) ([]byte, error) {
	if len(box) < ECPubSize+NonceSize+TagSize {
		return nil, errDecrypt
	}
	ephPub, err := ecdh.P256().NewPublicKey(box[:ECPubSize])
	if err != nil {
		return nil, errDecrypt
	}
	shared, err := priv.ECDH(ephPub)
	if err != nil {
		return nil, errDecrypt
	}
	salt := append(append([]byte{}, box[:ECPubSize]...), priv.PublicKey().Bytes()...)
	key, err := hkdf.Key(sha256.New, shared, salt, LabelSealECDH, KeySize)
	if err != nil {
		return nil, err
	}
	return Open(key, box[ECPubSize:], AADSealedFK)
}

// DeviceTag identifie un fichier du téléphone (nom + taille) avant envoi, sans révéler son nom.
func DeviceTag(masterKey []byte, name string, size int64) []byte {
	return dedupMAC(masterKey, []byte(AADDeviceTagNS+strings.ToLower(name)+"|"+strconv.FormatInt(size, 10)))
}

// FormatRecoveryKey : 32 octets → base32 sans remplissage, par groupes de 4 (« ABCD-EFGH-… »).
func FormatRecoveryKey(k []byte) string {
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(k)
	var b strings.Builder
	for i := 0; i < len(s); i += 4 {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(s[i:min(i+4, len(s))])
	}
	return b.String()
}

// ParseRecoveryKey accepte la clé avec ou sans tirets/espaces, en majuscules ou minuscules.
func ParseRecoveryKey(s string) ([]byte, error) {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
	k, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil || len(k) != KeySize {
		return nil, errors.New("clé de secours invalide")
	}
	return k, nil
}

// FileKeys dérive la clé d'un fichier et le préfixe de nonce de son flux à partir de son contenu
// (chiffrement convergent, limité au compte). Le même fichier donne toujours le même chiffré : un envoi
// interrompu reprend où il en était, depuis n'importe quel appareil, sans rien demander au serveur.
// Le serveur n'apprend rien de plus que ce que DedupTag lui dit déjà (deux envois identiques).
func FileKeys(masterKey, contentHash []byte) (fk, prefix []byte) {
	h := hex.EncodeToString(contentHash)
	return hkdfKey(masterKey, LabelFileKey+h), hkdfKey(masterKey, LabelFilePrefix+h)[:8]
}
