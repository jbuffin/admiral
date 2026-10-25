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
# this order, with `recorded` created last so a test can wait for it. A crew boat
# (its window has @admiral-crew-name) records in
# <FAKE_RECORD_DIR>/<tmux session name>/crew/<crew name>/ instead, with the same
# files, so a ship and its crew boats on one harness never overwrite each other:
#
#   harness   the name it was run as (claude, codex or copilot)
#   argv      its arguments, each one followed by a NUL byte
#   env       its environment, one NAME=value per line
#   cwd       its working directory (physical path)
#   name      the session option @admiral-name, as read at start
#   options   the output of `tmux show-options`, run as its first action (all session options)
#   window-options
#             the output of `tmux show-options -w -t "$TMUX_PANE"` at the same moment (the
#             options of the pane's window), so a test proves a crew window's options
#             were set before the harness started
#   dirs      "inbox=yes|no reports=yes|no": whether $ADMIRAL_INBOX is a directory and
#             the directory of $ADMIRAL_REPORT exists, checked before anything else
#   recorded  empty marker file
#
# Cue file: read once after recording, one cue per line, run in order:
#
#   hook <state>   run `tmux set -p -t "$TMUX_PANE" @admiral-state <state>` (state is
#                  needs_you or working). It names the pane: a bare `set -p` lands
#                  on the session's active pane, which is not a crew boat's
#   event <Event> <json>
#                  run the command the hook bundle holds for <Event>, as the harness
#                  would: the payload <json> (the rest of the line) on its stdin, then
#                  append the event name to `events-done` in the recording directory.
#                  Claude only: the bundle is the file after `--settings` in its argv
#                  (needs jq). It runs every handler of every group whose matcher
#                  matches: the matcher is a regex tested against the payload's
#                  tool_name (for Notification, notification_type), and a group with
#                  no matcher always matches. The match is unanchored, as jq's test()
#                  is: "Bash" also matches BashOutput, which is looser than Claude.
#                  Each handler appends "<Event>", its exit code and its command, tab
#                  separated, to `handlers`, and its stderr goes to `handler-<n>.stderr`
#                  (n counts from 1, in the order of `handlers`). A handler's exit code
#                  is not a fault. `events-done` still gets one line per cue, and
#                  `event-error` holds only a fault of the fake itself (no settings, no
#                  command, bad jq).
#   bell           ring the terminal bell
#   sleep <n>      sleep n seconds
#   exit <n>       create `cues-done`, then exit with status n
#   lines <n>      print n numbered lines, "line 0001" to "line <n>", each in an SGR
#                  colour (\033[31m..\033[36m, cycling) and reset at the end
#   altscreen      switch to the alternate screen (\033[?1049h), set application cursor
#                  keys (\033[?1h) and bracketed paste (\033[?2004h), clear it and print
#                  "ALT SCREEN" at the top left, leaving the cursor at column 10, row 0
#   bracketedpaste ask the terminal for bracketed paste (\033[?2004h) without the
#                  alternate screen, so tmux's `paste-buffer -p` brackets what it pastes
#   record         start the raw-stdin recorder now, in the background, and create
#                  `recording`. The terminal goes raw (stty raw -echo) from here on, so
#                  later output has no NL to CRLF translation; put `record` first in a
#                  cue file that prints nothing, or in none that relies on cooked output.
#                  Every byte read is appended to `stdin` while the later cues run, so a
#                  `wait <name>` then `event SessionStart ...` can follow input. Without
#                  it the recorder starts only after the cues end, as below.
#   wait <name>    block until the file <FAKE_RECORD_DIR>/<tmux session>/<name> exists,
#                  so a test decides when the next cue (an exit, say) runs
#
# With no cue file, or once the cues run out, it puts the terminal in raw mode
# (stty raw -echo: no echo, no line editing, ^C and CR arrive as bytes, and no
# NL to CRLF on output), creates `cues-done`, and stays running until the tmux
# server is killed. From then on it is the raw-stdin recorder: it appends every
# byte it reads to `stdin` in the recording directory, exactly as received. A
# test sends input only after `cues-done` exists (or `recording`, after a
# `record` cue, which starts the recorder before the other cues).

