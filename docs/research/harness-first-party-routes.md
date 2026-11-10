# The most first-party route to each harness's conversation

Follow-up to [harness-transcripts.md](https://github.com/jbuffin/admiral/blob/research/harness-transcripts/docs/research/harness-transcripts.md) (branch `research/harness-transcripts`), which settled what each transcript file holds. This note asks a narrower question: for each harness, what is the most first-party, supported way to get a ship's conversation as structured data while the interactive TUI keeps running in tmux and stays attachable? Screen scraping is out. Reading the transcript file is the floor.

Versions on this Mac on 2026-11-10: Claude Code 2.1.322, Claude Agent SDK (TypeScript) 0.3.322, GitHub Copilot CLI 1.0.108, `@github/copilot-sdk` 1.0.31, codex-cli 0.166.0 (latest release 0.167.0). Sources read at: `github/copilot-sdk` main `8aed714`, `microsoft/vscode` main `93c79de15`, `openai/codex` main `627b57921`.

Marks: **documented** means the vendor's docs or `--help` say so. **from source** means the vendor's own code or in-repo docs say so (`file:line` or file name given). **observed** means a probe on this Mac showed it. Probes ran in a scratch git repo under a private tmux socket.

## Summary

| | Best route that keeps the TUI attachable | Live? | Fallback |
| --- | --- | --- | --- |
| Claude Code | Transcript JSONL (read directly or through the Agent SDK's `getSessionMessages()`), plus hooks. OpenTelemetry events as an optional, documented supplement | Per finished content block; OTel batches every 5 s | Same file |
| Copilot CLI | **Launch the TUI with `--ui-server --port N`** and attach a Copilot SDK client to it: `getForegroundSessionId()`, `resumeSession()`, `session.on()` | Yes: deltas, tool output, `elicitation.requested` and permissions as they happen | `events.jsonl` plus hooks (still written in `--ui-server` mode) |
| Codex | App-server client (`thread/resume` on the TUI's app-server, TUI launched with `--remote`) | Yes | Rollout JSONL plus hooks |

The big change from the earlier note is Copilot: the TUI has a hidden TUI+server mode that the Copilot SDK documents, and a second client sees the full live event stream while the TUI keeps working. For Claude Code nothing local and supported gives more than the file: the Agent SDK's reader is documented but reads the same file and drops fields Admiral needs.

## Claude Code

### Agent SDK session functions

- The SDK exports `listSessions()`, `getSessionInfo()`, `getSessionMessages()`, `listSubagents()`, `getSubagentMessages()`, `forkSession()`, `renameSession()`, `tagSession()` and `deleteSession()` (Python: `list_sessions()`, `get_session_messages()` and so on). The sessions guide says: "Use them to build custom session pickers, cleanup logic, or transcript viewers." Documented: [agent-sdk/sessions](https://code.claude.com/docs/en/agent-sdk/sessions), [agent-sdk/typescript](https://code.claude.com/docs/en/agent-sdk/typescript).
- `getSessionMessages()` "Reads user and assistant messages from a past session transcript." It returns `SessionMessage[]`: `type` (`user`, `assistant`, or `system` with `includeSystemMessages`), `uuid`, `session_id`, `message` typed `unknown` ("Raw message payload from the transcript"), `parent_tool_use_id`, `parent_agent_id`. Documented. The envelope is documented; the payload is not.
- It is a one-shot read. There is no watch, tail or subscribe function in the SDK's types (`sdk.d.ts` has none). From source (the published `sdk.d.ts`).
- **It works on a session that is still open.** Run against this Mac's live interactive session (the one that launched this research), it returned 39 messages up to the latest assistant text, plus `getSessionInfo()` metadata (`summary`, `cwd`, `gitBranch`, `lastModified`). Observed. It reads the same file, so it is exactly as fresh as the file: one entry per finished content block.
- **Its shape is thinner than the raw file, not steadier.** In the same probe, `message` held only `{role, content}`. The top-level `toolUseResult` (structured `stdout`, `stderr`, `interrupted`) was gone, though the `tool_result` block's text survives. The two `system` entries (the file holds `turn_duration` and `stop_hook_summary` lines) came back as bare envelopes with no `message`, no `subtype` and no `durationMs`. Observed. So "Worked for N" and the folded tool output need the raw file anyway.
- With a session store, `getSessionMessages` "returns the linked message chain the agent would see on resume. After auto-compaction, earlier turns are replaced by a summary." Documented: [agent-sdk/session-storage](https://code.claude.com/docs/en/agent-sdk/session-storage). Whether the local-file path does the same was not checked.
- Using it means running Node or Python beside the Go server. It is a library over the file, so it buys a documented envelope and loses fields.

### Session stores

`SessionStore` (`append`, `load`, optional `listSessions`, `listSubkeys` and others) mirrors transcripts to a backend. It is an option on `query()` and `startup()` only: "The Claude Code subprocess always writes each batch of transcript entries to local disk first, and the SDK then forwards the same batch to your store's `append()`." Entries are to be treated "as opaque JSON-safe values". Documented: [session-storage](https://code.claude.com/docs/en/agent-sdk/session-storage). No setting or flag attaches a store to an interactive `claude` TUI. Not usable for ships.

### OpenTelemetry

A documented, interactive-session route with documented event schemas. Configured with env vars on the ship, exported to an OTLP endpoint Admiral could run on loopback. Documented: [monitoring-usage](https://code.claude.com/docs/en/monitoring-usage).

- `claude_code.user_prompt`: `prompt` / `prompt_text` (redacted unless `OTEL_LOG_USER_PROMPTS=1`), `message.uuid` "matching the persisted transcript entry".
- `claude_code.assistant_response`: "Logged after each API request that returns text content", text blocks only, `response` redacted unless `OTEL_LOG_ASSISTANT_RESPONSES=1` (or the prompts flag), truncated at 60 KB, with `message.uuid`.
- `claude_code.tool_decision` and `claude_code.tool_result`: tool name, `tool_use_id` (matches hooks), `success`, `duration_ms`, and with `OTEL_LOG_TOOL_DETAILS=1` a `tool_input` capped at about 4 K characters. **No tool output**: that is only in the `tool.output` span event, which needs the beta tracing flags and `OTEL_LOG_TOOL_CONTENT=1`.
- `claude_code.compaction`, `subagent_completed`, `hook_execution_start` / `_complete`, `permission_mode_changed`.
- Logs export every 5 s by default (`OTEL_LOGS_EXPORT_INTERVAL`, default 5000).
- No event carries a pending `AskUserQuestion` with its options, or a turn-level duration. `AskUserQuestion` calls are listed among calls that have no safety event.

Verdict: steadier and documented, but less complete than the file (no tool output, no question options) and batched. Useful as a cross-check or for user text without parsing, not as the conversation source. Not probed.

### Other first-party routes

- **`--output-format stream-json` / `--input-format stream-json`** are `--print` only (`claude --help`). Documented. A separate headless process; no TUI.
- **Remote Control** (`claude --remote-control`, `/remote-control`) keeps the terminal session live and "in sync across all connected devices", but "All traffic travels through the Anthropic API", needs a claude.ai subscription ("API keys are not supported"), and opens no local port. Documented: [remote-control](https://code.claude.com/docs/en/remote-control). The Agent SDK's `@anthropic-ai/claude-agent-sdk/bridge` export (`attachBridgeSession`, `@alpha`) is the worker side of that relay to Anthropic's servers ("Session state reported to the CCR /worker endpoint"). From source (`bridge.d.ts`). Not a local observation route.
- **Agent view / background sessions** (`claude --bg`, `claude attach`, `claude agents --json`). A supervisor hosts sessions; `claude agents --json` gives `state` (`working`, `blocked`, `done`, …), `status` and `waitingFor` (`permission prompt`, `input needed`, …) per session. Documented: [agent-view](https://code.claude.com/docs/en/agent-view). It is a research preview, it reports status not conversation, the page documents no API for reading a background session's conversation, and it warns that the files under `~/.claude/jobs/<id>/` "are not a stable interface". Interesting as a status source; it would also replace tmux as the host, which is a bigger change.
- **Channels** (`notifications/claude/channel/permission_request`) relay each pending tool approval to a local MCP server with `tool_name`, `description`, `input_preview`, and accept an allow or deny "whichever answer arrives first", while the terminal dialog stays open. Documented: [channels-reference](https://code.claude.com/docs/en/channels-reference). It sees no conversation, is a research preview, and a non-allowlisted channel needs `--dangerously-load-development-channels`, which shows a full-screen warning at startup. A possible approval route later; not a conversation route.
- **IDE integration** (`--ide`): the CLI connects to an MCP server the editor runs, to get diffs and selections. It does not publish the conversation. The VS Code extension runs its own SDK sessions rather than reading a terminal's. Documented (`claude --help`, [vs-code](https://code.claude.com/docs/en/vs-code)); not investigated further.

### Recommendation

**Claude Code: read the transcript JSONL directly with hooks for the path and the pending approval, as now; fall back to nothing better, since `getSessionMessages()` reads the same file with fewer fields.** OTel is the only documented schema for live events and is worth keeping in mind as a cross-check, not a source.

## Copilot CLI

### TUI+server mode: `--ui-server`

- The Copilot SDK documents `client.getForegroundSessionId()`: "Get the ID of the session currently displayed in the TUI. Only available when connecting to a server running in TUI+server mode (`--ui-server`)", and `setForegroundSessionId()`, and lifecycle events `session.foreground` / `session.background`. From source: `github/copilot-sdk` `nodejs/README.md` and `nodejs/src/client.ts:3792-3842` (the same in the Go, .NET, Python and Rust clients).
- `--ui-server` is not in `copilot --help`, but the 1.0.108 parser accepts it (an unknown flag errors; this one does not). Observed.
- The SDK connects to a running runtime with `RuntimeConnection.forUri(url, { connectionToken? })`. Documented in the SDK README. The headless server "only accepts connections from loopback (`127.0.0.1`), where it needs no authentication" by default; a non-loopback `--host` requires `COPILOT_CONNECTION_TOKEN`. From source: `docs/setup/backend-services.md`.
- `session.on()` delivers every event type, ephemeral ones included (deltas, `tool.shell_output`, `elicitation.requested`, `session.idle`), which the file never holds. The event schemas are documented in `docs/features/streaming-events.md`. From source.

**Observed end to end.** `copilot --ui-server --port 4329 --sync-session=off` in tmux showed the normal TUI (folder trust dialog, then the prompt) and listened on `127.0.0.1:4329`, with a "Server listening on port 4329" line in the TUI. A 40-line Node client (`@github/copilot-sdk` 1.0.31) connected, read the foreground session ID before any prompt, called `resumeSession(id, { suppressResumeEvent: true, continuePendingWork: true })` with no permission or input handlers, and subscribed. A prompt typed into the TUI (ask a Red or Blue question with `ask_user`, then run `echo hello`) produced, live at the second client:

- `user.message`, `assistant.turn_start` / `turn_end` per model call, `assistant.message` with `toolRequests`;
- `assistant.tool_call_delta` while the `ask_user` input streamed, then `tool.execution_start` for `ask_user` with `message` and the `enum` schema, then **`elicitation.requested`** (ephemeral) with `requestId`, `message`, `requestedSchema`;
- after the answer in the TUI: `human_response.recorded`, `elicitation.completed` (`action: accept`, `content: {color: "Red"}`), `tool.execution_complete`;
- for the shell call: `tool.execution_start` (`command`, `description`), `tool.shell_output` (`stdout`, `hello\n`), `tool.execution_partial_result`, `tool.execution_complete` with `shellExecution.exitCode`;
- `assistant.message_start`, four `assistant.message_delta` with text, `assistant.message`, `assistant.idle`, `session.idle`.

The TUI rendered and answered normally throughout, and `events.jsonl` was still written for the session. Observed. The command was approved automatically (Assisted Permissions), so a `permission.requested` reaching the second client was not seen.

Costs and open points:

- `--ui-server` is undocumented in the CLI's own help, so it can change without notice. The SDK feature that depends on it is documented across all six SDKs, which makes it more likely to stay than a private flag.
- A client that registers `onPermissionRequest` or `onUserInputRequest` could answer for the engineer. Admiral's observer must register neither. Whether the SDK auto-answers anything when handlers are absent was not seen: the README says permission requests are then "surfaced as events and left pending".
- The loopback port takes connections from any local process without a token by default. Whether `--ui-server` honours `COPILOT_CONNECTION_TOKEN` was not tested. Admiral needs a port per ship.
- The Copilot SDK is Node, Python, Go, .NET, Java and Rust. A Go client exists (`github/copilot-sdk/go`), so Admiral's server could use it in-process. Not tried.
- Behaviour with `--session-id` and on `/new`, `/clear` or `/resume` inside the TUI (the foreground changes; `session.foreground` should say so) was not probed.

### ACP and AHP

- `copilot --acp` is a stdio Agent Client Protocol server; clients can opt in to `github.com/copilot/sessionEvent` notifications. Documented: `copilot help acp`. It is its own process; the TUI does not attach to it.
- The Agent Host Protocol: `copilot --host-ahp` serves an AHP 1.0 WebSocket endpoint. With an `AHP_CLIENT` feature flag the TUI gains `/ahp start` and `/remote share`, which "publishes the foreground local session" on an in-process listener. From source: `docs/runtime-supervised-host.md`. On this Mac `copilot --host-ahp` printed `AHP 1.0 endpoint: ws://127.0.0.1:8765/?tkn=…` and showed no TUI, and `/ahp` is not a command in the TUI (the flag is off for this account). Observed. Not available to Admiral today.

### Remote control and Mission Control

`/remote on` steers a session "from GitHub on the web or mobile", and session history is "synced read-only to your GitHub account" when the account allows it. Documented: `copilot help sessions`. On this Mac a plain `copilot` start showed "Remote control status: connected" with a `github.com/copilot/tasks/<id>` link. Observed. It goes through GitHub's servers; there is no documented API for a third party to read that stream. Not usable.

### How GitHub's own apps read a CLI session

- VS Code's Copilot CLI integration runs its own sessions through `@github/copilot/sdk` in-process ("VS Code provides the UI, Copilot CLI SDK provides the agent") and reads history from `~/.copilot/session-state/<id>/events.jsonl`. "External sessions are never listed": sessions started in a terminal or by `copilot -p` are filtered out. From source: `microsoft/vscode` `extensions/copilot/src/extension/chatSessions/copilotcli/AGENTS.md`, `src/vs/workbench/contrib/chat/browser/copilotCliEventsUri.ts` (`buildLocalEventsUri`). VS Code also has an agent-host (AHP) path for `copilotcli` sessions.
- GitHub web and mobile get CLI sessions through Mission Control (above).

So GitHub's own clients either own the session through the SDK or read `events.jsonl`; none of them attaches to someone else's terminal session, except through `--ui-server`, which is what the SDK's foreground API exists for.

### Recommendation

**Copilot: launch each ship's TUI with `--ui-server --port <ship port>` and attach a Copilot SDK client that observes the foreground session without registering handlers; fall back to `events.jsonl` plus hooks, which `--ui-server` still writes.**

## Codex (status only)

- The app-server is still experimental: `codex app-server --help` reads "[experimental] Run the app server or related tooling", and so does `codex remote-control`. Observed (0.166.0). "WebSocket transport is experimental and unsupported"; the stdio transport and omitting `experimentalApi` keep a client "on the stable API surface". Documented: [app-server docs](https://learn.chatgpt.com/docs/app-server). The daemon "is experimental and its lifecycle contract may change". From source: `codex-rs/app-server-daemon/README.md` at `627b57921`.
- `thread/resume` "reopen[s] an existing thread by id"; the docs speak of a thread's "last subscriber", so several connections can subscribe. Documented. They do not say what resuming a thread another client has loaded does; the earlier note covered that from source and saw it work (a second client got deltas and items while the TUI ran).
- `--remote <ADDR>` still connects the TUI to an app-server (`ws://`, `wss://`, `unix://`), now with `--remote-auth-token-env`. Documented: `codex --help`.
- Rollout files are still written. `persisted_rollout_item` keeps writing `ItemCompleted` for "New, paginated rollouts", and still never writes `ExecApprovalRequest` or `RequestUserInput`. From source: `codex-rs/rollout/src/policy.rs` at `627b57921`. `thread/archive` moves "the persisted thread log (stored as a JSONL file on disk)". Documented. Today's rollouts exist under `~/.codex/sessions/2026/11/10/`. Observed.

**Codex: app-server client on an Admiral-owned app-server with the TUI on `--remote`; fall back to rollout JSONL plus hooks.** Adopting it is a separate decision.

## Unconfirmed

- Copilot: a `permission.requested` (and `permission.completed` for a human answer) reaching the `--ui-server` observer; whether an observer without handlers can ever settle a request; `COPILOT_CONNECTION_TOKEN` with `--ui-server`; foreground changes on `/new`, `/clear`, `/resume`; the Go SDK against `--ui-server`.
- Claude Code: whether local `getSessionMessages()` returns the post-compaction chain; OTel events from a ship in practice (not probed).
- Codex: what resuming a running thread from a second client does, beyond the earlier probe, is not in the docs.

## Sources

- Claude Code: [agent-sdk/sessions](https://code.claude.com/docs/en/agent-sdk/sessions), [agent-sdk/typescript](https://code.claude.com/docs/en/agent-sdk/typescript), [agent-sdk/session-storage](https://code.claude.com/docs/en/agent-sdk/session-storage), [monitoring-usage](https://code.claude.com/docs/en/monitoring-usage), [remote-control](https://code.claude.com/docs/en/remote-control), [agent-view](https://code.claude.com/docs/en/agent-view), [channels-reference](https://code.claude.com/docs/en/channels-reference), `claude --help`; `@anthropic-ai/claude-agent-sdk` 0.3.322 `sdk.d.ts`, `bridge.d.ts`.
- Copilot: `copilot --help`, `copilot help acp`, `copilot help sessions`, `copilot help monitoring`; `github/copilot-sdk` at `8aed714`: `nodejs/README.md`, `nodejs/src/client.ts`, `docs/setup/backend-services.md`, `docs/features/streaming-events.md`, `docs/features/remote-sessions.md`, `docs/runtime-supervised-host.md`; `@github/copilot-sdk` 1.0.31 `dist/types.d.ts`; `microsoft/vscode` at `93c79de15`: `extensions/copilot/src/extension/chatSessions/copilotcli/AGENTS.md`, `src/vs/workbench/contrib/chat/browser/copilotCliEventsUri.ts`.
- Codex: [app-server docs](https://learn.chatgpt.com/docs/app-server), `codex --help`, `codex app-server --help`; `openai/codex` at `627b57921`: `codex-rs/app-server/README.md`, `codex-rs/rollout/src/policy.rs`, `codex-rs/app-server-daemon/README.md`.
- Probes: on this Mac, 2026-11-10, as described in each section.
