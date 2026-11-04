package main

// Comptes, sessions et appareils.
//
// Le mot de passe ne quitte jamais l'appareil : le client en dérive (PBKDF2 + HKDF) une clé
// d'authentification, seule envoyée ici et stockée en bcrypt, et une clé qui chiffre sa clé maître.
// Le serveur conserve la clé maître chiffrée (pour qu'un nouvel appareil la retrouve) mais ne peut pas
// l'ouvrir. Conséquence assumée : un mot de passe oublié se récupère uniquement avec la clé de secours.

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionTTL = 30 * 24 * time.Hour
	inviteTTL  = 7 * 24 * time.Hour
)

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte(strings.Repeat("0", 64)), bcrypt.DefaultCost)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
	DeviceID int64  `json:"deviceId,omitempty"` // requête authentifiée par le certificat d'un téléphone appairé
}

func randomToken(n int) string { return hex.EncodeToString(randomBytes(n)) }

// tokenHash : jetons (sessions, invitations, codes d'appairage, liens de partage) jamais stockés en clair.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *App) cookieName() string {
	if a.secure {
		return "__Host-pellicule_session" // impose Secure, Path=/ et aucun domaine
	}
	return "pellicule_session"
}

func (a *App) sessionToken(r *http.Request) string {
	if c, err := r.Cookie(a.cookieName()); err == nil {
		return c.Value
	}
	return ""
}

// validUsername : lettres latines (accents compris), chiffres, « . », « _ », « - ». Pas de caractère de contrôle
// ou invisible, pas de lettre décomposée (e + accent combinant, qui s'afficherait comme « é » sans être égale),
// pas d'homoglyphe d'un autre alphabet (« а » cyrillique) : deux noms qui se ressemblent à l'écran sont
// soit égaux, soit visiblement différents (liste de rebuild, dossier Import/ sur un partage insensible à la casse).
func validUsername(username string) error {
	u := strings.TrimSpace(username)
	n := 0
	for _, r := range u {
		n++
		ok := r == '.' || r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			// lettres accentuées d'Europe (Latin-1 et Latin étendu A) ; pas les homoglyphes de i, l, a, s
			// du reste de l'alphabet latin Unicode (ı ſ ǀ ɑ, pleine chasse, petites capitales…)
			(r >= 0xC0 && r <= 0x17F && r != 0xD7 && r != 0xF7 && r != 0x131 && r != 0x17F && unicode.IsLetter(r))
		if !ok {
			return errors.New("nom d'utilisateur invalide : lettres (accents permis), chiffres, « . », « _ » ou « - », sans espace")
		}
	}
	if n < 2 || len(u) > 64 {
		return errors.New("nom d'utilisateur invalide (2 à 64 caractères)")
	}
	if strings.HasPrefix(u, ".") || strings.EqualFold(u, importDir) {
		return errReserved
	}
	return nil
}

// legacyUsernameOK : règle des noms avant la 0.5 (fichiers autonomes) : un compte créé alors doit rester
// reconstructible. Seules les contraintes de chemin comptent ici (le nom sert de dossier Import/<nom>).
func legacyUsernameOK(username string) bool {
	u := strings.TrimSpace(username)
	return len(u) >= 2 && len(u) <= 64 && !strings.ContainsAny(u, "/\\:*?\"<>| ") && !strings.HasPrefix(u, ".") &&
		!strings.EqualFold(u, importDir) && !strings.ContainsFunc(u, unicode.IsControl)
}

// foldUsername : clé d'unicité (« Élodie » et « élodie » sont le même compte, et le même dossier Import/).
func foldUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// usernameTaken : la contrainte UNIQUE de SQLite (COLLATE NOCASE) ne replie que l'ASCII.
func usernameTaken(db execer, username string) (bool, error) {
	rows, err := db.Query(`SELECT username FROM users`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	key := foldUsername(username)
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return false, err
		}
		if foldUsername(u) == key {
			return true, nil
		}
	}
	return false, rows.Err()
}

