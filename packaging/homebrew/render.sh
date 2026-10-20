#!/bin/sh
# Prints the tap's formula for one release: the template next to this script
# with VERSION and SHA256_OF_THE_TARBALL filled in.
#
#   sh packaging/homebrew/render.sh <x.y.z> <sha256 of the tarball> > Formula/admiral.rb
set -eu

[ $# -eq 2 ] || { echo "usage: render.sh <x.y.z> <sha256>" >&2; exit 1; }
echo "$2" | grep -Eq '^[0-9a-f]{64}$' || { echo "render.sh: '$2' is not a SHA-256" >&2; exit 1; }
sed -e '/^# /d' -e "s/VERSION/$1/g" -e "s/SHA256_OF_THE_TARBALL/$2/" "$(dirname "$0")/admiral.rb"
