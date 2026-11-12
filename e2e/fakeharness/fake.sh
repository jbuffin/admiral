#!/bin/sh
# Fake coding-agent harness for Admiral's server tests. `claude`, `codex` and
# `copilot` are symlinks to this script; $0 says which one ran.
#
# Two things come before any tmux call and need no pane:
#
#   --version      (first argument) prints the content of the file $FAKE_VERSION_FILE if it
#                  exists, else $FAKE_VERSION, else "2.1.300 (fake)", and exits 0. The
#                  content FAIL prints "boom" to stderr and exits 1.
#   -p <prompt>    a headless probe run: appends a line to $FAKE_RECORD_DIR/headless-runs,
#                  takes the command after the last ": " of the prompt, plays it as a Bash
#                  call through the --settings file's PreToolUse hooks (needs jq; a handler
#                  that exits 2 denies) and runs it only when none denied. $FAKE_PROBE is
#                  pass (default), skip-hook (runs no hooks, runs the command and says it was
#                  denied), run-anyway (runs the command despite a deny), different-command
#                  (asks the hooks about, and runs, the command with "&& true" added) or
#                  no-tool (never calls the tool).
#   -p with --mcp-config
#                  a fenced probe run (claude): prints an init line listing the tools of the
#                  config's one server, calls its first tool, and leaves headless-runs alone.
#
#   codex exec --json
#                  a fenced run (the Codex fence and config-home probes), not a judge call: the MCP server comes
#                  from the `-c mcp_servers.<name>.{command,args,env}` flags. With a server it calls the server's
#                  first tool and prints Codex's exec --json events (thread.started, an mcp_tool_call item, an
#                  agent_message item); without one it prints a thread and an agent_message. $FAKE_CODEX_PROBE
#                  (a dock env value) is pass (default), shell-ran (creates the sentinel file the fence probe
#                  watches for and prints a command_execution item), login-failed (401 on stderr, exit 1) or
#                  plugin-loaded (makes plugins/cache/github in $CODEX_HOME).
#
#   copilot -p … --output-format json
#                  a fenced run (the Copilot fence and config-home probes): the MCP server comes from
#                  --additional-mcp-config (inline JSON or @file). With the probe's server it calls the server's first tool and
#                  prints Copilot's events (tool.execution_start with toolName, mcpServerName and mcpToolName, then an
#                  assistant.message); without one it prints only the message. $FAKE_COPILOT_PROBE (a dock env value) is pass
#                  (default), shell-ran (creates the sentinel file the fence probe watches for and prints a bash call),
#                  login-failed ("not logged in" on stderr, exit 1) or plugin-loaded (makes installed-plugins/github in
#                  $COPILOT_HOME). Any other copilot -p is a headless run.
#
#   judge call     claude with `--tools ""` and no --mcp-config, `codex exec` without --json, or copilot with piped stdin and no -p:
#                  a judge probe or call. It reads the prompt from stdin, answers {"reply":
#                  "ok"} in the harness's real shape (claude's structured_output, codex's -o
#                  file, copilot's stdout) and leaves headless-runs alone. A claude call under
#                  --max-budget-usd below 0.001 reports error_max_budget_usd. $FAKE_JUDGE=leak
#                  makes it leak what each probe watches for: it creates the files and reads
#                  the file the prompt names (and answers with its contents), fetches the URL
#                  the prompt names, answers a plain "hello" instead of JSON and ignores the cap.
#                  The advise call (`admiral advise`) is the call whose --json-schema holds
#                  "stakes", or whose prompt holds "You are the advisor": it answers {"stakes":
#                  "low","reason":"stays on the ship's branch"}. $FAKE_JUDGE=advise-high answers
#                  high, advise-invalid answers a stakes value that is neither, and fail exits
#                  1 with "boom" on stderr for any judge call.
#                  The crew-writer call (Add crew on the Crew page) is the judge call whose prompt's
#                  first line is "You write one Admiral crew member." (every harness gets the prompt on
#                  stdin, so the first line is the one test that works for all three). It answers a valid
#                  draft: a member named quartermaster with a built-in role, no based-on, no skills, no
#                  model, a one-line pick-me-when and short instructions. $FAKE_JUDGE=crew-invalid answers
#                  a purser draft with a custom role and no based-on, which the resolver refuses.
#                  fail and leak act on it as on every judge call.
#
# Otherwise it runs inside a ship's tmux pane (so TMUX and TMUX_PANE are set by tmux, and
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
#   statusline <json>
#                  run the `statusLine` command of the `--settings` file (Claude only,
#                  needs jq) with the payload <json> on its stdin, as Claude does for a
#                  status line. Its stdout is appended to `statusline-stdout`, its
#                  stderr to `statusline-stderr`, its exit code to `statusline-exit`, a
#                  fault of the fake itself to `statusline-error`, and `statusline-done`
#                  is created after each run.
#   bell           ring the terminal bell
#   sleep <n>      sleep n seconds
#   exit <n>       create `cues-done`, then exit with status n
#   menu <json>    Claude only, needs python3, and the last cue: creates `cues-done`, then draws Claude's AskUserQuestion menu for the
#                  call <json> holds (a list of {text, labels, multi}) through `fakemenu.py` and logs every key it reads to `stdin`
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
#   mcp <name>     needs python3: waits for the file <name> in the recording directory
#                  holding {"name": <tool>, "arguments": {...}}, makes that one tools/call through
#                  the first server of the --mcp-config in its argv (Codex: the `-c mcp_servers.<name>.*`
#                  flags; with the server's env), and
#                  writes the tool result to <name>.reply (an RPC error becomes an isError result)
#                  It lists the tools first and, as Claude Code does, fails the cue (no reply, a line
#                  in mcp-error) when a tool's inputSchema is not an object with an object properties
#   copilotready   a trusted Copilot TUI waiting for its first message: shows its composer footer and remembers the folder
#                  in $COPILOT_HOME/config.json. `copilotreply` does both too, but only after the cues before it.
#   copilotreply <text>
#                  Copilot only, needs jq, and the last cue (it never ends): like `reply`, for a Copilot chat. Per message it
#                  appends the user's text and an assistant line holding <text> to $COPILOT_HOME/session-state/<id>/events.jsonl,
#                  <id> being the --session-id in its argv, and binds the transcript on the first
#                  message of a session, by running the plugin's sessionStart hook (source new). A message of /clear writes a
#                  session.shutdown line (as `usage` writes) to the session it ends and starts a new id, bound only by the next
#                  message, as the real CLI does. It appends every byte it reads to `stdin`.
#   codexready     a trusted Codex TUI waiting for its first message: shows the composer and writes the hook's trust entry
#                  to $CODEX_HOME/config.toml. `codexreply` does both too, but only after the cues before it.
#   codexreply <text>
#                  Codex only, needs jq, and the last cue (it never ends): like `reply`, for a Codex chat. It
#                  writes a rollout under $CODEX_HOME/sessions (a token_count line as `usage` writes, then per
#                  message the user's text and an assistant line holding <text>). It shows Codex's composer, records the
#                  hooks trust Codex would have written to $CODEX_HOME/config.toml, and sets the pane's
#                  @admiral-transcript only when the first message arrives, as a first turn's SessionStart would,
#                  creates `cues-done`, goes raw and appends every byte it reads to `stdin`.
#   wait <name>    block until the file <FAKE_RECORD_DIR>/<tmux session>/<name> exists,
#                  so a test decides when the next cue (an exit, say) runs
#   reply <text>   Claude only, needs jq, and the last cue (it never ends): creates `cues-done`,
#                  puts the terminal in raw mode and answers every message it is pasted. A
#                  message is the bytes up to the first CR, with LFs kept and the bracketed-paste
#                  markers, other escape sequences (ESC [ ... and a lone ESC) removed. Per message it appends two lines to the
#                  transcript $CLAUDE_CONFIG_DIR/projects/zz/<id>.jsonl, <id> being the
#                  --session-id in its argv: a user line holding the message and an assistant
#                  line holding <text>. Put `bracketedpaste` before it, or tmux pastes without
#                  brackets. Every byte it reads is appended to `stdin` as well. With no
#                  --session-id (or one holding "/") or no $CLAUDE_CONFIG_DIR it writes why to
#                  `reply-error` and stderr and exits 1, before `cues-done`.
#   events <text>  Copilot only, needs jq: appends a prompt ("Question <n>"), an assistant message
#                  holding <text> and its turn end to $COPILOT_HOME/session-state/<id>/events.jsonl, in
#                  the shape Copilot records, <id> being the --session-id in its argv. Unlike
#                  `reply` it is not the last cue and answers nothing. With no usable --session-id
#                  or no $COPILOT_HOME it writes why to `events-error` and stderr and exits 1.
#   ask <message>|<option>|...
#                  Copilot only, needs jq: appends a pending ask_user call to events.jsonl, one
#                  single-choice property in the oneOf shape Copilot records.
#   prompted <text>
#                  Copilot only, needs jq: appends one user.message line holding <text>, the echo of a message
#                  sent to the pane.
#   copilothook <event> <json>
#                  Copilot only, needs jq: runs the commands of the plugin's hooks.json (the
#                  --plugin-dir in argv) for <event>, <json> on stdin, as Copilot's hook stdin reads
#                  (snake_case: notification_type, toolName). A matcher is tested against
#                  notification_type or toolName; the event name goes to `events-done`.
#   usage          append the usage line this harness writes at its end, with fixed figures (the
#                  tests read them): claude a cost-state line (input 100, output 50 with 10
#                  thinking, cache read 300, cache write 40, $0.25) to the transcript in the pane's
#                  @admiral-transcript; copilot a session.shutdown line (input 150 with 100 cache
#                  read and 40 cache write, output 30, 2.5 credits, 3 premium requests) to the same;
#                  codex a token_count event (input 500 with 200 cached, output 60 with 20 reasoning)
#                  to a rollout under ${CODEX_HOME:-$HOME/.codex}/sessions, then sets the pane's
#                  @admiral-transcript to it, as the SessionStart hook would. With no option to
#                  write to it puts why in `usage-error` and exits 1.
#   replydelay <n> sleep n seconds before each message's lines are appended, so a test sees the
#                  message sent and not yet recorded. Put it before `reply`.
#
# With no cue file, or once the cues run out, it puts the terminal in raw mode
# (stty raw -echo: no echo, no line editing, ^C and CR arrive as bytes, and no
# NL to CRLF on output), creates `cues-done`, and stays running until the tmux
# server is killed. From then on it is the raw-stdin recorder: it appends every
# byte it reads to `stdin` in the recording directory, exactly as received. A
# test sends input only after `cues-done` exists (or `recording`, after a
# `record` cue, which starts the recorder before the other cues).

