#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/fpk"
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
    CORE_SUBDIR=x86
    CORE_ASSET_GLOB='mihomo-linux-amd64-*.gz'
    ;;
  arm|arm64|aarch64)
    PLATFORM="arm"
    PACKAGE_ARCH="arm64"
    CORE_SUBDIR=arm
    CORE_ASSET_GLOB='mihomo-linux-arm64-*.gz'
    ;;
  all|universal)
    PLATFORM="all"
    PACKAGE_ARCH="all"
    BUNDLE_CORE=false
    CORE_SUBDIR=""
    CORE_ASSET_GLOB=""
    ;;
  *)
    usage
    exit 2
    ;;
esac

GO_BIN="$(command -v go || true)"
[ -n "$GO_BIN" ] || { echo 'Missing Go compiler' >&2; exit 1; }
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SHARED="$WORK/shared"
"$ROOT/scripts/prepare-shared.sh" "$SHARED"
WEB="$SHARED/web"
CORE_DIR="$SHARED/resources/core/$CORE_SUBDIR"
CORE_ASSETS=()
if [ "$BUNDLE_CORE" = true ]; then
  for f in EXPECTED_ASSET.txt THIRD_PARTY_NOTICES.txt bundled-core.json; do
    [ -f "$CORE_DIR/$f" ] || { echo "Missing $CORE_DIR/$f" >&2; exit 1; }
  done
  shopt -s nullglob
  CORE_ASSETS=("$CORE_DIR"/$CORE_ASSET_GLOB)
  shopt -u nullglob
  [ "${#CORE_ASSETS[@]}" -eq 1 ] || { echo 'Expected one architecture-specific Mihomo asset' >&2; exit 1; }
fi
STAGE="$WORK/stage"
PKG="$WORK/pkg"
mkdir -p "$OUT" "$STAGE" "$PKG"
VITE_APP_BASE=/app/clash-for-fnos/ npm --prefix "$WEB" run build

# Stage common source. Only this staged copy is modified.
cp -a "$SRC/." "$STAGE/"
"$ROOT/scripts/sync-version.sh" "$STAGE"
rm -rf "$STAGE/app/server"
mkdir -p "$STAGE/app/server/public"
cp -a "$WEB/dist/." "$STAGE/app/server/public/"
mkdir -p "$STAGE/app/geodata" "$STAGE/app/licenses" "$STAGE/app/core"
cp -a "$SHARED/assets/geodata/." "$STAGE/app/geodata/"
cp -a "$SHARED/assets/licenses/." "$STAGE/app/licenses/"
cp "$SHARED/assets/licenses/Mihomo-LICENSE-GPL-3.txt" "$STAGE/app/core/"
python3 - "$ROOT/upstream.lock" "$STAGE/app/build-info.json" "$WEB/dist" "$STAGE/manifest" <<'PYTHON'
import hashlib, json, sys
from pathlib import Path
info=json.loads(Path(sys.argv[1]).read_text())
public=Path(sys.argv[3])
version=next(line.split('=',1)[1].strip() for line in Path(sys.argv[4]).read_text().splitlines() if line.startswith('version'))
info=dict(upstream=info, packageVersion=version, frontend={str(p.relative_to(public)):hashlib.sha256(p.read_bytes()).hexdigest() for p in public.rglob('*') if p.is_file()})
Path(sys.argv[2]).write_text(json.dumps(info, indent=2)+'\n')
PYTHON

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

# The Go gateway is the public fnOS service. Architecture packages contain one
# matching binary; the universal package contains both and selects at runtime.
mkdir -p "$STAGE/app/server/bin"
build_go_web() {
  local goarch="$1" suffix="$2"
  (
    cd "$SHARED/backend"
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" "$GO_BIN" build \
      -trimpath -ldflags "-s -w -X main.version=$VERSION" \
      -o "$STAGE/app/server/bin/clash-for-fnos-web-$suffix" \
      ./cmd/clash-for-fnos-web
    CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" "$GO_BIN" build \
      -trimpath -ldflags "-s -w -X main.version=$VERSION" \
      -o "$STAGE/app/server/bin/clash-for-fnos-helper-$suffix" \
      ./cmd/clash-for-fnos-helper
  )
}
case "$PACKAGE_ARCH" in
  x86_64) build_go_web amd64 x86_64 ;;
  arm64) build_go_web arm64 arm64 ;;
  all)
    build_go_web amd64 x86_64
    build_go_web arm64 arm64
    ;;
esac
chmod 755 "$STAGE/app/server/bin/"*

# app.tgz is the contents of app/, not the app directory itself.
# macOS tar otherwise emits AppleDouble metadata alongside Linux app files.
COPYFILE_DISABLE=1 LC_ALL=C tar -C "$STAGE/app" -czf "$PKG/app.tgz" .
CHECKSUM="$(md5sum "$PKG/app.tgz" | awk '{print $1}')"

cp -a "$STAGE/cmd" "$STAGE/config" "$STAGE/wizard" "$PKG/"
cp "$STAGE/manifest" "$PKG/manifest"
cp "$STAGE/ICON.PNG" "$STAGE/ICON_256.PNG" "$PKG/"
# Keep the license in app/licenses/; a top-level LICENSE adds an agreement
# step to the fnOS installer.
sed -E "s/^checksum.*/checksum        = ${CHECKSUM}/" "$PKG/manifest" > "$WORK/manifest"
cp "$WORK/manifest" "$PKG/manifest"

chmod 755 "$PKG/cmd" "$PKG/config" "$PKG/wizard"
chmod 755 "$PKG/cmd/"*

NAME="Clash for fnos_${VERSION}_${PACKAGE_ARCH}.fpk"
COPYFILE_DISABLE=1 LC_ALL=C tar -C "$PKG" -czf "$OUT/$NAME" .
(
  cd "$OUT"
  sha256sum "$NAME" > "$NAME.sha256"
)

echo "$OUT/$NAME"
