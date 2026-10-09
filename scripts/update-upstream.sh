#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
commit="${1:-}"
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || { echo 'Usage: update-upstream.sh <full lowercase commit SHA>' >&2; exit 2; }
read -r repository current_version current_commit <<< "$(python3 "$ROOT/scripts/read-upstream.py" "$ROOT/upstream.lock")"
CACHE="${CLASH_UPSTREAM_CACHE:-$ROOT/.cache/upstream.git}"
mkdir -p "$(dirname "$CACHE")"
if [ ! -d "$CACHE" ]; then git init --bare -q "$CACHE"; fi
git --git-dir="$CACHE" fetch -q --depth=1 "$repository" "$commit"
shared_version="$(git --git-dir="$CACHE" show "$commit:VERSION")"
[[ "$shared_version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid shared VERSION' >&2; exit 1; }
python3 - "$ROOT/upstream.lock" "$repository" "$shared_version" "$commit" <<'PYTHON'
import json, sys
from pathlib import Path
path=Path(sys.argv[1])
data=dict(schema=1, repository=sys.argv[2], version=sys.argv[3], commit=sys.argv[4])
temporary=path.with_suffix('.lock.tmp')
temporary.write_text(json.dumps(data, indent=2) + '\n')
temporary.replace(path)
PYTHON
printf 'Pinned Clash Manager %s at %s; run ./scripts/check.sh before committing.\n' "$shared_version" "$commit"
