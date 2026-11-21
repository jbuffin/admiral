#!/bin/sh
# Prints "<date> <count>" for each first-parent commit that touched
# scripts/checks/, oldest first: how many checks the repo had then.
set -u
log=$(git log --first-parent --reverse --format='%H %ad' --date=short HEAD -- scripts/checks/) || exit 2
[ -z "$log" ] && exit 0
echo "$log" | while read -r sha day; do
	count=0
	for name in $(git ls-tree --name-only "$sha" scripts/checks/); do
		case $name in
		scripts/checks/*.sh) count=$((count + 1)) ;;
		esac
	done
	echo "$day $count"
done
