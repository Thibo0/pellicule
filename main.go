// Pellicule : galerie photo auto-hébergée, pensée pour les petites machines
// (VM Freebox à 1 Go de RAM). Un seul binaire, SQLite, miniatures en tâche de fond.
package main

import (
	"database/sql"
	"embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata" // fuseaux horaires embarqués (VM minimales sans tzdata)
)

//go:embed web
var webFS embed.FS

var version = "dev"

type App struct {
	db       *sql.DB
	dataDir  string
	libDir   string
	thumbDir string
	tools    tools
	kick     chan struct{}
	scanKick chan struct{}
	imp      importState
	limiter  *loginLimiter
	secure   bool
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", env("PELLICULE_ADDR", ":8420"), "adresse d'écoute")
	data := flag.String("data", env("PELLICULE_DATA", "./data"), "dossier de la base et des miniatures (disque local)")
	lib := flag.String("library", env("PELLICULE_LIBRARY", ""), "dossier des photos originales (par défaut <data>/library)")
	thumbs := flag.String("thumbs", env("PELLICULE_THUMBS", ""), "dossier des miniatures (par défaut <library>/.thumbs)")
	requireMarker := flag.Bool("require-marker", env("PELLICULE_REQUIRE_MARKER", "") == "1",
		"refuser de démarrer si <library>/.pellicule-library est absent (partage réseau non monté)")
	secure := flag.Bool("secure-cookie", env("PELLICULE_SECURE_COOKIE", "") == "1", "cookies HTTPS uniquement (derrière un reverse proxy TLS)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Pellicule %s\n\nUsage:\n  pellicule [options]\n  pellicule reset-password <utilisateur> <nouveau-mot-de-passe>\n\nOptions:\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := os.MkdirAll(*data, 0o755); err != nil {
		log.Fatalf("création de %s : %v", *data, err)
	}
	db, err := openDB(filepath.Join(*data, "pellicule.db"))
	if err != nil {
		log.Fatalf("base de données : %v", err)
	}

	if flag.Arg(0) == "reset-password" {
		if flag.NArg() != 3 {
			log.Fatal("usage : pellicule reset-password <utilisateur> <nouveau-mot-de-passe>")
		}
		if err := (&App{db: db}).resetPassword(flag.Arg(1), flag.Arg(2)); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Mot de passe modifié.")
		return
	}

	if *lib == "" {
		*lib = filepath.Join(*data, "library")
	}
	if *thumbs == "" {
		*thumbs = filepath.Join(*lib, ".thumbs")
	}
	// Si le disque de la Freebox n'est pas monté, /mnt/... est un dossier vide de la VM :
	// on refuse d'y écrire plutôt que de remplir le petit disque de la VM.
	if *requireMarker {
		if _, err := os.Stat(filepath.Join(*lib, ".pellicule-library")); err != nil {
			log.Fatalf("%s/.pellicule-library introuvable : le disque des photos n'est pas monté ?", *lib)
		}
	}
	for _, d := range []string{*lib, filepath.Join(*lib, ".tmp"), *thumbs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatalf("création de %s : %v", d, err)
		}
	}

	app := &App{
		db:       db,
		dataDir:  *data,
		libDir:   *lib,
		thumbDir: *thumbs,
		tools:    detectTools(),
		kick:     make(chan struct{}, 1),
		scanKick: make(chan struct{}, 1),
		limiter:  newLoginLimiter(),
		secure:   *secure,
	}

	log.Printf("Pellicule %s — photos : %s, données : %s", version, *lib, *data)
	app.tools.report()

	go app.worker()
	go app.janitor()
	go app.importer()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Printf("en écoute sur %s", *addr)
	log.Fatal(srv.ListenAndServe())
}
