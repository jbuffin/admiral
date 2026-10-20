#!/bin/sh
# Prints the next release version as x.y.z, with no leading v.
#
#   sh packaging/next-version.sh <patch|minor|major> [x.y.z]
#
# An explicit x.y.z wins over the bump and must not be tagged yet. Otherwise
# the highest v<x.y.z> tag is raised; with no such tag the first release is 0.1.0.
set -eu

fail() { echo "next-version.sh: $*" >&2; exit 1; }

bump=${1:-}
explicit=${2:-}

case $bump in
patch | minor | major) ;;
*) fail "bump must be patch, minor or major, not '$bump'" ;;
esac

if [ -n "$explicit" ]; then
	echo "$explicit" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' || fail "version '$explicit' is not x.y.z"
	if git rev-parse -q --verify "refs/tags/v$explicit" >/dev/null; then fail "tag v$explicit already exists"; fi
	echo "$explicit"
	exit 0
fi

latest=$(git tag -l 'v*' | sed -n 's/^v\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)$/\1/p' | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1)
if [ -z "$latest" ]; then
	echo 0.1.0
	exit 0
fi

major=${latest%%.*}
rest=${latest#*.}
minor=${rest%%.*}
patch=${rest#*.}
case $bump in
patch) patch=$((patch + 1)) ;;
minor) minor=$((minor + 1)) patch=0 ;;
major) major=$((major + 1)) minor=0 patch=0 ;;
esac
echo "$major.$minor.$patch"
