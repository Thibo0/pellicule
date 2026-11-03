# Pellicule

Galerie photo auto-hébergée, inspirée de Google Photos / Immich, mais pensée pour les petites machines :
un seul programme, ~30 Mo de RAM, tourne sur une VM Freebox à 1 Go à côté d'AdGuard Home.

**Coffre-fort :** les photos sont **chiffrées de bout en bout**, sur le téléphone ou dans le navigateur, avant
d'être envoyées. Le serveur, le disque de la Freebox et la base ne contiennent que des données illisibles sans
ton mot de passe. Détails, limites et analyse de sécurité : [`docs/SECURITE.md`](docs/SECURITE.md).

**Ce qu'il y a :** sauvegarde depuis le téléphone ou le PC (doublons ignorés), photos classées par jour,
albums, favoris, corbeille (30 jours), recherche par nom, vidéos, téléchargement en ZIP, liens de partage
d'album chiffrés (avec expiration), plusieurs comptes séparés, appli installable sur l'écran d'accueil.

**Ce qu'il n'y a pas (volontairement, 1 Go de RAM) :** reconnaissance faciale, recherche « plage / chien »,
carte, sauvegarde automatique sur iPhone.

---

## Installation sur la VM Freebox

Prérequis : une VM **Debian ou Ubuntu** (celle d'AdGuard convient), un disque dans la Freebox, un téléphone Android.

1. **Freebox OS → Paramètres de la Freebox → Partages Windows** : activer, choisir un utilisateur et un mot de passe.
2. Copier l'archive sur la VM (`scp pellicule-*.tar.gz utilisateur@IP-de-la-VM:`), puis dans la VM :
   ```bash
   tar xzf pellicule-*.tar.gz
   cd pellicule-*/
   sudo ./install.sh
   ```
   Le script installe tout, monte le disque de la Freebox et termine en affichant **un QR code**.
3. Installer l'APK sur le téléphone, ouvrir l'app → **Scanner le QR code** → nom + mot de passe → **noter la clé de secours**.

C'est tout. Le QR code contient l'adresse du serveur, l'empreinte de son certificat (l'app refusera tout autre
serveur) et un code d'installation à usage unique : personne d'autre sur le Wi-Fi ne peut prendre la place de l'administrateur.

**Mise à jour :** relancer `sudo ./install.sh` depuis la nouvelle archive et répondre « O » à « Mettre à jour ».

### L'app Android

- **Ajouter quelqu'un** : *Réglages → Inviter quelqu'un* → l'autre personne scanne le QR code avec son app.
- **Nouveau téléphone** : *J'ai déjà un compte* → adresse, nom, mot de passe.
- **Téléphone perdu** : *Réglages → Téléphones appairés → Révoquer* (depuis un autre téléphone ou un navigateur).
- **Sauvegarde automatique** : en Wi-Fi, quand le serveur répond (donc à la maison), toutes les 15 min et dès
  qu'une nouvelle photo est prise. Tout est chiffré **sur le téléphone** avant l'envoi. Ce qui est déjà sur le serveur
  n'est jamais renvoyé.
- Par défaut seul le dossier de l'appareil photo est sauvegardé ; *Sauvegarde → Dossiers et réglages* pour
  ajouter WhatsApp, captures d'écran…

Le téléphone garde sa clé dans sa puce de sécurité, jamais le mot de passe. Il ne parle qu'au serveur appairé.

### Depuis un navigateur (iPhone, ordinateur)

Ouvrir `https://<IP de la VM>:8420`. La première fois, le navigateur avertit que le certificat est inconnu : c'est normal
(il est propre à ton serveur, voir son empreinte dans *Réglages*). Pour supprimer l'avertissement et être alerté si un faux
serveur apparaît : *Réglages → Certificat du serveur*, puis l'installer comme certificat de confiance (Windows : double-clic →
Installer → « Autorités de certification racines de confiance »).

### Sauvegarde automatique sans l'app (FolderSync)

Chaque compte a un **dossier d'import** sur le disque de la Freebox : `Pellicule/Import/<utilisateur>`.
Tout ce qui y arrive est ajouté automatiquement (vérification toutes les 3 minutes) : le serveur le chiffre,
puis **efface le fichier en clair**. Les photos restent donc lisibles sur le partage quelques minutes : l'app
Android, qui chiffre avant l'envoi, est plus sûre.

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
L'appli est alors sur `https://photos.maison:8420` (nom inclus dans le certificat).

### Depuis l'extérieur de la maison

