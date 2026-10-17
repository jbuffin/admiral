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
#
# With no cue file, or once the cues run out, it creates `cues-done` and stays
# running (sleep) until the tmux server is killed.

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
		exit)
			: >"$d/cues-done"
			exit "$arg"
			;;
		esac
	done <"$FAKE_CUE"
fi
: >"$d/cues-done"
exec sleep 3600
