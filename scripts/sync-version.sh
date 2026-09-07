#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# A staging directory may be supplied by the packager. Source builds use fpk/.
FPK_DIR="${1:-$ROOT/fpk}"
APP_RELEASE_VERSION="$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/,"",$2);print $2;exit}' "$FPK_DIR/manifest")"
if [[ ! "$APP_RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Invalid version in $FPK_DIR/manifest: expected major.minor.patch" >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
# Replace root package metadata only; dependency versions must never change.
PACKAGE_DIRS=("$FPK_DIR/app/server")
if [ "$FPK_DIR" = "$ROOT/fpk" ]; then PACKAGE_DIRS+=("$ROOT/web"); fi
index=0
for package_dir in "${PACKAGE_DIRS[@]}"; do
  for name in package.json package-lock.json; do
    file="$package_dir/$name"
    [ -f "$file" ] || continue
    output="$WORK/${index}-${name}"
    awk -v version="$APP_RELEASE_VERSION" '
      /^    "": \{/ { rootPackage = 1 }
      /^    \},?$/ { rootPackage = 0 }
      /^  "version":/ || (rootPackage && /^      "version":/) {
        sub(/"version": "[^"]*"/, "\"version\": \"" version "\"")
      }
      { print }
    ' "$file" > "$output"
    if ! cmp -s "$file" "$output"; then cp "$output" "$file"; fi
  done
  index=$((index + 1))
done
