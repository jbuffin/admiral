#!/bin/sh
# Re-vendors fixtures/crew-resolution/ from a local jbuffin/skills checkout, keeping symlinks,
# and records the checkout's short commit in crew-resolution/SOURCE.
# Usage: internal/crew/testdata/sync.sh <path to the skills checkout>
set -eu

[ $# -eq 1 ] || { echo "usage: $0 <path to the skills checkout>" >&2; exit 2; }
from="$1/fixtures/crew-resolution"
[ -d "$from" ] || { echo "$from not found" >&2; exit 1; }
if [ -n "$(git -C "$1" status --porcelain -- fixtures/crew-resolution)" ]; then
	echo "the fixtures in $1 have uncommitted changes; the recorded commit would be wrong" >&2
	exit 1
fi
sha=$(git -C "$1" rev-parse --short HEAD)

to="$(cd "$(dirname "$0")" && pwd)/crew-resolution"
rm -rf "$to"
cp -RP "$from" "$to"
printf 'jbuffin/skills %s\n' "$sha" >"$to/SOURCE"
echo "synced crew-resolution from jbuffin/skills $sha"
