#!/bin/sh
# Fails when a Markdown file under docs/agents/ (or the directory given as the
# argument, resolved against the caller's cwd) has a line over 1,000 bytes or is
# over 40,000 bytes, so an agent reads a doc in pieces. Walks with find so it
# also works on a temp directory, and follows no name through the shell's word
# splitting, so a name with a space or a glob character is still checked.
set -u
LC_ALL=C
export LC_ALL

if [ $# -ge 1 ]; then
	dir=$1
else
	dir=$(dirname "$0")/../docs/agents
fi
if [ ! -d "$dir" ]; then
	echo "lint-agent-docs: $dir is not a directory" >&2
	exit 2
fi

max_line=1000
max_file=40000
status=0

list=$(mktemp) || exit 2
trap 'rm -f "$list"' EXIT
# -type l lets a symlinked file through; a broken link then fails at wc.
if ! find "$dir" -name '*.md' \( -type f -o -type l \) >"$list"; then
	echo "lint-agent-docs: find failed on $dir" >&2
	exit 2
fi
sorted=$(mktemp) || exit 2
trap 'rm -f "$list" "$sorted"' EXIT
sort "$list" >"$sorted"

# Reading from a file, not a pipe, keeps status in this shell.
while IFS= read -r f; do
	if ! size=$(wc -c <"$f" 2>/dev/null); then
		echo "lint-agent-docs: cannot read $f" >&2
		status=1
		continue
	fi
	size=$(printf '%s' "$size" | tr -d ' ')
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
done <"$sorted"

exit $status
