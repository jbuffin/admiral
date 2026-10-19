# Crew boats: how Admiral can learn them, and whether each gets a terminal

Research for [#5](https://github.com/jbuffin/admiral/issues/5), October 2026. Versions checked: Claude Code 2.1.303, Codex CLI 0.162.1 (source at tag `rust-v0.162.1`, commit `092d3acd`), Copilot CLI 1.0.98, tmux 3.8.

A crew boat is "a Commodore teammate, or a subagent the harness spawned" (`GLOSSARY.md`), and the flotilla switches the terminal to it.

Marks: **confirmed** means a primary source says so (doc URL, source `file:line`, or `--help` output). **binary** means it comes from reading the minified code in the installed Claude Code binary. It is primary, but undocumented, and needs a live probe before Admiral depends on it. **unconfirmed** means no source settles it. Codex source links are relative to `https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/` and are written `codex:<path>:<lines>`.

## Summary

| Harness | Sub-agent kind | Start/stop signal | Own process | Own terminal Admiral can stream |
| --- | --- | --- | --- | --- |
| Claude Code | Subagent (Agent tool) | `SubagentStart` / `SubagentStop` hooks with `agent_id`, `agent_type` | No | No |
| Claude Code | Agent-team teammate, in-process (default) | `SubagentStart` per message, `TeammateIdle`, `TaskCreated`/`TaskCompleted` | No | No |
| Claude Code | Agent-team teammate, tmux mode | No spawn hook; team `config.json` lists `tmuxPaneId` | Yes | **Yes**: a pane split in the ship's own window on Admiral's socket (binary) |
| Claude Code | Background session (`claude --bg`), which **is what Commodore uses** | None to the ship; `claude agents --json` lists it | Yes | Only by running `claude attach <id>` in a pane Admiral makes |
| Codex CLI | Subagent (multi-agent threads) | `SubagentStart` / `SubagentStop` hooks with `agent_id`, `agent_type` | No | No |
| Codex CLI | Commodore teammate (`codex exec --json`) | None to the ship | Yes, no TTY | No |
| Copilot CLI | Subagent (`task` tool, `/fleet`) | `subagentStart` / `subagentStop` hooks | No | No |
| Copilot CLI | Commodore teammate (`copilot -p`) | None to the ship | Yes, no TTY | No |

Only one case gives each crew boat a real terminal on Admiral's socket: Claude Code agent teams in tmux mode. Commodore does not use it today.

## Claude Code

### Kinds

- **Subagents** started with the Agent tool: built-in Explore, Plan and general-purpose, plus custom agents from `.claude/agents`. In interactive sessions they run in the background by default, and they nest up to 3 deep (`CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH`). Confirmed: [sub-agents](https://code.claude.com/docs/en/sub-agents), [#run-subagents-in-foreground-or-background](https://code.claude.com/docs/en/sub-agents#run-subagents-in-foreground-or-background), CHANGELOG 2.1.232.
- **Agent-team teammates**, experimental, enabled by `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` in the environment or a settings `env` block. With teams on, an Agent call with a `name` (not a fork, no `isolation`) starts a teammate. One team per session, no nesting, and under `-p` they fall back to plain subagents. Confirmed: [agent-teams#enable-agent-teams](https://code.claude.com/docs/en/agent-teams#enable-agent-teams), [#how-claude-starts-agent-teams](https://code.claude.com/docs/en/agent-teams#how-claude-starts-agent-teams), [#limitations](https://code.claude.com/docs/en/agent-teams#limitations), CHANGELOG 2.1.178.
- **Background sessions**: `claude --bg` runs a full session under a supervisor, managed with `claude agents`, `attach`, `logs`, `stop`. Confirmed: [agent-view](https://code.claude.com/docs/en/agent-view), `claude --help`.

### Signals

- Every hook input has `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`, plus `agent_id` ("the subagent or in-process teammate the hook fires in") and `agent_type` inside a subagent. Confirmed: [hooks#common-input-fields](https://code.claude.com/docs/en/hooks#common-input-fields).
- `SubagentStart`: `agent_id`, `agent_type`; matcher on agent type. Fires on spawn, on resume, and each time an in-process teammate takes a new message. No name, description or prompt. Confirmed: [hooks](https://code.claude.com/docs/en/hooks). The Agent tool's `PreToolUse` `tool_input` probably holds the description and name: unconfirmed.
- `SubagentStop`: `stop_hook_active`, `agent_id`, `agent_type`, `agent_transcript_path` (`…/subagents/agent-<id>.jsonl`), `last_assistant_message`, `background_tasks`, `session_crons`. It also fires for Claude Code's internal agents (prompt suggestions, `/btw`) with `agent_type` set to the session's own agent or `""`. Confirmed: [hooks](https://code.claude.com/docs/en/hooks).
- `TeammateIdle`: `teammate_name`, `team_name` (deprecated), `agent_id` (2.1.290+, may be absent). No matcher. `TaskCreated` / `TaskCompleted`: `task_id`, `task_subject`, optional `task_description`, `teammate_name`, `team_name`, `agent_id`. Confirmed: [hooks](https://code.claude.com/docs/en/hooks).
- No hook fires when a teammate is spawned. The docs only describe `SubagentStart` per message. Confirmed by silence.
- `Stop` carries `background_tasks[]` (`id`, `type` such as `subagent`, `teammate`, `shell`, `status`, `description`, `agent_type`), so a hook can tell "turn over, crew still working" from "done". Confirmed: [hooks#stop-input](https://code.claude.com/docs/en/hooks#stop-input).
- Team state on disk: `~/.claude/teams/session-<first 8 of session id>/config.json` with `members[]` (name, agentId, agentType, `tmuxPaneId`), tasks in `~/.claude/tasks/<team>/`, both under the fleet's config dir. The docs say not to edit them. Confirmed: [agent-teams#architecture](https://code.claude.com/docs/en/agent-teams#architecture).
- `claude agents --json` lists background sessions with `id`, `sessionId`, `name`, `cwd`, `kind`, `state` (working/blocked/done/failed/stopped), `pid`, `status`, `waitingFor`. Nothing links a session to the one that started it. Confirmed: [agent-view#list-sessions-as-json](https://code.claude.com/docs/en/agent-view#list-sessions-as-json).

### Process and terminal

- Subagents and in-process teammates run inside the lead's process, with no terminal of their own. Confirmed: [sub-agents](https://code.claude.com/docs/en/sub-agents), [agent-teams#choose-a-display-mode](https://code.claude.com/docs/en/agent-teams#choose-a-display-mode).
- `teammateMode` is `"in-process"` (default), `"auto"`, `"tmux"` or `"iterm2"`. `auto` uses split panes inside tmux, or in iTerm2 with the `it2` CLI. `--teammate-mode <mode>` overrides it for a session but is missing from `claude --help` on 2.1.303. Split panes don't work in VS Code's terminal, Windows Terminal or Ghostty. Confirmed: [settings-reference#teammatemode](https://code.claude.com/docs/en/settings-reference#teammatemode), [agent-teams#choose-a-display-mode](https://code.claude.com/docs/en/agent-teams#choose-a-display-mode), `claude --help`.
- A split-pane teammate "runs as a separate Claude Code process". Confirmed: [sub-agents](https://code.claude.com/docs/en/sub-agents) (Available tools).
- tmux mode honours `$TMUX` (binary). It reads `TMUX` and `TMUX_PANE` and passes `-S <socket from $TMUX>` on every call, so inside a ship it uses Admiral's socket and the ship's window:
  - first teammate: `split-window -d -t <leader pane> -h -l 70% … -- cat`, so the ship's pane shrinks to about 30% of the width; later teammates split those panes and the window is re-tiled;
  - each teammate pane gets its name as pane title, border colours, `pane-border-status top` and `remain-on-exit failed`, then `respawn-pane -k` starts the teammate;
  - the pane ids go in the team `config.json` (`tmuxPaneId`, confirmed in the docs above);
  - outside tmux it starts its own server `tmux -L claude-swarm-<pid>`. A second path, `new-window -t claude-swarm -n teammate-<name>` on the default server, exists; when it runs is unconfirmed.
- The teammate command line is `cd <cwd> && env CLAUDECODE=1 CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 <provider vars> claude --agent-id … --agent-name … --team-name … --agent-color … --parent-session-id <lead> [--agent-type …]` plus inherited flags (binary).
- `claude --bg` sessions are separate processes under the supervisor. `claude attach <id>` takes over the current terminal fullscreen. Confirmed: [agent-view#the-supervisor-process](https://code.claude.com/docs/en/agent-view#the-supervisor-process), `claude attach --help`. They inherit the dispatching shell's `PATH` and provider variables; whether `TMUX`/`TMUX_PANE` reach them is undocumented.

### Typing into one

- In-process teammates: ↑/↓ in the lead's panel selects one, Enter opens its transcript, typing messages it, Esc interrupts, x stops. Confirmed: [agent-teams#talk-to-teammates-directly](https://code.claude.com/docs/en/agent-teams#talk-to-teammates-directly).
- Split-pane teammates: click into the pane; each is a full terminal. Confirmed in the same section. Keys Admiral sends to that pane would reach the teammate (inference).
- Subagents: while a subagent's row is in the panel, its transcript takes input. Confirmed: [sub-agents#resume-subagents](https://code.claude.com/docs/en/sub-agents#resume-subagents). All of this is inside the lead's terminal.
- Permission prompts from teammates and background subagents show in the lead. Confirmed: [agent-teams](https://code.claude.com/docs/en/agent-teams).

### Do `--settings` hooks reach them?

- Subagents and in-process teammates: yes. Settings and plugin hooks run inside subagents, and tool events carry `agent_id`/`agent_type`. Confirmed: [hooks#hook-locations](https://code.claude.com/docs/en/hooks#hook-locations), CHANGELOG 2.1.69. So their tool calls run `tmux set -p @admiral-state working` on the **ship's** pane.
- Split-pane teammates: very likely. The teammate command line forwards the lead's `--settings`, `--setting-sources`, `--managed-settings`, each `--plugin-dir`, `--permission-mode`, `--model`, `--effort`, `--mcp-config` and others (binary; `--setting-sources` forwarding is documented in [agent-teams#context-and-communication](https://code.claude.com/docs/en/agent-teams#context-and-communication), CHANGELOG 2.1.281). Each teammate would then set `@admiral-state` on its own pane, from its own `$TMUX_PANE`. Unconfirmed end to end.
- `claude --bg`: a fresh one gets only the flags on its own command line. `--settings` and `--plugin-dir` carry over only when an existing session is sent to the background. Confirmed: [agent-view#settings-plugins-and-mcp-servers](https://code.claude.com/docs/en/agent-view#settings-plugins-and-mcp-servers).
- Whether `Stop` fires in a tmux teammate's own process at the end of each turn is undocumented; only `TeammateIdle` in the lead is described.

## Codex CLI

### Kinds

- One kind: subagents ("multi-agent", "collab"), threads inside the one Codex process. The `multi_agent` feature is stable and on (`codex features list`: `multi_agent stable true`; `codex:features/src/lib.rs:1415-1416`). "Current Codex releases enable subagent workflows by default", and `agents.enabled = false` turns them off ([subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents)). Confirmed.
- Tools: v1 `spawn_agent`, `send_input`, `wait_agent`, `resume_agent`, `close_agent`; v2 `spawn_agent`, `send_message`, `followup_task`, `interrupt_agent`, `list_agents`. Confirmed: `codex:core/src/tools/spec_plan.rs:733-770`.
- Built-in types `default`, `worker`, `explorer`; custom agents are TOML in `~/.codex/agents/` or `.codex/agents/`. Confirmed: [subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents).
- No agent teams. `codex agents` browses sessions on the local app-server daemon, not sub-agents (`codex agents --help`). Confirmed.

### Signals

- Hook events: `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `Stop`, `Interrupt`. Confirmed: `codex:hooks/src/lib.rs:23-36`, [hooks](https://learn.chatgpt.com/docs/hooks).
- `SubagentStart`: `session_id`, `turn_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `permission_mode`, `agent_id`, `agent_type`. `SubagentStop` adds `agent_transcript_path`, `stop_hook_active`, `last_assistant_message`. Confirmed: `codex:hooks/src/schema.rs:549-562`, `:606-622`.
- `session_id` is the parent's, shared by every thread under it; `agent_id` is the sub-agent's thread id; `agent_type` is its role or `default`. No task description and no explicit parent thread id. Matchers match `agent_type`. Confirmed: `codex:core/src/session/session.rs:667-670`, `codex:core/src/hook_runtime.rs:1054-1060`, `codex:hooks/src/lib.rs:43-53`; the docs say "Subagent hooks use the parent session id".
- `SubagentStart` fires on a fresh spawn or fork; a sub-agent's finished turn fires `SubagentStop` instead of `Stop`; Codex's internal sub-agents fire neither. `SessionEnd` is root-only, and `Interrupt` "doesn't run for idle threads or subagents". Confirmed: `codex:core/src/hook_runtime.rs:133-151`, `:399-445`, `:480-484`, [hooks](https://learn.chatgpt.com/docs/hooks).
- Tool hooks inside sub-agents carry `agent_id`/`agent_type`. Confirmed: `codex:core/src/hook_runtime.rs:196-199`, `:265-268`, and the other call sites.
- Outside the TUI: `codex exec --json` emits `collab_tool_call` items (`tool`, `sender_thread_id`, `receiver_thread_ids`, `prompt`, `agents_states`, `status`), and the app-server v2 protocol has `CollabAgentToolCall`. Neither is available from the TUI Admiral runs. Confirmed: `codex:exec/src/exec_events.rs:124,250-259`, `codex:app-server-protocol/src/protocol/v2/item.rs:374-393`.

### Process and terminal

- No own process: a sub-agent is a thread managed by `AgentControl` in the same process. Confirmed: `codex:core/src/agent/control.rs`.
- All threads share the ship's terminal. `/subagents` picks one, Alt+Left/Right switch threads, `/agents` opens the agent command center. Confirmed: `codex:tui/src/slash_command.rs:80-81,141`, `codex:tui/src/multi_agents.rs:106-111`.

### Typing into one

- The TUI can make a sub-agent the active thread, and input is sent per thread. Confirmed: `codex:tui/src/app/session_lifecycle.rs:540`, `codex:tui/src/app_event.rs:417`. That typing in that view prompts the sub-agent is unconfirmed; the docs only say "To steer, stop, or close subagents, ask Codex directly."

### Do `-c hooks.*` hooks reach them?

- Yes, from the source. A sub-agent's config copies the parent's effective config, a role adds one layer on top and keeps the `-c` layer, and each sub-agent builds its hooks from it. Confirmed in code (`codex:core/src/agent/child_config.rs:109-140`, `codex:core/src/agent/role.rs:242-257`, `codex:core/src/session/mod.rs:5135-5160`), not tested live. Same `CODEX_HOME`, so the fleet's hook trust should cover them: unconfirmed.

## Copilot CLI

Docs below are from docs.github.com; line numbers are in the article markdown.

### Kinds

- Subagents via the `task` tool, with `list_agents`, `read_agent` and `write_agent`. Built-in agents: code-review, explore, general-purpose, research, rubber-duck, security-review, task; custom agents are `.agent.md` files with their own context window. Confirmed: [cli-command-reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference) ("Agent and task delegation tools", "Built-in agents"), [about-custom-agents](https://docs.github.com/en/copilot/concepts/agents/copilot-cli/about-custom-agents).
- Sync or background (`mode: "background"`; a sync one can be backgrounded from `/tasks`). Nesting defaults to depth 4 and 32 at once. Agents can message each other with `list_agents` / `write_agent`. Confirmed: cli-command-reference ("Subagent limits", "Agent communication"), changelog 1.0.35.
- `/fleet` and `--fleet` split a plan into parallel subagents of the same kind. Confirmed: [fleet](https://docs.github.com/en/copilot/concepts/agents/copilot-cli/fleet), `copilot --help`.
- `/delegate` hands the session to the remote cloud agent, so it is not a local crew boat. Confirmed: `copilot help commands`.

### Signals

- `subagentStart` ("A subagent is spawned (before it runs)"): `sessionId`, `timestamp`, `cwd`, `transcriptPath`, `agentName`, `agentDisplayName?`, `agentDescription?`; matcher on `agentName`. No documented `agentId`. Confirmed: [cli-hooks-reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference).
- `subagentStop` ("when a subagent completes normally"): `sessionId`, `timestamp`, `cwd`, `transcriptPath`, `agentId`, `agentType`, `agentName`, `agentDisplayName?`, `response`, `stopReason`. Nothing documented for a killed subagent. Confirmed: same page.
- The `general-purpose` agent emits neither event. Confirmed: same page.
- Every hook fired inside a subagent adds `agentId`, `agentType` and `parentAgentId` ("when known"); root hook inputs are unchanged. Tool hooks fire for subagent tool calls, and are re-emitted on the parent. Confirmed: same page; changelog 1.0.49, 1.0.81.
- `notification` with `agent_completed` / `agent_idle` covers background agents, with no documented agent id. `agentStop` is the main agent only. Confirmed: same page.
- Start (`agentName`) and stop (`agentId`) can't be paired by id from the docs alone.

### Process and terminal

- In-process: "Both top-level sessions and subagent invocations … use span kind INTERNAL (in-process)". Each has its own session and transcript. Confirmed: cli-command-reference (OpenTelemetry section), changelog 1.0.81.
- No tmux or pane mode is documented. `COPILOT_MULTIPLEXER` only tells Copilot which multiplexer it runs in (`copilot help environment`).

### Typing into one

- In `/tasks`, Enter will "teleport into the selected subagent's own session view", and "you can send it a steering message from the prompt box". Inside the ship's one terminal. Confirmed: cli-command-reference; changelog 1.0.79, 1.0.92.

### Do `--plugin-dir` hooks reach them?

- Plugin hooks are combined with policy, user and project hooks and "all hook entries from all sources are run", and hooks fire inside subagents, so Admiral's plugin hooks should fire for subagent tool calls. The docs never say outright that `--plugin-dir` plugins contribute hooks (`bundle.go` already marks `--plugin-dir` loading unconfirmed). Unconfirmed, strongly implied: [cli-hooks-reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference), changelog 0.0.421, 1.0.49.

## The Commodore skill

Source: `~/.agents/skills/commodore/` and `~/.agents/skills/agent-teammates/` (the same files as `~/github_repos_Personal/skills/skills/engineering/`).

- Commodore does not use Claude Code agent teams, the Agent tool or tmux panes to start teammates. It delegates every launch to the `agent-teammates` skill through `launch-teammate.sh --role <role>` (`commodore/SKILL.md:46-52`). A teammate is "a full agent session started in the background on the run's harness" (`agent-teammates/SKILL.md:8`). The `~/.claude/teams/<run>/` folders are Commodore run directories (`commodore/scripts/team-init.sh:23`), not agent-team configs.
- Per harness (`agent-teammates/scripts/drivers/<harness>/launch`):
  - **Claude Code**: `cd <worktree> && claude --bg -n <name> [--disallowedTools …] [--advisor …] --model … --permission-mode … "You are teammate <name>. Read <prompt> …"` (`drivers/claude/launch:15`). Re-tasked with SendMessage to the session name (`references/harness-claude.md:20-26`); watched with `claude agents --json` (`drivers/claude/status:7`). If `--bg` is unavailable it falls back to an in-process Agent-tool subagent (`harness-claude.md:48-57`).
  - **Codex**: `codex exec --json -C <worktree> …` detached in its own process group, stdout to an events file, one turn per job, resumed with `codex exec resume` (`drivers/codex/launch:17-25`, `drivers/codex/lib.zsh:43-47`, `drivers/codex/send:15-19`). No TTY.
  - **Copilot**: `copilot -p <prompt> < /dev/null > <state>.out`, one turn per job, resumed by `--session-id` (`drivers/copilot/lib.zsh:67`, `drivers/copilot/driver.env:1-4`). No TTY.
- Each teammate runs in its own git worktree (`agent-teammates/SKILL.md:80`, `commodore/SKILL.md:126`).
- None of these launches passes Admiral's `--settings`, `-c hooks.…` or `--plugin-dir`, so Admiral's hooks never fire in a Commodore teammate.
- The registry `<run>/teammates.tsv` holds `name id tasked-epoch|closed viewer-ref|- workdir` (`agent-teammates/scripts/team-env.zsh:21`). The run dir is `${AGENT_TEAMS_DIR:-${CLAUDE_CONFIG_DIR:-~/.claude}/teams}/<run>`.
- `VIEWER=tmux` in `team.env` makes the Claude driver run `tmux split-window -d -h -P -F '#{pane_id}' "claude attach <id>"` when `$TMUX` is set (`drivers/claude/launch:34`). Inside a ship that targets Admiral's socket and the ship's pane, because tmux takes the socket from `$TMUX` and the pane from `$TMUX_PANE` (`man tmux`, ADR 0013). Default is `none` (`agent-teammates/SKILL.md:30`). Codex and Copilot have no viewer: "They can't [attach]" (`commodore/references/harness-codex.md:56`, `harness-copilot.md:55`).
- The coordinator itself uses subagents for searches on Claude Code ("Send searches to a subagent", `commodore/SKILL.md:160`); on Codex it is told there are none (`commodore/references/harness-codex.md:49`).

## Implications for Admiral

What already happens with today's hooks:

- On all three harnesses, subagent tool calls and permission prompts run Admiral's hooks against the ship's pane. A subagent's approval prompt rightly flags the ship needs-you. On Codex a sub-agent's turn end fires `SubagentStop`, not `Stop`, so it doesn't flag the ship.
- On Claude Code the lead's `Stop` can fire (needs-you) while background subagents still work, and their next tool call flips the ship back to working. `Stop`'s `background_tasks[]` could tell the two apart.
- Commodore teammates are invisible to Admiral on every harness: no hooks, no pane.

Options, which can be combined:

1. **Hooks write crew to a pane option.** Hook `SubagentStart`/`SubagentStop` (all three harnesses) and write e.g. `@admiral-crew` on the ship's pane, keyed by `agent_id`, with `agent_type` as the label.
   - Gains: works for every harness's subagents, fits ADR 0007/0012 (tmux is the record).
   - Costs: the hook must parse JSON from stdin, so it can't stay a bare `tmux set`; a small script or a hidden `admiral hook` subcommand is needed, and on Codex any hook change brings back the review dialog once per fleet (ADR 0013), and an install-path command would bring it back on every upgrade. Copilot's start has no `agentId` and `general-purpose` sends nothing; Claude's `SubagentStop` also fires for internal agents. A subagent that crashes or is killed may never send stop, so crew entries need clearing when the ship's turn ends.
   - Terminal: none. The flotilla could show these crew boats read-only (from `agent_transcript_path` / `last_assistant_message`) or not open them at all.
2. **Claude Code agent teams in tmux mode: teammates as panes in the ship's session.** Launch Claude ships with `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` and `--teammate-mode tmux` (or `auto`, since Admiral's panes are inside tmux).
   - Gains: each teammate is its own process in its own pane on Admiral's socket, named by pane title, recorded with `tmuxPaneId`, likely with Admiral's hooks so each gets its own `@admiral-state`. The bridge can stream a pane per crew boat and keystrokes go straight to that teammate.
   - Costs: experimental feature; most of the tmux behaviour is binary-only and needs a probe; Claude Code splits the ship's window (ship pane down to ~30%), so the bridge must stream panes rather than the window and resize panes itself; the teammate panes sit in the ship's session, which ADR 0002/0012 assume has one pane; only Claude Code has it; and Commodore would have to switch from `claude --bg` to agent teams, which is a change in the skills repo, not Admiral.
3. **Commodore `VIEWER=tmux`: `claude attach <id>` panes.** Already supported by Commodore's Claude driver; inside a ship it splits the ship's window on Admiral's socket.
   - Gains: no Claude Code change, gives each Commodore teammate a live, typeable terminal.
   - Costs: Claude Code only; off by default (set per run in `team.env`); the pane is a viewer, so Admiral's hooks don't run in the teammate and its state would come from `claude agents --json` or nowhere; Admiral would have to recognise these panes (nothing marks them, though the command is `claude attach <id>`).
4. **Admiral reads Commodore's `teammates.tsv` or `claude agents --json`.** Admiral could list teammates and run `claude attach <id>` in a pane of its own.
   - Costs: couples Admiral to the skill's private file format and to Claude Code's supervisor; nothing links a background session to the ship that started it except the run directory; Codex and Copilot teammates still have no terminal. It also goes against the spirit of ADR 0003 (Admiral owns its own launching).
5. **Steer the harness's own sub-agent view through keystrokes.** Copilot `/tasks` + Enter, Codex `/subagents` or Alt+Left/Right, Claude's ↑/↓ + Enter in the panel. The ship's single stream shows the chosen sub-agent.
   - Costs: screen-driving that breaks with every UI change, which ADR 0007 rules out for state, and there is still only one stream per ship.

Open questions before deciding:

- Probe Claude Code agent teams inside an Admiral ship: does tmux mode split the ship's pane on Admiral's socket, are `--settings` hooks forwarded, and does `Stop` fire in a teammate's own process?
- Whether Commodore should move its Claude teammates to agent teams, or set `VIEWER=tmux` when it runs inside Admiral.
- Whether crew boats without a terminal (all subagents, all Codex and Copilot crew) appear in the flotilla read-only or not at all.
