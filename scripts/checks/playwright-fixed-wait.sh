#!/bin/sh
set -u
name=$(basename "$0" .sh)
hits=$(git grep -n 'waitForTimeout' -- 'web/e2e')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "wait for what the page shows, not a fixed time" >&2
printf '%s\n' "$hits" >&2
exit 1
