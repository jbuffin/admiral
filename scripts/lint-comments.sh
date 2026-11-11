#!/bin/sh
# Fails on what AGENTS.md keeps out of the code: an issue number in a Go or
# TypeScript comment, a fixed wait in the Playwright journeys, and a
# whole-second sleep in the Go e2e tests. Uses git grep so it reads the same
# on macOS and Ubuntu.
set -u
cd "$(dirname "$0")/.." || exit 2
status=0

fail() {
	echo "lint-comments: $1" >&2
	echo "$2" >&2
	status=1
}

# A # and digits after a comment opener. A # after a slash, a letter or & is a
# URL anchor, a hex color or an HTML entity, not an issue.
hits=$(git grep -nE '(//|/\*|^[[:space:]]*\*).*(^|[^/[:alnum:]_&])#[0-9]+([^[:alnum:]_]|$)' \
	-- '*.go' '*.ts' '*.tsx' '*.js' '*.mjs' ':!web/node_modules')
[ -z "$hits" ] || fail "issue numbers belong in the commit or PR, not a comment:" "$hits"

hits=$(git grep -n 'waitForTimeout' -- 'web/e2e')
[ -z "$hits" ] || fail "wait for what the page shows, not a fixed time:" "$hits"

hits=$(git grep -nE 'time\.Sleep\(([0-9]+ *\* *)?time\.Second\)' -- 'e2e')
[ -z "$hits" ] || fail "poll for the change instead of sleeping whole seconds:" "$hits"

exit $status
