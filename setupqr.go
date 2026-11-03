package main

// QR code d'installation, affiché dans le terminal à la fin de install.sh : l'app le scanne et obtient
// d'un coup l'adresse du serveur, l'empreinte de son certificat (épinglée) et le code d'installation.
// Plus rien à recopier, et aucun risque de se connecter à un autre serveur.

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"rsc.io/qr"
)

func setupPayload(server, fp, code string) string {
	return "pellicule:setup?" + url.Values{"url": {server}, "fp": {fp}, "code": {code}}.Encode()
}

// printQR dessine le QR code avec des demi-blocs (deux rangées par ligne), noir sur fond blanc forcé :
// lisible quel que soit le thème du terminal.
func printQR(w io.Writer, text string) error {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return err
	}
	const quiet = 2
	n := c.Size
	black := func(x, y int) bool { return x >= 0 && y >= 0 && x < n && y < n && c.Black(x, y) }
	var b strings.Builder
	for y := -quiet; y < n+quiet; y += 2 {
		b.WriteString("  \x1b[30;47m")
		for x := -quiet; x < n+quiet; x++ {
			top, bottom := black(x, y), black(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// loginPayload : QR code de reconnexion (après une restauration, nouveau certificat) : l'app épingle
// l'empreinte lue dans le QR code, sans comparaison à l'œil, puis demande le nom et le mot de passe.
func loginPayload(server, fp string) string {
	return "pellicule:login?" + url.Values{"url": {server}, "fp": {fp}}.Encode()
}

func printLoginQR(w io.Writer, server, fp string) error {
	if err := printQR(w, loginPayload(server, fp)); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n  Adresse : %s\n  Empreinte (sans appareil photo, l'app demande deux de ces groupes) :\n%s", server, fingerprintNumbered(fp))
	return nil
}

func printSetupQR(w io.Writer, server, fp, code string) error {
	if err := printQR(w, setupPayload(server, fp, code)); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n  Sans appareil photo : adresse %s, code %s\n  Empreinte (l'app demandera deux de ces groupes) :\n%s", server, code, fingerprintNumbered(fp))
	return nil
}
