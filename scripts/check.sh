#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
python3 "$ROOT/scripts/test-security-toolchains.py"
python3 "$ROOT/scripts/security-toolchains.py"
for f in "$ROOT/fpk/cmd/"* "$ROOT/scripts/"*.sh; do bash -n "$f"; done
python3 "$ROOT/scripts/test-lifecycle.py"
python3 "$ROOT/scripts/test-upstream.py"
python3 "$ROOT/scripts/test-update-source.py"
python3 "$ROOT/scripts/read-update-source.py" "$ROOT/update-source.json" >/dev/null
WORK="$(mktemp -d)"
# npm 11 resolves /var -> /private/var as a linked package; use one physical path.
WORK="$(cd "$WORK" && pwd -P)"
trap 'rm -rf "$WORK"' EXIT
"$ROOT/scripts/prepare-shared.sh" "$WORK/shared"
(cd "$WORK/shared/backend" && go test -race ./... && go vet ./...)
VITE_APP_BASE=/app/clash-for-fnos/ npm --prefix "$WORK/shared/web" run check
