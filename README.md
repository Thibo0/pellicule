# Pellicule

Galerie photo auto-hébergée, inspirée de Google Photos / Immich, mais pensée pour les petites machines :
un seul programme, ~30 Mo de RAM, tourne sur une VM Freebox à 1 Go à côté d'AdGuard Home.

**Ce qu'il y a :** sauvegarde depuis le téléphone ou le PC (doublons ignorés), photos classées par jour,
albums, favoris, corbeille (30 jours), recherche par nom, vidéos, téléchargement en ZIP, liens de partage
d'album (avec expiration), plusieurs comptes séparés, appli installable sur l'écran d'accueil.

**Ce qu'il n'y a pas (volontairement, 1 Go de RAM) :** reconnaissance faciale, recherche « plage / chien »,
carte, sauvegarde automatique sur iPhone.

---

## Installation sur la VM Freebox

Prérequis : une VM **Debian ou Ubuntu** (celle d'AdGuard convient), un disque dans la Freebox.

1. **Freebox OS → Paramètres de la Freebox → Partages Windows** : activer, choisir un utilisateur et un mot de passe.
2. Copier l'archive sur la VM, puis dans la VM :
   ```bash
   tar xzf pellicule-*.tar.gz
   cd pellicule-*/
   sudo ./install.sh
   ```
   Le script installe libvips + ffmpeg, monte le disque de la Freebox dans `/mnt/freebox`,
   crée le service et affiche l'adresse (par ex. `http://192.168.1.50:8420`).
3. Ouvrir cette adresse et créer le compte administrateur. Les autres comptes se créent dans **Réglages**.

Pour copier l'archive depuis un PC : `scp pellicule-*.tar.gz utilisateur@IP-de-la-VM:`

**Mise à jour :** relancer `sudo ./install.sh` depuis la nouvelle archive et répondre « O » à « Mettre à jour ».

### App Android (recommandé)

Installer `pellicule-android-<version>.apk` sur le téléphone (l'envoyer par mail/Drive/câble, l'ouvrir, autoriser
« installer des applis inconnues »). Au premier lancement : adresse du serveur (ex. `192.168.1.50:8420`),
utilisateur, mot de passe, puis autoriser l'accès à **toutes** les photos.

- La galerie complète (albums, partage…) dans l'app.
- **Sauvegarde automatique** : en Wi-Fi, quand le serveur répond (donc à la maison), toutes les 15 min
  et dès qu'une nouvelle photo est prise. Rien à ouvrir.
- Par défaut seul le dossier de l'appareil photo est sauvegardé ; *Sauvegarde → Dossiers et réglages* pour
  ajouter WhatsApp, captures d'écran…
- Ce qui est déjà sur le serveur n'est jamais renvoyé.

### Sauvegarde automatique sans l'app (FolderSync)

Chaque compte a un **dossier d'import** sur le disque de la Freebox : `Pellicule/Import/<utilisateur>`.
Tout ce qui y arrive est ajouté automatiquement (vérification toutes les 3 minutes), sans être déplacé.

Avec **FolderSync** (Play Store) :
1. *Comptes* → *+* → *SMB* : serveur `mafreebox.freebox.fr`, utilisateur et mot de passe du partage Windows de la Freebox.
2. *Folderpairs* → *+* : synchro **vers le dossier distant** (envoi seulement), local `DCIM/Camera`,
   distant `Pellicule/Import/<utilisateur>` sur le partage.
3. Planification : synchro instantanée ou toutes les heures.
4. Connexion : **Wi-Fi uniquement**, SSID autorisé = le Wi-Fi de la maison (FolderSync demande la localisation pour lire le nom du Wi-Fi).
5. Ne pas activer la suppression des fichiers source ni la synchro des suppressions.

Le guide est aussi dans l'appli, page *Sauvegarde*, avec l'état de l'import et un bouton « Vérifier maintenant ».

### Envoi manuel depuis le téléphone

- **iPhone** : ouvrir l'adresse dans Safari → Partager → *Sur l'écran d'accueil*. Puis *Sauvegarde* → *Choisir des photos*.
- **Android** : Chrome → ⋮ → *Ajouter à l'écran d'accueil*.

Garder l'appli ouverte pendant l'envoi. Relancer de temps en temps : ce qui est déjà envoyé est ignoré.

### Joli nom avec AdGuard Home

AdGuard → *Filtres → Réécritures DNS* → ajouter `photos.maison` → IP de la VM.
L'appli est alors sur `http://photos.maison:8420`.

### Depuis l'extérieur de la maison

Non configuré par défaut (c'est plus sûr). Le plus simple est **Tailscale** sur la VM et sur les téléphones.
Évitez d'ouvrir le port 8420 sur Internet sans HTTPS devant.

---

## Où sont les fichiers

| Quoi | Où |
|---|---|
| Photos originales | `/mnt/freebox/Pellicule/<utilisateur>/<année>/<mois>/` (disque de la Freebox) |
| Miniatures | `/mnt/freebox/Pellicule/.thumbs/` (se régénèrent si on les efface) |
| Base de données | `/var/lib/pellicule/pellicule.db` (albums, favoris, comptes : **à sauvegarder**) |

Les originaux restent de simples fichiers rangés par date : même sans Pellicule, rien n'est perdu.

## Dépannage

```bash
systemctl status pellicule          # état
journalctl -u pellicule -f          # journaux en direct
sudo mount -a                       # remonter le disque de la Freebox
sudo -u pellicule /usr/local/bin/pellicule -data /var/lib/pellicule reset-password <utilisateur> <nouveau-mdp>
```

- **« .pellicule-library introuvable »** : le disque de la Freebox n'est pas monté (Freebox redémarrée, mot de passe
  du partage changé…). Vérifier avec `mount | grep freebox`, puis `sudo mount -a`. Le service redémarre tout seul.
- **Photos HEIC sans miniature** : vérifier que `vipsthumbnail` est installé (`apt install libvips-tools`).
- **Gros envois qui échouent** : le partage Windows de la Freebox supporte mal les très longs transferts ; envoyer par lots
  de quelques centaines de photos. Les doublons étant ignorés, on peut relancer sans risque.

---

## Développement

```bash
go run .                       # http://localhost:8420, données dans ./data
./build.sh 1.0.0               # → dist/pellicule-1.0.0.tar.gz
```

Go + SQLite (sans CGO), interface en HTML/CSS/JS sans framework ni étape de build (`web/`, embarqué dans le binaire).
