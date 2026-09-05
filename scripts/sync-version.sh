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
for name in package.json package-lock.json; do
  file="$FPK_DIR/app/server/$name"
  [ -f "$file" ] || continue
  awk -v version="$APP_RELEASE_VERSION" '
    /^    "": \{/ { rootPackage = 1 }
    /^    \},?$/ { rootPackage = 0 }
    /^  "version":/ || (rootPackage && /^      "version":/) {
      sub(/"version": "[^"]*"/, "\"version\": \"" version "\"")
    }
    { print }
  ' "$file" > "$WORK/$name"
  if ! cmp -s "$file" "$WORK/$name"; then cat "$WORK/$name" > "$file"; fi
done

file="$FPK_DIR/app/server/public/index.html"
sed -E "s/([?]v=)[0-9]+\.[0-9]+\.[0-9]+/\1${APP_RELEASE_VERSION}/g" "$file" > "$WORK/index.html"
if ! cmp -s "$file" "$WORK/index.html"; then cat "$WORK/index.html" > "$file"; fi