// canonicalUsername : le nom tel qu'il est enregistré, quelle que soit la casse saisie (« élodie » → « Élodie »).
// Les recherches `username=?` passent par COLLATE NOCASE, qui ne replie que l'ASCII ; sans ceci, un nom
// accentué ne se connectait qu'avec sa casse exacte, alors que la règle d'unicité (usernameTaken) les confond.
// Nom inconnu : renvoyé tel quel (la recherche échouera comme avant).
func (a *App) canonicalUsername(name string) string {
	name = strings.TrimSpace(name)
	var u string
	if a.db.QueryRow(`SELECT username FROM users WHERE username=?`, name).Scan(&u) == nil {
		return u
	}
	rows, err := a.db.Query(`SELECT username FROM users`)
	if err != nil {
		return name
	}
	defer rows.Close()
	key := foldUsername(name)
	for rows.Next() {
		if rows.Scan(&u) == nil && foldUsername(u) == key {
			return u
		}
	}
	return name
}

// --- enveloppe de clés d'un compte ---

// KeyBundle : tout ce qu'un appareil doit récupérer pour retrouver la clé maître avec le mot de passe.
type KeyBundle struct {
	Salt        []byte `json:"salt"`
	WrappedMK   []byte `json:"wrappedMK"`
	Pub         []byte `json:"pub"`
	WrappedPriv []byte `json:"wrappedPriv"`
}

// accountKeys : envoyé par le client à la création d'un compte (toutes les clés sont générées chez lui).
type accountKeys struct {
	Username     string `json:"username"`
	AuthKey      []byte `json:"authKey"`
	Salt         []byte `json:"salt"`
	WrappedMK    []byte `json:"wrappedMK"`
	RecoveryMK   []byte `json:"recoveryMK"`
	RecoveryAuth []byte `json:"recoveryAuth"`
	Pub          []byte `json:"pub"`
	WrappedPriv  []byte `json:"wrappedPriv"`
}

const boxSize = NonceSize + KeySize + TagSize // une clé de 32 octets chiffrée

func (k *accountKeys) validate() error {
	if err := validUsername(k.Username); err != nil {
		return err
	}
	if len(k.AuthKey) != KeySize || len(k.Salt) != SaltSize || len(k.WrappedMK) != boxSize ||
		len(k.RecoveryMK) != boxSize || len(k.RecoveryAuth) != KeySize || len(k.WrappedPriv) < NonceSize+TagSize+32 ||
		len(k.WrappedPriv) > 1024 {
		return errors.New("clés invalides")
	}
	if _, err := ecdh.P256().NewPublicKey(k.Pub); err != nil {
		return errors.New("clé publique invalide")
	}
	return nil
}

func hashAuthKey(k []byte) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(k)), bcrypt.DefaultCost)
	return string(h), err
}

func recoveryAuthHash(k []byte) string {
	sum := sha256.Sum256(k)
	return hex.EncodeToString(sum[:])
}

// createAccount : l'appelant écrit ensuite compte.json (writeAccountFile), une fois la transaction validée.
func (a *App) createAccount(db execer, k accountKeys, admin bool) (int64, error) {
	if err := k.validate(); err != nil {
		return 0, err
	}
	if taken, err := usernameTaken(db, k.Username); err != nil {
		return 0, err
	} else if taken {
		return 0, errors.New("ce nom d'utilisateur existe déjà")
	}
	h, err := hashAuthKey(k.AuthKey)
	if err != nil {
		return 0, err
	}
	res, err := db.Exec(`INSERT INTO users(username, auth_hash, salt, wrapped_mk, recovery_mk, recovery_auth, pub_key, wrapped_priv, is_admin, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`, strings.TrimSpace(k.Username), h, k.Salt, k.WrappedMK, k.RecoveryMK,
		recoveryAuthHash(k.RecoveryAuth), k.Pub, k.WrappedPriv, admin, time.Now().UnixMilli())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errors.New("ce nom d'utilisateur existe déjà")
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (a *App) keyBundle(userID int64) (KeyBundle, error) {
	var b KeyBundle
	err := a.db.QueryRow(`SELECT salt, wrapped_mk, pub_key, wrapped_priv FROM users WHERE id=?`, userID).
		Scan(&b.Salt, &b.WrappedMK, &b.Pub, &b.WrappedPriv)
	return b, err
}

func (a *App) userCount() int {
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

// --- sessions ---

func (a *App) startSession(w http.ResponseWriter, userID int64) error {
	token := randomToken(32)
	exp := time.Now().Add(sessionTTL)
	if _, err := a.db.Exec(`INSERT INTO sessions(token, user_id, expires_at) VALUES (?,?,?)`, tokenHash(token), userID, exp.UnixMilli()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: a.cookieName(), Value: token, Path: "/", Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: a.secure,
	})
	return nil
}

func (a *App) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secure})
}

