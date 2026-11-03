# Sécurité de Pellicule (0.5)

Pellicule chiffre les photos **de bout en bout** : elles sont chiffrées sur le téléphone ou dans le navigateur
avant d'être envoyées, et le serveur ne stocke que des données qu'il ne peut pas ouvrir. Ce document décrit
ce qui est protégé, contre qui, ce qui ne l'est pas, et le résultat de l'analyse de sécurité de novembre 2026.

## En bref

| Situation | Ce que l'attaquant obtient |
|---|---|
| Quelqu'un écoute le Wi-Fi | Rien : tout passe en TLS. |
| Vol du disque de la Freebox, lecture du partage Windows | Des fichiers chiffrés par des clés aléatoires de 256 bits : rien à deviner, pas même les mots de passe (le disque ne contient ni la base ni rien qui en dérive). Il voit le nombre de comptes, de photos, leurs dates et leurs tailles. |
| Écriture sur le partage Windows | Il peut **effacer** les photos (aucune protection contre cela : garder une copie hors ligne du disque), ou déposer des fichiers dans `Import/` (ils arrivent dans la photothèque). |
| Copie de la base de données (disque de la VM) | Des clés chiffrées par les mots de passe : à lui de deviner les mots de passe, hors ligne (voir R4). |
| Faux serveur face à l'app Android | Rien : l'app refuse tout certificat autre que celui appairé. |
| Faux serveur face à un navigateur | **Dépend de toi** : le navigateur affiche un avertissement, que tu ne dois pas ignorer (voir R1). |
| Prise de contrôle de la VM (administrateur) | Les données au repos restent chiffrées. **Dans l'app Android**, il ne peut pas piéger l'interface (embarquée dans l'APK). **Dans un navigateur**, il peut piéger la page et récupérer les clés au prochain déverrouillage (voir R2). |
| Téléphone volé, déverrouillé | L'accès aux photos, jusqu'à la révocation du téléphone (voir R6). |

**À ne pas perdre : la clé de secours de chaque compte.** Sans elle, un mot de passe oublié **ou la perte de la VM**
= photos perdues pour toujours. L'app la fait recopier en partie à la création du compte pour vérifier qu'elle est notée.

**Fichiers autonomes (reprise après perte de la VM).** Le disque des photos porte tout ce qu'il faut pour reconstruire
la base (`pellicule rebuild`), et rien qui permette de deviner un mot de passe :
- `u<n>/compte.json` : nom, rôle, clé maître chiffrée **par la clé de secours**, `SHA-256` de la preuve de secours,
  clé publique, clé privée chiffrée par la clé maître. Ni la clé maître chiffrée par le mot de passe, ni le hash
  d'authentification, ni sessions, ni téléphones ;
- `u<n>/<n°/1000>/<n°>.k` à côté de chaque fichier : sa clé chiffrée par la clé maître, ses métadonnées chiffrées,
  sa date, ses empreintes de dédoublonnage, favori et corbeille ;
- `u<n>/albums.json` : noms d'albums chiffrés et leur contenu.

La reconstruction affiche les comptes trouvés (et l'administrateur) et exige de taper `RECONSTRUIRE` ; elle refuse
deux comptes du même nom et ignore les fichiers invalides. Aucun téléphone, session ni invitation n'est recréé, aucun
mot de passe ne fonctionne : chaque compte se reprend avec sa clé de secours (qui ouvre la clé maître, puis en choisit un
nouveau). À la reprise, l'app et la page **revérifient la clé publique** contre la clé privée : remplacée sur le disque,
elle ferait chiffrer les imports pour un autre. La **clé maître ne change jamais** (ni au changement de mot de passe,
ni à la reprise) : une clé de secours notée reste valable pour toujours.

## Ce que le serveur voit encore

Le serveur voit :
- les noms d'utilisateur, les noms des téléphones appairés et leurs adresses IP ;
- la **date de prise de vue** (nécessaire pour trier et paginer), le type (photo ou vidéo) et la taille chiffrée de chaque élément ;
- le nombre d'éléments, quelle photo est dans quel album, les favoris, la corbeille, l'existence des liens de partage ;
- quand deux envois d'un même compte sont le **même fichier** (dédoublonnage).

Sont chiffrés : les images et vidéos, les miniatures et aperçus, les noms de fichiers, les types, les dimensions,
l'EXIF (y compris le GPS) et les noms d'albums.

## Format cryptographique (v1)

Les primitives sont natives dans WebCrypto, Android et Go : AES-256-GCM, ECDH P-256, HKDF-SHA256, PBKDF2-SHA256, HMAC-SHA256.
Les trois implémentations (`crypto.go`, `web/crypto.js`, `Crypto.kt`) sont vérifiées contre les mêmes vecteurs (`testdata/vectors.json`).

**Compte**
- `K = PBKDF2-SHA256(mot de passe NFC, sel 16 o, 600 000 tours)`.
- `authKey = HKDF(K, "pellicule auth")` : seule valeur envoyée au serveur, stockée en bcrypt. **Le mot de passe ne quitte jamais l'appareil.**
- `wrapKey = HKDF(K, "pellicule wrap")` chiffre la clé maître `MK` (256 bits aléatoires).
- La **clé de secours** (32 octets, base32) chiffre une seconde copie de `MK`. Le serveur ne garde que `SHA-256(HKDF(secours, "pellicule recovery auth"))`.
- Une paire ECDH P-256 par compte : la clé publique est en clair (import FolderSync), la clé privée est chiffrée par `MK`.
- Pour un compte inexistant, `prelogin` renvoie un faux sel stable (HMAC d'un secret du serveur) : on ne peut pas savoir quels comptes existent.

**Boîtes** : `nonce (12) ‖ AES-256-GCM(clair) ‖ tag (16)`, nonce aléatoire. L'AAD indique l'usage
(`pellicule/mk`, `pellicule/fk`, `pellicule/meta`, `pellicule/thumb`…) : une boîte ne peut pas être rejouée pour un autre usage.

**Fichier**
- `FK = HKDF(MK, "pellicule file key|" ‖ hex(H))`, où `H` est le SHA-256 de la suite des SHA-256 de chaque bloc de 1 Mio.
  C'est un chiffrement convergent **limité au compte** : un envoi interrompu reprend à l'identique depuis n'importe quel appareil.
  Le serveur n'apprend rien de plus que via le dédoublonnage.
- Original : en-tête `"PLC1" ‖ taille de bloc ‖ préfixe de nonce (8)`, puis des blocs de 1 Mio en AES-GCM.
  Le nonce vaut `préfixe ‖ n° de bloc`. L'AAD contient l'en-tête et le drapeau « dernier bloc » :
  on peut lire une plage (vidéo), mais une troncature ou un réordonnancement est détecté.
- Miniature (400 px) et aperçu (1600 px) : calculés par le client, scellés par `FK`.
- `dedupTag = HMAC(HKDF(MK, "pellicule dedup"), H)` ; `deviceTag = HMAC(…, "device|" ‖ nom ‖ "|" ‖ taille)`.

**Import FolderSync** : le serveur voit ces fichiers, puisqu'ils arrivent en clair sur le partage. Il génère une `FK` aléatoire,
la scelle pour la clé publique du compte (ECDH éphémère + HKDF + AES-GCM), chiffre le fichier, puis **efface le clair**.
Le premier client déverrouillé rechiffre ensuite la `FK` avec `MK`.

**Lien de partage** : `https://…/s/<jeton>#<SK>`. `SK` (256 bits) est dans le fragment, que le navigateur n'envoie jamais.
Le serveur stocke `SHA-256(jeton)`, la `FK` de chaque photo de l'album scellée par `SK`, et le lien complet scellé par `MK`
(pour que le propriétaire puisse le réafficher).

## Transport et appareils

- **TLS natif** : certificat ECDSA P-256 auto-signé, généré au premier démarrage (`data/tls/`, clé privée 0600).
  Son empreinte est affichée par `install.sh`, dans les journaux, dans les Réglages et par `pellicule fingerprint`.
- **Téléphone** : la paire EC P-256 est générée dans le Keystore (StrongBox si disponible), la clé privée ne peut pas en sortir.
  L'app se lie au serveur de trois façons, toutes par TLS épinglé avec le certificat de l'appareil :
  - QR code d'**installation** affiché par `install.sh` (adresse, empreinte, code d'installation à usage unique) : le compte
    administrateur est créé sur le téléphone ;
  - QR code d'**invitation** (« Inviter quelqu'un ») : création d'un compte ;
  - **connexion** avec le nom et le mot de passe, ou QR code d'appairage affiché par un navigateur.
  Sans QR code, l'empreinte est affichée pour être comparée à l'œil. Le téléphone s'authentifie ensuite par
  **certificat client TLS** : il n'a ni cookie ni mot de passe stocké. La clé de secours s'affiche une seule fois,
  avec les captures d'écran bloquées.
- La **clé maître** est chiffrée par une clé AES du Keystore. Elle est donnée à la page par un canal `WebMessageListener`
  que la WebView n'ouvre que pour l'origine exacte du serveur épinglé.
- **Révocation** (Réglages) : immédiate côté serveur. Au lancement suivant, l'app vérifie elle-même la révocation, puis efface ses clés.
- **Navigateur** : session par cookie `__Host-`, `HttpOnly`, `Secure`, `SameSite=Strict`, stockée hachée côté serveur.
  La clé maître est importée en `CryptoKey` **non extractible** (IndexedDB) et effacée à la déconnexion ou quand la session expire.
- **En-têtes** : CSP stricte (`script-src 'self'`, aucun script en ligne), `frame-ancestors 'none'`, `nosniff`, `no-referrer`, HSTS, COOP/CORP.
- **Écritures** : en-tête `X-Pellicule` obligatoire, ce qui protège contre le CSRF.
- **Tentatives** : 10 essais par 15 minutes, par IP et par compte (connexion, secours, appairage, code d'installation).

## Analyse de sécurité (2 novembre 2026)

L'analyse a porté sur la conception cryptographique, le code du serveur Go, l'interface web, l'app Android et le déploiement.
Les scénarios ont été rejoués :
- tests Go : comptes, isolation, partage, appairage, import ;
- test de bout en bout dans Chromium : 40 vérifications ;
- émulateur Android 15 : appairage, 63 envois, vidéo, téléchargement, révocation, faux certificat.

### Corrigé pendant l'analyse

| # | Gravité | Problème | Correction |
|---|---|---|---|
| C1 | Élevée | Un **lien symbolique** déposé dans le dossier d'import était suivi : le serveur aurait chiffré un fichier quelconque de la VM (clé TLS…) et l'aurait livré au compte. | Seuls les fichiers ordinaires sont importés (vérifié au parcours et à l'ouverture), test ajouté. |
| C2 | Moyenne | Contenu déchiffré servi avec le type MIME choisi par l'expéditeur (`blob:` et `/vault/` héritent de notre origine) : un fichier « vidéo » de type HTML ou SVG partagé par un tiers aurait pu s'exécuter chez nous. | Types limités à image, vidéo et audio, sinon `application/octet-stream`. Les réponses du service worker portent `nosniff` et une CSP `sandbox`. |
| C3 | Moyenne | Miniatures de l'import écrites **en clair sur le partage** le temps du traitement. | Écrites dans le `/tmp` privé de la VM. |
| C4 | Moyenne | Téléphone révoqué : l'app affichait le formulaire de connexion mais **gardait la clé maître**. | L'app vérifie la révocation par sa propre connexion, puis efface ses clés et revient à l'appairage. |
| C5 | Moyenne | Session expirée : la clé maître restait dans le navigateur. | Effacée dès que le serveur ne reconnaît plus la session. |
| C6 | Faible | Codes à usage unique (invitation, appairage, installation) vérifiés puis supprimés en deux temps : deux requêtes simultanées pouvaient les utiliser deux fois. | Consommés en une seule instruction SQL (`DELETE … RETURNING`). |
| C7 | Disponibilité | Avec un certificat auto-signé, **Chrome refuse le service worker** : une longue vidéo était entièrement téléchargée et déchiffrée en mémoire. | App : vidéos déchiffrées par plages par l'app elle-même. Navigateur : certificat à installer (Réglages). |
| C8 | Confort | Le serveur demandait un certificat client à tout navigateur : Chrome sous Windows ouvrait une fenêtre « choisir un certificat ». | Le serveur n'annonce que l'émetteur « Pellicule appareil ». |

### Risques restants

| # | Gravité | Risque | Recommandation |
|---|---|---|---|
| R1 | **Élevée** (navigateur) | Le certificat étant auto-signé, le navigateur avertit à la première visite et on prend l'habitude de cliquer « Continuer ». Un appareil malveillant sur le Wi-Fi (usurpation ARP/DNS) produirait **le même avertissement** et pourrait servir une page piégée qui récupère le mot de passe. | Installer le certificat du serveur sur les ordinateurs (Réglages → Certificat du serveur) : un faux serveur déclenche alors une vraie alerte. Sinon, comparer l'empreinte. L'app Android n'est pas concernée. Les QR codes d'invitation et d'appairage créés dans un navigateur ne portent plus d'empreinte : l'app fait vérifier celle du serveur qu'elle joint (deux groupes à recopier). |
| R2 | **Élevée** pour le navigateur, **réglé** pour l'app | Dans un **navigateur**, c'est le serveur qui fournit le code de l'interface : un attaquant qui **modifie** le programme sur la VM peut piéger la page et obtenir la clé maître au prochain déverrouillage. Le chiffrement protège les données **au repos** (disque, partage, base copiée, vol), pas contre un serveur compromis qui reste actif. **L'app Android** n'est plus concernée : l'interface est embarquée dans l'APK (`WebAssets.kt`). La page garde l'origine du serveur, mais tout ce qui n'est pas `/api/` est servi depuis l'APK ou refusé ; une navigation vers `/api/` est refusée (un document du serveur ne peut pas s'exécuter dans l'origine de la page) ; CSP stricte (scripts nommés un par un, ni cadre ni worker) ; profil WebView neuf (`setDataDirectorySuffix`, Android 9 et plus : aucun service worker ni cache d'une version qui chargeait la page depuis le serveur) et données web effacées à chaque mise à jour de l'APK. Contrepartie : une évolution de l'interface demande une nouvelle version de l'APK. Reste possible dans l'app : un serveur compromis envoie de fausses données (chiffrées : il ne peut pas les forger sans les clés) ou refuse le service. | Préférer l'app au navigateur. Garder la VM à jour, sans accès depuis Internet (Tailscale plutôt qu'un port ouvert). |
| R3 | **Élevée** (disponibilité) | Perte de la VM : la base est perdue. | Fait : fichiers autonomes sur le disque des photos et `pellicule rebuild`, reprise par clé de secours. Pas de copie de la base sur le partage : elle permettrait de deviner les mots de passe hors ligne. Contrepartie : **sans clé de secours, un compte est perdu** avec la VM. Le certificat TLS n'est pas copié (il permettrait d'usurper le serveur) : après une réinstallation, chaque téléphone se reconnecte par QR code. |
| R4 | Moyenne | Quiconque obtient la base (ou un téléphone appairé) peut tester des mots de passe **hors ligne** contre la clé maître chiffrée. PBKDF2 à 600 000 tours ralentit l'attaque, sans plus. Argon2id ferait mieux mais n'existe pas dans WebCrypto. Le serveur ne peut pas imposer de règles, puisqu'il ne voit jamais le mot de passe : seule la page exige 10 caractères. | Phrases de passe longues (4 à 5 mots). |
| R5 | Moyenne | **FolderSync** : les photos sont en clair sur le partage jusqu'à l'import (jusqu'à environ 5 min). Après effacement, elles restent récupérables sur le disque avec des outils spécialisés. Leur nom apparaît dans les journaux en cas d'erreur. | Préférer l'app Android, qui chiffre avant l'envoi. |
| R6 | Moyenne | Téléphone volé **déverrouillé** : accès complet (lecture, suppression) jusqu'à révocation. La clé de l'app n'exige pas d'empreinte digitale, sinon la sauvegarde en arrière-plan serait impossible. | Verrouillage d'écran sur les téléphones, révocation rapide depuis un navigateur. |
| R7 | Faible | Métadonnées non chiffrées (voir plus haut). Le serveur peut aussi modifier les dates, les favoris ou l'ordre, ou supprimer des fichiers : la confidentialité est garantie, l'intégrité de ces métadonnées et la disponibilité ne le sont pas face au serveur. | Accepté : il faut pouvoir trier et paginer. |
| R8 | Faible | La clé d'un album partagé est **dans le lien** : quiconque le reçoit voit l'album, et il reste dans l'historique du navigateur. « Téléchargement désactivé » ne fait que masquer le bouton : les vidéos restent lisibles et une photo affichée peut toujours être capturée. | Liens avec expiration, à supprimer après usage. |
| R9 | Faible | Le blocage après 10 essais permet à un appareil du réseau d'**empêcher** quelqu'un de se connecter pendant 15 min. Il n'y a pas de quota disque par compte. | Accepté pour un usage familial. |
| R10 | Faible | L'appairage manuel repose sur la comparaison de l'empreinte par l'utilisateur. Un QR code photographié dans les 5 minutes permet d'appairer un autre appareil, qui pourrait alors tenter de deviner le mot de passe hors ligne (R4). | Préférer le QR code, le refermer après usage, et vérifier la liste des téléphones dans les Réglages. |
| R11 | Info | Dans le navigateur, la clé non extractible est quand même présente dans le **profil du navigateur** sur le disque de l'ordinateur. | Se déconnecter sur un ordinateur partagé. |
| R12 | Info | Si la Freebox n'accepte que SMB 1, le partage utilise un protocole ancien (les fichiers restent chiffrés). | Activer SMB 2 ou 3 si possible ; `install.sh` le signale. |
| R13 | Faible | **Usurpation à la reconstruction.** Qui peut écrire sur le partage peut remplacer le `compte.json` d'un utilisateur (l'administrateur, par exemple) par ses propres clés, sous le même nom : la liste affichée par `rebuild` semble normale, il reprend le compte avant son propriétaire et en obtient les droits. Les photos du vrai compte restent illisibles pour lui (clés de fichiers chiffrées par la vraie clé maître). | Aucun import n'est scellé pour un compte pas encore repris. Si la vraie clé de secours est refusée après une reconstruction, l'app dit que le disque a pu être modifié : ne pas insister, réinstaller et vérifier le partage. Reprendre les comptes juste après la reconstruction. |
| R14 | Faible | Dans l'app, l'interface vient de l'APK, mais ses requêtes `/api/` ne sont épinglées que par `onReceivedSslError`, qui n'est pas appelé quand le certificat présenté est **valide** (autorité reconnue). Un attaquant placé entre le téléphone et le serveur, avec un vrai certificat pour l'adresse utilisée, ne serait pas détecté : il ne recevrait que des données chiffrées et des requêtes qu'il ne peut ni rejouer (TLS mutuel) ni déchiffrer, et ne peut pas obtenir la clé maître (elle n'est donnée qu'à la page embarquée). Négligeable avec une adresse IP de la maison ; **à revoir si le serveur est un jour joint par un nom doté d'un vrai certificat** (`*.ts.net` de Tailscale, DDNS + Let's Encrypt). | Accès par l'IP locale ou Tailscale sans certificat public. |
| R15 | Faible | Android 8 (API 26-27) ne permet pas de séparer le profil WebView : un service worker installé par un serveur **déjà compromis avant la 0.5** pourrait survivre jusqu'au nettoyage des données web à la mise à jour (asynchrone). Android 9 et plus : profil neuf, sans risque. | Mettre à jour l'app sur un serveur sain. |
| R16 | Moyenne | **Fichiers piégés dans `Import/`.** Quiconque peut écrire sur le partage y dépose des fichiers que le serveur ouvre avec libvips et ffmpeg, qui choisissent leur décodeur **d'après le contenu**. Un faux `.jpg` (ImageMagick, SVG…) ou une playlist HLS pourrait faire lire un fichier de la VM (base, clé TLS) et le livrer, chiffré, au compte de l'attaquant. | Corrigé (0.5.1) : signature du fichier contrôlée selon l'extension (sinon mis de côté dans `Import/<nom>/.refusés/`, signalé dans l'app) ; `VIPS_BLOCK_UNTRUSTED=1` (pas de décodeur ImageMagick, Matlab, FITS…) ; `-format_whitelist` pour ffmpeg et ffprobe (MP4/MOV, MKV/WebM, AVI, MPEG-TS seulement). Un fichier qui fait planter le serveur deux fois de suite est mis de côté au lieu d'être retenté en boucle. Testé en CI avec les vrais outils. Reste la surface des décodeurs autorisés (JPEG, HEIF, TIFF, H.264…), service durci par systemd. Le contrôle de signature peut être contourné en réécrivant le fichier sur le partage pendant l'import (le fichier est relu plusieurs fois) : les deux autres protections, elles, ne dépendent pas du contenu. Copier le fichier sur le disque de la VM avant de l'ouvrir fermerait ce trou, au prix d'une copie de chaque vidéo sur un petit disque : non fait. |

### Points vérifiés sans problème

- **Isolation entre comptes** : chaque requête filtre par propriétaire (photos, albums, partages, appareils, réécriture des clés). C'est testé.
- **Injection SQL** : toutes les requêtes sont paramétrées ; les seuls fragments dynamiques sont des constantes.
- **Chemins** : les fichiers sont nommés par identifiant numérique. Les noms d'utilisateur sont filtrés (`/ \ : * ? " < > |`, pas de nom commençant par `.`).
- **Injection HTML** : toute donnée affichée passe par `esc()`. La CSP interdit tout script en ligne.
- **TLS mutuel** : Go vérifie la preuve de possession de la clé du téléphone (`CertificateVerify`) même sans chaîne. L'appareil est reconnu à l'empreinte exacte de son certificat, vérifiée à chaque requête : la révocation est immédiate.
- **Nonces** : ils sont aléatoires (96 bits) pour les petites boîtes, à des volumes très inférieurs à la limite ; pour les flux, ils sont dérivés du contenu, sans réutilisation sur des clairs différents.
- **Envoi reprenable** : la position est vérifiée côté serveur, et la taille de chaque morceau et des miniatures est bornée.
- **Durcissement du service** (`install.sh`) : utilisateur dédié, `ProtectSystem=strict`, écriture limitée à la base et aux photos, pas de capacités, mémoire bornée.
