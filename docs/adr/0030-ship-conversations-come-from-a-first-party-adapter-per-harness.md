# 30. Ship conversations come from a first-party adapter per harness

Status: accepted

## Context

The redesign's bridge opens on the ship's conversation ([#208](https://github.com/jbuffin/admiral/issues/208), [ADR 0029](0029-the-bridge-answers-simple-prompts-by-sending-the-harness-keys.md)). Admiral reads none today. [ADR 0019](0019-the-chat-is-carried-by-its-transcript-and-tmux.md) reads Claude Code's transcript for the chat only, never for a ship. [ADR 0021](0021-crew-boats-record-their-transcript-path.md) records a crew boat's transcript path but never opens the file. [ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md) records no conversation IDs.

Research for [#231](https://github.com/jbuffin/admiral/issues/231) found that every harness can give a structured conversation without scraping the screen, and a follow-up on the `research/harness-first-party-routes` branch checked which supported routes run beside the live TUI. [#238](https://github.com/jbuffin/admiral/issues/238) decided the rule and the adapters, and [#239](https://github.com/jbuffin/admiral/issues/239) decided Codex's route.

## Decision

**Admiral reads every ship's conversation through one adapter per harness, each on the most first-party route that runs beside the live TUI. The TUI in tmux stays the ship, the terminal stays the fallback, and hooks still own state and approvals.**

- **The rule.** A route must read, subscribe or attach as a second client beside the TUI. A route that drops the TUI (a headless SDK session with Admiral as its only client) is out, since it would remove the real terminal and reverse ADRs 0002, 0012 and 0013. Screen scraping stays out. The transcript file is the floor, and the Terminal tab is the fallback when nothing parses.
- **Claude Code: the transcript JSONL plus hooks.** Ships launch with `--session-id`, so Admiral knows the path before the first hook. No supported route gives more: the Agent SDK's `getSessionMessages()` reads the same file with fewer fields, OpenTelemetry has no tool output or question options, and Remote Control goes through Anthropic's servers.
- **Copilot: `--ui-server` plus an observing client, with `events.jsonl` plus hooks as the fallback.** Ships launch with `--session-id` and `--ui-server --port N` on a port Admiral picks. Admiral attaches an SDK client that registers no permission or input handlers, so it can never answer for the engineer, and gets the conversation live, deltas included. When the server can't be reached, the adapter reads `events.jsonl`.
- **Codex: a per-ship app-server, with the rollout file plus hooks as the fallback.**
  - A small `admiral` wrapper in the ship's pane starts `codex app-server` as its child, waits about 5 s for the socket, then runs `codex --remote unix://…` in the foreground. Admiral connects an observing client that registers no handlers and never answers over the protocol.
  - **Per ship, not per fleet.** With `--remote` the agent runs in the app-server process, so its commands and hooks run there, with the server's environment. Admiral's hooks find their ship through `$TMUX_PANE` and `ADMIRAL_SHIP`, and ships carry `ADMIRAL_AUTOPILOT`, `ADMIRAL_REPORT` and others. A server started inside the pane inherits all of that; one shared by a fleet would lose it.
  - **Fallback.** If the server doesn't come up, or the installed `codex` has no `app-server`, the wrapper runs plain `codex`. If Admiral's client can't complete `initialize` and `thread/resume`, the adapter reads the rollout through the hook's `transcript_path`. A ship always launches; a broken app-server only costs live text.
  - **Socket.** `/tmp/admiral-<uid>/<short-hash>.sock`, in a directory with mode 0700, which keeps the path under macOS's 104-byte limit. Its path is in the pane option `@admiral-codex-socket`.
  - **Lifetime.** The server lives exactly as long as the TUI. When the TUI exits, the wrapper kills the server and deletes the socket. Each feedback round of a moored ship gets a new wrapper and server. Nothing resumes an old server.
  - **The engineer's own `--remote` stays refused** by the launcher. Admiral adds its own after that check.
  - `item/tool/requestUserInput` brings a pending Codex question and its options. The app-server's `requestApproval` only adds the details shown with an approval.
- **Finding the conversation.** `--session-id` where the harness takes one (Claude Code, Copilot), the hook's `transcript_path` for Codex. The path or id sits in the pane option `@admiral-transcript` for every session, with [ADR 0019](0019-the-chat-is-carried-by-its-transcript-and-tmux.md)'s checks per harness root (`projects/`, `sessions/`, `session-state/`): no symlinks, a regular file.
- **One conversation model.** Every adapter emits the same items: user message; assistant text; tool call with its output folded in; pending question with its options (not from Codex's rollout); turn boundary with "worked for N"; compaction marker. Thinking and system or meta lines are dropped. A subagent's run is one tool-call item, and its separate file isn't read.
- **State and approvals come from hooks only**, as [ADR 0007](0007-needs-you-comes-from-harness-hooks.md) says: `PermissionRequest` for Claude Code and Codex, Copilot's `notification` hook. "Working" between items comes from hooks too.
- **Freshness.** Copilot's and Codex's servers give live text. The file routes update per finished item. Watching anything else live means the Terminal tab.
- **Who uses the adapters.** Ships, crew boats and AdmiralBot. The chat moves onto the Claude Code adapter but keeps its text-only filter.
- **Lifetimes.** An anchored ship's conversation stays readable until it is dismissed. A moored ship's feedback rounds each record their path in `$ADMIRAL_HOME/moored/<ship>.json`, and the bridge shows the rounds in order with a divider. Admiral stores paths and never copies the files. A file the harness has since removed shows "conversation no longer available".
- **Format drift.** Each adapter skips what it doesn't know and never fails the view. A recorded path with nothing parseable shows "Admiral can't read this conversation" and points to the terminal. Each adapter has one test against a recorded sample.

## Considered options

- **The best first-party route beside the TUI, per harness** (chosen). A live view where the harness offers one, the file where it doesn't, and the real terminal kept in every case.
- **The transcript file for every harness.** Simpler, but Copilot and Codex offer live text from their own servers.
- **A headless SDK session with Admiral as the only client.** It drops the TUI the engineer can open, and with it ADRs 0002, 0012 and 0013.
- **One Codex app-server per fleet.** Hooks and commands would run with the server's environment instead of the ship's, and lose the ship.
- **Reading the screen.** It changes with every release, and scrollback is not a conversation.

## Consequences

- This supersedes [ADR 0019](0019-the-chat-is-carried-by-its-transcript-and-tmux.md)'s chat-only scope: Admiral reads ships' and crew boats' conversations as well as the chat's. It supersedes [ADR 0021](0021-crew-boats-record-their-transcript-path.md)'s rule that Admiral never opens the file: a crew boat's conversation is read from its recorded path.
- It amends [ADR 0012](0012-ships-live-in-tmux-and-the-rest-in-admiral-home.md): session ids live in pane options and in the moored file, and Admiral never resumes from them, so #230's rejection of resuming holds.
- It amends [ADR 0013](0013-each-harness-starts-with-the-prompt-on-its-command-line.md): Claude Code and Copilot ships launch with `--session-id`, Copilot ships with `--ui-server --port N`, and Codex ships through Admiral's wrapper with Admiral's own `--remote`.
- **Accepted risks.** Copilot's port and Codex's socket take any local client without a token; Admiral controls which fleet launches which ship. Codex's app-server is experimental, and a broken one falls back to the rollout.
- **Windows.** A future native Windows host (out of scope) keeps the pattern, since the server runs inside the ship's session. If Codex's `unix://` doesn't work there, the documented alternative is `ws://127.0.0.1:<port>` with `--remote-auth-token-env`. The address stays inside the Codex adapter and the wrapper.
- **Unverified, to check while building:** a Copilot permission prompt reaching the observing client; Copilot's server across `/new`, `/clear` and `/resume`; a Go client for Copilot's server (only the Node SDK was observed); that `--ui-server` stays accepted, since `copilot --help` doesn't list it; that hooks from a `--remote` Codex TUI see the pane's `TMUX_PANE`; an approval or `requestUserInput` reaching Codex's observing client while the TUI still answers it; a WebSocket client over the Unix socket in Go; `unix://` on Windows.
