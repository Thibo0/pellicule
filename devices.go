package main

// Appairage des téléphones. Le téléphone génère sa paire de clés dans son Keystore matériel (la clé
// privée n'en sort jamais) et se présente ensuite par certificat client TLS. L'appairage se fait avec
// un code à usage unique (5 minutes) affiché dans un navigateur déjà connecté avec le mot de passe ;
// le QR code contient aussi l'empreinte du certificat du serveur, que l'app épingle.

import (
	"net/http"
	"strings"
	"time"
)

const pairCodeTTL = 5 * time.Minute

// codes lisibles : sans 0/O ni 1/I/L
const pairAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newPairCode() string {
	b := randomBytes(10)
	var s strings.Builder
	for i, x := range b {
		if i == 5 {
			s.WriteByte('-')
		}
		s.WriteByte(pairAlphabet[int(x)%len(pairAlphabet)])
	}
	return s.String() // 10 caractères ≈ 49 bits, valable 5 minutes, usage unique
}

func normPairCode(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(c))
}

func (a *App) handlePairCode(w http.ResponseWriter, r *http.Request, u *User) {
	code := newPairCode()
	exp := time.Now().Add(pairCodeTTL)
	a.db.Exec(`DELETE FROM pair_codes WHERE user_id=? OR expires_at<?`, u.ID, time.Now().UnixMilli())
	if _, err := a.db.Exec(`INSERT INTO pair_codes(code, user_id, expires_at) VALUES (?,?,?)`,
		tokenHash(normPairCode(code)), u.ID, exp.UnixMilli()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"code": code, "expiresAt": exp.UnixMilli(), "fingerprint": a.certFP, "username": u.Username})
}

// handlePair : appelé par le téléphone, qui présente son certificat client dans la poignée de main TLS.
func (a *App) handlePair(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		httpError(w, http.StatusBadRequest, "certificat de l'appareil absent")
		return
	}
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if !readPublicJSON(w, r, &body) {
		return
	}
	ip := clientIP(r)
	if !a.limiter.reserve("appairage:" + ip) {
		httpError(w, http.StatusTooManyRequests, "trop d'essais, réessaie dans 15 minutes")
		return
	}
	h := tokenHash(normPairCode(body.Code))
	var userID int64
	// consommé en une seule instruction : deux appareils ne peuvent pas utiliser le même code
	err := a.db.QueryRow(`DELETE FROM pair_codes WHERE code=? AND expires_at>? RETURNING user_id`, h, time.Now().UnixMilli()).Scan(&userID)
	if err != nil {
		httpError(w, http.StatusUnauthorized, "code d'appairage invalide ou expiré")
		return
	}

	if err := a.registerDevice(a.db, r, userID, body.Name); err != nil {
		serverError(w, err)
		return
	}
	var username string
	a.db.QueryRow(`SELECT username FROM users WHERE id=?`, userID).Scan(&username)
	writeJSON(w, map[string]any{"ok": true, "username": username})
}

// registerDevice enregistre le certificat client présenté dans cette connexion TLS comme appareil du compte.
func (a *App) registerDevice(db execer, r *http.Request, userID int64, name string) error {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		name = "Téléphone"
	}
	fp := certFingerprint(r.TLS.PeerCertificates[0].Raw)
	now := time.Now().UnixMilli()
	// un même téléphone réappairé remplace son ancienne entrée
	_, err := db.Exec(`INSERT INTO devices(user_id, name, cert_fp, created_at, last_seen) VALUES (?,?,?,?,?)
		ON CONFLICT(cert_fp) DO UPDATE SET user_id=excluded.user_id, name=excluded.name, created_at=excluded.created_at`,
		userID, name, fp, now, now)
	return err
}

// handleDeviceLogin : l'app s'appaire directement avec le nom et le mot de passe du compte (sans navigateur).
// Même vérification et même limitation des essais qu'une connexion ; le téléphone présente son certificat
// dans la poignée de main TLS et repart avec l'enveloppe de clés (il ouvre la clé maître lui-même).
func (a *App) handleDeviceLogin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		httpError(w, http.StatusBadRequest, "certificat de l'appareil absent")
		return
	}
	var body struct {
		Username string `json:"username"`
		AuthKey  []byte `json:"authKey"`
		Name     string `json:"name"`
	}
	if !readPublicJSON(w, r, &body) {
		return
	}
	id, ok := a.checkLogin(w, r, body.Username, body.AuthKey)
	if !ok {
		return
	}
	keys, err := a.keyBundle(id)
	if err == nil {
		err = a.registerDevice(a.db, r, id, body.Name)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"keys": keys})
}

func (a *App) handleDevices(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(`SELECT id, name, created_at, last_seen FROM devices WHERE user_id=? ORDER BY created_at`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	type dev struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Created  int64  `json:"createdAt"`
		LastSeen int64  `json:"lastSeen"`
		Current  bool   `json:"current"`
	}
	out := []dev{}
	for rows.Next() {
		var d dev
		rows.Scan(&d.ID, &d.Name, &d.Created, &d.LastSeen)
		d.Current = d.ID == u.DeviceID
		out = append(out, d)
	}
	writeJSON(w, out)
}

// handleRevokeDevice : un appareil révoqué n'est plus reconnu au prochain échange TLS.
func (a *App) handleRevokeDevice(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := mustExec(a.db, `DELETE FROM devices WHERE id=? AND user_id=?`, id, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleRevokeSelf : un téléphone qui se déconnecte se retire lui-même (il ne peut révoquer que lui).
func (a *App) handleRevokeSelf(w http.ResponseWriter, r *http.Request, u *User) {
	if u.DeviceID == 0 {
		httpError(w, http.StatusBadRequest, "réservé aux téléphones appairés")
		return
	}
	if err := mustExec(a.db, `DELETE FROM devices WHERE id=? AND user_id=?`, u.DeviceID, u.ID); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}