// currentUser : téléphone appairé (certificat client TLS) ou session de navigateur (cookie).
func (a *App) currentUser(r *http.Request) *User {
	if u := a.deviceUser(r); u != nil {
		return u
	}
	token := a.sessionToken(r)
	if token == "" {
		return nil
	}
	h := tokenHash(token)
	var u User
	var exp int64
	now := time.Now()
	err := a.db.QueryRow(`SELECT u.id, u.username, u.is_admin, s.expires_at FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, h, now.UnixMilli()).Scan(&u.ID, &u.Username, &u.IsAdmin, &exp)
	if err != nil {
		return nil
	}
	if time.UnixMilli(exp).Sub(now) < sessionTTL/2 {
		a.db.Exec(`UPDATE sessions SET expires_at=? WHERE token=?`, now.Add(sessionTTL).UnixMilli(), h)
	}
	return &u
}

func (a *App) deviceUser(r *http.Request) *User {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return nil
	}
	fp := certFingerprint(r.TLS.PeerCertificates[0].Raw)
	var u User
	var lastSeen int64
	err := a.db.QueryRow(`SELECT u.id, u.username, u.is_admin, d.id, d.last_seen FROM devices d JOIN users u ON u.id=d.user_id
		WHERE d.cert_fp=?`, fp).Scan(&u.ID, &u.Username, &u.IsAdmin, &u.DeviceID, &lastSeen)
	if err != nil {
		return nil
	}
	if now := time.Now(); now.Sub(time.UnixMilli(lastSeen)) > time.Hour {
		a.db.Exec(`UPDATE devices SET last_seen=? WHERE id=?`, now.UnixMilli(), u.DeviceID)
	}
	return &u
}

type userHandler func(w http.ResponseWriter, r *http.Request, u *User)

// csrfOK : les écritures exigent un en-tête qu'un formulaire ou un <img> d'un autre site ne peut pas envoyer.
func csrfOK(r *http.Request) bool {
	return r.Method == http.MethodGet || r.Method == http.MethodHead || r.Header.Get("X-Pellicule") == "1"
}

func (a *App) auth(h userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !csrfOK(r) {
			httpError(w, http.StatusForbidden, "requête refusée")
			return
		}
		u := a.currentUser(r)
		if u == nil {
			httpError(w, http.StatusUnauthorized, "non connecté")
			return
		}
		h(w, r, u)
	}
}

func (a *App) admin(h userHandler) http.HandlerFunc {
	return a.auth(func(w http.ResponseWriter, r *http.Request, u *User) {
		if !u.IsAdmin {
			httpError(w, http.StatusForbidden, "réservé à l'administrateur")
			return
		}
		h(w, r, u)
	})
}

// browserOnly : actions sensibles (appairer un téléphone, gérer les comptes) refusées à un téléphone volé.
func (a *App) browserOnly(h userHandler) userHandler {
	return func(w http.ResponseWriter, r *http.Request, u *User) {
		if u.DeviceID != 0 {
			httpError(w, http.StatusForbidden, "action possible seulement depuis un navigateur connecté avec le mot de passe")
			return
		}
		h(w, r, u)
	}
}

// --- points d'entrée publics ---

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	writeJSON(w, map[string]any{"user": u, "needsSetup": a.userCount() == 0, "version": version,
		"certFingerprint": fingerprintGroups(a.certFP)})
}

// handlePrelogin renvoie le sel PBKDF2 du compte. Pour un compte inconnu, un faux sel stable
// (HMAC du nom par un secret du serveur) : la réponse ne révèle pas si le compte existe.
func (a *App) handlePrelogin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if !readPublicJSON(w, r, &body) || !plausibleUsername(w, body.Username) {
		return
	}
	var salt []byte
	if a.db.QueryRow(`SELECT salt FROM users WHERE username=?`, a.canonicalUsername(body.Username)).Scan(&salt) != nil {
		salt = fakeBytes(a.secret, "salt", body.Username, SaltSize)
	}
	writeJSON(w, map[string]any{"salt": salt})
}

// plausibleUsername : refuse d'emblée un nom absurde (il deviendrait une clé du limiteur, gardée 15 min).
func plausibleUsername(w http.ResponseWriter, username string) bool {
	if len(username) > 64 {
		httpError(w, http.StatusBadRequest, "nom d'utilisateur invalide")
		return false
	}
	return true
}

func fakeBytes(secret []byte, purpose, username string, n int) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(purpose + "|" + strings.ToLower(strings.TrimSpace(username))))
	out := m.Sum(nil)
	for len(out) < n {
		out = append(out, m.Sum(out)...)
	}
	return out[:n]
}

// setupCode : tant qu'aucun compte n'existe, la création du compte administrateur exige ce code,
// affiché dans les journaux et par install.sh. Sans lui, n'importe quel appareil du réseau arrivé
// le premier sur la page pourrait s'approprier le serveur.
func (a *App) setupCode() string {
	var v []byte
	if a.db.QueryRow(`SELECT value FROM settings WHERE key='setup'`).Scan(&v) == nil {
		return string(v)
	}
	a.db.Exec(`INSERT OR IGNORE INTO settings(key, value) VALUES ('setup', ?)`, []byte(newPairCode()))
	a.db.QueryRow(`SELECT value FROM settings WHERE key='setup'`).Scan(&v)
	return string(v)
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	var body struct {
		SetupCode  string `json:"setupCode"`
		DeviceName string `json:"deviceName"`
		accountKeys
	}
	if !readPublicJSON(w, r, &body) {
		return
	}
	if a.userCount() > 0 {
		httpError(w, http.StatusConflict, "déjà configuré")
		return
	}
	ip := clientIP(r)
	if !a.limiter.reserve("setup:" + ip) {
		httpError(w, http.StatusTooManyRequests, "trop d'essais, réessaie dans 15 minutes")
		return
	}
	if subtle.ConstantTimeCompare([]byte(normPairCode(body.SetupCode)), []byte(normPairCode(a.setupCode()))) != 1 {
		httpError(w, http.StatusForbidden, "code d'installation incorrect (affiché à la fin de l'installation et dans les journaux)")
		return
	}
	if err := body.accountKeys.validate(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	// code consommé, compte et téléphone : tout ou rien (un échec laisse le code utilisable)
	var id int64
	errConflict := errors.New("déjà configuré")
	err := a.inTx(func(tx *sql.Tx) error {
		// code consommé dans la transaction : deux créations simultanées ne donnent pas deux administrateurs
		res, err := tx.Exec(`DELETE FROM settings WHERE key='setup'`)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return errConflict
		}
		if id, err = a.createAccount(tx, body.accountKeys, true); err != nil {
			return userError{err}
		}
		return a.registerDevice(tx, r, id, body.DeviceName) // compte créé depuis l'app
	})
	if !txOK(w, err, errConflict) {
		return
	}
	a.writeAccountFile(id) // sans lui, impossible de reprendre ce compte si la VM est perdue
	if err := a.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// checkLogin vérifie la clé d'authentification, avec limitation par IP et par compte.
func (a *App) checkLogin(w http.ResponseWriter, r *http.Request, username string, authKey []byte) (int64, bool) {
	if !plausibleUsername(w, username) {
		return 0, false
	}
	ip, acct := clientIP(r), "compte:"+strings.ToLower(strings.TrimSpace(username))
	if !a.limiter.reserve(ip, acct) {
		httpError(w, http.StatusTooManyRequests, "trop d'essais, réessaie dans 15 minutes")
		return 0, false
	}
	var id int64
	var hash string
	err := a.db.QueryRow(`SELECT id, auth_hash FROM users WHERE username = ?`, a.canonicalUsername(username)).Scan(&id, &hash)
	if err == nil && needsRecovery(hash) {
		// base reconstruite (rebuild) : il n'y a plus de mot de passe, seulement la clé de secours.
		// (Ceci révèle que le compte existe, seulement dans cet état transitoire.)
		httpError(w, http.StatusConflict, "serveur reconstruit : reprends ton compte avec ta clé de secours")
		return 0, false
	}
	if err == nil {
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(hex.EncodeToString(authKey)))
	} else if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(hex.EncodeToString(authKey))) // même durée : ne révèle pas les comptes
	}
	if err != nil {
		httpError(w, http.StatusUnauthorized, "identifiants incorrects")
		return 0, false
	}
	a.limiter.reset(ip)
	a.limiter.reset(acct)
	return id, true
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	var body struct {
		Username string `json:"username"`
		AuthKey  []byte `json:"authKey"`
	}
	if !readPublicJSON(w, r, &body) {
		return
	}
	id, ok := a.checkLogin(w, r, body.Username, body.AuthKey)
	if !ok {
		return
	}
	keys, err := a.keyBundle(id)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := a.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"keys": keys})
}

func (a *App) handleKeys(w http.ResponseWriter, r *http.Request, u *User) {
	keys, err := a.keyBundle(u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, keys)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request, u *User) {
	if t := a.sessionToken(r); t != "" {
		if err := mustExec(a.db, `DELETE FROM sessions WHERE token = ?`, tokenHash(t)); err != nil {
			serverError(w, err)
			return
		}
	}
	a.clearCookie(w)
	writeJSON(w, map[string]any{"ok": true})
}

// rewrap : nouveau mot de passe = nouvelle clé d'authentification + clé maître rechiffrée (calculées par le client).
type rewrap struct {
	AuthKey   []byte `json:"authKey"`
	Salt      []byte `json:"salt"`
	WrappedMK []byte `json:"wrappedMK"`
}

func (k rewrap) valid() bool {
	return len(k.AuthKey) == KeySize && len(k.Salt) == SaltSize && len(k.WrappedMK) == boxSize
}

func (a *App) applyRewrap(db execer, userID int64, k rewrap) error {
	h, err := hashAuthKey(k.AuthKey)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE users SET auth_hash=?, salt=?, wrapped_mk=? WHERE id=?`, h, k.Salt, k.WrappedMK, userID)
	return err
}