# Before anything touches tmux, so the server's version check and a headless probe
# run never need a pane.
if [ "$1" = "--version" ]; then
  v=${FAKE_VERSION:-2.1.300 (fake)}
  [ -n "$FAKE_VERSION_FILE" ] && [ -f "$FAKE_VERSION_FILE" ] && v=$(cat "$FAKE_VERSION_FILE")
  if [ "$v" = FAIL ]; then
    echo boom >&2
    exit 1
  fi
  echo "$v"
  exit 0
fi
# codex as a ship's app-server, started by `admiral codex-pane` in the pane: it must neither
# record nor run cues, or the ship would run them twice. By default it exits 1 at once, so the
# wrapper falls back to the plain TUI. FAKE_APP_SERVER=listen (a fleet env value) listens on the
# --listen unix socket instead, until it is killed, for the journey that wants the live path.
# The wrapper asks codex --help whether --no-daemon exists; answer without recording.
if [ "$(basename "$0")" = codex ] && [ "$1" = --help ] && [ $# -eq 1 ]; then
  echo "Usage: codex [OPTIONS]"
  echo "      --no-daemon  Do not use the shared daemon"
  exit 0
fi
if [ "$(basename "$0")" = codex ] && [ "$1" = app-server ]; then
  if [ "$FAKE_APP_SERVER" = listen ]; then
    path=""
    prev=""
    for a in "$@"; do
      [ "$prev" = "--listen" ] && path=${a#unix://}
      prev=$a
    done
    exec python3 -c 'import os, socket, sys
p = sys.argv[1]
s = socket.socket(socket.AF_UNIX)
s.bind(p)
s.listen(8)
while True:
    s.accept()[0].close()' "$path"
  fi
  echo "error: unrecognized subcommand 'app-server'" >&2
  exit 1
fi
settings=""
prompt=""
headless=""
mcpcfg=""
prev=""
copilotfmt=""
copilotmcp=""
copilotlogdir=""
for a in "$@"; do
  [ "$prev" = "--log-dir" ] && copilotlogdir=$a
  [ "$prev" = "--settings" ] && settings=$a
  [ "$prev" = "--additional-mcp-config" ] && copilotmcp=$a
  [ "$a" = "--output-format" ] && copilotfmt=1
  if [ "$prev" = "-p" ]; then
    headless=1
    prompt=$a
  fi
  prev=$a
done
# Speaks MCP to the first server a --mcp-config file names, with that server's env on top of ours.
#   probe <config>                   initialize, list tools, print Claude's init line, call the first tool
#   call <config> <request> <reply>  one tools/call from the JSON request file, its tool result to <reply>
MCPPY=$(
	cat <<'PYEOF'
import json, os, subprocess, sys
mode, cfg = sys.argv[1], sys.argv[2]
name, spec = next(iter(json.load(open(cfg))["mcpServers"].items()))
env = dict(os.environ); env.update(spec.get("env", {}))
p = subprocess.Popen([spec["command"]] + spec.get("args", []), stdin=subprocess.PIPE, stdout=subprocess.PIPE, env=env, text=True)
n = [0]
def rpc(method, params=None):
    n[0] += 1
    p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": n[0], "method": method, "params": params or {}}) + "\n"); p.stdin.flush()
    while True:
        line = p.stdout.readline()
        if not line: raise SystemExit("the MCP server closed its output")
        r = json.loads(line)
        if r.get("id") == n[0]: return r
def call(tool, args):
    r = rpc("tools/call", {"name": tool, "arguments": args})
    if "error" in r:
        return {"isError": True, "content": [{"type": "text", "text": r["error"].get("message", "error")}]}
    return r["result"]
rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "fake", "version": "1"}})
p.stdin.write(json.dumps({"jsonrpc": "2.0", "method": "notifications/initialized"}) + "\n"); p.stdin.flush()
if mode == "probe":
    tools = rpc("tools/list")["result"]["tools"]
    print(json.dumps({"type": "system", "subtype": "init", "tools": ["mcp__%s__%s" % (name, t["name"]) for t in tools],
                      "mcp_servers": [{"name": name, "status": "connected"}]}), flush=True)
    if tools: call(tools[0]["name"], {})
