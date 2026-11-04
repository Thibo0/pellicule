package main

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func (a *App) routes() http.Handler {
	m := http.NewServeMux()

	// comptes et sessions (le mot de passe ne transite jamais : voir auth.go)
	m.HandleFunc("GET /api/me", a.handleMe)
	m.HandleFunc("GET /api/cert", a.handleCert)
	m.HandleFunc("POST /api/prelogin", a.handlePrelogin)
	m.HandleFunc("POST /api/setup", a.handleSetup)
	m.HandleFunc("POST /api/login", a.handleLogin)
	m.HandleFunc("POST /api/recover/start", a.handleRecoverStart)
	m.HandleFunc("POST /api/recover", a.handleRecover)
	m.HandleFunc("GET /api/invites/{token}", a.handleCheckInvite)
	m.HandleFunc("POST /api/register", a.handleRegister)
	m.HandleFunc("POST /api/logout", a.auth(a.handleLogout))
	m.HandleFunc("GET /api/keys", a.auth(a.handleKeys))
	m.HandleFunc("POST /api/me/password", a.auth(a.browserOnly(a.handleChangePassword)))
	m.HandleFunc("GET /api/stats", a.auth(a.handleStats))

	// administration
	m.HandleFunc("GET /api/users", a.admin(a.handleListUsers))
	m.HandleFunc("POST /api/invites", a.admin(a.handleCreateInvite)) // aussi depuis l'app : « Inviter quelqu'un »
	m.HandleFunc("DELETE /api/users/{id}", a.admin(a.browserOnly(a.handleDeleteUser)))

	// téléphones appairés (certificat client)
	m.HandleFunc("POST /api/devices/pair-code", a.auth(a.browserOnly(a.handlePairCode)))
	m.HandleFunc("POST /api/devices/pair", a.handlePair)
	m.HandleFunc("POST /api/devices/login", a.handleDeviceLogin)
	m.HandleFunc("GET /api/devices", a.auth(a.handleDevices))
	m.HandleFunc("DELETE /api/devices/{id}", a.auth(a.browserOnly(a.handleRevokeDevice)))
	m.HandleFunc("POST /api/devices/self/revoke", a.auth(a.handleRevokeSelf))

	// photos (blobs chiffrés)
	m.HandleFunc("POST /api/assets", a.auth(a.handleCreateAsset))
	m.HandleFunc("PUT /api/assets/{id}/original", a.auth(a.handlePutOriginal))
	m.HandleFunc("PUT /api/assets/{id}/thumb", a.auth(a.putSmallBlob("s", "has_thumb", maxThumb)))
	m.HandleFunc("PUT /api/assets/{id}/preview", a.auth(a.putSmallBlob("m", "has_preview", maxPreview)))
	m.HandleFunc("POST /api/assets/{id}/complete", a.auth(a.handleCompleteAsset))
	m.HandleFunc("POST /api/assets/check", a.auth(a.handleCheck))
	m.HandleFunc("GET /api/assets/resend", a.auth(a.handleResend))
	m.HandleFunc("POST /api/assets/keys", a.auth(a.handleRewrapKeys))
	m.HandleFunc("GET /api/timeline", a.auth(a.handleTimeline))
	m.HandleFunc("GET /api/assets/{id}", a.auth(a.handleAsset))
	m.HandleFunc("GET /api/assets/{id}/thumb", a.auth(a.serveOwned("s")))
	m.HandleFunc("GET /api/assets/{id}/preview", a.auth(a.serveOwned("m")))
	m.HandleFunc("GET /api/assets/{id}/original", a.auth(a.serveOwned("o")))
	m.HandleFunc("POST /api/assets/favorite", a.auth(a.handleFavorite))
	m.HandleFunc("POST /api/assets/trash", a.auth(a.handleTrash))
	m.HandleFunc("POST /api/assets/restore", a.auth(a.handleRestore))
	m.HandleFunc("POST /api/assets/delete", a.auth(a.handleDeleteForever))
	m.HandleFunc("POST /api/trash/empty", a.auth(a.handleEmptyTrash))
	m.HandleFunc("POST /api/import/scan", a.auth(a.handleImportScan))

	// albums
	m.HandleFunc("GET /api/albums", a.auth(a.handleAlbums))
	m.HandleFunc("POST /api/albums", a.auth(a.handleCreateAlbum))
	m.HandleFunc("GET /api/albums/{id}", a.auth(a.handleAlbum))
	m.HandleFunc("PATCH /api/albums/{id}", a.auth(a.handleUpdateAlbum))
	m.HandleFunc("DELETE /api/albums/{id}", a.auth(a.handleDeleteAlbum))
	m.HandleFunc("POST /api/albums/{id}/add", a.auth(a.handleAlbumAdd))
	m.HandleFunc("POST /api/albums/{id}/remove", a.auth(a.handleAlbumRemove))
	m.HandleFunc("GET /api/albums/{id}/shares", a.auth(a.handleAlbumShares))
	m.HandleFunc("POST /api/albums/{id}/shares", a.auth(a.browserOnly(a.handleCreateShare)))
	m.HandleFunc("POST /api/shares/{id}/keys", a.auth(a.handleShareKeys))
	m.HandleFunc("DELETE /api/shares/{id}", a.auth(a.handleDeleteShare))

	// partage public (la clé est dans le fragment de l'URL, jamais envoyé au serveur)
	m.HandleFunc("GET /api/s/{token}", a.handlePublicAlbum)
	m.HandleFunc("GET /api/s/{token}/assets/{id}/thumb", a.servePublic("s"))
	m.HandleFunc("GET /api/s/{token}/assets/{id}/preview", a.servePublic("m"))
	m.HandleFunc("GET /api/s/{token}/assets/{id}/original", a.servePublic("o"))

	// interface
	static, _ := fs.Sub(webFS, "web")
	files := http.FileServer(http.FS(static))
	index, _ := fs.ReadFile(static, "index.html")
	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(index)
	}
	m.HandleFunc("GET /s/{token}", serveIndex)
	m.HandleFunc("GET /{$}", serveIndex)
	m.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			httpError(w, http.StatusNotFound, "introuvable")
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})

	return a.securityHeaders(m)
}

