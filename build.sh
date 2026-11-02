#!/usr/bin/env bash
# Construit l'archive à envoyer : dist/pellicule-<version>.tar.gz (binaires Linux ARM64 + x86_64, script d'installation).
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-$(date +%Y.%m.%d)}"
OUT="dist/pellicule-$VERSION"
rm -rf "$OUT"
mkdir -p "$OUT"
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$OUT/pellicule-linux-$arch" .
done
cp deploy/install.sh README.md "$OUT/"
sed "s/{{VERSION}}/$VERSION/g" deploy/INSTALLATION.md > "$OUT/INSTALLATION.md"
# app Android (compilée à part : cd android && ./gradlew assembleRelease -PappVersion=<version>)
APK=android/app/build/outputs/apk/release/app-release.apk
if [ -f "$APK" ]; then cp "$APK" "$OUT/pellicule-android-$VERSION.apk"; fi
chmod +x "$OUT/install.sh" "$OUT"/pellicule-linux-*
tar -C dist -czf "dist/pellicule-$VERSION.tar.gz" "pellicule-$VERSION"
ls -lh "dist/pellicule-$VERSION.tar.gz"
