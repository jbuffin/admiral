#!/bin/sh
set -u
name=$(basename "$0" .sh)
hits=$(git grep -nE "import[^;]*[{ ,](test|webkit|chromium|firefox)[ ,}][^;]*from '@playwright/test'|\.launch(PersistentContext)?\(" -- 'web/e2e' ':!web/e2e/fixtures.ts')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "import test from ./fixtures, which silences the browser's audio, and open pages through it" >&2
printf '%s\n' "$hits" >&2
exit 1