// CSP : aucun script hors de /app.js (une injection HTML ne peut rien exécuter) ; les images et vidéos
// déchiffrées dans la page sont des URL blob:. Les attributs style="" restent permis (mise en page).
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self'; worker-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func (a *App) securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "no-referrer") // le jeton d'un lien de partage ne fuit jamais
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Content-Security-Policy", contentSecurityPolicy)
		hd.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		hd.Set("Cross-Origin-Opener-Policy", "same-origin")
		hd.Set("Cross-Origin-Resource-Policy", "same-origin")
		if a.secure {
			hd.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

// readJSON : requêtes d'un utilisateur connecté (16 Mio : dépôt des clés de partage d'un gros album…).
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return readJSONMax(w, r, v, 16<<20)
}

// readPublicJSON : routes accessibles sans être connecté (connexion, installation, secours, appairage) :
// 64 Kio suffisent largement, et un inconnu ne peut pas faire allouer des Mo à chaque requête.
func readPublicJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	return readJSONMax(w, r, v, 64<<10)
}

func readJSONMax(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "requête invalide")
		return false
	}
	return true
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("erreur : %v", err)
	httpError(w, http.StatusInternalServerError, "erreur interne")
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "identifiant invalide")
		return 0, false
	}
	return id, true
}

type idsBody struct {
	IDs []int64 `json:"ids"`
}

func readIDs(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	var b idsBody
	if !readJSON(w, r, &b) {
		return nil, false
	}
	if len(b.IDs) == 0 {
		httpError(w, http.StatusBadRequest, "aucun élément sélectionné")
		return nil, false
	}
	if len(b.IDs) > 20000 {
		httpError(w, http.StatusBadRequest, "trop d'éléments")
		return nil, false
	}
	return b.IDs, true
}
