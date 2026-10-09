#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for f in "$ROOT/fpk/cmd/"* "$ROOT/scripts/"*.sh; do bash -n "$f"; done
python3 "$ROOT/scripts/test-upstream.py"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
"$ROOT/scripts/prepare-shared.sh" "$WORK/shared"
(cd "$WORK/shared/backend" && go test -race ./... && go vet ./...)
VITE_APP_BASE=/app/clash-for-fnos/ npm --prefix "$WORK/shared/web" run check
