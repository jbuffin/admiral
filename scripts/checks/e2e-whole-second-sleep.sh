#!/bin/sh
set -u
hits=$(git grep -nE 'time\.Sleep\(([0-9]+ *\* *)?time\.Second\)' -- 'e2e')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$hits" ] && exit 0
echo "e2e-whole-second-sleep: poll for the change instead of sleeping whole seconds" >&2
echo "$hits" >&2
exit 1
