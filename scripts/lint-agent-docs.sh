#!/bin/sh
# Fails when a Markdown file under docs/agents/ (or the directory given as the
# argument) has a line over 1,000 bytes or is over 40,000 bytes, so an agent
# reads a doc in pieces. Walks with find so it also works on a temp directory.
set -u
LC_ALL=C
export LC_ALL
cd "$(dirname "$0")/.." || exit 2
dir=${1:-docs/agents}
status=0

max_line=1000
max_file=40000

files=$(find "$dir" -type f -name '*.md' | sort)
for f in $files; do
	size=$(wc -c <"$f" | tr -d ' ')
	if [ "$size" -gt "$max_file" ]; then
		echo "lint-agent-docs: $f is $size bytes, over $max_file" >&2
		echo "split a self-contained section into its own file, or trim history" >&2
		status=1
	fi
	hits=$(awk -v max="$max_line" 'length($0) > max { printf "%s:%d: line is %d bytes, over %d\n", FILENAME, NR, length($0), max }' "$f")
	if [ -n "$hits" ]; then
		echo "$hits" >&2
		echo "lint-agent-docs: break the line into shorter sub-bullets or a short paragraph per fact" >&2
		status=1
	fi
done

exit $status