else:
    # Claude Code drops every tool of a server when one inputSchema is not an object with an object properties.
    for t in rpc("tools/list")["result"]["tools"]:
        sch = t.get("inputSchema")
        if not (isinstance(sch, dict) and sch.get("type") == "object" and isinstance(sch.get("properties"), dict)):
            sys.exit("tool %s: inputSchema must be an object with type object and an object properties, got %s" % (t.get("name"), json.dumps(sch)))
    req = json.load(open(sys.argv[3]))
    out = json.dumps(call(req["name"], req.get("arguments", {})))
    open(sys.argv[4] + ".tmp", "w").write(out)
    os.rename(sys.argv[4] + ".tmp", sys.argv[4])
p.stdin.close()
p.wait(5)
PYEOF
)
mcpdrive() { python3 -c "$MCPPY" "$@"; }
# Codex takes its MCP servers as `-c mcp_servers.<n>.<key>=<toml>` flags; this prints them as a --mcp-config JSON file.
CODEXPY=$(
	cat <<'PYEOF2'
import json, sys, tomllib
cfg = {}
a = sys.argv[1:]
for i, x in enumerate(a):
    if x in ("-c", "--config") and i + 1 < len(a) and "=" in a[i + 1]:
        k, v = a[i + 1].split("=", 1)
        if k.startswith("mcp_servers."):
            name, key = k[len("mcp_servers."):].split(".", 1)
            cfg.setdefault(name, {}).update(tomllib.loads(key + " = " + v))
print(json.dumps({"mcpServers": cfg}))
PYEOF2
)
codexmcpcfg() { python3 -c "$CODEXPY" "$@"; }
# A judge call, which also carries -p on claude and so comes before the guard run below.
judge=""
codexprobe=""
case "$(basename "$0")" in
claude)
  # An empty tool list with no MCP config; the chat and the fence probe pass both.
  tp=""
  notools=""
  for a in "$@"; do
    [ "$tp" = "--tools" ] && [ -z "$a" ] && notools=1
    [ "$a" = "--mcp-config" ] && mcpcfg=1
    tp=$a
  done
  [ -n "$notools" ] && [ -z "$mcpcfg" ] && judge=1
  ;;
