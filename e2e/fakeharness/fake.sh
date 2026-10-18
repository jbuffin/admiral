#!/bin/sh
# Fake coding-agent harness for Admiral's server tests. `claude`, `codex` and
# `copilot` are symlinks to this script; $0 says which one ran.
#
# Run inside a ship's tmux pane (so TMUX and TMUX_PANE are set by tmux, and
# plain `tmux` talks to the ship's own server). It needs two variables from the
# fleet's `env` in config.json:
#
#   FAKE_RECORD_DIR  directory the recording goes in (required)
#   FAKE_CUE         path of a cue file (optional)
#
# Recording: <FAKE_RECORD_DIR>/<tmux session name>/ gets these files, written in
# this order, with `recorded` created last so a test can wait for it:
#
#   harness   the name it was run as (claude, codex or copilot)
#   argv      its arguments, each one followed by a NUL byte
#   env       its environment, one NAME=value per line
#   cwd       its working directory (physical path)
#   name      the session option @admiral-name, as read at start
#   options   the output of `tmux show-options`, run as its first action (all session options)
#   recorded  empty marker file
#
# Cue file: read once after recording, one cue per line, run in order:
#
#   hook <state>   run `tmux set -p @admiral-state <state>`, the command Admiral's
#                  hook bundle uses (state is needs_you or working)
#   bell           ring the terminal bell
#   sleep <n>      sleep n seconds
#   exit <n>       create `cues-done`, then exit with status n
#   lines <n>      print n numbered lines, "line 0001" to "line <n>", each in an SGR
#                  colour (\033[31m..\033[36m, cycling) and reset at the end
#   altscreen      switch to the alternate screen (\033[?1049h), set application cursor
#                  keys (\033[?1h) and bracketed paste (\033[?2004h), clear it and print
#                  "ALT SCREEN" at the top left, leaving the cursor at column 10, row 0
#   wait <name>    block until the file <FAKE_RECORD_DIR>/<tmux session>/<name> exists,
#                  so a test decides when the next cue (an exit, say) runs
#
# With no cue file, or once the cues run out, it puts the terminal in raw mode
# (stty raw -echo: no echo, no line editing, ^C and CR arrive as bytes, and no
# NL to CRLF on output), creates `cues-done`, and stays running until the tmux
# server is killed. From then on it is the raw-stdin recorder: it appends every
# byte it reads to `stdin` in the recording directory, exactly as received. A
# test sends input only after `cues-done` exists.

# The first thing it does: read the session options, so a test can prove
# Admiral set them before the harness could run anything.
first=$(tmux show-options 2>&1)
me=$(basename "$0")
sess=$(tmux display-message -p '#{session_name}')
d="$FAKE_RECORD_DIR/$sess"
mkdir -p "$d"
echo "$me" >"$d/harness"
printf '%s\0' "$@" >"$d/argv"
env >"$d/env"
pwd -P >"$d/cwd"
tmux show-options -v @admiral-name >"$d/name"
printf '%s\n' "$first" >"$d/options"
: >"$d/recorded"

if [ -n "$FAKE_CUE" ] && [ -f "$FAKE_CUE" ]; then
	while read -r cmd arg; do
		case "$cmd" in
		hook) tmux set -p @admiral-state "$arg" ;;
		bell) printf '\a' ;;
		sleep) sleep "$arg" ;;
		lines) awk -v n="$arg" 'BEGIN { for (i = 1; i <= n; i++) printf "\033[3%dmline %04d\033[0m\n", i % 6 + 1, i }' ;;
		altscreen) printf '\033[?1049h\033[?1h\033[?2004h\033[2J\033[HALT SCREEN' ;;
		wait)
			while [ ! -e "$d/$arg" ]; do sleep 0.1; done
			;;
		exit)
			: >"$d/cues-done"
			exit "$arg"
			;;
		esac
	done <"$FAKE_CUE"
fi
stty raw -echo 2>/dev/null
: >"$d/cues-done"
exec cat -u >"$d/stdin"
