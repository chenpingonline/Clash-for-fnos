#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FPK_DIR="${1:-$ROOT/fpk}"
APP_RELEASE_VERSION="$(awk -F= '/^version[[:space:]]*=/{gsub(/[[:space:]]/,"",$2);print $2;exit}' "$FPK_DIR/manifest")"
[[ "$APP_RELEASE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid FPK manifest version' >&2; exit 1; }
