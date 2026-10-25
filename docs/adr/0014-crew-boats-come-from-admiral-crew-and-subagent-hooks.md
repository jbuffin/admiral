# 14. Crew boats come from `admiral crew` and the subagent hooks

Status: accepted

## Context

A ship's crew boats are its teammates and its subagents, and the flotilla switches the terminal to one ([How Admiral learns a session's crew boats](https://github.com/jbuffin/admiral/issues/5)). The research in [`docs/research/crew-boats.md`](../research/crew-boats.md) found two different things behind that word:

- **Subagents** run inside the ship's own process on Claude Code, Codex CLI and Copilot CLI. None has a process or terminal of its own. All three harnesses fire subagent start and stop hooks, and Admiral's launch hooks (ADR 0007) already reach them.
- **Teammates** are full sessions a skill starts. Commodore starts them through the `agent-teammates` skill as `claude --bg`, a detached `codex exec --json` or `copilot -p`: outside the ship's tmux session, without Admiral's hooks, and on Codex and Copilot with no TTY. Admiral cannot see them.

## Decision

**A teammate is a crew boat only when Admiral launched it.** Inside a ship, a skill starts teammates through Admiral:

- `admiral crew launch --name <name> --dir <dir> [--harness <harness>] [--role <role>] [--arg <value>]... -- <prompt>` starts an interactive harness the way a ship starts (ADR 0013): from the ship's fleet config, with Admiral's hooks and the prompt on the command line, in a new tmux window in the ship's tmux session. The harness defaults to the ship's and must be one the fleet allows. Each `--arg` is passed to the harness after Admiral's hook flags and before the prompt; a value that overrides or disables the hooks is refused. A caller with `ADMIRAL_AUTOPILOT=1` can launch only claude crew, and its `--arg` values must fit an allowlist of flags that don't loosen the dock (the autopilot args allowlist).
- `admiral crew send <name> <text>` types into it, `admiral crew list --json` lists the ship's crew boats with their state, and `admiral crew close <name>` ends one.
- Each ship has `ADMIRAL_SHIP=<session>` in its environment. A skill that sees it uses `admiral crew`; without it, the skill runs teammates its own way. Commodore needs no change of its own; only the `agent-teammates` drivers get an Admiral branch.
- The crew boat's window holds its record as window options next to the ship's session options (ADR 0012): `@admiral-crew-name`, `@admiral-harness`, `@admiral-dir`, `@admiral-launched` and, when `--role` was given, `@admiral-role`. Its pane carries its own `@admiral-state` from its own hooks.

**Subagents come from the harness's subagent hooks** and are kept on the ship's pane as `@admiral-subagents`: id, type and start time. They are drawn as a smaller mark that cannot be boarded. An interrupted subagent may never send its stop hook, so each harness has its own clearing rule:

- **Claude Code**: the list is replaced from every `Stop`'s `background_tasks[]`.
- **Codex CLI**: on `SubagentStop` only. Its subagents keep running after the lead is interrupted.
- **Copilot CLI**: on `subagentStop`, and also on the lead's next `userPromptSubmitted` or `agentStop`. Ctrl-C sends no hook at all.
- **Every harness**: when the pane dies.

A stop for an id Admiral never saw starts is ignored. Claude Code's internal agents send those after most turns.

**Every hook runs `admiral hook <event>`,** which reads the harness's JSON from stdin and sets the options on the pane named by `$TMUX_PANE` (`tmux set -p -t "$TMUX_PANE"`). A bare `set -p` writes to the session's active pane, which is wrong once a ship has crew windows. A working or waiting hook that comes from inside a subagent (Copilot's `agentStop` and `userPromptSubmitted` carry an `agentId`) does not change `@admiral-state`. This replaces ADR 0013's bare `tmux set -p @admiral-state …`. To keep Codex's hook trust across upgrades, the command names no path: Admiral keeps `~/.admiral/bin/admiral` pointing at the running binary, rewrites it at every start, and puts that directory first on each ship's and crew boat's `PATH`, so the hook text never changes and `admiral` always resolves to the build that launched the ship, never to another install earlier on `PATH`.

**Needs-you follows each crew boat.** A crew boat's own needs-you shows on the crew boat in the flotilla and also sets the ship needs-you in open water, so a waiting teammate is never hidden behind a quiet lead.

**Crew boats live and die with their window.** A teammate whose harness exits stays as an anchored crew boat until `admiral crew close` or the engineer closes it. When the ship goes, its tmux session goes, and its crew with it.

## Considered options

- **A raw convention** (env vars and tmux tags that each skill builds itself). No new CLI, but every skill would need the harness command lines, the hook flags and the interactive launch for each harness, and would break whenever Admiral changes them.
- **Claude Code agent teams in tmux mode.** Real panes per teammate, but experimental, Claude-only, known only from the minified binary, and it splits the ship's window to about 30% width.
- **Reading `claude agents --json` or Commodore's `teammates.tsv`.** Ties Admiral to another tool's private state against ADR 0003, and Codex and Copilot teammates still have no terminal.
- **Teammates only, no subagents.** Simpler, but hides work the engineer may want to see is happening.
- **Driving each harness's own subagent view with keystrokes.** Screen-driving, which ADR 0007 rules out.

## Consequences

- Commodore's teammates show as crew boats only after the `agent-teammates` drivers learn `admiral crew`. That change lives in the skills repo.
- On Codex and Copilot, a Commodore teammate inside Admiral becomes a live interactive session re-tasked by typing, not a one-turn headless job.
- Changing the hook command brings back Codex's hook review dialog once per fleet, one time.
- A ship's tmux session can have more than one window. Code that assumed one pane per session (ADRs 0002, 0012) targets the ship's first window.

Amended 2026-10-19 after the live check in [`docs/research/crew-boats.md`](../research/crew-boats.md) ([#72](https://github.com/jbuffin/admiral/issues/72)): hooks pass `-t "$TMUX_PANE"`, subagent-scoped hooks leave `@admiral-state` alone, Copilot subagents carry ids, and subagent entries clear by a rule for each harness.

Amended 2026-10-19 ([#69](https://github.com/jbuffin/admiral/issues/69)): `~/.admiral/bin` goes first on `PATH`, not just somewhere on it.