codex)
  [ "$1" = exec ] && judge=1
  for a in "$@"; do [ "$a" = "--json" ] && judge="" && codexprobe=1; done
  ;;
copilot) [ -z "$headless" ] && [ ! -t 0 ] && judge=1 ;;
esac
if [ -n "$judge" ]; then
  jprompt=$(cat)
  out=""
  budget=""
  schema=""
  advise=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "-o" ] && out=$a
    case "$a" in
    --max-budget-usd=*) budget=${a#*=} ;;
    --json-schema=*) schema=1 ;;
    esac
    case "$a" in --json-schema=*stakes*) advise=1 ;; esac
    prev=$a
  done
  case "$jprompt" in *"You are the advisor"*) advise=1 ;; esac
  crewwriter=""
  [ "$(printf '%s\n' "$jprompt" | head -n 1)" = "You write one Admiral crew member." ] && crewwriter=1
  if [ "$FAKE_JUDGE" = fail ]; then
    echo boom >&2
    exit 1
  fi
  text=ok
  body=""
  if [ "$FAKE_JUDGE" = leak ]; then
    urls=$(printf '%s' "$jprompt" | grep -o 'http://[^ ]*' | sed "s/[.,;)\"']*\$//")
    rest=$(printf '%s' "$jprompt" | sed 's#https\{0,1\}://[^ ]*##g')
    for p in $(printf '%s' "$rest" | grep -o '/[A-Za-z0-9_.-]*/[A-Za-z0-9_./-]*' | sed 's/[.]*$//'); do
      if [ -f "$p" ]; then text=$(cat "$p"); else touch "$p" 2>/dev/null; fi
    done
    for u in $urls; do curl -s -m 5 "$u" >/dev/null 2>&1; done
    body=hello
  elif [ -n "$budget" ] && awk -v b="$budget" 'BEGIN{exit !(b+0 < 0.001)}'; then
    echo '{"type":"result","subtype":"error_max_budget_usd","is_error":true,"errors":["Reached maximum budget"],"total_cost_usd":0.0001}'
    exit 1
  fi
  if [ -z "$body" ] && [ -n "$advise" ]; then
    case "$FAKE_JUDGE" in
    advise-high) body='{"stakes":"high","reason":"it posts outside the ship"}' ;;
    advise-invalid) body='{"stakes":"maybe","reason":"unsure"}' ;;
    *) body='{"stakes":"low","reason":"stays on the ship'"'"'s branch"}' ;;
    esac
  fi
  if [ -z "$body" ] && [ -n "$crewwriter" ]; then
    case "$FAKE_JUDGE" in
    crew-invalid) body='{"name":"purser","role":"purser","based-on":"","skills":[],"model":"","pick-me-when":"Pick for the ship'"'"'s accounts.","instructions":"Keep the ledger."}' ;;
    *) body='{"name":"quartermaster","role":"implementer","based-on":"","skills":[],"model":"","pick-me-when":"Pick for provisioning chores.","instructions":"Check the stores before each voyage."}' ;;
    esac
  fi
  [ -z "$body" ] && body="{\"reply\":\"$text\"}"
  if [ "$(basename "$0")" = claude ]; then
    if [ -n "$schema" ] && [ "$body" != hello ]; then
      printf '{"type":"result","subtype":"success","is_error":false,"result":"","structured_output":%s,"total_cost_usd":0.0001}\n' "$body"
    else
      esc=$(printf '%s' "$body" | sed 's/"/\\"/g')
      printf '{"type":"result","subtype":"success","is_error":false,"result":"%s","total_cost_usd":0.0001}\n' "$esc"
    fi
    exit 0
  fi
  [ -n "$out" ] && printf '%s' "$body" >"$out"
  printf '%s' "$body"
  exit 0
fi
# A fenced codex exec (the fence and config-home probes): see the header. It leaves headless-runs alone.
if [ -n "$codexprobe" ]; then
  if [ "${FAKE_CODEX_PROBE:-pass}" = login-failed ]; then
    echo "401 Unauthorized" >&2
    exit 1
  fi
  echo '{"type":"thread.started","thread_id":"fake-thread"}'
  cfgfile="${TMPDIR:-/tmp}/fake-codex-mcp.$$"
  codexmcpcfg "$@" >"$cfgfile"
  if grep -q '"command"' "$cfgfile"; then
    mcpdrive probe "$cfgfile" >/dev/null
    printf '%s\n' '{"type":"item.completed","item":{"type":"mcp_tool_call","server":"probe","tool":"ping","status":"completed"}}'
    case "$FAKE_CODEX_PROBE" in
    shell-ran)
      dir=$(python3 -c 'import json,sys; print(next(iter(json.load(open(sys.argv[1]))["mcpServers"].values()))["env"]["ADMIRAL_PROBE_DIR"])' "$cfgfile")
      nonce=$(python3 -c 'import json,sys; print(next(iter(json.load(open(sys.argv[1]))["mcpServers"].values()))["env"]["ADMIRAL_PROBE_NONCE"])' "$cfgfile")
      : >"$dir/ran-$nonce"
      echo '{"type":"item.completed","item":{"type":"command_execution","command":"touch x","status":"completed"}}'
      ;;
    esac
  else
    case "$FAKE_CODEX_PROBE" in
    plugin-loaded)
      mkdir -p "$CODEX_HOME/plugins/cache/github"
      ;;
    esac
  fi
  rm -f "$cfgfile"
  echo '{"type":"item.completed","item":{"type":"agent_message","text":"pong"}}'
  echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
  exit 0