func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		Current       []byte `json:"currentAuthKey"`
		LogoutDevices bool   `json:"logoutDevices"` // mot de passe compromis : les téléphones aussi
		rewrap
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !body.rewrap.valid() {
		httpError(w, http.StatusBadRequest, "requête invalide")
		return
	}
	if _, ok := a.checkLogin(w, r, u.Username, body.Current); !ok {
		return
	}
	err := a.inTx(func(tx *sql.Tx) error {
		if err := a.applyRewrap(tx, u.ID, body.rewrap); err != nil {
			return err
		}
		// les autres navigateurs connectés avec l'ancien mot de passe sont déconnectés
		if err := mustExec(tx, `DELETE FROM sessions WHERE user_id=? AND token<>?`, u.ID, tokenHash(a.sessionToken(r))); err != nil {
			return err
		}
		if body.LogoutDevices {
			return mustExec(tx, `DELETE FROM devices WHERE user_id=? AND id<>?`, u.ID, u.DeviceID)
		}
		return nil
	})
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// --- clé de secours ---

// handleRecoverStart renvoie la clé maître chiffrée par la clé de secours (inexploitable sans elle :
// 256 bits aléatoires). Compte inconnu : fausse boîte stable, pour ne rien révéler.
func (a *App) handleRecoverStart(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if !readPublicJSON(w, r, &body) || !plausibleUsername(w, body.Username) {
		return
	}
	var box []byte
	if a.db.QueryRow(`SELECT recovery_mk FROM users WHERE username=?`, a.canonicalUsername(body.Username)).Scan(&box) != nil {
		box = fakeBytes(a.secret, "recovery", body.Username, boxSize)
	}
	writeJSON(w, map[string]any{"recoveryMK": box})
}

