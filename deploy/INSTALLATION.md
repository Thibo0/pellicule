# Pellicule {{VERSION}}

Galerie photo auto-hébergée pour la maison : un serveur qui tourne sur une VM (Freebox ou autre) et une app Android qui y sauvegarde les photos automatiquement.

## Quel fichier télécharger ?

| Fichier | Pour quoi faire |
|---|---|
| **`pellicule-{{VERSION}}.tar.gz`** | Archive complète : le serveur, le script d'installation, l'app Android et ce guide. **À prendre pour une première installation.** |
| **`pellicule-android-{{VERSION}}.apk`** | L'app Android seule, à télécharger directement depuis le téléphone. |

---

## Installation (10 minutes, une seule fois)

Il faut une VM **Debian ou Ubuntu** (par exemple celle d'AdGuard sur la Freebox) et un téléphone **Android**.

1. Dans **Freebox OS → Paramètres de la Freebox → Partages Windows**, activer le partage et choisir un utilisateur
   et un mot de passe. C'est là que les photos (chiffrées) seront rangées.
2. Copier l'archive sur la VM, puis sur la VM :
   ```bash
   tar xzf pellicule-{{VERSION}}.tar.gz
   cd pellicule-{{VERSION}}
   sudo ./install.sh
   ```
   Répondre aux questions sur le partage. Pour le reste, la touche Entrée suffit. À la fin, **un QR code s'affiche**.
3. Sur le téléphone : installer **`pellicule-android-{{VERSION}}.apk`**. Android demande d'autoriser l'installation
   d'applis inconnues : accepter.
4. Ouvrir l'app → **Scanner le QR code** → choisir son nom et son mot de passe → **noter la clé de secours**
   sur papier. C'est fini : les photos du téléphone partent toutes seules, chiffrées, dès qu'il est sur le Wi-Fi de la maison.

**Ajouter quelqu'un** (famille…) : dans l'app, **Réglages → Inviter quelqu'un**, puis l'autre personne installe l'app et
scanne le QR code affiché.

**Nouveau téléphone** : installer l'app → **J'ai déjà un compte** → adresse du serveur, nom, mot de passe.

> **La règle : la clé de secours.** Tout est chiffré sur le téléphone : ni le serveur ni le disque de la Freebox
> ne peuvent voir les photos, et personne ne peut réinitialiser un mot de passe. La clé de secours de chaque compte est
> le **seul** moyen de retrouver ses photos si le mot de passe est oublié **ou si la VM est perdue**. Sans elle, les
> photos du compte sont perdues pour toujours. Chaque personne invitée reçoit la sienne et doit la noter.

**Si la VM est perdue :** sur une VM neuve, relancer `sudo ./install.sh` avec le même disque de la Freebox. Le script
liste les comptes trouvés sur le disque ; vérifier les noms, taper `RECONSTRUIRE`. Puis, sur chaque téléphone, scanner
le QR code affiché → **Mot de passe oublié / serveur réinstallé** → nom, clé de secours, nouveau mot de passe.

**iPhone ou ordinateur :** ouvrir `https://<adresse du serveur>:8420` dans le navigateur. Il y a un avertissement de
certificat la première fois : c'est normal, le certificat est propre à ton serveur. Se connecter avec son nom et son mot de passe.

---

## Mettre à jour

- **Serveur :** copier la nouvelle archive sur la VM, la décompresser, lancer `sudo ./install.sh` et répondre **O** à « Mettre à jour ». Les photos et les comptes sont conservés.
  *Depuis une 0.4 :* la 0.5 chiffre tout et ne peut pas reprendre une base 0.4 (photos en clair). Le serveur refuse de démarrer
  et explique quoi déplacer pour repartir de zéro.
- **App Android :** installer le nouvel APK par-dessus l'ancien. Il n'y a rien à désinstaller.
  L'interface de l'app est dans l'APK (le serveur ne peut pas la modifier) : mettre à jour le serveur ET l'app, en même temps.

## En cas de problème

```bash
systemctl status pellicule      # le service tourne-t-il ?
journalctl -u pellicule -f      # journaux en direct
sudo mount -a                   # remonter le disque de la Freebox (après un redémarrage)
```

Le guide complet (accès depuis l'extérieur, nom `photos.maison`, FolderSync, reconstruction après perte de la VM…) est dans le `README.md` de l'archive,
et le fonctionnement du chiffrement dans `docs/SECURITE.md`.
