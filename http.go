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

	// session
	m.HandleFunc("GET /api/me", a.handleMe)
	m.HandleFunc("POST /api/setup", a.handleSetup)
	m.HandleFunc("POST /api/login", a.handleLogin)
	m.HandleFunc("POST /api/logout", a.auth(a.handleLogout))
	m.HandleFunc("POST /api/me/password", a.auth(a.handleChangePassword))
	m.HandleFunc("GET /api/stats", a.auth(a.handleStats))

	// comptes
	m.HandleFunc("GET /api/users", a.admin(a.handleListUsers))
	m.HandleFunc("POST /api/users", a.admin(a.handleCreateUser))
	m.HandleFunc("DELETE /api/users/{id}", a.admin(a.handleDeleteUser))
	m.HandleFunc("POST /api/users/{id}/password", a.admin(a.handleAdminSetPassword))

	// photos
	m.HandleFunc("POST /api/upload", a.auth(a.handleUpload))
	m.HandleFunc("POST /api/assets/check", a.auth(a.handleCheck))
	m.HandleFunc("GET /api/timeline", a.auth(a.handleTimeline))
	m.HandleFunc("GET /api/assets/{id}", a.auth(a.handleAsset))
	m.HandleFunc("GET /api/assets/{id}/thumb", a.auth(a.serveOwned("s")))
	m.HandleFunc("GET /api/assets/{id}/preview", a.auth(a.serveOwned("m")))
	m.HandleFunc("GET /api/assets/{id}/original", a.auth(a.serveOwned("")))
	m.HandleFunc("GET /api/download", a.auth(a.handleDownloadSelection))
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
	m.HandleFunc("GET /api/albums/{id}/zip", a.auth(a.handleAlbumZip))
	m.HandleFunc("GET /api/albums/{id}/shares", a.auth(a.handleAlbumShares))
	m.HandleFunc("POST /api/albums/{id}/shares", a.auth(a.handleCreateShare))
	m.HandleFunc("DELETE /api/shares/{token}", a.auth(a.handleDeleteShare))

	// partage public
	m.HandleFunc("GET /api/s/{token}", a.handlePublicAlbum)
	m.HandleFunc("GET /api/s/{token}/assets/{id}/thumb", a.servePublic("s"))
	m.HandleFunc("GET /api/s/{token}/assets/{id}/preview", a.servePublic("m"))
	m.HandleFunc("GET /api/s/{token}/assets/{id}/original", a.servePublic(""))
	m.HandleFunc("GET /api/s/{token}/zip", a.handlePublicZip)

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

	return securityHeaders(m)
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
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
