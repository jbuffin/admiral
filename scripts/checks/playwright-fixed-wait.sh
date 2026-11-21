#!/bin/sh
set -u
hits=$(git grep -n 'waitForTimeout' -- 'web/e2e')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$hits" ] && exit 0
echo "playwright-fixed-wait: wait for what the page shows, not a fixed time" >&2
echo "$hits" >&2
exit 1
