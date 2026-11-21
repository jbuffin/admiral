#!/bin/sh
set -u
name=$(basename "$0" .sh)
hits=$(git grep -nE 'time\.Sleep\(([0-9]+ *\* *)?time\.Second\)' -- 'e2e')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "poll for the change instead of sleeping whole seconds" >&2
printf '%s\n' "$hits" >&2
exit 1
