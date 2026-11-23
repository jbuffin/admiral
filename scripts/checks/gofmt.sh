#!/bin/sh
set -u
name=$(basename "$0" .sh)
git rev-parse --git-dir >/dev/null || exit 2
# macOS xargs has no -r, so an empty list is tested for first.
[ -n "$(git ls-files -- '*.go')" ] || exit 0
hits=$(git ls-files -z -- '*.go' | xargs -0 gofmt -l --)
[ $? -eq 0 ] || exit 2
[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "run gofmt -w on these files" >&2
printf '%s\n' "$hits" >&2
exit 1
