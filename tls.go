package main

// TLS natif : au premier démarrage, Pellicule génère sa propre clé et un certificat auto-signé.
// Pas d'autorité de certification : l'app Android épingle l'empreinte du certificat (affichée dans
// les journaux, par install.sh et dans les Réglages), le navigateur affiche un avertissement la
// première fois. Les téléphones appairés s'authentifient par certificat client (TLS mutuel).

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// certFingerprint renvoie l'empreinte SHA-256 du certificat (DER), en hexadécimal minuscule.
func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// fingerprintGroups présente une empreinte pour la comparer à l'œil : "AB12 CD34 …".
func fingerprintGroups(fp string) string {
	fp = strings.ToUpper(fp)
	var b strings.Builder
	for i := 0; i < len(fp); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(fp[i:min(i+4, len(fp))])
	}
	return b.String()
}

// fingerprintNumbered : l'empreinte en deux lignes de 8 groupes numérotés, pour que l'app puisse demander
// de recopier « le groupe n° 7 et le n° 12 » (tirés au hasard) sans qu'il faille compter.
func fingerprintNumbered(fp string) string {
	fp = strings.ToUpper(fp)
	var b strings.Builder
	for row := 0; row < 2; row++ {
		b.WriteString("  ")
		for i := row * 8; i < row*8+8; i++ {
			fmt.Fprintf(&b, "%-6s", fmt.Sprintf("%d.", i+1))
		}
		b.WriteString("\n  ")
		for i := row * 8; i < row*8+8; i++ {
			if 4*i+4 <= len(fp) {
				fmt.Fprintf(&b, "%-6s", fp[4*i:4*i+4])
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// loadOrCreateCert charge data/tls/{cert,key}.pem, ou les crée au premier démarrage.
func loadOrCreateCert(dataDir string) (tls.Certificate, string, error) {
	dir := filepath.Join(dataDir, "tls")
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		if err := createCert(dir, certPath, keyPath); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("certificat TLS : %w", err)
	}
	return cert, certFingerprint(cert.Certificate[0]), nil
}

func createCert(dir, certPath, keyPath string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	host, _ := os.Hostname()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Pellicule", Organization: []string{"Pellicule"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "photos.maison"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if host != "" {
		tpl.DNSNames = append(tpl.DNSNames, host, host+".local")
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
				tpl.IPAddresses = append(tpl.IPAddresses, ipn.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// DeviceCertName : émetteur (et sujet) des certificats auto-signés que les téléphones génèrent dans
// leur Keystore. Le serveur l'annonce dans sa demande de certificat client : un navigateur ne propose
// que les certificats de cet émetteur, donc aucun (pas de fenêtre « choisir un certificat » sur un PC
// qui en a d'autres, professionnels ou Windows Hello).
var DeviceCertName = pkix.Name{CommonName: "Pellicule appareil", Organization: []string{"Pellicule"}}

// deviceCAPool : un certificat factice dont seul le nom compte (sa clé est jetée aussitôt).
// Rien n'est vérifié par chaîne : un appareil est reconnu à l'empreinte exacte de son certificat.
func deviceCAPool() *x509.CertPool {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: DeviceCertName, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().AddDate(1, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	c, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(c)
	return pool
}

// tlsConfig : TLS 1.2 minimum ; le certificat client est demandé mais pas exigé
// (les navigateurs n'en ont pas, les téléphones appairés oui).
func tlsConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		ClientAuth:   tls.RequestClientCert,
		ClientCAs:    deviceCAPool(), // seulement pour la liste d'émetteurs annoncée (RequestClientCert ne vérifie rien)
	}
}

// handleCert : le certificat du serveur (public), à installer comme « certificat de confiance » sur les
// ordinateurs et téléphones de la maison. Le navigateur cesse alors d'afficher un avertissement, et un
// faux serveur déclencherait de nouveau une vraie alerte au lieu d'un avertissement qu'on a pris
// l'habitude d'ignorer. Le service worker (vidéos en flux) ne fonctionne qu'à cette condition.
func (a *App) handleCert(w http.ResponseWriter, r *http.Request) {
	if len(a.certDER) == 0 {
		httpError(w, http.StatusNotFound, "TLS désactivé")
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="pellicule.crt"`)
	w.Write(a.certDER)
}
