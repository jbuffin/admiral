# Can each harness give a structured transcript?

Research for [#231](https://github.com/jbuffin/admiral/issues/231), feeding the conversation-first bridge on the redesign map [#206](https://github.com/jbuffin/admiral/issues/206). November 2026. Versions on this Mac on 2026-11-10: Claude Code 2.1.322, codex-cli 0.166.0 (the shared app-server daemon running here was 0.165.0), GitHub Copilot CLI 1.0.107. Codex source read at `openai/codex` main, commit `c875667e`; Copilot SDK at `github/copilot-sdk` main, commit `c07cf0e`.

Question: can Claude Code, Codex CLI and Copilot CLI give Admiral a conversation view (user and assistant messages, tool calls with folded output, the pending question with its options, turn boundaries, "worked for N minutes") without scraping the terminal, and how fresh is it? Ships stay interactive TUIs in tmux, so a headless mode only counts if it can run beside the TUI or replace it.

Marks: **confirmed** means a primary source says so (doc URL, source `file:line`, `--help` output). **observed** means a probe on this Mac showed it: each harness ran in a tmux session on a private socket in a scratch git repo, and was sent three or four short prompts (a two-option question, a shell command, a 600-word essay), while the file was polled every 2 s. **undocumented** means the vendor gives no format for it. **unconfirmed** means nothing settles it. Codex source is written `codex:<path>:<line>`, relative to `https://github.com/openai/codex/blob/c875667eb837663697d5ffa32d375043434e73b4/codex-rs/`.

## Summary

| | Claude Code | Codex CLI | Copilot CLI |
| --- | --- | --- | --- |
| **File** | `<config>/projects/<project>/<session-id>.jsonl` | `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl` | `$COPILOT_HOME/session-state/<session-id>/events.jsonl` |
| **Format** | Documented as internal: "changes between versions" | Undocumented; the persistence rules are in source | File undocumented; the event types it holds are documented by the Copilot SDK |
| **Finding the file** | `--session-id` fixes the name; every hook carries `transcript_path` | No `--session-id`; every hook carries `transcript_path` | `--session-id` fixes the directory; `agentStop` carries `transcriptPath` |
| **Messages, tool calls, output** | Yes: `user` / `assistant` lines, `tool_use`, `tool_result` with stdout and stderr | Yes: `item_completed` with `UserMessage`, `AgentMessage`, `CommandExecution` (with `aggregated_output`), `FileChange`, MCP calls | Yes: `user.message`, `assistant.message`, `tool.execution_start` / `_complete` with `result` |
| **Pending question** | Yes, while pending: the `AskUserQuestion` `tool_use` with its options (observed) | **No**: `RequestUserInput` and every approval request are never written (confirmed) | Yes, while pending: the `ask_user` `tool.execution_start` with `message` and an enum schema (observed) |
| **Pending approval** | **No**: only the `tool_use`; the prompt itself is not written (observed). Needs the `PermissionRequest` hook | **No** (confirmed). Needs the `PermissionRequest` hook | `permission.requested` / `permission.completed`, but also for calls approved automatically (observed) |
| **Turn boundary and duration** | `system` line `turn_duration` with `durationMs` (observed) | `task_started` / `task_complete` with `duration_ms` (confirmed, observed) | `assistant.turn_start` / `turn_end` per model call; a user turn's length must be computed |
| **Freshness** | One line per finished content block; text appears only when its block is done (observed) | One line per finished item; no text until the message is done (confirmed, observed) | One event per finished message or tool step; no deltas (confirmed, observed) |
| **Live stream beside the TUI** | None local | **Yes**: the TUI runs on an app-server; a second client can `thread/resume` the running thread and gets text deltas, items, approvals and questions live (observed, experimental) | None: `--headless` and `--acp` are separate processes the TUI does not attach to |
| **Recommendation** | Hooks plus transcript | App-server client; hooks plus rollout as the fallback | Hooks plus `events.jsonl` |

None of the three needs the terminal scraped for the conversation itself. Each file lags the screen by up to one content block, so streaming text, and the "thinking" spinner, are terminal-only except on Codex through its app-server.

## What Admiral already does

- The chat reads Claude Code's transcript ([ADR 0019](../adr/0019-the-chat-is-carried-by-its-transcript-and-tmux.md), `internal/chat/transcript.go`): the path comes from a `SessionStart` hook into the pane option `@admiral-transcript`, the file is tailed from an offset, and only user text and assistant `text` blocks are served. Tool use, tool results, thinking, meta, sidechain and compaction lines are dropped.
- Crew boats record their transcript path in `@admiral-crew-transcript` ([ADR 0021](../adr/0021-crew-boats-record-their-transcript-path.md), `internal/cli/hook.go` `crewTranscriptCall`): Claude Code and Codex `transcript_path` on any event, Copilot `transcriptPath` on a lead `agentStop`. Admiral never opens those files.
- State comes from hooks only ([ADR 0007](../adr/0007-needs-you-comes-from-harness-hooks.md)). [ADR 0019](../adr/0019-the-chat-is-carried-by-its-transcript-and-tmux.md) is the one place a transcript is read, and only for the chat.
- The Codex launcher refuses a user-supplied `--remote`, because "The session would run on a remote app server, not here" (`internal/launcher/hookargs.go`).

So a conversation view for ships reverses ADR 0019's "never for a ship" and ADR 0021's "Admiral never opens or reads the file". That is a decision for the map, not this note.

## Claude Code

### Where and how stable

- "By default, Claude Code stores transcripts as JSONL at `~/.claude/projects/<project>/<session-id>.jsonl`", with `CLAUDE_CONFIG_DIR` moving the root and `CLAUDE_CODE_PROJECT_DIR_NAME` naming the `<project>` directory. Confirmed: [sessions#where-transcripts-are-stored](https://code.claude.com/docs/en/sessions#where-transcripts-are-stored).
- "Each line is a JSON object: a message, a tool use or result, a record of a request Claude Code sent the model, or a metadata entry. The entry format is internal to Claude Code and changes between versions, so scripts that parse these files directly can break on any release." Confirmed: same page. The docs point scripts at `/export`, `claude -p --resume`, hooks' `transcript_path` and the Agent SDK instead.
- The Agent SDK has a documented reader, `getSessionMessages(sessionId, {dir, limit, offset, includeSystemMessages})`, returning `{type, uuid, session_id, message, parent_tool_use_id, parent_agent_id}` with `message` typed `unknown` ("Raw message payload from the transcript"). Confirmed: [agent-sdk/typescript#getsessionmessages](https://code.claude.com/docs/en/agent-sdk/typescript), [agent-sdk/sessions](https://code.claude.com/docs/en/agent-sdk/sessions). It is a TypeScript or Python library and reads the same file, so it moves the parsing risk to Anthropic only for the envelope, not for the content blocks. Whether it reads a file that is still being written is unconfirmed.
- The churn is real: the probe's file held line types the chat parser has never seen (`api-request`, `api-request-blob`, `api-request-shape`, `session-owner`, `atis-latch`, `bridge-session`, `tools-used`, `ai-title`). Observed. The chat parser survives them because it drops anything without `type` `user` or `assistant`.
- The same session resumed in two terminals interleaves both into one transcript, and `/branch` switches the process to a new file; `/clear` starts a new file with `SessionStart` source `clear` (ADR 0019 records this for 2.1.310). Confirmed: [sessions#branch-a-session](https://code.claude.com/docs/en/sessions#branch-a-session).

### Finding the file for a ship

- `--session-id <uuid>` fixes the file name at launch, and every hook carries `session_id` and `transcript_path`. Confirmed: [hooks#common-input-fields](https://code.claude.com/docs/en/hooks#common-input-fields). `SessionStart` (source `startup`, `resume`, `clear`, `compact`, `fork`) is the event that rebinds after `/clear`. Admiral's ship bundle does not register `SessionStart` today (ADR 0019).
- Subagent transcripts are separate files, `…/subagents/agent-<id>.jsonl`, named in `SubagentStop`'s `agent_transcript_path` ([crew-boats.md](crew-boats.md)).

### What the file holds

- Each assistant content block is its own line, sharing `message.id`: `thinking`, `text`, `tool_use`. Lines chain by `uuid` / `parentUuid`. Observed.
- Tool calls: `tool_use` with `name` and `input` on an `assistant` line; the result is a `user` line with a `tool_result` block, and a top-level `toolUseResult` (for Bash: `stdout`, `stderr`, `interrupted`, `isImage`, `noOutputExpected`). Observed. That is enough for a folded tool card.
- Questions: `AskUserQuestion`'s `tool_use` `input` is `{questions: [{question, header, multiSelect, options: [{label, description}]}]}`, and the answer comes back in `toolUseResult.answers` keyed by question text. Observed.
- Turn length: a `system` line with `subtype` `turn_duration`, `durationMs` and `messageCount`, written as the turn ends. Observed: the three turns of the probe wrote 2696, 10237 and 10123 ms, matching the TUI's "Baked for 2s" and "Cogitated for 10s".
- Compaction summaries carry `isCompactSummary`; the chat already skips them.

### Freshness (observed)

- **Pending question is in the file.** While the `AskUserQuestion` dialog was on screen, the file's last line was the `tool_use` for it.
- **Pending approval is not.** With `--permission-mode default` and a `mkdir … > out/x.txt` command, the permission dialog was on screen and the file ended at the `tool_use: Bash` line. No line marks the prompt. The only `permission`-named lines are `permission-mode`. The `PermissionRequest` hook (`tool_name`, `tool_input`, no delay) is the documented signal ([harness-waiting-signals.md](harness-waiting-signals.md)).
- **A running tool shows as a `tool_use` with no result yet.** After approval, during the command's `sleep 8`, the last line was still the `tool_use`. The `tool_result` landed when the command finished.
- **No streaming text.** For a 600-word essay the screen filled for 14 s while the file's last text block stayed at its earlier length. The whole 3583-character block landed at once when it finished.
- The `Stop` hook docs warn the transcript "isn't guaranteed to include the final message at Stop time", and advise `last_assistant_message` ([hooks](https://code.claude.com/docs/en/hooks)). The bridge should take the final text from the hook if the file hasn't caught up.

### Other routes, rejected

- `claude -p --output-format stream-json` is documented and streams partial messages, but it is a separate process with no TUI to attach to. ADR 0019 already rejected it for the chat.
- Remote Control relays a live local session to claude.ai and the Claude app ([remote-control](https://code.claude.com/docs/en/remote-control)). It goes through Anthropic's servers, not a local API. Not usable by Admiral.

### Verdict

Hooks plus transcript, as the chat does now, with a parser that keeps tool calls, results, `AskUserQuestion` and `turn_duration`. The pending approval comes from `PermissionRequest`. Streaming text stays terminal-only. The format can break on any release and Admiral owns that risk, as ADR 0019 already says.

## Codex CLI

### The rollout file

- `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-<timestamp>-<thread id>.jsonl`. Every hook carries it as `transcript_path` (sometimes `null`), confirmed in [crew-boats.md](crew-boats.md) from `codex:hooks/src/schema.rs`. Codex has no `--session-id`, so the first hook is how Admiral learns it ([harness-waiting-signals.md](harness-waiting-signals.md)).
- No doc describes the format. Undocumented. The repo's `docs/` mentions no rollout schema.
- What gets written is decided in `persisted_event_msg` (`codex:rollout/src/policy.rs:128-224`). Confirmed:
  - Written: `ItemCompleted`, `TokenCount`, `TurnStarted`, `TurnComplete`, `TurnAborted`, `ThreadRolledBack`, `ThreadGoalUpdated`, `ThreadSettingsApplied`. The legacy `UserMessage`, `AgentMessage`, `ExecCommandEnd`-style events are written only when the thread's history mode is `Legacy`.
  - Never written ("Transient, non-durable events"): `ExecApprovalRequest`, `ApplyPatchApprovalRequest`, `RequestPermissions`, `RequestUserInput`, `ElicitationRequest`, `ItemStarted`, `AgentMessageContentDelta`, `ExecCommandOutputDelta`, `PlanUpdate`, `HookStarted` and others.
- So the rollout has finished items and turn boundaries, and never a pending question or approval.
- Item shapes across the local rollouts (2,000+ turns): `CommandExecution` (`command`, `parsed_cmd`, `cwd`, `aggregated_output`, `exit_code`, `duration`, `status`), `AgentMessage` (`content`, `phase`), `UserMessage`, `Reasoning`, `FileChange` (`changes`, `stdout`, `stderr`), `McpToolCall`, `SubAgentActivity`, `ContextCompaction`. Observed.
- `task_complete` carries `duration_ms`, `time_to_first_token_ms`, `last_agent_message` and, on failure, `error`. `turn_aborted` carries `reason`. Confirmed: `TurnCompleteEvent` (`codex:protocol/src/protocol.rs:2443`), and observed.
- 17 `AgentMessage` items in the local rollouts carry `questions: [{title, options}]` with `delivery: "async"` and `phase: "final_answer"`: a final answer that ends with a choice. Observed; what renders them, and whether the TUI waits on them, is unconfirmed.
- Codex is moving history to "paginated thread history" (`codex migrate-rollouts --help`; `thread_history_1.sqlite` sits in `CODEX_HOME`). The rollout files are still written by 0.166.0 (observed), but they may not stay the store. Unconfirmed.

### Freshness of the rollout (observed)

For a 600-word essay the file grew at the start (`UserMessage`, a `reasoning` item) and then not at all for 12 s while the screen filled, until the finished `AgentMessage`, `token_count` and `task_complete` landed together. One line per finished item, as the policy says.

### The app-server: a live stream beside the TUI

The Codex TUI is a client of an app-server, and one app-server serves many clients.

- `codex --remote <ADDR>`: "Connect the TUI to a remote app server endpoint. Accepted forms: `ws://host:port`, `wss://host:port`, `unix://`, or `unix://PATH`." Confirmed: `codex --help`.
- Without `--remote`, the TUI probes the default daemon socket in `CODEX_HOME` and attaches to it if one is running; otherwise it starts an embedded server. Confirmed: `codex:tui/src/lib.rs:544-620`, `codex:app-server-daemon/README.md` ("If an implicitly discovered daemon cannot initialize the connection, the TUI starts an embedded server instead. Explicit `--remote` endpoints remain authoritative"). On this Mac a managed daemon was already running (`codex app-server --listen unix:// --managed-daemon`, socket `~/.codex/app-server-control/app-server-control.sock`). Observed.
- Threads have a set of subscribed connections, and `thread/resume` of a running thread sends "the thread's history to the client and atomically subscrib[es] for new updates". Confirmed: `codex:app-server/src/thread_state.rs:67`, `:364`, `:677-733`.
- Server requests (approvals, user input) go to the thread's subscribed connections, and `serverRequest/resolved` is broadcast when one is answered. A human-command checkpoint goes only to connections marked eligible. Confirmed: `codex:app-server/src/outgoing_message.rs:388-431`, `:1029`.
- The methods: `item/started`, `item/completed`, `item/agentMessage/delta`, `item/commandExecution/outputDelta`, `turn/started`, `turn/completed`, and the server requests `item/commandExecution/requestApproval`, `item/fileChange/requestApproval`, `item/permissions/requestApproval`, `item/tool/requestUserInput`, `mcpServer/elicitation/request`. Confirmed: `codex:app-server-protocol/src/protocol/common.rs:1892-1917`, `:2078-2108`. The [app-server docs](https://learn.chatgpt.com/docs/app-server) list the Unix socket transport (`--listen unix://PATH`) and the streaming notifications, and say "WebSocket transport is experimental and unsupported". `tool/requestUserInput` is marked experimental there.
- **Observed end to end.** A 60-line Python client spoke WebSocket over the daemon's Unix socket, sent `initialize`, `thread/loaded/list` (which listed the TUI's thread) and `thread/resume` for it, then watched while a prompt was typed into the TUI in tmux. It received `turn/started`, `item/started userMessage`, `item/started reasoning`, 38 `item/agentMessage/delta` notifications as the text streamed, `item/started commandExecution`, `item/commandExecution/outputDelta`, `item/completed commandExecution`, and `turn/completed` 7.2 s later. The TUI kept working normally.
- Not probed: an approval or `requestUserInput` reaching the second client. Codex's automatic approval reviewer (`guardian_approval`) approved the probe's command without a prompt.

Costs of this route:

- It is experimental (`codex app-server --help`: "[experimental] Run the app server"), and the daemon's "lifecycle contract may change" (`app-server-daemon/README.md`). The WebSocket framing on the Unix socket is what the daemon speaks (the probe's handshake returned `101 Switching Protocols`), and the docs call WebSocket unsupported.
- The engineer's shared daemon is the engineer's: it runs with "the environment inherited when the daemon started" and auto-updates (`app-server-daemon/README.md`), and it was a different version (0.165.0) from the CLI (0.166.0). Admiral should run its own app-server per fleet (`CODEX_HOME=<fleet> codex app-server --listen unix://<admiral path>`) and launch ships with `--remote unix://<that path>`, so the thread is on a server Admiral knows. That changes Admiral's launcher, which refuses `--remote` today, and the Codex hook trust question in [harness-waiting-signals.md](harness-waiting-signals.md) still applies to the hooks.
- A second client could also answer approvals and questions, and start turns (`turn/start`, `turn/steer`). The bridge's message box can stay a tmux paste, as the chat's is; answering through the protocol is a later option.

### Verdict

Two choices, both without scraping:

1. **App-server client** (live): text deltas, items, approvals, questions and turn boundaries as they happen. Experimental and needs an Admiral-owned app-server per fleet.
2. **Hooks plus rollout** (per item): finished messages, commands with output, turn duration. Pending approvals from the `PermissionRequest` hook. A pending `request_user_input` has no hook and is not in the file, so it is terminal-only.

## Copilot CLI

### Where and how stable

- `session-state/` "Contains session history data, organized by session ID in subdirectories. Each session directory stores an event log (`events.jsonl`) and workspace artifacts (plans, checkpoints, tracked files)." Confirmed: [cli-config-dir-reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference). No format or stability promise there.
- The event types are documented by the Copilot SDK, which drives the same runtime: an envelope of `id`, `timestamp`, `parentId`, `ephemeral`, `type`, `data`; "**Persisted event**: Saved to the session event log on disk. Replayed when resuming a session"; "**Ephemeral event**: … not persisted to the session log". Confirmed: [copilot-sdk docs/features/streaming-events.md](https://github.com/github/copilot-sdk/blob/main/docs/features/streaming-events.md), with a table of every type and which are ephemeral. The local files match: their `type` values are the SDK's, plus undocumented internal ones (`hook.start`, `model.*`, `session.usage_record`, `human_response.recorded`). Observed.
- `COPILOT_SESSION_EVENT_LOG_ROTATION_BYTES` can rotate `events.jsonl` into `events.N.jsonl` and "atomically replace" the head with checkpoint events. Off by default. Confirmed: [copilot-sdk docs/features/session-persistence.md](https://github.com/github/copilot-sdk/blob/main/docs/features/session-persistence.md). A follower must handle the file being replaced, as `internal/chat` already does.

### Finding the file for a ship

`--session-id <id>`: "Resume an existing session or task by ID, or set the UUID for a new session" (`copilot --help`), so the path is known at launch: `$COPILOT_HOME/session-state/<id>/events.jsonl`. Observed: the directory appeared at start, and `events.jsonl` once the first prompt was handled. The `agentStop` hook's `transcriptPath` points at the same file (ADR 0021's tests use that shape).

### What the file holds

- `user.message` (`content`), `assistant.message` (`content`, `toolRequests`, `turnId`), `tool.execution_start` (`toolName`, `arguments`, `toolCallId`), `tool.execution_complete` (`success`, `result`, `toolCallId`), `assistant.turn_start` / `assistant.turn_end` (`turnId`), `session.compaction_start` / `_complete`, `subagent.*`, `session.task_complete` (`summary`, in autopilot), `abort`, `session.shutdown`. Confirmed (SDK table) and observed.
- Persisted: `permission.requested` (`requestId`, `permissionRequest` with `kind`, `fullCommandText`, `intention`) and `permission.completed` (`result.kind`: `approved`, `denied-by-rules`, `denied-interactively-by-user`, …). Confirmed (SDK). `permission.requested` is written for every check, including calls that Assisted Permissions approved without asking: the probe's `mkdir` wrote it and was approved automatically. Observed.
- Ephemeral, so never in the file: `assistant.message_delta`, `tool.execution_partial_result`, `tool.shell_output`, `session.idle`, `user_input.requested` / `_completed`, `elicitation.requested` / `_completed`, `exit_plan_mode.requested`. Confirmed (SDK table).
- But the interactive TUI's question tool is the `ask_user` tool, and its `tool.execution_start` is persisted: in the probe, while "Copilot needs information" was on screen, the file held `{"message":"Do you prefer Red or Blue?","requestedSchema":{"properties":{"color":{"type":"string","title":"Color preference","enum":["Red","Blue"]}}}}`. The answer arrives as `human_response.recorded` and the tool's `execution_complete`. Observed. In older local sessions, `ask_user` was idle for 30 s and 5 min between start and answer, so it is written at once, not at the answer.
- No per-turn duration field. A user turn runs from `user.message` to the last `assistant.turn_end` before the next `user.message`, or to the `agentStop` hook.

### Freshness (observed)

For a 600-word essay the file did not grow for 16 s while the screen filled, then the finished `assistant.message` (3823 characters) and the `model.*` lines landed together. One event per finished step.

### Other routes, rejected

- `copilot --acp` ("Start as Agent Client Protocol server") and the SDK's `--headless --stdio` / `--port` runtime (`copilot-sdk nodejs/src/client.ts:4001-4013`) stream every event, deltas included, but each is its own process. The TUI has no flag to attach to one. `--connect` is for remote (cloud) sessions. Confirmed: `copilot --help`, `copilot help acp`.

### Verdict

Hooks plus `events.jsonl`. It holds messages, tool calls with results, the pending `ask_user` question with its choices, permission requests with their outcomes, and turn boundaries. A pending approval should come from the `notification` hook's `permission_prompt` (or a `permission.requested` with no `permission.completed` after a short wait), because `permission.requested` alone fires for automatic approvals too. Streaming text stays terminal-only.

## What the bridge needs to normalise

One conversation model, filled by one adapter per harness. The parts each harness can supply:

| Item | Claude Code | Codex (rollout / app-server) | Copilot |
| --- | --- | --- | --- |
| User message | `user` line, string or `text` blocks; skip `isMeta`, command lines | `UserMessage` item | `user.message` |
| Assistant text | `assistant` `text` block | `AgentMessage` item / `item/agentMessage/delta` | `assistant.message.content` |
| Tool call | `tool_use` (`name`, `input`) | `CommandExecution`, `FileChange`, `McpToolCall` items | `tool.execution_start` |
| Tool output, folded | `tool_result` + `toolUseResult` | `aggregated_output` / `outputDelta` | `tool.execution_complete.result` |
| Pending question | `AskUserQuestion` `tool_use` without result | app-server `item/tool/requestUserInput` only | `ask_user` start without complete |
| Pending approval | `PermissionRequest` hook | `PermissionRequest` hook / app-server `requestApproval` | `notification` `permission_prompt`; `permission.requested` |
| Turn start / end | user line; `turn_duration` | `task_started` / `task_complete` (`turn/started`, `turn/completed`) | `user.message`; last `assistant.turn_end`, `agentStop` hook |
| "Worked for N" | `durationMs` | `duration_ms` | computed from timestamps |
| Compaction | `isCompactSummary` | `ContextCompaction` item | `session.compaction_*` |
| Subagents | separate file per agent | `SubAgentActivity` item | `subagent.*` events |

Rules the adapters share:

- Every adapter must skip what it does not know and never fail the view, as ADR 0019 does. All three formats change without notice (Claude Code says so outright).
- Freshness is per item for all three files. The bridge should show "working" from hooks between items, and keep the terminal tab for anything live, unless Codex goes through the app-server.
- A pending question shown in the conversation needs its options. Claude Code and Copilot have them in the file; Codex only through the app-server.
- The paths are untrusted (ADR 0019, ADR 0021): the same root and symlink checks, per harness root (`projects/`, `sessions/`, `session-state/`).
- Answering a question from the conversation is a keystroke sequence in the pane, not a paste of text: a numbered choice in Claude Code's dialog, arrow keys and Enter in Copilot's. That mapping is terminal-specific and unconfirmed for each harness.

## Unconfirmed

- Whether Claude Code's `getSessionMessages()` reads a session that is still open, and whether its `message` payload is any steadier than the file.
- An approval or `requestUserInput` reaching a second Codex app-server client, and the TUI's behaviour when that client answers first.
- Whether Codex keeps writing rollout files after the "paginated thread history" migration.
- What the Codex `AgentMessage` `questions` field (`delivery: "async"`) is and whether the TUI waits on it.
- Copilot `permission.completed` `result.kind` for an approval the engineer gave in the TUI (the probe only saw an automatic one).
- Keystrokes that answer each harness's question dialog from outside.

## Sources

- Claude Code: [sessions](https://code.claude.com/docs/en/sessions), [how-claude-code-works#work-with-sessions](https://code.claude.com/docs/en/how-claude-code-works#work-with-sessions), [agent-sdk/sessions](https://code.claude.com/docs/en/agent-sdk/sessions), [agent-sdk/typescript](https://code.claude.com/docs/en/agent-sdk/typescript), [hooks](https://code.claude.com/docs/en/hooks), [remote-control](https://code.claude.com/docs/en/remote-control).
- Codex: `openai/codex` at `c875667e` (`rollout/src/policy.rs`, `app-server/src/thread_state.rs`, `app-server/src/outgoing_message.rs`, `app-server-protocol/src/protocol/common.rs`, `protocol/src/protocol.rs`, `tui/src/lib.rs`, `app-server-daemon/README.md`), [app-server docs](https://learn.chatgpt.com/docs/app-server), `codex --help`, `codex app-server --help`.
- Copilot: [cli-config-dir-reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference), [cli-hooks-reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference), `github/copilot-sdk` at `c07cf0e` ([streaming-events.md](https://github.com/github/copilot-sdk/blob/main/docs/features/streaming-events.md), [session-persistence.md](https://github.com/github/copilot-sdk/blob/main/docs/features/session-persistence.md), `nodejs/src/client.ts`), `copilot --help`, `copilot help acp`.
- Admiral: [ADR 0007](../adr/0007-needs-you-comes-from-harness-hooks.md), [ADR 0019](../adr/0019-the-chat-is-carried-by-its-transcript-and-tmux.md), [ADR 0021](../adr/0021-crew-boats-record-their-transcript-path.md), [harness-waiting-signals.md](harness-waiting-signals.md), [crew-boats.md](crew-boats.md), `internal/chat/transcript.go`, `internal/cli/hook.go`, `internal/launcher/hookargs.go`.
- Probes: on this Mac, 2026-11-10, as described under Marks.
