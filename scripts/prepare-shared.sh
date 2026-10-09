#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DESTINATION="${1:?Usage: prepare-shared.sh <new build directory>}"
[ ! -e "$DESTINATION" ] || { echo 'Shared build directory already exists' >&2; exit 1; }
"$ROOT/scripts/sync-version.sh"
read -r repository shared_version commit <<< "$(python3 "$ROOT/scripts/read-upstream.py" "${CLASH_UPSTREAM_LOCK:-$ROOT/upstream.lock}")"
CACHE="$("$ROOT/scripts/fetch-upstream.sh")"
mkdir -p "$DESTINATION"
git --git-dir="$CACHE" archive "$commit" | COPYFILE_DISABLE=1 tar -xf - -C "$DESTINATION"
# Build in an isolated export. Neither repository's shared source is rewritten.
package_version="$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/,"",$2);print $2;exit}' "$ROOT/fpk/manifest")"
printf '%s\n' "$package_version" > "$DESTINATION/VERSION"
cp "$ROOT/CHANGELOG.md" "$DESTINATION/CHANGELOG.md"
mkdir -p "$DESTINATION/web/public/icons"
cp "$ROOT/fpk/app/ui/images/icons/"*.png "$DESTINATION/web/public/icons/"
cp "$ROOT/fpk/app/ui/images/icon_64.png" "$DESTINATION/web/public/icon-current.png"
cp "$ROOT/fpk/app/ui/images/icon_256.png" "$DESTINATION/web/public/icon-current-256.png"
"$DESTINATION/scripts/sync-version.sh"
npm --prefix "$DESTINATION/web" ci --no-audit --no-fund
