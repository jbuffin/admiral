#!/bin/sh
# Tests scripts/lint-agent-docs.sh through its command line: exit code and message.
set -u

root=$(cd "$(dirname "$0")/.." && pwd)
script=$root/scripts/lint-agent-docs.sh
tmp=$(mktemp -d) || exit 2
trap 'rm -rf "$tmp"' EXIT

fails=0
out=
code=0

fail() {
	echo "FAIL: $1"
	fails=$((fails + 1))
}

# run <dir-or-empty>: runs the check, sets $out and $code.
run() {
	if [ ! -f "$script" ]; then
		out="missing $script"
		code=127
		return
	fi
	if [ $# -eq 0 ]; then
		out=$(cd "$root" && sh "$script" 2>&1)
	else
		out=$(sh "$script" "$1" 2>&1)
	fi
	code=$?
}

expect_pass() {
	if [ "$code" -ne 0 ]; then fail "$1: want exit 0, got $code: $out"; fi
}

expect_fail() {
	if [ "$code" -eq 0 ] || [ "$code" -eq 127 ]; then
		fail "$1: want a failing exit, got $code: $out"
		return 1
	fi
}

# line <n>: n ASCII characters, no newline.
line() {
	head -c "$1" /dev/zero | tr '\0' 'a'
}

# lines <count> <width>: count lines of width characters (newline included in the width).
lines() {
	i=0
	while [ "$i" -lt "$1" ]; do
		line $(($2 - 1))
		echo
		i=$((i + 1))
	done
}

new_case() {
	rm -rf "$tmp/case"
	mkdir -p "$tmp/case/docs/agents" "$tmp/case/docs/other"
	dir=$tmp/case/docs/agents
}

# A file at exactly 1,000 bytes per line and exactly 40,000 bytes passes.
new_case
lines 40 1000 >"$dir/edge.md"
run "$dir"
expect_pass "exact limits"

# A line of 1,001 bytes fails, naming the file and the line number, with a fix hint.
new_case
{
	echo "# Title"
	line 1001
	echo
} >"$dir/long.md"
run "$dir"
if expect_fail "long line"; then
	case $out in *long.md*) ;; *) fail "long line: message does not name the file: $out" ;; esac
	printf '%s\n' "$out" | grep -Eq '(:|[Ll]ine )2([^0-9]|$)' || fail "long line: message does not name line 2: $out"
	printf '%s\n' "$out" | grep -Eiq 'split|shorten|wrap|break|trim' || fail "long line: no fix hint: $out"
fi

# A file of 40,001 bytes fails, naming the file and its size, with a fix hint.
new_case
{
	lines 40 1000
	printf x
} >"$dir/big.md"
run "$dir"
if expect_fail "big file"; then
	case $out in *big.md*) ;; *) fail "big file: message does not name the file: $out" ;; esac
	case $out in *40001*) ;; *) fail "big file: message does not give the size 40001: $out" ;; esac
	printf '%s\n' "$out" | grep -Eiq 'split|shorten|trim|move' || fail "big file: no fix hint: $out"
fi

# Markdown in a subdirectory is checked.
new_case
mkdir -p "$dir/codebase/deep"
line 1001 >"$dir/codebase/deep/nested.md"
run "$dir"
if expect_fail "nested file"; then
	case $out in *nested.md*) ;; *) fail "nested file: message does not name the file: $out" ;; esac
fi

# Files outside the checked directory, and non-Markdown files inside it, are never checked.
new_case
echo ok >"$dir/ok.md"
line 1001 >"$tmp/case/docs/other/long.md"
line 1001 >"$dir/data.txt"
run "$dir"
expect_pass "files outside docs/agents or not Markdown"

# A directory that does not exist fails with exit 2 and names it.
new_case
run "$tmp/case/docs/nope"
if expect_fail "missing directory"; then
	[ "$code" -eq 2 ] || fail "missing directory: want exit 2, got $code: $out"
	case $out in *"is not a directory"*nope*|*nope*"is not a directory"*) ;; *) fail "missing directory: message does not say it is not a directory: $out" ;; esac
fi

# A name with a space is still checked and named.
new_case
line 1001 >"$dir/two words.md"
run "$dir"
if expect_fail "name with a space"; then
	case $out in *"two words.md"*) ;; *) fail "name with a space: message does not name the file: $out" ;; esac
fi

# A name with glob characters is still checked and named.
new_case
line 1001 >"$dir/a[1]*.md"
run "$dir"
if expect_fail "name with glob characters"; then
	case $out in *'a[1]*.md'*) ;; *) fail "name with glob characters: message does not name the file: $out" ;; esac
fi

# A symlinked Markdown file is checked.
new_case
line 1001 >"$tmp/case/docs/other/target.md"
ln -s "$tmp/case/docs/other/target.md" "$dir/link.md"
run "$dir"
if expect_fail "symlinked file"; then
	case $out in *link.md*) ;; *) fail "symlinked file: message does not name the file: $out" ;; esac
fi

# A relative directory argument resolves against the caller's cwd, not the repo root.
new_case
line 1001 >"$dir/long.md"
out=$(cd "$tmp/case" && sh "$script" docs/agents 2>&1)
code=$?
if expect_fail "relative directory"; then
	case $out in *long.md*) ;; *) fail "relative directory: message does not name the file: $out" ;; esac
fi

# With no argument it checks the real docs/agents, which must pass.
run
expect_pass "real docs/agents"

if [ "$fails" -ne 0 ]; then
	echo "$fails check(s) failed"
	exit 1
fi
echo "ok"