Non configuré par défaut (c'est plus sûr). Le plus simple est **Tailscale** sur la VM et sur les téléphones.
Évitez d'ouvrir le port 8420 sur Internet : le chiffrement protège les photos, pas le serveur lui-même (voir `docs/SECURITE.md`, R2).

---

## Où sont les fichiers

| Quoi | Où |
|---|---|
| Photos, vidéos, miniatures (**chiffrées**) | `/mnt/freebox/Pellicule/u<n° de compte>/…` (disque de la Freebox) |
| Dépôt FolderSync (transit, en clair) | `/mnt/freebox/Pellicule/Import/<utilisateur>/` |
| Base de données | `/var/lib/pellicule/pellicule.db` |
| Certificat TLS du serveur | `/var/lib/pellicule/tls/` |

### La règle : la clé de secours

À la création de chaque compte, l'app affiche une **clé de secours** (13 groupes de 4 caractères) et fait recopier
deux groupes pour vérifier qu'elle est notée. C'est **le seul moyen** de retrouver ses photos :
- si on oublie son mot de passe ;
- si la VM est perdue ou réinstallée.

Personne ne peut la retrouver, pas même l'administrateur : elle n'est enregistrée nulle part. Sans elle, les photos
du compte sont perdues pour toujours. C'est le prix d'un disque de la Freebox inattaquable : il ne contient rien qui
permette de deviner un mot de passe.

### Si la VM est perdue

Chaque photo chiffrée a, à côté d'elle, un petit fichier `<n°>.k` (sa clé, chiffrée par la clé maître du compte) ;
chaque compte a son `compte.json` (clé maître chiffrée **par la clé de secours seulement**) et son `albums.json`.
Sur une VM neuve, relancer `sudo ./install.sh` avec le même disque de la Freebox : le script trouve ces fichiers,
**affiche la liste des comptes** et ne reconstruit que si l'on tape `RECONSTRUIRE`. Vérifier que chaque nom est quelqu'un
de la maison.

Reviennent : comptes, photos, vidéos, favoris, corbeille, albums. Ne reviennent pas : les mots de passe, les téléphones
appairés, les liens de partage (à recréer).

Ensuite, sur chaque téléphone : *Appairer de nouveau* (ou *Scanner le QR code*) → QR code de reconnexion affiché par le
script → **Mot de passe oublié / serveur réinstallé** → nom, clé de secours, nouveau mot de passe.
Pour réafficher ce QR code : `sudo -u pellicule pellicule -data /var/lib/pellicule login-qr https://<IP>:8420`.

(À la main : `sudo systemctl stop pellicule`, déplacer `/var/lib/pellicule/pellicule.db`, puis
`sudo -u pellicule pellicule -data /var/lib/pellicule -library /mnt/freebox/Pellicule rebuild`, puis `sudo systemctl start pellicule`.)

## Dépannage

```bash
systemctl status pellicule          # état
journalctl -u pellicule -f          # journaux en direct
sudo mount -a                       # remonter le disque de la Freebox
P="sudo -u pellicule pellicule -data /var/lib/pellicule"
$P fingerprint                      # empreinte du certificat (à comparer avec celle du navigateur ou de l'app)
$P invite [admin]                   # lien d'invitation, si plus aucun admin ne peut se connecter
$P delete-user <utilisateur>        # supprime un compte et ses photos
```

- **Mot de passe oublié** : page de connexion → *Mot de passe oublié ?*, avec la clé de secours. Sans elle, il n'y a
  aucun moyen de récupérer les photos, par conception.
- **L'app affiche « Ce n'est pas ton serveur »** : le certificat a changé. Si tu as réinstallé le serveur
  (dossier `/var/lib/pellicule/tls` effacé), appaire de nouveau le téléphone. Sinon, ne va pas plus loin.
- **« .pellicule-library introuvable »** : le disque de la Freebox n'est pas monté (Freebox redémarrée, mot de passe
  du partage changé…). Vérifier avec `mount | grep freebox`, puis `sudo mount -a`. Le service redémarre tout seul.
- **Photos HEIC sans miniature (import FolderSync)** : vérifier que `vipsthumbnail` est installé (`apt install libvips-tools`).
  Les photos envoyées par l'app ou le navigateur ont leur miniature faite par l'appareil.
- **Gros envois qui échouent** : le partage Windows de la Freebox supporte mal les très longs transferts ; envoyer par lots
  de quelques centaines de photos. Les doublons étant ignorés, on peut relancer sans risque.

---

## Développement

```bash
go run .                       # https://localhost:8420, données dans ./data
go test ./...                  # serveur (sécurité, isolation, partage, appairage, import)
node --test web/crypto.test.mjs   # crypto JS contre les vecteurs Go
(cd android && ./gradlew testDebugUnitTest)   # crypto Kotlin contre les mêmes vecteurs
./build.sh 1.0.0               # → dist/pellicule-1.0.0.tar.gz
```

Go + SQLite (sans CGO), interface en HTML/CSS/JS sans framework ni étape de build (`web/`, embarqué dans le binaire).
