# PROTOTYPE: ConPTY session host → bridge stub → xterm.js (#876)

Throwaway. Lives only on `prototype/windows-conpty-876`; never merge. It answers
"Prototype: do Claude Code and Codex work in a ConPTY through to the web
terminal?" (jbuffin/admiral#876).

It is its own Go module, so none of its dependencies reach Admiral's `go.mod`.

## Run (Windows 11 24H2+)

```
go build -o proto.exe . && ./proto.exe run
```

Open http://127.0.0.1:7876. `run` starts `proto.exe host` detached
(`DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP | CREATE_BREAKAWAY_FROM_JOB`) if no host is
on the pipe, then serves the bridge stub. Kill the bridge and run it again: the host and
its panes stay.

- `host`: owns the pseudoconsoles (`charmbracelet/x/conpty` v0.2.0, `ReleasePseudoConsole`
  when kernel32 has it, one kill-on-close job per pane), one `tuios.dev/vt` v0.3.3
  emulator per pane, and a `go-winio` pipe locked to the user's SID. Every pane gets
  `ADMIRAL_PANE=<id>`.
- `bridge`: an HTTP server with the page, `/api/*` and `/ws/bridge/<pane>`, speaking a
  cut-down `docs/bridge-protocol.md` (`join`/`resize`/`input`; `join` snapshot, binary
  output, `size`). The page replays `join` exactly as `web/src/bridgeReplay.ts` does,
  and sends ESC CR for Shift+Enter as `web/src/terminal.ts` does.
- Presets: `claude` (with `--settings` hooks in exec and shell form), `codex` (with
  `-c hooks.*`, `notify` and `tui.notification_method="bel"` as `internal/launcher`
  passes them), `pwsh`, `cmd`. Hooks run `probe.js`, which records the env it got.
- `drive.mjs` and `t*.mjs` drive the bridge from Node to take the measurements.

Logs (PROTOTYPE, wipe me): `%LOCALAPPDATA%\admiral-proto-876-WIPE-ME\`. Each pane has
`*.out.bin` (raw ConPTY output), `*.events.jsonl` (queries, replies, modes, bells,
titles, sizes), `*.in.jsonl` (input). There is also `hooks.jsonl` and `host.log`.
