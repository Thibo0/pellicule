# Pellicule {{VERSION}}

Galerie photo auto-hébergée pour la maison : un serveur qui tourne sur une VM (Freebox ou autre) et une app Android qui y sauvegarde les photos automatiquement.

## Quel fichier télécharger ?

| Fichier | Pour quoi faire |
|---|---|
| **`pellicule-{{VERSION}}.tar.gz`** | Archive complète : le serveur, le script d'installation, l'app Android et ce guide. **À prendre pour une première installation.** |
| **`pellicule-android-{{VERSION}}.apk`** | L'app Android seule, à télécharger directement depuis le téléphone. |

---

## 1. Installer le serveur (une seule fois)

Il faut une VM **Debian ou Ubuntu**, par exemple celle d'AdGuard sur la Freebox.

1. Dans **Freebox OS → Paramètres de la Freebox → Partages Windows**, activer le partage, puis choisir un utilisateur et un mot de passe. C'est là que les photos seront stockées.
2. Copier l'archive sur la VM. Depuis un PC :
   ```bash
   scp pellicule-{{VERSION}}.tar.gz utilisateur@IP-de-la-VM:
   ```
3. Sur la VM :
   ```bash
   tar xzf pellicule-{{VERSION}}.tar.gz
   cd pellicule-{{VERSION}}
   sudo ./install.sh
   ```
   Le script pose quelques questions (adresse de la Freebox, nom du partage, utilisateur, mot de passe). À la fin, il affiche l'adresse du serveur, par exemple `http://192.168.1.50:8420`.
4. Ouvrir cette adresse dans un navigateur et créer le **compte administrateur**. Les autres comptes (famille…) se créent dans **Réglages**.

## 2. Installer l'app Android

1. Sur le téléphone, télécharger **`pellicule-android-{{VERSION}}.apk`**. Il est aussi dans l'archive.
2. L'ouvrir. Android demande d'**autoriser l'installation d'applis inconnues** : accepter pour le navigateur ou le gestionnaire de fichiers.
3. Au premier lancement, saisir l'adresse du serveur (ex. `192.168.1.50:8420`), l'utilisateur et le mot de passe, puis autoriser l'accès à **toutes** les photos.

Les photos se sauvegardent ensuite toutes seules en Wi-Fi à la maison, sans jamais être envoyées deux fois.

**iPhone :** il n'y a pas d'app. Ouvrir l'adresse dans Safari → Partager → *Sur l'écran d'accueil*, puis *Sauvegarde* → *Choisir des photos*.

---

## Mettre à jour

- **Serveur :** copier la nouvelle archive sur la VM, la décompresser, lancer `sudo ./install.sh` et répondre **O** à « Mettre à jour ». Les photos et les comptes sont conservés.
- **App Android :** installer le nouvel APK par-dessus l'ancien. Il n'y a rien à désinstaller.

## En cas de problème

```bash
systemctl status pellicule      # le service tourne-t-il ?
journalctl -u pellicule -f      # journaux en direct
sudo mount -a                   # remonter le disque de la Freebox (après un redémarrage)
```

Le guide complet (accès depuis l'extérieur, nom `photos.maison`, FolderSync…) est dans le `README.md` de l'archive.