func (a *App) handleRecover(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	var body struct {
		Username     string `json:"username"`
		RecoveryAuth []byte `json:"recoveryAuth"`
		DeviceName   string `json:"deviceName"` // l'app : le téléphone (certificat client) est appairé du même coup
		rewrap
	}
	if !readPublicJSON(w, r, &body) {
		return
	}
	if !plausibleUsername(w, body.Username) {
		return
	}
	ip, acct := clientIP(r), "secours:"+strings.ToLower(strings.TrimSpace(body.Username))
	if !a.limiter.reserve(ip, acct) {
		httpError(w, http.StatusTooManyRequests, "trop d'essais, réessaie dans 15 minutes")
		return
	}
	var id int64
	var stored string
	err := a.db.QueryRow(`SELECT id, recovery_auth FROM users WHERE username=?`, a.canonicalUsername(body.Username)).Scan(&id, &stored)
	if err != nil || !body.rewrap.valid() ||
		subtle.ConstantTimeCompare([]byte(stored), []byte(recoveryAuthHash(body.RecoveryAuth))) != 1 {
		httpError(w, http.StatusUnauthorized, "clé de secours incorrecte")
		return
	}
	asDevice := body.DeviceName != "" && r.TLS != nil && len(r.TLS.PeerCertificates) > 0
	// secours = mot de passe oublié ou compromis : toutes les sessions et tous les téléphones sont déconnectés,
	// dans la même transaction que le nouveau mot de passe (jamais l'un sans l'autre)
	err = a.inTx(func(tx *sql.Tx) error {
		if err := a.applyRewrap(tx, id, body.rewrap); err != nil {
			return err
		}
		if err := mustExec(tx, `DELETE FROM sessions WHERE user_id=?`, id); err != nil {
			return err
		}
		if err := mustExec(tx, `DELETE FROM devices WHERE user_id=?`, id); err != nil {
			return err
		}
		if asDevice {
			return a.registerDevice(tx, r, id, body.DeviceName)
		}
		return nil
	})
	if err != nil {
		serverError(w, err)
		return
	}
	a.limiter.reset(acct)
	if asDevice {
		keys, err := a.keyBundle(id)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "keys": keys})
		return
	}
	if err := a.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// --- invitations (l'admin ne choisit plus le mot de passe des autres : il ne pourrait pas leur laisser
