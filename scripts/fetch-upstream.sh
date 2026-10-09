#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
LOCK="${CLASH_UPSTREAM_LOCK:-$ROOT/upstream.lock}"
read -r repository shared_version commit <<< "$(python3 "$ROOT/scripts/read-upstream.py" "$LOCK")"
[ -n "${commit:-}" ] || { echo 'Invalid upstream lock' >&2; exit 1; }
CACHE="${CLASH_UPSTREAM_CACHE:-$ROOT/.cache/upstream.git}"
mkdir -p "$(dirname "$CACHE")"
if [ ! -d "$CACHE" ]; then git init --bare -q "$CACHE"; fi
if [ -n "${CLASH_SHARED_SOURCE:-}" ]; then
  [ "$(git -C "$CLASH_SHARED_SOURCE" rev-parse HEAD)" = "$commit" ] || { echo 'Local shared source does not match pinned commit' >&2; exit 1; }
  git -C "$CLASH_SHARED_SOURCE" diff --quiet HEAD -- || { echo 'Local shared source has uncommitted changes' >&2; exit 1; }
  [ -z "$(git -C "$CLASH_SHARED_SOURCE" ls-files --others --exclude-standard)" ] || { echo 'Local shared source has untracked files' >&2; exit 1; }
  git --git-dir="$CACHE" fetch -q --depth=1 "$CLASH_SHARED_SOURCE" "$commit"
elif ! git --git-dir="$CACHE" cat-file -e "$commit^{commit}" 2>/dev/null; then
  [ "${CLASH_OFFLINE:-0}" != 1 ] || { echo 'Pinned source is not cached; an online fetch is required' >&2; exit 1; }
  git --git-dir="$CACHE" fetch -q --depth=1 "$repository" "$commit"
fi
[ "$(git --git-dir="$CACHE" rev-parse "$commit^{commit}")" = "$commit" ] || { echo 'Fetched commit mismatch' >&2; exit 1; }
[ "$(git --git-dir="$CACHE" show "$commit:VERSION")" = "$shared_version" ] || { echo 'Pinned version does not match shared VERSION' >&2; exit 1; }
printf '%s\n' "$CACHE"