fi
# A fenced copilot -p --output-format json (the fence and config-home probes): see the header. It leaves headless-runs alone.
if [ "$(basename "$0")" = copilot ] && [ -n "$headless" ] && [ -n "$copilotfmt" ]; then
  if [ "${FAKE_COPILOT_PROBE:-pass}" = login-failed ]; then
    echo "Error: not logged in, run copilot login" >&2
    exit 1
  fi
  cfgfile="${TMPDIR:-/tmp}/fake-copilot-mcp.$$"
  case "$copilotmcp" in
  @*) cp "${copilotmcp#@}" "$cfgfile" ;;
  *) printf '%s' "$copilotmcp" >"$cfgfile" ;;
  esac
  if grep -q ADMIRAL_PROBE_DIR "$cfgfile"; then
    server=$(python3 -c 'import json,sys; print(next(iter(json.load(open(sys.argv[1]))["mcpServers"])))' "$cfgfile")
    mcpdrive probe "$cfgfile" >/dev/null
    # The model request's tool set, as Copilot logs it at --log-level all: the probe's one tool.
    if [ -n "$copilotlogdir" ]; then
      mkdir -p "$copilotlogdir"
      printf '[DEBUG] Wire request: {\n  "copilotToolsFingerprint": {\n    "count": 1,\n    "tools": [\n      "%s-ping:42282fc8"\n    ]\n  }\n}\n' "$server" >"$copilotlogdir/process-1.log"
    fi
    case "$FAKE_COPILOT_PROBE" in
    shell-ran)
      dir=$(python3 -c 'import json,sys; print(next(iter(json.load(open(sys.argv[1]))["mcpServers"].values()))["env"]["ADMIRAL_PROBE_DIR"])' "$cfgfile")
      nonce=$(python3 -c 'import json,sys; print(next(iter(json.load(open(sys.argv[1]))["mcpServers"].values()))["env"]["ADMIRAL_PROBE_NONCE"])' "$cfgfile")
      : >"$dir/ran-$nonce"
      echo '{"type":"tool.execution_start","data":{"toolName":"bash"}}'
      ;;
    esac
    printf '{"type":"tool.execution_start","data":{"toolName":"%s-ping","mcpServerName":"%s","mcpToolName":"ping"}}\n' "$server" "$server"
  else
    case "$FAKE_COPILOT_PROBE" in
    plugin-loaded) mkdir -p "$COPILOT_HOME/installed-plugins/github" ;;
    esac
  fi
  rm -f "$cfgfile"
  echo '{"type":"assistant.message","data":{"content":"pong"}}'
  exit 0
fi
# A fenced run (the fence probe): no tools of its own, one MCP server. It leaves headless-runs alone.
if [ "$(basename "$0")" = claude ] && [ -n "$headless" ] && [ -n "$mcpcfg" ]; then
  cfg=""
  prev=""
  for a in "$@"; do
    [ "$prev" = "--mcp-config" ] && cfg=$a
    prev=$a
  done
  mcpdrive probe "$cfg"
  exit $?
fi
if [ -n "$headless" ]; then
  mkdir -p "$FAKE_RECORD_DIR"
  echo "$(basename "$0")" >>"$FAKE_RECORD_DIR/headless-runs"
  command=${prompt##*: }
  denied=""
  case "${FAKE_PROBE:-pass}" in
  no-tool)
    echo "I will not run that."
    exit 0
    ;;
  skip-hook)
    sh -c "$command"
    echo "The command was denied by the hook."
    exit 0
    ;;
  different-command)
    command="$command && true"
    ;;
  esac
  # The PreToolUse hooks of the --settings file, as Claude runs them for a Bash call:
  # a handler that exits 2 denies the call.
  payload=$(jq -cn --arg c "$command" '{tool_name: "Bash", tool_input: {command: $c}, cwd: "."}')
  cmds=$(jq -r '.hooks.PreToolUse[]? | (.matcher // "") as $m | select($m == "" or ("Bash" | test($m))) | .hooks[]?.command' "$settings")
  while IFS= read -r handler; do
    [ -z "$handler" ] && continue
    printf '%s' "$payload" | sh -c "$handler" >/dev/null 2>&1
    [ $? -eq 2 ] && denied=1
  done <<EOF2
$cmds
EOF2
  if [ -n "$denied" ] && [ "$FAKE_PROBE" != run-anyway ]; then
    echo "The command was blocked by a hook."
    exit 0
  fi
  sh -c "$command"
  echo done
  exit 0
