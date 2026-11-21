#!/bin/sh
set -u
name=$(basename "$0" .sh)
# A # and digits after a comment opener. A # after a slash, a letter or & is a
# URL anchor, a hex color or an HTML entity, not an issue.
hits=$(git grep -nE '(//|/\*|^[[:space:]]*\*).*(^|[^/[:alnum:]_&])#[0-9]+([^[:alnum:]_]|$)' \
	-- '*.go' '*.ts' '*.tsx' '*.js' '*.mjs' ':!web/node_modules')
status=$?
[ "$status" -le 1 ] || exit 2
[ -z "$hits" ] && exit 0
printf '%s: %s\n' "$name" "issue numbers belong in the commit or PR, not a comment" >&2
printf '%s\n' "$hits" >&2
exit 1
