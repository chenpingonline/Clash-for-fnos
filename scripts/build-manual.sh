#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/fpk"
CORE_RES="$ROOT/resources/core"
OUT="$ROOT/dist"
TARGET="${1:-}"

usage() {
  echo "Usage: $0 <x86|arm>" >&2
  echo "  x86 -> fnOS x86 package, bundled linux/amd64 Mihomo" >&2
  echo "  arm -> fnOS ARM package, bundled linux/arm64 Mihomo" >&2
}

case "$TARGET" in
  x86|amd64|x86_64)
    PLATFORM="x86"
    CORE_DIR="$CORE_RES/x86"
    CORE_ASSET_GLOB='mihomo-linux-amd64-*.gz'
    ;;
  arm|arm64|aarch64)
    PLATFORM="arm"
    CORE_DIR="$CORE_RES/arm"
    CORE_ASSET_GLOB='mihomo-linux-arm64-*.gz'
    ;;
  *)
    usage
    exit 2
    ;;
esac

[ -d "$CORE_DIR" ] || { echo "Missing core resources: $CORE_DIR" >&2; exit 1; }
for f in EXPECTED_ASSET.txt THIRD_PARTY_NOTICES.txt bundled-core.json; do
  [ -f "$CORE_DIR/$f" ] || { echo "Missing $CORE_DIR/$f" >&2; exit 1; }
done

shopt -s nullglob
CORE_ASSETS=("$CORE_DIR"/$CORE_ASSET_GLOB)
shopt -u nullglob
[ "${#CORE_ASSETS[@]}" -eq 1 ] || {
  echo "Expected exactly one Mihomo asset in $CORE_DIR matching $CORE_ASSET_GLOB" >&2
  exit 1
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
STAGE="$WORK/stage"
PKG="$WORK/pkg"
mkdir -p "$OUT" "$STAGE" "$PKG"

# Stage common source. Only this staged copy is modified.
cp -a "$SRC/." "$STAGE/"

# Select exactly one architecture-specific Mihomo Core.
rm -f "$STAGE/app/core"/mihomo-linux-*.gz \
      "$STAGE/app/core/EXPECTED_ASSET.txt" \
      "$STAGE/app/core/THIRD_PARTY_NOTICES.txt" \
      "$STAGE/app/core/bundled-core.json"
cp "$CORE_DIR/EXPECTED_ASSET.txt" "$STAGE/app/core/"
cp "$CORE_DIR/THIRD_PARTY_NOTICES.txt" "$STAGE/app/core/"
cp "$CORE_DIR/bundled-core.json" "$STAGE/app/core/"
cp "${CORE_ASSETS[0]}" "$STAGE/app/core/"

# Patch architecture only in the staged manifest.
sed -i -E "s/^platform[[:space:]]*=.*/platform        = ${PLATFORM}/" "$STAGE/manifest"

VERSION="$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/,"",$2);print $2;exit}' "$STAGE/manifest")"
[ -n "$VERSION" ] || { echo "manifest version missing" >&2; exit 1; }

# app.tgz is the contents of app/, not the app directory itself.
tar -C "$STAGE/app" -czf "$PKG/app.tgz" .
CHECKSUM="$(md5sum "$PKG/app.tgz" | awk '{print $1}')"

cp -a "$STAGE/cmd" "$STAGE/config" "$STAGE/wizard" "$PKG/"
cp "$STAGE/manifest" "$PKG/manifest"
cp "$STAGE/ICON.PNG" "$STAGE/ICON_256.PNG" "$PKG/"
sed -i -E "s/^checksum.*/checksum        = ${CHECKSUM}/" "$PKG/manifest"

chmod 755 "$PKG/cmd" "$PKG/config" "$PKG/wizard"
chmod 755 "$PKG/cmd/"*

NAME="Clash for fnos_${VERSION}_${PLATFORM}.fpk"
tar -C "$PKG" -czf "$OUT/$NAME" .
(
  cd "$OUT"
  sha256sum "$NAME" > "$NAME.sha256"
)

echo "$OUT/$NAME"
