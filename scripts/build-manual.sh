#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/fpk"
CORE_RES="$ROOT/resources/core"
OUT="$ROOT/dist"
TARGET="${1:-}"

usage() {
  echo "Usage: $0 <x86|arm|all>" >&2
  echo "  x86 -> fnOS x86 package, bundled linux/amd64 Mihomo" >&2
  echo "  arm -> fnOS ARM package, bundled linux/arm64 Mihomo" >&2
  echo "  all -> fnOS x86 + ARM package, no bundled Mihomo; downloads by runtime architecture" >&2
}

BUNDLE_CORE=true
case "$TARGET" in
  x86|amd64|x86_64)
    PLATFORM="x86"
    PACKAGE_ARCH="x86_64"
    CORE_DIR="$CORE_RES/x86"
    CORE_ASSET_GLOB='mihomo-linux-amd64-*.gz'
    ;;
  arm|arm64|aarch64)
    PLATFORM="arm"
    PACKAGE_ARCH="arm64"
    CORE_DIR="$CORE_RES/arm"
    CORE_ASSET_GLOB='mihomo-linux-arm64-*.gz'
    ;;
  all|universal)
    PLATFORM="all"
    PACKAGE_ARCH="all"
    BUNDLE_CORE=false
    CORE_DIR=""
    CORE_ASSET_GLOB=""
    ;;
  *)
    usage
    exit 2
    ;;
esac

CORE_ASSETS=()
if [ "$BUNDLE_CORE" = true ]; then
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
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
STAGE="$WORK/stage"
PKG="$WORK/pkg"
mkdir -p "$OUT" "$STAGE" "$PKG"

# Stage common source. Only this staged copy is modified.
cp -a "$SRC/." "$STAGE/"
"$ROOT/scripts/sync-version.sh" "$STAGE"

# Development-only type tooling and tests are not runtime dependencies. Keep
# local node_modules and test sources out of the FPK even when building from a
# developer checkout after npm install.
rm -rf "$STAGE/app/server/node_modules" "$STAGE/app/server/test"
rm -f "$STAGE/app/server/tsconfig.json" "$STAGE/app/server/tsconfig.strict.json" "$STAGE/app/server/package-lock.json"

# Select one architecture-specific Core, or mark the all package for online delivery.
rm -f "$STAGE/app/core"/mihomo-linux-*.gz \
      "$STAGE/app/core/EXPECTED_ASSET.txt" \
      "$STAGE/app/core/THIRD_PARTY_NOTICES.txt" \
      "$STAGE/app/core/bundled-core.json" \
      "$STAGE/app/core/online-core.json"
if [ "$BUNDLE_CORE" = true ]; then
  cp "$CORE_DIR/EXPECTED_ASSET.txt" "$STAGE/app/core/"
  cp "$CORE_DIR/THIRD_PARTY_NOTICES.txt" "$STAGE/app/core/"
  cp "$CORE_DIR/bundled-core.json" "$STAGE/app/core/"
  cp "${CORE_ASSETS[0]}" "$STAGE/app/core/"
else
  printf '%s\n' '{"mode":"online","source":"MetaCubeX/mihomo GitHub Releases"}' > "$STAGE/app/core/online-core.json"
fi

# Patch architecture only in the staged manifest.
sed -E "s/^platform[[:space:]]*=.*/platform        = ${PLATFORM}/" "$STAGE/manifest" > "$WORK/manifest"
cp "$WORK/manifest" "$STAGE/manifest"

VERSION="$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/,"",$2);print $2;exit}' "$STAGE/manifest")"
[ -n "$VERSION" ] || { echo "manifest version missing" >&2; exit 1; }

# app.tgz is the contents of app/, not the app directory itself.
tar -C "$STAGE/app" -czf "$PKG/app.tgz" .
CHECKSUM="$(md5sum "$PKG/app.tgz" | awk '{print $1}')"

cp -a "$STAGE/cmd" "$STAGE/config" "$STAGE/wizard" "$PKG/"
cp "$STAGE/manifest" "$PKG/manifest"
cp "$STAGE/ICON.PNG" "$STAGE/ICON_256.PNG" "$PKG/"
sed -E "s/^checksum.*/checksum        = ${CHECKSUM}/" "$PKG/manifest" > "$WORK/manifest"
cp "$WORK/manifest" "$PKG/manifest"

chmod 755 "$PKG/cmd" "$PKG/config" "$PKG/wizard"
chmod 755 "$PKG/cmd/"*

NAME="Clash for fnos_${VERSION}_${PACKAGE_ARCH}.fpk"
tar -C "$PKG" -czf "$OUT/$NAME" .
(
  cd "$OUT"
  sha256sum "$NAME" > "$NAME.sha256"
)

echo "$OUT/$NAME"