// une clé maître que lui seul ne connaîtrait pas) ---

func (a *App) createInvite(byID *int64, admin bool) (string, error) {
	token := randomToken(24)
	_, err := a.db.Exec(`INSERT INTO invites(token, is_admin, created_by, expires_at) VALUES (?,?,?,?)`,
		tokenHash(token), admin, byID, time.Now().Add(inviteTTL).UnixMilli())
	return token, err
}

func (a *App) handleCreateInvite(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		IsAdmin bool `json:"isAdmin"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	// depuis un téléphone (peut-être volé et déverrouillé) : jamais d'invitation administrateur
	admin := body.IsAdmin && u.DeviceID == 0
	token, err := a.createInvite(&u.ID, admin)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"token": token, "isAdmin": admin, "expiresAt": time.Now().Add(inviteTTL).UnixMilli()})
}

func (a *App) validInvite(token string) (bool, bool) {
	var admin bool
	err := a.db.QueryRow(`SELECT is_admin FROM invites WHERE token=? AND expires_at>?`, tokenHash(token), time.Now().UnixMilli()).Scan(&admin)
	return err == nil, admin
}

func (a *App) handleCheckInvite(w http.ResponseWriter, r *http.Request) {
	ok, admin := a.validInvite(r.PathValue("token"))
	if !ok {
		httpError(w, http.StatusNotFound, "invitation invalide ou expirée")
		return
	}
	writeJSON(w, map[string]any{"ok": true, "isAdmin": admin})
}

func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	var body struct {
		Token      string `json:"token"`
		DeviceName string `json:"deviceName"`
		accountKeys
	}
	if !readPublicJSON(w, r, &body) {
		return
	}
	if err := body.accountKeys.validate(); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	// invitation consommée, compte et téléphone : tout ou rien. Nom déjà pris… : l'invitation reste utilisable.
	var id int64
	errNoInvite := errors.New("invitation invalide ou expirée")
	err := a.inTx(func(tx *sql.Tx) error {
		var admin bool
		// consommée en une instruction : deux inscriptions simultanées ne passent pas
		if tx.QueryRow(`DELETE FROM invites WHERE token=? AND expires_at>? RETURNING is_admin`, tokenHash(body.Token), time.Now().UnixMilli()).Scan(&admin) != nil {
			return errNoInvite
		}
		var err error
		if id, err = a.createAccount(tx, body.accountKeys, admin); err != nil {
			return userError{err}
		}
		return a.registerDevice(tx, r, id, body.DeviceName) // compte créé depuis l'app
	})
	if errors.Is(err, errNoInvite) {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	if !txOK(w, err, nil) {
		return
	}
	a.writeAccountFile(id) // sans lui, impossible de reprendre ce compte si la VM est perdue
	if err := a.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// --- gestion des comptes (admin) ---

func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(`SELECT u.id, u.username, u.is_admin,
		(SELECT COUNT(*) FROM assets WHERE owner_id=u.id AND complete=1),
		(SELECT COALESCE(SUM(orig_size),0) FROM assets WHERE owner_id=u.id)
		FROM users u ORDER BY u.id`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		User
		Count int64 `json:"count"`
		Bytes int64 `json:"bytes"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		rows.Scan(&x.ID, &x.Username, &x.IsAdmin, &x.Count, &x.Bytes)
		out = append(out, x)
	}
	writeJSON(w, out)
}

