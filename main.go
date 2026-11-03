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
	tools    tools
	scanKick chan struct{}
	imp      importState
	limiter  *loginLimiter
	secure   bool
	certFP   string // empreinte SHA-256 du certificat TLS ("" si TLS désactivé)
	certDER  []byte // le certificat lui-même (à installer dans les appareils de la maison)
	secret   []byte // secret propre à l'installation (faux sels)
	files    filesState
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", env("PELLICULE_ADDR", ":8420"), "adresse d'écoute")
	data := flag.String("data", env("PELLICULE_DATA", "./data"), "dossier de la base et du certificat (disque local)")
	lib := flag.String("library", env("PELLICULE_LIBRARY", ""), "dossier des fichiers chiffrés (par défaut <data>/library)")
	requireMarker := flag.Bool("require-marker", env("PELLICULE_REQUIRE_MARKER", "") == "1",
		"refuser de démarrer si <library>/.pellicule-library est absent (partage réseau non monté)")
	noTLS := flag.Bool("no-tls", env("PELLICULE_TLS", "") == "off",
		"servir en HTTP simple (uniquement derrière un reverse proxy HTTPS ; déconseillé)")
	flag.StringVar(&trustedProxy, "trusted-proxy", env("PELLICULE_TRUSTED_PROXY", ""),
		"IP du reverse proxy autorisé à transmettre l'IP du client (X-Forwarded-For)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Pellicule %s

Usage:
  pellicule [options]
  pellicule fingerprint              empreinte du certificat TLS (à comparer lors de l'appairage)
  pellicule setup-code               code de création du compte administrateur (première installation)
  pellicule setup-qr <https://…>     QR code d'installation à scanner avec l'app (adresse, empreinte, code)
  pellicule login-qr <https://…>     QR code de reconnexion d'un téléphone (adresse, empreinte)
  pellicule invite [admin]           lien d'invitation pour créer un compte (valable 7 jours)
  pellicule delete-user <utilisateur> supprime un compte et ses photos chiffrées
  pellicule rebuild                  VM perdue : reconstruit la base depuis le disque des photos
                                     (service arrêté) ; chacun reprend son compte avec sa clé de secours

Options:
`, version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if err := os.MkdirAll(*data, 0o700); err != nil {
		log.Fatalf("création de %s : %v", *data, err)
	}
	db, err := openDB(filepath.Join(*data, "pellicule.db"))
	if err != nil {
		log.Fatalf("base de données : %v", err)
	}
	if *lib == "" {
		*lib = filepath.Join(*data, "library")
	}
	app := &App{
		db:       db,
		dataDir:  *data,
		libDir:   *lib,
		tools:    detectTools(),
		scanKick: make(chan struct{}, 1),
		limiter:  newLoginLimiter(),
		secure:   !*noTLS,
		secret:   serverSecret(db),
	}

	switch flag.Arg(0) {
	case "":
	case "fingerprint":
		_, fp, err := loadOrCreateCert(*data)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Print(fingerprintNumbered(fp))
		return
	case "setup-qr":
		if app.userCount() > 0 {
			fmt.Println("Le compte administrateur existe déjà. Pour un autre téléphone : dans l'app, « J'ai déjà un compte ».")
			return
		}
		if flag.NArg() != 2 {
			log.Fatal("usage : pellicule setup-qr https://<adresse>:8420")
		}
		_, fp, err := loadOrCreateCert(*data)
		if err != nil {
			log.Fatal(err)
		}
		if err := printSetupQR(os.Stdout, flag.Arg(1), fp, app.setupCode()); err != nil {
			log.Fatal(err)
		}
		return
	case "login-qr":
		if flag.NArg() != 2 {
			log.Fatal("usage : pellicule login-qr https://<adresse>:8420")
		}
		_, fp, err := loadOrCreateCert(*data)
		if err != nil {
			log.Fatal(err)
		}
		if err := printLoginQR(os.Stdout, flag.Arg(1), fp); err != nil {
			log.Fatal(err)
		}
		return
	case "setup-code":
		if app.userCount() > 0 {
			log.Fatal("le compte administrateur existe déjà : utiliser « pellicule invite admin »")
		}
		fmt.Println(app.setupCode())
		return
	case "invite":
		token, err := app.createInvite(nil, flag.Arg(1) == "admin")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("/#invite=" + token)
		return
	case "rebuild":
		if err := app.rebuild(os.Stdin, os.Stdout); err != nil {
			log.Fatalf("reconstruction : %v", err)
		}
		return
	case "delete-user":
		var id int64
		if flag.NArg() != 2 || db.QueryRow(`SELECT id FROM users WHERE username=?`, flag.Arg(1)).Scan(&id) != nil {
			log.Fatal("usage : pellicule delete-user <utilisateur existant>")
		}
		if err := app.deleteUser(id); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Compte supprimé.")
		return
	default:
		flag.Usage()
		os.Exit(2)
	}

	// Si le disque de la Freebox n'est pas monté, /mnt/... est un dossier vide de la VM :
	// on refuse d'y écrire plutôt que de remplir le petit disque de la VM.
	if *requireMarker {
		if _, err := os.Stat(filepath.Join(*lib, ".pellicule-library")); err != nil {
			log.Fatalf("%s/.pellicule-library introuvable : le disque des photos n'est pas monté ?", *lib)
		}
	}
	for _, d := range []string{*lib, filepath.Join(*lib, ".tmp")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatalf("création de %s : %v", d, err)
		}
	}

	log.Printf("Pellicule %s — fichiers chiffrés : %s, données : %s", version, *lib, *data)
	app.tools.report()
	if app.userCount() == 0 {
		log.Printf("première installation : code de création du compte administrateur : %s", app.setupCode())
	}

	go app.janitor()
	go func() {
		for range time.Tick(time.Minute) {
			app.limiter.purge()
		}
	}()
	go app.importer()

	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 15 * time.Second,
		// un morceau d'envoi fait au plus 8 Mio : 10 minutes laissent passer une connexion lente (≈ 15 ko/s),
		// pas une connexion tenue ouverte indéfiniment. Les originaux (vidéos) prolongent leur délai (serveBlob).
		ReadTimeout:  10 * time.Minute,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}
	if *noTLS {
		log.Printf("en écoute sur %s (HTTP simple : TLS désactivé)", *addr)
		if trustedProxy == "" {
			log.Print("attention : sans -trusted-proxy, toutes les requêtes semblent venir du proxy et partagent la même limite d'essais")
		}
		log.Fatal(srv.ListenAndServe())
	}
	cert, fp, err := loadOrCreateCert(*data)
	if err != nil {
		log.Fatal(err)
	}
	app.certFP = fp
	app.certDER = cert.Certificate[0]
	srv.TLSConfig = tlsConfig(cert)
	log.Printf("en écoute sur https://%s — empreinte du certificat : %s", *addr, fingerprintGroups(fp))
	log.Fatal(srv.ListenAndServeTLS("", ""))
}
