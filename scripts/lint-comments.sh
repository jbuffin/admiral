#!/bin/sh
# Runs every scripts/checks/*.sh, one rule per file, and fails if any does.
# Each check reads the tracked files with git grep, so it reads the same on
# macOS and Ubuntu. A new rule is a new file there, with no list to edit.
set -u
cd "$(dirname "$0")/.." || exit 2
status=0
for f in scripts/checks/*.sh; do
	[ -e "$f" ] || continue
	sh "$f" || status=1
done
exit $status