# The first things it does: check the ship's directories exist, and read the
# session options, so a test can prove Admiral made and set them before the
# harness could run anything.
dirs="inbox=no reports=no"
[ -d "$ADMIRAL_INBOX" ] && dirs="inbox=yes reports=no"
if [ -z "$ADMIRAL_REPORT" ]; then
  dirs=${dirs%reports=no}reports=unset
elif [ -d "$(dirname "$ADMIRAL_REPORT")" ]; then
  dirs=${dirs%reports=no}reports=yes
fi
first=$(tmux show-options 2>&1)
firstw=$(tmux show-options -w -t "$TMUX_PANE" 2>&1)
me=$(basename "$0")
sess=$(tmux display-message -p '#{session_name}')
d="$FAKE_RECORD_DIR/$sess"
crew=$(tmux show-options -wqv -t "$TMUX_PANE" @admiral-crew-name)
[ -n "$crew" ] && d="$d/crew/$crew"
mkdir -p "$d"
echo "$me" >"$d/harness"
printf '%s\0' "$@" >"$d/argv"
env >"$d/env"
pwd -P >"$d/cwd"
tmux show-options -v @admiral-name >"$d/name"
printf '%s\n' "$first" >"$d/options"
printf '%s\n' "$firstw" >"$d/window-options"
printf '%s\n' "$dirs" >"$d/dirs"
: >"$d/recorded"

exec 3<&0 # the terminal, for a `record` cue: the loop below reads the cue file on fd 0
if [ -n "$FAKE_CUE" ] && [ -f "$FAKE_CUE" ]; then
	while read -r cmd arg; do
		case "$cmd" in
		hook) tmux set -p -t "$TMUX_PANE" @admiral-state "$arg" ;;
		event)
			ev=${arg%% *}
			payload=${arg#"$ev"}
			settings=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--settings" ] && settings=$a
				prev=$a
			done
			# The handlers of every group whose matcher matches, run one at a time
			# in file order. A matcher is tested against tool_name (for Notification,
			# notification_type); a group with none always matches.
			subject=$(printf '%s' "$payload" | jq -r --arg e "$ev" 'if $e == "Notification" then .notification_type else .tool_name end // ""' 2>>"$d/event-error")
			: >"$d/.cmds"
			[ -n "$settings" ] && jq -r --arg e "$ev" --arg s "$subject" '.hooks[$e][]? | (.matcher // "") as $m | select($m == "" or ($s | test($m))) | .hooks[]?.command' "$settings" >"$d/.cmds" 2>>"$d/event-error"
			if [ ! -s "$d/.cmds" ]; then
				echo "no hook command for $ev in settings '$settings'" >>"$d/event-error"
			else
				while IFS= read -r command; do
					n=$(($(wc -l 2>/dev/null <"$d/handlers" || echo 0) + 1))
					printf '%s' "$payload" | sh -c "$command" >>"$d/event-stdout" 2>"$d/handler-$n.stderr"
					printf '%s\t%s\t%s\n' "$ev" "$?" "$command" >>"$d/handlers"
				done <"$d/.cmds"
			fi
			echo "$ev" >>"$d/events-done"
			;;
		bell) printf '\a' ;;
		sleep) sleep "$arg" ;;
		lines) awk -v n="$arg" 'BEGIN { for (i = 1; i <= n; i++) printf "\033[3%dmline %04d\033[0m\n", i % 6 + 1, i }' ;;
		altscreen) printf '\033[?1049h\033[?1h\033[?2004h\033[2J\033[HALT SCREEN' ;;
		bracketedpaste) printf '\033[?2004h' ;;
		record)
			stty raw -echo <&3 2>/dev/null
			cat -u <&3 >>"$d/stdin" &
			recorder=$!
			: >"$d/recording"
			;;
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
# After a `record` cue the recorder already runs; stay as long as it does.
if [ -n "$recorder" ]; then
	wait "$recorder"
	exit
fi
exec cat -u >"$d/stdin"
