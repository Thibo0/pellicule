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

# adresse IPv4 de la VM sur le réseau local (celle du QR code) ; hostname -I peut commencer par une IPv6
lan_ip() {
  local ip
  ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "src") { print $(i + 1); exit }}')"
  [ -n "$ip" ] || ip="$(hostname -I | tr ' ' '\n' | grep -E '^[0-9]+(\.[0-9]+){3}$' | head -n 1)"
  [ -n "$ip" ] || die "adresse IPv4 de la VM introuvable"
  echo "$ip"
}

install_packages() {
  apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends libvips-tools ffmpeg cifs-utils ca-certificates
  # VIPS_BLOCK_UNTRUSTED (blocage des décodeurs non sûrs, R16) n'existe qu'à partir de libvips 8.13
  local v
  v="$(vips --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+' | head -n 1 || true)"
  if [ -z "$v" ] || [ "$(printf '%s\n%s\n' "$v" 8.13 | sort -V | head -n 1)" != 8.13 ]; then
    echo
    echo "Attention : libvips ${v:-?} est trop ancien (8.13 minimum, Debian 12 ou Ubuntu 24.04)."
    echo "Pellicule ne l'utilisera pas : miniatures JPEG/PNG/WebP seulement, pas de HEIC."
  fi
}

# write_unit : le service systemd (réécrit aussi à chaque mise à jour, pour que les durcissements suivent)
write_unit() {
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
UMask=0077
# confinement : le service n'écrit que dans sa base et dans le dossier des photos
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/var/lib/pellicule "$LIB"
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
SystemCallArchitectures=native
CapabilityBoundingSet=

[Install]
WantedBy=multi-user.target
EOF
}

# --- mise à jour d'une installation existante ---
if [ -f "$UNIT" ] && confirm "Pellicule est déjà installé. Mettre à jour le programme seulement ?"; then
  say "Mise à jour"
  # réglages de l'installation existante, relus dans le service actuel
  LIB="$(grep -m 1 'PELLICULE_LIBRARY=' "$UNIT" | sed 's/.*PELLICULE_LIBRARY=//; s/"$//')"
  OLDPORT="$(grep -m 1 'PELLICULE_ADDR=' "$UNIT" | sed 's/.*://; s/"$//')"
  PORT="${OLDPORT:-$PORT}"
  SMB=0; grep -q '^RequiresMountsFor=' "$UNIT" && SMB=1
  [ -n "$LIB" ] || die "dossier des photos introuvable dans $UNIT"
  install_packages
  systemctl stop pellicule
  install -m 755 "$DIR/$BIN" /usr/local/bin/pellicule
  write_unit
  systemctl daemon-reload
  systemctl start pellicule
  sleep 2
  systemctl is-active --quiet pellicule && echo "OK, Pellicule est à jour et tourne." \
    || { journalctl -u pellicule -n 20 --no-pager; die "le service ne démarre pas"; }
  # compte pas encore créé : on réaffiche le QR code d'installation
  runuser -u pellicule -- /usr/local/bin/pellicule -data /var/lib/pellicule setup-qr "https://$(lan_ip):$PORT" || true
  exit 0
fi

say "1/5 Paquets (libvips et ffmpeg : miniatures des fichiers déposés par FolderSync)"
install_packages

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
  # le plus sûr d'abord : SMB 3 chiffré (seal), puis SMB 3, 2.1, 2.0 ; SMB 1 seulement si on l'accepte
  VERS=""
  for v in 3.0,seal 3.0 2.1 2.0; do
    if mount -t cifs "//$HOST/$SHARE" "$MNT" -o "$OPTS,vers=$v" 2>/dev/null; then VERS=$v; break; fi
  done
  if [ -z "$VERS" ] && mount -t cifs "//$HOST/$SHARE" "$MNT" -o "$OPTS,vers=1.0" 2>/dev/null; then
    umount "$MNT"
    echo
    echo "Le partage ne fonctionne qu'avec SMB 1, un vieux protocole peu sûr (les photos restent chiffrées)."
    echo "Si Freebox OS propose SMB 2 ou 3, active-le et relance plutôt l'installation."
    if confirm "Utiliser SMB 1 quand même ?"; then
      mount -t cifs "//$HOST/$SHARE" "$MNT" -o "$OPTS,vers=1.0" && VERS=1.0
    else
      die "installation arrêtée : active SMB 2 ou 3 sur la Freebox"
    fi
  fi
  [ -n "$VERS" ] || die "impossible de monter //$HOST/$SHARE. Vérifie que le partage Windows est activé, le nom du partage, l'utilisateur et le mot de passe."
  echo "Partage monté (SMB $VERS)."
  VERS_OPT="vers=$VERS"

  # montage automatique au démarrage (les espaces s'écrivent \040 dans fstab)
  sed -i "\# $MNT cifs #d" /etc/fstab
  ESC_SHARE="$(printf '%s' "$SHARE" | sed 's/ /\\040/g')"
  echo "//$HOST/$ESC_SHARE $MNT cifs $OPTS,${VERS_OPT},_netdev,nofail 0 0" >> /etc/fstab
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

