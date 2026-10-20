# How each harness signals it is waiting on the engineer

Research for [#22](https://github.com/jbuffin/admiral/issues/22), feeding [#6 Detecting when a ship needs you](https://github.com/jbuffin/admiral/issues/6) and [#7 When a ship counts as docked](https://github.com/jbuffin/admiral/issues/7). This note lists the signals; it does not pick a design.

Question: for Claude Code, Codex CLI and GitHub Copilot CLI, what machine-readable signals show that a session is (a) waiting on the engineer, (b) working again after an answer, (c) finished or exited? What must be set at launch, and can it be set per fleet without touching the engineer's own config (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME`)?

Versions on this Mac on 2026-10-17: Claude Code 2.1.302, codex-cli 0.162.1, GitHub Copilot CLI 1.0.98. "Observed" means a probe was run here: each harness was started in a tmux session on a private socket and no prompt was sent to any model. Everything else comes from the vendor's docs or source code, linked inline.

## Summary

| | Claude Code | Codex CLI | Copilot CLI |
| --- | --- | --- | --- |
| **Waiting: approval** | `PermissionRequest` hook (fires as the decision is needed); `Notification` hook, `notification_type` `permission_prompt` (after about 6 s untyped) | `PermissionRequest` hook ("when Codex is about to ask for approval") | `notification` hook, `notification_type` `permission_prompt`; `permissionRequest` hook fires *before* rule checks, so it also fires for auto-approved calls |
| **Waiting: question** | `PreToolUse` with matcher `AskUserQuestion`; `Notification` `elicitation_dialog` (MCP elicitation) | No dedicated hook. TUI notification types `plan-mode-prompt`, `async-question` exist (terminal escape only) | `notification` `elicitation_dialog`; `ask_user` tool in `preToolUse` |
| **Waiting: turn over, engineer's move** | `Stop` hook; `Notification` `idle_prompt` about 60 s later | `Stop` hook; legacy `notify` program (`agent-turn-complete`) | `agentStop` hook (`stopReason: "end_turn"`) |
| **Working again** | `UserPromptSubmit`; any later `PreToolUse` / `PostToolUse` | `UserPromptSubmit`; `PostToolUse`; `Interrupt` (engineer pressed Esc) | `userPromptSubmitted`; `postToolUse` |
| **Finished / exited** | `SessionEnd` (`reason` `prompt_input_exit`, `other`, also `clear`, `resume`, `logout`); `StopFailure` for API errors | `SessionEnd` ("when Codex closes normally"; `reason` `other`) | `sessionEnd` (`reason` `complete`, `error`, `abort`, `timeout`, `user_exit`; observed `user_exit` on `/exit`) |
| **Per fleet, without touching the engineer's config** | **Yes.** `--settings <file or JSON>` adds hooks for that launch; observed merging with project hooks. `--plugin-dir` also works | **Partly.** `-c notify=[…]` needs no trust. `-c hooks.<Event>=[…]` loads (observed) but stops the TUI on a "Hooks need review" dialog unless trusted: `--dangerously-bypass-hook-trust`, a per-launch trusted hash, or trust stored in `CODEX_HOME` | **Yes.** `--plugin-dir <dir>` with a `hooks.json`; observed `sessionEnd` firing |

In all three, a crash or kill fires no hook. The only harness-independent exit signal is the process ending: the tmux pane dies ([ADR 0002](../adr/0002-sessions-run-in-a-headless-session-host.md)).

## Claude Code

### Hook events that matter

From the [hooks reference](https://code.claude.com/docs/en/hooks):

- **`Notification`**, matcher on `notification_type`. Input adds `message`, `title`, `notification_type`. Relevant types:
  - `permission_prompt`: "Claude needs you to approve a tool use or a sandboxed command's network request, and the prompt has waited about six seconds". "The timer starts when the permission prompt appears, and each keystroke defers it."
  - `idle_prompt`: "Claude finished responding about 60 seconds ago and you haven't typed since", and "only if … no background agent, such as a background subagent, is still running."
  - `elicitation_dialog`, `elicitation_url_dialog`: an MCP server asks for input.
  - `agent_needs_input`, `agent_completed`: background sessions, only "while agent view is open in a terminal". Not useful for Admiral's tmux ships.
  - Notification hooks "can't block or modify notifications."
- **`PermissionRequest`**: "When a tool call needs a permission decision". Gets `tool_name`, `tool_input`, `permission_suggestions`. No delay, unlike `permission_prompt`. A hook here can also answer the prompt (`allow` / `deny`), which Admiral must not do by accident: an empty output leaves the prompt to the engineer.
- **`PreToolUse`** with matcher `AskUserQuestion`: fires before every tool call, so it marks Claude asking the engineer a multiple-choice question.
- **`Stop`**: "When Claude finishes responding". Input adds `stop_hook_active`, `last_assistant_message`, `background_tasks`, `session_crons`. The docs advise using `last_assistant_message` rather than the transcript, which "isn't guaranteed to include the final message at Stop time".
- **`StopFailure`**: the turn ended on an API error (`rate_limit`, `authentication_failed`, `billing_error`, …). Also a "needs you" case.
- **`UserPromptSubmit`**: a prompt was submitted, "Also fires on turns Claude Code starts on its own".
- **`SessionEnd`**: `reason` is `clear`, `resume`, `logout`, `prompt_input_exit` or `other`. It also fires on `/clear` and interactive `/resume`, which do not end the process. Hooks share a 1.5 s budget.
- **`SessionStart`**: `source` `startup`, `resume`, `clear`, `compact`, `fork`. Useful to bind a ship to its `session_id` and `transcript_path`.

Every event carries `session_id`, `transcript_path`, `cwd`, `hook_event_name`, plus `permission_mode` and others. Admiral already fixes the conversation ID at launch with `--session-id <uuid>` ([Commodore Launcher harnesses.md](https://github.com/jbuffin/commodore-launcher/blob/main/docs/harnesses.md)), so hook payloads can be matched to a ship by `session_id`.

Hook handler types: `command`, `http` (POSTs the event JSON to a URL; non-2xx and connection failures are non-blocking), `mcp_tool`, `prompt`, `agent`. `"async": true` runs a command hook in the background. All matching hooks run in parallel ([hooks guide](https://code.claude.com/docs/en/hooks-guide)).

### Setting it per fleet

- `--settings`: "Path to a settings JSON file or an inline JSON string. Values you set here override the same keys in your `settings.json` files for this session" ([CLI reference](https://code.claude.com/docs/en/cli-reference)). Its precedence is just below managed settings ([settings](https://code.claude.com/docs/en/settings)), and "When you set the same list key … in more than one file, Claude Code combines the lists".
  - **Observed:** `claude --init-only --settings '{"hooks":{"SessionStart":[…]}}'` ran the hook, which received `{"session_id":…,"transcript_path":…,"cwd":…,"hook_event_name":"SessionStart","source":"startup"}`. With a project `.claude/settings.local.json` that also had a `SessionStart` hook, both ran. So `--settings` hooks add to the engineer's hooks rather than replacing them, and nothing under `CLAUDE_CONFIG_DIR` is written.
- `--plugin-dir <dir>`: loads a plugin for the session only; plugin `hooks/hooks.json` is a documented hook location.
- Things that switch Admiral's hooks off: `disableAllHooks: true` (at managed level it wins; at user or project level Admiral's `--settings` outranks it), managed `allowManagedHooksOnly`, `allowedHttpHookUrls` for HTTP hooks, and the `--bare` / `--safe-mode` flags.

### Transcripts

`<config>/projects/<dir>/<session_id>.jsonl` is written asynchronously ("may lag the current turn") and records messages, not prompt state. Hooks are the documented signal.

## Codex CLI

### Hook events that matter

Codex has Claude-style lifecycle hooks, on by default (`[features] hooks`; `codex features list` shows `hooks stable true`). From [Hooks](https://learn.chatgpt.com/docs/hooks) (`developers.openai.com/codex/hooks` redirects there):

- **`PermissionRequest`**: "when Codex is about to ask for approval". Input: `turn_id`, `tool_name`, `tool_input`, `tool_input.description`. Can allow or deny. Caveat: with the `guardian_approval` feature (on here), some approvals are reviewed automatically; whether the hook fires before or instead of the auto-review was not checked.
- **`Stop`**: the turn ends. `turn_id`, `stop_hook_active`, `last_assistant_message`.
- **`UserPromptSubmit`**, **`PreToolUse`**, **`PostToolUse`**: working.
- **`Interrupt`**: the engineer interrupted the main turn.
- **`SessionEnd`**: "when you archive or delete a conversation that's still open, when Codex closes normally," or after 30 idle minutes with no client connected. Only `reason` value documented: `other`.
- **`SessionStart`**: `source` `startup`, `resume`, `clear`, `compact`, `fork`.
- There is **no `Notification` event**. Hosted tools such as WebSearch don't trigger tool hooks.

Common fields: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`. Codex cannot fix the session ID at launch, so Admiral would learn it from the first hook payload.

### Other Codex signals

- **`notify`** ([advanced config](https://learn.chatgpt.com/docs/config-file/config-advanced)): an argv run on "`agent-turn-complete`" only, with one JSON argument: `type`, `thread-id`, `turn-id`, `cwd`, `input-messages`, `last-assistant-message`. The source calls it legacy: "TODO: Remove this hook … when legacy `notify` support is removed" ([`legacy_notify.rs`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/legacy_notify.rs)). It ignores project `.codex/config.toml`. It is not part of hook trust.
- **TUI notifications**: `tui.notifications` (bool or list of types), `tui.notification_method` (`auto`, `osc9`, `bel`), `tui.notification_condition` (`unfocused` by default, or `always`) ([config schema](https://github.com/openai/codex/blob/main/codex-rs/core/config.schema.json)). Types in [`notifications.rs`](https://github.com/openai/codex/blob/main/codex-rs/tui/src/chatwidget/notifications.rs): `agent-turn-complete`; `approval-requested` (exec approval, edit approval, MCP elicitation); `plan-mode-prompt`; `async-question`. These are terminal escapes (BEL or OSC 9) written to the pane, so Admiral would have to spot them in the pane output or through tmux's bell alert. That path was not tested. It is the only Codex signal that covers questions.
- **Rollout files** `$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl`: a local file shows `event_msg` `task_started` / `task_complete` per turn. No approval events were seen in it. The format is undocumented.

### Setting it per fleet

- `-c key=value` overrides any config key for one launch ("parsed as TOML"). `-c notify='[…]'` and `-c tui.notification_method="bel"` need no trust.
- Hooks via `-c 'hooks.SessionStart=[{hooks=[{type="command",command="…"}]}]'` are picked up from the "session flags" layer. **Observed:** the TUI opened on "Hooks need review / 1 hook is new or changed" with *Review hooks*, *Trust all and continue*, *Continue without trusting (hooks won't run)*. Until someone answers, the ship is stuck on a dialog Admiral didn't expect. The docs: "new or changed hooks are marked for review and skipped until trusted"; trust is "recorded against the hook's current hash".
- Ways past that dialog:
  1. `--dangerously-bypass-hook-trust`: "Run enabled hooks without requiring persisted hook trust for this invocation. DANGEROUS." It also lifts trust for every other hook the session finds, including repository hooks. Not probed here: the session's safety policy refused to run it.
  2. Pre-trust the exact hook for one launch: `hook_states_from_stack` reads `hooks.state."<key>".trusted_hash` from the **user and session-flags layers** ([`config_rules.rs`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/config_rules.rs)), so `-c 'hooks.state."<key>".trusted_hash="…"'` should work. But the key is `<source path>:<event>:<group>:<handler>` and the hash is Codex's internal TOML hash of the normalised hook ([`discovery.rs`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/engine/discovery.rs)). Admiral would be copying an internal format that may change. Not tested.
  3. Trust once through `/hooks`. That writes `hooks.state` into `$CODEX_HOME/config.toml`, which is the engineer's own config for that fleet.
- `SessionEnd` and `Stop` hooks are the only clean way to learn the session ID, since Codex has no `--session-id`.

## GitHub Copilot CLI

### Hook events that matter

From the [hooks reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference):

- **`notification`**: "fires asynchronously when the CLI emits a system notification", "fire-and-forget". Fields: `sessionId`, `timestamp`, `cwd`, `message`, `title`, `notification_type`. Types: `permission_prompt` ("The agent requests permission to execute a tool"), `elicitation_dialog` ("The agent requests additional information from the user"), `agent_idle` and `agent_completed` (both about **background** agents), `shell_completed`, `shell_detached_completed`. No delay is documented.
- **`permissionRequest`**: "Fires before the permission service runs (before rule checks, session approvals, and user prompting)". It fires even when a rule would approve the call, so on its own it does not mean "waiting".
- **`agentStop`** (`Stop` in the VS Code-compatible form): "The main agent finishes a turn". `stopReason: "end_turn"`, `stop_hook_active`, `transcriptPath`.
- **`userPromptSubmitted`**, **`preToolUse`** / **`postToolUse`**: working.
- **`sessionEnd`**: `reason` `complete`, `error`, `abort`, `timeout`, `user_exit`; `/clear` also fires it with `user_exit`. **Observed:** `/exit` in the TUI gave `{"sessionId":"0d7b…","timestamp":1792257886287,"cwd":"…/proj","reason":"user_exit"}`.
- **`errorOccurred`**: an error during execution.
- Event names in camelCase give camelCase payloads; PascalCase (`Stop`, `SessionEnd`) gives Claude/VS Code-style snake_case payloads.

Sources are combined: "all hook entries from all sources are run", in the order policy, user (`$COPILOT_HOME/hooks/*.json`, `settings.json` `hooks`), project (`.github/hooks/*.json`, `.github/copilot/settings*.json`, **and the repo's `.claude/settings*.json`**), plugins. Repository hooks "require workspace admission". Handler types: `command` (`bash` / `powershell` / `exec`), `http` (HTTPS only unless `COPILOT_HOOK_ALLOW_LOCALHOST=1`), `prompt`.

### Other Copilot signals

- `<COPILOT_HOME>/session-state/<id>/events.jsonl`: local files contain `assistant.turn_start` / `assistant.turn_end`, `permission.requested` / `permission.completed`, `tool.execution_start` (including the `ask_user` tool), `session.shutdown`, `abort`. The format is undocumented, and `permission.requested` is probably logged for auto-approved calls too.
- The `notifications` / `terminalNotifications` settings (`copilot help config`) make desktop toasts. Under tmux they fall back to the OS backend, so they don't help Admiral.

### Setting it per fleet

- `--plugin-dir <directory>`: "Load a plugin from a local directory". **Observed:** a directory with `plugin.json` and a `hooks.json` (`{"version":1,"hooks":{"sessionEnd":[{"type":"command","bash":"…"}]}}`) ran its `sessionEnd` hook, without writing to `COPILOT_HOME`. Its `sessionStart` hook did **not** fire in a session that never got a prompt. Either sessionStart fires lazily on the first prompt, or it fired before the plugin loaded. Not resolved.
- The probe first stopped on "Confirm folder trust" for an unfamiliar folder. That is a needs-you state before any hook can run. `COPILOT_ALLOW_ALL=true` trusts the folder but also approves every tool.
- `--session-id <uuid>` fixes the session ID at launch, as with Claude Code.

## Things #6 and #7 should weigh

- Claude Code and Copilot can each get Admiral's hooks per launch from a directory or file Admiral owns. Codex cannot, unless Admiral accepts `--dangerously-bypass-hook-trust`, copies Codex's internal trust hash, or writes trust into the fleet's `CODEX_HOME`. `notify` and the TUI's BEL/OSC 9 work per launch but cover less.
- Startup dialogs (Copilot folder trust, Codex hook review, Claude Code workspace trust) are "needs you" moments that come before any hook can fire. Only the terminal shows them.
- A hook tells "turn over" (Stop / agentStop) apart from "approval pending" (PermissionRequest / permission_prompt). Claude Code's `idle_prompt` and `permission_prompt` are deliberately delayed (60 s and about 6 s).
- `SessionEnd` also fires on `/clear` in Claude Code and Copilot, and not on a crash. Process exit, seen as a dead tmux pane, is the reliable "exited" signal. "Mission complete" (#7) is a separate question.
- Hook handlers run on the Mac, so they can write a marker file or POST to Admiral (`http` hooks: Claude Code allows any URL by default; Copilot needs `COPILOT_HOOK_ALLOW_LOCALHOST=1` for `http://localhost`; Codex documents `command` handlers, and parses `prompt` and `agent` handlers but skips them).
