package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "pellicule_session"
	sessionTTL    = 90 * 24 * time.Hour
)

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("pellicule"), bcrypt.DefaultCost)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashPassword(p string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	return string(h), err
}

func validCredentials(username, password string) error {
	if len(strings.TrimSpace(username)) < 2 || strings.ContainsAny(username, "/\\:*?\"<>| ") {
		return errors.New("nom d'utilisateur invalide (2 caractères minimum, sans espace ni / \\ : * ? \" < > |)")
	}
	if strings.HasPrefix(username, ".") || strings.EqualFold(strings.TrimSpace(username), importDir) {
		return errReserved
	}
	if len(password) < 8 {
		return errors.New("le mot de passe doit faire au moins 8 caractères")
	}
	return nil
}

func (a *App) createUser(username, password string, admin bool) (int64, error) {
	if err := validCredentials(username, password); err != nil {
		return 0, err
	}
	h, err := hashPassword(password)
	if err != nil {
		return 0, err
	}
	res, err := a.db.Exec(`INSERT INTO users(username, password_hash, is_admin, created_at) VALUES (?,?,?,?)`,
		strings.TrimSpace(username), h, admin, time.Now().UnixMilli())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, errors.New("ce nom d'utilisateur existe déjà")
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (a *App) resetPassword(username, password string) error {
	if len(password) < 8 {
		return errors.New("le mot de passe doit faire au moins 8 caractères")
	}
	h, err := hashPassword(password)
	if err != nil {
		return err
	}
	res, err := a.db.Exec(`UPDATE users SET password_hash=? WHERE username=?`, h, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("utilisateur introuvable")
	}
	var id int64
	a.db.QueryRow(`SELECT id FROM users WHERE username=?`, username).Scan(&id)
	_, err = a.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	return err
}

func (a *App) userCount() int {
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

func (a *App) startSession(w http.ResponseWriter, userID int64) error {
	token := randomToken(32)
	exp := time.Now().Add(sessionTTL)
	if _, err := a.db.Exec(`INSERT INTO sessions(token, user_id, expires_at) VALUES (?,?,?)`, token, userID, exp.UnixMilli()); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.secure,
	})
	return nil
}

func (a *App) currentUser(r *http.Request) *User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	var u User
	var exp int64
	now := time.Now()
	err = a.db.QueryRow(`SELECT u.id, u.username, u.is_admin, s.expires_at FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > ?`, c.Value, now.UnixMilli()).Scan(&u.ID, &u.Username, &u.IsAdmin, &exp)
	if err != nil {
		return nil
	}
	// session glissante : tant qu'elle sert (l'app du téléphone sauvegarde régulièrement), elle ne périme pas
	if time.UnixMilli(exp).Sub(now) < sessionTTL/2 {
		a.db.Exec(`UPDATE sessions SET expires_at=? WHERE token=?`, now.Add(sessionTTL).UnixMilli(), c.Value)
	}
	return &u
}

type userHandler func(w http.ResponseWriter, r *http.Request, u *User)

// auth protège une route : session valide + en-tête anti-CSRF sur les écritures.
func (a *App) auth(h userHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Pellicule") != "1" {
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

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	u := a.currentUser(r)
	writeJSON(w, map[string]any{"user": u, "needsSetup": a.userCount() == 0, "version": version})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Pellicule") != "1" {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	if a.userCount() > 0 {
		httpError(w, http.StatusConflict, "déjà configuré")
		return
	}
	id, err := a.createUser(c.Username, c.Password, true)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Pellicule") != "1" {
		httpError(w, http.StatusForbidden, "requête refusée")
		return
	}
	ip := clientIP(r)
	if a.limiter.blocked(ip) {
		httpError(w, http.StatusTooManyRequests, "trop d'essais, réessaie dans 15 minutes")
		return
	}
	var c credentials
	if !readJSON(w, r, &c) {
		return
	}
	var id int64
	var hash string
	err := a.db.QueryRow(`SELECT id, password_hash FROM users WHERE username = ?`, strings.TrimSpace(c.Username)).Scan(&id, &hash)
	if err == nil {
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(c.Password))
	} else if errors.Is(err, sql.ErrNoRows) {
		// même coût qu'un vrai mot de passe, pour ne pas révéler les comptes existants
		bcrypt.CompareHashAndPassword(dummyHash, []byte(c.Password))
	}
	if err != nil {
		a.limiter.fail(ip)
		httpError(w, http.StatusUnauthorized, "identifiants incorrects")
		return
	}
	a.limiter.reset(ip)
	if err := a.startSession(w, id); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request, u *User) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token = ?`, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	var hash string
	a.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, u.ID).Scan(&hash)
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Current)) != nil {
		httpError(w, http.StatusBadRequest, "mot de passe actuel incorrect")
		return
	}
	if len(body.New) < 8 {
		httpError(w, http.StatusBadRequest, "le mot de passe doit faire au moins 8 caractères")
		return
	}
	h, err := hashPassword(body.New)
	if err != nil {
		serverError(w, err)
		return
	}
	a.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, h, u.ID)
	writeJSON(w, map[string]any{"ok": true})
}

// --- gestion des comptes (admin) ---

func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(`SELECT u.id, u.username, u.is_admin,
		(SELECT COUNT(*) FROM assets WHERE owner_id=u.id), (SELECT COALESCE(SUM(size),0) FROM assets WHERE owner_id=u.id)
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

func (a *App) handleCreateUser(w http.ResponseWriter, r *http.Request, u *User) {
	var body struct {
		credentials
		IsAdmin bool `json:"isAdmin"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	id, err := a.createUser(body.Username, body.Password, body.IsAdmin)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": id})
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
	// Les fichiers originaux restent sur le disque : on ne supprime que les entrées.
	a.db.Exec(`DELETE FROM users WHERE id=?`, id)
	writeJSON(w, map[string]any{"ok": true})
}

func (a *App) handleAdminSetPassword(w http.ResponseWriter, r *http.Request, u *User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.Password) < 8 {
		httpError(w, http.StatusBadRequest, "le mot de passe doit faire au moins 8 caractères")
		return
	}
	h, err := hashPassword(body.Password)
	if err != nil {
		serverError(w, err)
		return
	}
	a.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, h, id)
	a.db.Exec(`DELETE FROM sessions WHERE user_id=?`, id)
	writeJSON(w, map[string]any{"ok": true})
}

// --- limitation des tentatives de connexion ---

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{fails: map[string][]time.Time{}} }

func (l *loginLimiter) recent(ip string) []time.Time {
	cut := time.Now().Add(-15 * time.Minute)
	keep := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.fails[ip] = keep
	return keep
}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) >= 10
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recent(ip), time.Now())
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