# VM réinstallée (ancienne perdue) : chaque photo a son fichier de clé sur le disque, chaque compte son
# compte.json. « pellicule rebuild » affiche les comptes trouvés et ne reconstruit que si l'on tape RECONSTRUIRE.
if [ ! -f /var/lib/pellicule/pellicule.db ] && ls "$LIB"/u*/compte.json >/dev/null 2>&1; then
  echo
  echo "Ce disque contient déjà des photos Pellicule (la VM a été réinstallée ?)."
  if runuser -u pellicule -- /usr/local/bin/pellicule -data /var/lib/pellicule -library "$LIB" rebuild < /dev/tty; then
    RESTORED=1
  else
    rm -f /var/lib/pellicule/pellicule.db /var/lib/pellicule/pellicule.db-wal /var/lib/pellicule/pellicule.db-shm
    die "reconstruction annulée : rien n'a été modifié sur le disque des photos. Relance sudo ./install.sh pour recommencer."
  fi
fi

say "4/5 Service systemd"
write_unit
systemctl daemon-reload
systemctl enable --now pellicule
sleep 3
if ! systemctl is-active --quiet pellicule; then
  journalctl -u pellicule -n 30 --no-pager
  die "le service ne démarre pas (voir les lignes ci-dessus)"
fi

IP="$(lan_ip)"
P() { runuser -u pellicule -- /usr/local/bin/pellicule -data /var/lib/pellicule "$@"; }
say "5/5 Terminé !"
if [ "${RESTORED:-0}" = 1 ]; then
cat <<EOF

  Pellicule est reconstruit : comptes, photos et albums sont revenus, mais les mots de passe NON.
  Chaque personne reprend son compte avec SA CLÉ DE SECOURS (notée à la création du compte) :
    1. dans l'app : « Appairer de nouveau » (ou « Scanner le QR code ») et vise celui-ci :

EOF
P login-qr "https://$IP:$PORT" || true
cat <<EOF

    2. touche « Mot de passe oublié / serveur réinstallé » ;
    3. saisis ton nom, ta clé de secours, puis un nouveau mot de passe.

  Sans clé de secours, les photos de ce compte restent illisibles : personne ne peut les récupérer.

  Ce QR code se réaffiche avec :  sudo -u pellicule pellicule -data /var/lib/pellicule login-qr https://$IP:$PORT

EOF
exit 0
fi
cat <<EOF

  Il reste une seule chose à faire, sur ton téléphone Android :
    1. installe l'app (fichier pellicule-android-*.apk de ce dossier) ;
    2. ouvre-la, touche « Scanner le QR code » et vise celui-ci :

EOF
P setup-qr "https://$IP:$PORT" || true
cat <<EOF

    3. choisis ton nom et ton mot de passe, puis NOTE LA CLÉ DE SECOURS affichée, sur papier.

  LA RÈGLE : la clé de secours est le seul moyen de retrouver les photos si le mot de passe est oublié
  OU si cette VM est perdue. Personne ne peut la retrouver, pas même l'administrateur. Chaque personne
  invitée reçoit la sienne : dis-leur de la noter.

  C'est tout : les photos du téléphone partent toutes seules, chiffrées, dès qu'il est sur le Wi-Fi.
  Pour ajouter quelqu'un : dans l'app, Réglages → « Inviter quelqu'un ».

  Ce QR code se réaffiche en relançant :  sudo ./install.sh   (puis « O »)
  Photos (chiffrées) : $LIB
  Base : /var/lib/pellicule/pellicule.db (si la VM est perdue : sudo ./install.sh la reconstruit depuis le disque)
  Journaux : journalctl -u pellicule -f

EOF