func (a *App) handleDeleteUser(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == u.ID {
		httpError(w, http.StatusBadRequest, "impossible de supprimer son propre compte")
		return
	}
	if err := a.deleteUser(id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// deleteUser supprime le compte et ses fichiers chiffrés (illisibles sans sa clé de toute façon).
func (a *App) deleteUser(id int64) error {
	var name string
	if err := a.db.QueryRow(`SELECT username FROM users WHERE id=?`, id).Scan(&name); err != nil {
		return err
	}
	if _, err := a.db.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		return err
	}
	// son dossier d'import aussi : un futur compte du même nom n'hérite pas de ce qui y traîne
	if legacyUsernameOK(name) {
		if err := os.RemoveAll(filepath.Join(a.libDir, importDir, name)); err != nil {
			return err
		}
	}
	return os.RemoveAll(filepath.Join(a.libDir, userDir(id)))
}

// --- limitation des tentatives ---

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{fails: map[string][]time.Time{}} }

func (l *loginLimiter) recent(key string) []time.Time {
	cut := time.Now().Add(-15 * time.Minute)
	keep := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = keep
	return keep
}

// reserve compte un essai pour chaque clé, d'un bloc, avant la vérification ; false si l'une est épuisée.
// Ainsi des requêtes lancées en parallèle (bcrypt prend ~100 ms) ne passent pas toutes avant d'être comptées.
// En cas de succès, l'appelant remet le compteur à zéro (reset).
func (l *loginLimiter) reserve(keys ...string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		if len(l.recent(k)) >= 10 {
			return false
		}
	}
	now := time.Now()
	for _, k := range keys {
		l.fails[k] = append(l.recent(k), now)
	}
	return true
}

// purge oublie les compteurs sans échec récent (appelée chaque minute).
func (l *loginLimiter) purge() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k := range l.fails {
		l.recent(k)
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// trustedProxy : adresse du reverse proxy (option -trusted-proxy). Seules ses requêtes peuvent indiquer
// l'IP du client par X-Forwarded-For ; sinon tout le monde partagerait la même limite d'essais.
var trustedProxy string

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if trustedProxy != "" && host == trustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1]) // ajouté par notre proxy : le seul fiable
		}
	}
	return host
}
