#!/bin/sh
set -u
name=$(basename "$0" .sh)
hits=$(git grep -nE 'os\.WriteFile\(.*0o7' -- 'internal/probes/*_test.go' ':!internal/probes/exe_test.go')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "write an executable fake with writeExe (exe_test.go); an in-process write fd makes the exec fail with text file busy on Linux" >&2
printf '%s\n' "$hits" >&2
exit 1
