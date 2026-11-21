#!/bin/sh
# Prints "<date> <count>" for each first-parent commit that touched
# scripts/checks/, oldest first: how many checks the repo had then.
set -u
log=$(git log --first-parent --reverse --format='%H %ad' --date=short HEAD -- scripts/checks/) || exit 2
[ -z "$log" ] && exit 0
while read -r sha day; do
	tree=$(git -c core.quotePath=false ls-tree "$sha" scripts/checks/) || exit 2
	count=0
	while IFS= read -r line; do
		case $line in
		"100644 blob "*.sh | "100755 blob "*.sh) count=$((count + 1)) ;;
		esac
	done <<TREE
$tree
TREE
	echo "$day $count"
done <<LOG
$log
LOG