fi

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
tmux show-options -qv @admiral-name >"$d/name" 2>/dev/null
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
		statusline)
			# Runs the --settings file's statusLine command with the JSON on its stdin, as Claude does.
			settings=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--settings" ] && settings=$a
				prev=$a
			done
			command=""
			[ -n "$settings" ] && command=$(jq -r '.statusLine.command // empty' "$settings" 2>>"$d/statusline-error")
			if [ -z "$command" ]; then
				echo "no statusLine command in settings '$settings'" >>"$d/statusline-error"
			else
				printf '%s' "$arg" | sh -c "$command" >>"$d/statusline-stdout" 2>>"$d/statusline-stderr"
				echo "$?" >>"$d/statusline-exit"
			fi
			: >"$d/statusline-done"
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
		menu)
			: >"$d/cues-done"
			exec python3 "$(dirname "$0")/fakemenu.py" "$arg" "$d/stdin" <&3
			;;
		mcp)
			cfg=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--mcp-config" ] && cfg=$a
				prev=$a
			done
			if [ "$me" = codex ]; then
				cfg="$d/.mcp-config"
				codexmcpcfg "$@" >"$cfg"
			fi
			if [ "$me" = copilot ]; then
				cfg="$d/.mcp-config"
				case "$copilotmcp" in
				@*) cp "${copilotmcp#@}" "$cfg" ;;
				*) printf '%s' "$copilotmcp" >"$cfg" ;;
				esac
			fi
			while [ ! -e "$d/$arg" ]; do sleep 0.1; done
			mcpdrive call "$cfg" "$d/$arg" "$d/$arg.reply" </dev/null 2>>"$d/mcp-error" || echo "mcp cue $arg: the MCP call failed" >>"$d/mcp-error"
			;;
		copilotready)
			# A trusted Copilot TUI waiting for its first message: the composer shows and the folder is remembered in the chat home.
			[ -n "$COPILOT_HOME" ] && mkdir -p "$COPILOT_HOME" && { [ -e "$COPILOT_HOME/config.json" ] || printf '%s\n' '{"trustedFolders":["/fake"]}' >"$COPILOT_HOME/config.json"; }
			printf '%s\n' '❯' ' ← open sidebar · Interactive · Manual Approval · / commands · ? help · tab next tab    GPT-5.6 Luna'
			stty raw -echo <&3 2>/dev/null
			;;
		copilotreply)
			sid=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--session-id" ] && sid=$a
				prev=$a
			done
			case "$sid" in
			"" | */*) echo "copilotreply cue: no usable --session-id in argv" | tee "$d/reply-error" >&2; exit 1 ;;
			esac
			if [ -z "$COPILOT_HOME" ]; then
				echo "copilotreply cue: COPILOT_HOME is unset" | tee "$d/reply-error" >&2
				exit 1
			fi
			mkdir -p "$COPILOT_HOME" "$d/.copilot-state"
			[ -e "$COPILOT_HOME/config.json" ] || printf '%s\n' '{"trustedFolders":["/fake"]}' >"$COPILOT_HOME/config.json"
			printf '%s' "$sid" >"$d/.copilot-state/sid"
			plugin=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--plugin-dir" ] && plugin=$a
				prev=$a
			done
			# Like the real CLI: the first prompt of a session binds its transcript (sessionStart, source new), and /clear
			# ends the session at once and starts a new one that binds again only at the next prompt.
			cat >"$d/.append" <<'EOF5'
sleep "${FAKE_REPLY_DELAY:-0}"
st="$FAKE_STATE"
sid=$(cat "$st/sid")
path="$COPILOT_HOME/session-state/$sid/events.jsonl"
mkdir -p "$(dirname "$path")"
if [ "$(cat "$FAKE_MSG")" = /clear ]; then
  printf '%s\n' '{"type":"session.shutdown","data":{"totalNanoAiu":2500000000,"accountingSnapshot":{"totalPremiumRequestCost":3,"aiCreditsStatus":"complete"},"modelMetrics":{"fake":{"usage":{"inputTokens":150,"outputTokens":30,"cacheReadTokens":100,"cacheWriteTokens":40,"reasoningTokens":0}}}}}' >>"$path"
  k=$(($(cat "$st/clears" 2>/dev/null || echo 0) + 1))
  echo "$k" >"$st/clears"
  printf '00000000-0000-4000-8000-%012d' "$k" >"$st/sid"
  rm -f "$st/bound"
  exit 0
fi
if [ ! -e "$st/bound" ]; then
  # The plugin's own sessionStart hook binds the transcript, as Copilot runs it: camelCase payload on stdin.
  jq -r '.hooks.sessionStart[]? | .bash' "$FAKE_PLUGIN/hooks.json" | while IFS= read -r command; do
    jq -cn --arg s "$sid" --arg c "$(pwd -P)" '{sessionId: $s, timestamp: (now * 1000 | floor), cwd: $c, source: "new", initialPrompt: "x"}' | sh -c "$command" >>"$st/hook-out" 2>&1
  done
  : >"$st/bound"
fi
jq -cn --rawfile t "$FAKE_MSG" --arg r "$FAKE_REPLY" --arg n "$1" '
  {type: "user.message", id: ("fake-user-" + $n), parentId: null, timestamp: (now | todate), data: {content: $t}},
  {type: "assistant.message", id: ("fake-reply-" + $n), parentId: null, timestamp: (now | todate), data: {messageId: ("fake-m-" + $n), content: $r, toolRequests: []}},
  {type: "assistant.turn_end", id: ("fake-end-" + $n), parentId: null, timestamp: (now | todate), data: {turnId: "0"}}' >>"$path"
EOF5
			printf '%s\n' '❯' ' ← open sidebar · Interactive · Manual Approval · / commands · ? help · tab next tab    GPT-5.6 Luna'
			stty raw -echo <&3 2>/dev/null
			: >"$d/cues-done"
			export FAKE_REPLY_DELAY FAKE_REPLY=$arg FAKE_PLUGIN="$plugin" FAKE_STATE="$d/.copilot-state" FAKE_MSG="$d/.msg" FAKE_APPEND="$d/.append"
			cat -u <&3 | tee -a "$d/stdin" | awk 'BEGIN { RS = "\r" }
				{
					gsub("\033\\[[0-9;?]*[ -/]*[@-~]", ""); gsub(/\033/, "")
					if ($0 == "") next
					n++
					printf "%s", $0 > ENVIRON["FAKE_MSG"]
					close(ENVIRON["FAKE_MSG"])
					system("sh \"$FAKE_APPEND\" " n)
				}'
			exit
			;;
		codexready)
			# A trusted Codex TUI waiting for its first message: the composer shows and the hook's trust entry is in the chat home.
			mkdir -p "$CODEX_HOME"
			grep -qs ':session_start:' "$CODEX_HOME/config.toml" || printf '%s\n' "[hooks.state.\"$CODEX_HOME/config.toml:session_start:0:0\"]" 'trusted_hash = "sha256:fake"' >>"$CODEX_HOME/config.toml"
			printf '%s\n' '› Ask Codex to do anything' '  ← for agents · ? for shortcuts'
			stty raw -echo <&3 2>/dev/null
			;;
		replydelay) FAKE_REPLY_DELAY=$arg ;;
		events)
			sid=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--session-id" ] && sid=$a
				prev=$a
			done
			case "$sid" in
			"" | */*) echo "events cue: no usable --session-id in argv" | tee "$d/events-error" >&2; exit 1 ;;
			esac
			if [ -z "$COPILOT_HOME" ]; then
				echo "events cue: COPILOT_HOME is unset" | tee "$d/events-error" >&2
				exit 1
			fi
			mkdir -p "$COPILOT_HOME/session-state/$sid"
			n=$(($(cat "$d/.events-n" 2>/dev/null || echo 0) + 1))
			echo "$n" >"$d/.events-n"
			jq -cn --arg t "$arg" --arg n "$n" '
			  def line($type; $data): {type: $type, id: ("fake-" + $type + "-" + $n), parentId: null, timestamp: (now | todate), data: $data};
			  line("user.message"; {content: ("Question " + $n)}),
			  line("assistant.message"; {messageId: ("fake-m-" + $n), content: $t, toolRequests: []}),
			  line("assistant.turn_end"; {turnId: "0"})' >>"$COPILOT_HOME/session-state/$sid/events.jsonl"
			;;
		ask)
			# Copilot only: a pending ask_user call, <message>|<option>|<option>..., as the recorded oneOf form.
			sid=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--session-id" ] && sid=$a
				prev=$a
			done
			case "$sid" in
			"" | */*) echo "ask cue: no usable --session-id in argv" | tee "$d/events-error" >&2; exit 1 ;;
			esac
			[ -n "$COPILOT_HOME" ] || { echo "ask cue: COPILOT_HOME is unset" | tee "$d/events-error" >&2; exit 1; }
			mkdir -p "$COPILOT_HOME/session-state/$sid"
			jq -cn --arg a "$arg" '
			  ($a | split("|")) as $p
			  | {type: "tool.execution_start", id: "fake-ask", parentId: null, timestamp: (now | todate),
			     data: {toolCallId: "fake-ask-call", toolName: "ask_user", turnId: "0",
			            arguments: {message: $p[0], requestedSchema: {type: "object", required: ["answer"],
			              properties: {answer: {type: "string", title: "Answer", oneOf: ($p[1:] | map({const: ascii_downcase, title: .}))}}}}}}' >>"$COPILOT_HOME/session-state/$sid/events.jsonl"
			;;
		prompted)
			# Copilot only: the echo of a sent message, one user.message line holding <text>.
			sid=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--session-id" ] && sid=$a
				prev=$a
			done
			case "$sid" in
			"" | */*) echo "prompted cue: no usable --session-id in argv" | tee "$d/events-error" >&2; exit 1 ;;
			esac
			[ -n "$COPILOT_HOME" ] || { echo "prompted cue: COPILOT_HOME is unset" | tee "$d/events-error" >&2; exit 1; }
			mkdir -p "$COPILOT_HOME/session-state/$sid"
			jq -cn --arg t "$arg" '{type: "user.message", id: ("fake-echo-" + ($t | length | tostring)), parentId: null, timestamp: (now | todate), data: {content: $t}}' >>"$COPILOT_HOME/session-state/$sid/events.jsonl"
			;;
		copilothook)
			# Copilot only: run the hook commands of the plugin's hooks.json for <event> <json>, the payload on stdin,
			# in Copilot's recorded snake_case shape; a matcher is tested against notification_type or toolName.
			ev=${arg%% *}
			payload=${arg#"$ev"}
			plugin=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--plugin-dir" ] && plugin=$a
				prev=$a
			done
			subject=$(printf '%s' "$payload" | jq -r '.notification_type // .toolName // ""' 2>>"$d/event-error")
			: >"$d/.cmds"
			[ -n "$plugin" ] && jq -r --arg e "$ev" --arg s "$subject" '.hooks[$e][]? | (.matcher // "") as $m | select($m == "" or ($s | test($m))) | .bash' "$plugin/hooks.json" >"$d/.cmds" 2>>"$d/event-error"
			if [ ! -s "$d/.cmds" ]; then
				echo "no hook command for $ev in plugin '$plugin'" >>"$d/event-error"
			else
				while IFS= read -r command; do
					printf '%s' "$payload" | sh -c "$command" >>"$d/event-stdout" 2>>"$d/handler-copilot.stderr"
				done <"$d/.cmds"
			fi
			echo "$ev" >>"$d/events-done"
			;;
		reply)
			sid=""
			bad=""
			prev=""
			for a in "$@"; do
				[ "$prev" = "--session-id" ] && sid=$a
				prev=$a
			done
			case "$sid" in
			"" | */*) bad="reply cue: no usable --session-id in argv" ;;
			*) [ -n "$CLAUDE_CONFIG_DIR" ] || bad="reply cue: CLAUDE_CONFIG_DIR is unset" ;;
			esac
			if [ -n "$bad" ]; then
				echo "$bad" | tee "$d/reply-error" >&2
				exit 1
			fi
			mkdir -p "$CLAUDE_CONFIG_DIR/projects/zz"
			cat >"$d/.append" <<'EOF3'
sleep "${FAKE_REPLY_DELAY:-0}"
jq -cn --rawfile t "$FAKE_MSG" --arg r "$FAKE_REPLY" --arg n "$1" '
  {uuid: ("fake-user-" + $n), type: "user", timestamp: (now | todate), message: {role: "user", content: $t}},
  {uuid: ("fake-reply-" + $n), type: "assistant", timestamp: (now | todate), message: {role: "assistant", content: [{type: "text", text: $r}]}}' >>"$FAKE_TRANSCRIPT"
EOF3
			stty raw -echo <&3 2>/dev/null
			: >"$d/cues-done"
			export FAKE_REPLY_DELAY FAKE_REPLY=$arg FAKE_TRANSCRIPT="$CLAUDE_CONFIG_DIR/projects/zz/$sid.jsonl" FAKE_MSG="$d/.msg" FAKE_APPEND="$d/.append"
			# awk splits at CR, the byte Enter sends in raw mode; the message goes to a file so jq reads it verbatim.
			cat -u <&3 | tee -a "$d/stdin" | awk 'BEGIN { RS = "\r" }
				{
					gsub("\033\\[[0-9;?]*[ -/]*[@-~]", ""); gsub(/\033/, "")
					if ($0 == "") next
					n++
					printf "%s", $0 > ENVIRON["FAKE_MSG"]
					close(ENVIRON["FAKE_MSG"])
					system("sh \"$FAKE_APPEND\" " n)
				}'
			exit
			;;
		codexreply)
			if [ -z "$CODEX_HOME" ]; then
				echo "codexreply cue: CODEX_HOME is unset" | tee "$d/reply-error" >&2
				exit 1
			fi
			path="$CODEX_HOME/sessions/2026/11/12/rollout-2026-11-12T10-00-00-$sess.jsonl"
			# A trusted hook leaves this entry in the chat home's config.toml; a first turn then binds the transcript, as
			# Codex's SessionStart does, so the option and the rollout appear only when the first message arrives.
			mkdir -p "$CODEX_HOME"
			grep -qs ':session_start:' "$CODEX_HOME/config.toml" || printf '%s\n' "[hooks.state.\"$CODEX_HOME/config.toml:session_start:0:0\"]" 'trusted_hash = "sha256:fake"' >>"$CODEX_HOME/config.toml"
			cat >"$d/.append" <<'EOF4'
sleep "${FAKE_REPLY_DELAY:-0}"
if [ "$1" = 1 ]; then
  mkdir -p "$(dirname "$FAKE_TRANSCRIPT")"
  printf '%s\n' '{"timestamp":"2026-11-12T10:00:00.000Z","ordinal":0,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":500,"cached_input_tokens":200,"cache_write_input_tokens":0,"output_tokens":60,"reasoning_output_tokens":20,"total_tokens":560}}}}' >>"$FAKE_TRANSCRIPT"
  tmux set -p -t "$TMUX_PANE" @admiral-transcript "1700000000000000000 startup $FAKE_TRANSCRIPT"
fi
jq -cn --rawfile t "$FAKE_MSG" --arg r "$FAKE_REPLY" --argjson n "$1" '
  {timestamp: (now | todate), ordinal: ($n * 2 + 1), type: "event_msg", payload: {type: "item_completed", item: {type: "UserMessage", id: ("fake-user-" + ($n | tostring)), content: [{text: $t}]}}},
  {timestamp: (now | todate), ordinal: ($n * 2 + 2), type: "event_msg", payload: {type: "item_completed", item: {type: "AgentMessage", id: ("fake-reply-" + ($n | tostring)), content: [{text: $r}]}}}' >>"$FAKE_TRANSCRIPT"
EOF4
			printf '%s\n' '› Ask Codex to do anything' '  ← for agents · ? for shortcuts'
			stty raw -echo <&3 2>/dev/null
			: >"$d/cues-done"
			export FAKE_REPLY_DELAY FAKE_REPLY=$arg FAKE_TRANSCRIPT="$path" FAKE_MSG="$d/.msg" FAKE_APPEND="$d/.append"
			cat -u <&3 | tee -a "$d/stdin" | awk 'BEGIN { RS = "\r" }
				{
					gsub("\033\\[[0-9;?]*[ -/]*[@-~]", ""); gsub(/\033/, "")
					if ($0 == "") next
					n++
					printf "%s", $0 > ENVIRON["FAKE_MSG"]
					close(ENVIRON["FAKE_MSG"])
					system("sh \"$FAKE_APPEND\" " n)
				}'
			exit
			;;
		usage)
			raw=$(tmux show-options -pqv -t "$TMUX_PANE" @admiral-transcript)
			case "$me" in
			codex)
				path="${CODEX_HOME:-$HOME/.codex}/sessions/2026/11/11/rollout-2026-11-11T15-22-11-$sess.jsonl"
				mkdir -p "$(dirname "$path")"
				printf '%s\n' '{"timestamp":"2026-11-11T20:20:00.000Z","ordinal":1,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":500,"cached_input_tokens":200,"cache_write_input_tokens":0,"output_tokens":60,"reasoning_output_tokens":20,"total_tokens":560}}}}' >>"$path"
				tmux set -p -t "$TMUX_PANE" @admiral-transcript "1700000000000000000 startup $path"
				;;
			*)
				path=${raw#* * }
				if [ -z "$raw" ] || [ "$path" = "$raw" ]; then
					echo "usage cue: no @admiral-transcript on the pane" | tee "$d/usage-error" >&2
					exit 1
				fi
				mkdir -p "$(dirname "$path")"
				if [ "$me" = claude ]; then
					printf '%s\n' '{"type":"cost-state","totalCostUSD":0.25,"modelUsage":{"claude-fake":{"inputTokens":100,"outputTokens":50,"thinkingTokens":10,"cacheReadInputTokens":300,"cacheCreationInputTokens":40}}}' >>"$path"
				else
					printf '%s\n' '{"type":"session.shutdown","data":{"totalNanoAiu":2500000000,"accountingSnapshot":{"totalPremiumRequestCost":3,"aiCreditsStatus":"complete"},"modelMetrics":{"fake":{"usage":{"inputTokens":150,"outputTokens":30,"cacheReadTokens":100,"cacheWriteTokens":40,"reasoningTokens":0}}}}}' >>"$path"
				fi
				;;
			esac
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
