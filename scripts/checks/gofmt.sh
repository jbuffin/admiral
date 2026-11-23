#!/bin/sh
set -u
name=$(basename "$0" .sh)
git rev-parse --git-dir >/dev/null || exit 2
files=$(git ls-files -- '*.go')
[ -z "$files" ] && exit 0
hits=$(printf '%s\n' "$files" | xargs gofmt -l)
[ $? -eq 0 ] || exit 2
[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "run gofmt -w on these files" >&2
printf '%s\n' "$hits" >&2
exit 1
