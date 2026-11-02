#!/usr/bin/env bash
# Installe (ou met à jour) Pellicule sur une VM Debian/Ubuntu, par exemple une VM Freebox.
# Usage : sudo ./install.sh        (depuis le dossier décompressé)
set -euo pipefail

PORT="${PORT:-8420}"
MNT=/mnt/freebox
UNIT=/etc/systemd/system/pellicule.service

say()  { printf '\n\033[1;33m==> %s\033[0m\n' "$*"; }
die()  { printf '\n\033[1;31mErreur : %s\033[0m\n' "$*" >&2; exit 1; }
ask()  { local r; read -rp "$1 [$2] : " r; echo "${r:-$2}"; }
confirm() { local r; read -rp "$1 [O/n] : " r; [[ ! "$r" =~ ^[nN] ]]; }

[ "$(id -u)" = 0 ] || die "lance ce script avec sudo : sudo ./install.sh"
command -v apt-get >/dev/null || die "ce script est prévu pour Debian ou Ubuntu"
command -v systemctl >/dev/null || die "systemd est nécessaire"

case "$(uname -m)" in
  aarch64|arm64) BIN=pellicule-linux-arm64 ;;
  x86_64|amd64)  BIN=pellicule-linux-amd64 ;;
  *) die "processeur $(uname -m) non pris en charge" ;;
esac
DIR="$(cd "$(dirname "$0")" && pwd)"
[ -f "$DIR/$BIN" ] || die "$BIN introuvable à côté du script"

# --- mise à jour d'une installation existante ---
if [ -f "$UNIT" ] && confirm "Pellicule est déjà installé. Mettre à jour le programme seulement ?"; then
  say "Mise à jour"
  systemctl stop pellicule
  install -m 755 "$DIR/$BIN" /usr/local/bin/pellicule
  systemctl start pellicule
  sleep 2
  systemctl is-active --quiet pellicule && echo "OK, Pellicule est à jour et tourne." \
    || { journalctl -u pellicule -n 20 --no-pager; die "le service ne démarre pas"; }
  exit 0
fi

say "1/5 Paquets (libvips pour les miniatures et le HEIC, ffmpeg pour les vidéos)"
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends libvips-tools ffmpeg cifs-utils ca-certificates

say "2/5 Utilisateur système « pellicule »"
id pellicule >/dev/null 2>&1 || useradd --system --home-dir /var/lib/pellicule --shell /usr/sbin/nologin pellicule
install -d -o pellicule -g pellicule -m 750 /var/lib/pellicule
install -m 755 "$DIR/$BIN" /usr/local/bin/pellicule

say "3/5 Stockage des photos"
SMB=0
if confirm "Stocker les photos sur le disque dur de la Freebox (recommandé) ?"; then
  SMB=1
  echo
  echo "Il faut que le partage Windows soit activé dans Freebox OS :"
  echo "  Paramètres de la Freebox → Partages Windows → activer, et définir un utilisateur + mot de passe."
  echo
  HOST="$(ask 'Adresse de la Freebox' 'mafreebox.freebox.fr')"
  SHARE="$(ask 'Nom du partage (tel que vu depuis un PC : \\mafreebox.freebox.fr\<nom>)' 'Freebox')"
  SMBUSER="$(ask 'Utilisateur du partage' 'freebox')"
  read -rsp "Mot de passe du partage : " SMBPASS; echo

  install -d -m 700 /etc/pellicule
  umask 077
  printf 'username=%s\npassword=%s\n' "$SMBUSER" "$SMBPASS" > /etc/pellicule/smb.cred
  umask 022
  mkdir -p "$MNT"
  mountpoint -q "$MNT" && umount "$MNT"

  OPTS="credentials=/etc/pellicule/smb.cred,uid=pellicule,gid=pellicule,file_mode=0640,dir_mode=0750,iocharset=utf8"
  VERS=""
  for v in 3.0 2.1 2.0 1.0; do
    if mount -t cifs "//$HOST/$SHARE" "$MNT" -o "$OPTS,vers=$v" 2>/dev/null; then VERS=$v; break; fi
  done
  [ -n "$VERS" ] || die "impossible de monter //$HOST/$SHARE. Vérifie que le partage Windows est activé, le nom du partage, l'utilisateur et le mot de passe."
  echo "Partage monté (SMB $VERS)."

  # montage automatique au démarrage (les espaces s'écrivent \040 dans fstab)
  sed -i "\# $MNT cifs #d" /etc/fstab
  ESC_SHARE="$(printf '%s' "$SHARE" | sed 's/ /\\040/g')"
  echo "//$HOST/$ESC_SHARE $MNT cifs $OPTS,vers=$VERS,_netdev,nofail 0 0" >> /etc/fstab
  systemctl daemon-reload

  SUB="$(ask 'Dossier des photos sur le disque' 'Pellicule')"
  LIB="$MNT/$SUB"
else
  LIB=/var/lib/pellicule/library
  echo "Les photos seront stockées sur le disque de la VM : $LIB"
fi
mkdir -p "$LIB"
touch "$LIB/.pellicule-library"
[ "$SMB" = 1 ] || chown -R pellicule:pellicule "$LIB"
runuser -u pellicule -- test -w "$LIB" || die "l'utilisateur pellicule ne peut pas écrire dans $LIB"

say "4/5 Service systemd"
cat > "$UNIT" <<EOF
[Unit]
Description=Pellicule (photos)
After=network-online.target remote-fs.target
Wants=network-online.target
$( [ "$SMB" = 1 ] && echo "RequiresMountsFor=$MNT" )

[Service]
User=pellicule
Group=pellicule
Environment=PELLICULE_ADDR=:$PORT
Environment=PELLICULE_DATA=/var/lib/pellicule
Environment="PELLICULE_LIBRARY=$LIB"
Environment=PELLICULE_REQUIRE_MARKER=1
Environment=GOMEMLIMIT=200MiB
Environment=TZ=Europe/Paris
ExecStart=/usr/local/bin/pellicule
Restart=always
RestartSec=15
Nice=5
MemoryMax=450M
NoNewPrivileges=yes
ProtectSystem=full
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now pellicule
sleep 3
if ! systemctl is-active --quiet pellicule; then
  journalctl -u pellicule -n 30 --no-pager
  die "le service ne démarre pas (voir les lignes ci-dessus)"
fi

IP="$(hostname -I | awk '{print $1}')"
say "5/5 Terminé !"
cat <<EOF

  Ouvre  http://$IP:$PORT  depuis un téléphone ou un PC de la maison,
  puis crée le compte administrateur.

  Photos :       $LIB
  Base :         /var/lib/pellicule/pellicule.db  (à sauvegarder de temps en temps)
  Journaux :     journalctl -u pellicule -f
  Redémarrer :   systemctl restart pellicule

EOF
