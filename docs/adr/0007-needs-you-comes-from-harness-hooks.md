# 7. Needs-you comes from harness hooks

Status: accepted

## Context

A ship must flip to needs-you within a couple of seconds of its session waiting on the engineer, and back once answered. Needs-you covers a question, an approval, and a finished turn waiting for the next prompt. Claude Code, Codex CLI and Copilot CLI all have documented lifecycle hooks for these moments ([How each harness signals it is waiting on the engineer](https://github.com/jbuffin/admiral/issues/22)). Their transcripts hold the same events in undocumented formats, and the terminal screen changes with every harness release. Admiral starts each harness itself in a tmux session it owns (ADRs 0002 and 0003), so it can add hooks to each launch.

## Decision

Admiral learns a ship's state only from the harness's own hooks, added to each launch the way the harness expects: `--settings` for Claude Code, `--plugin-dir` for Copilot CLI, `-c hooks.…` for Codex CLI.

- **Waiting hooks** (approval, question, turn over) set needs-you. **Working hooks** (prompt submitted, tool use) clear it. A keystroke the engineer sends to the ship through the bridge also clears it at once, so a long command after an approval does not keep the ship shaking. The next waiting hook sets it again.
- **Each hook writes the state to a tmux pane option** on Admiral's socket (`tmux set -p @admiral-state …`). Admiral follows the option through tmux control mode. The state lives with the session, so a restarted Admiral reads it back.
- **Codex hooks are trusted once per fleet** through Codex's own "Hooks need review" dialog, which stores the trust in that fleet's `CODEX_HOME`. Admiral never bypasses trust or writes the trust hash itself, and keeps the hook command identical across launches so the trust holds.
- **Codex questions ring the bell.** Codex has no hook for its in-terminal questions, so Admiral launches it with `tui.notification_method="bel"`, and a bell in a Codex pane (tmux `alert-bell`) sets needs-you. This exception applies to Codex only.
- **No first hook within about 10 seconds of launch means needs-you.** Folder trust, workspace trust and Codex's hook review come before any hook can fire. Every launch passes the order's text as the prompt, so a healthy session fires a hook almost at once.

A crash fires no hook. The pane dying is the exit signal, and what that means for the ship is decided in [When a ship counts as docked](https://github.com/jbuffin/admiral/issues/7).

## Considered options

- **Harness hooks** (chosen). Documented and fast, and all three harnesses take them per launch without changes to the engineer's own settings, apart from Codex's one-time trust.
- **Transcripts.** They hold turn and tool events, but their formats are undocumented and differ per harness.
- **Terminal idle plus a prompt pattern.** It needs no harness support, but screen reading breaks whenever a harness changes its UI.
- **Hooks that call Admiral's server, or write marker files.** Hooks that call the server lose any state change sent while it is down, and marker files add a directory to watch and clean up. A tmux pane option needs nothing but `tmux`, which the hook already runs inside.

## Consequences

- Each harness needs its own hook bundle that Admiral writes and passes at launch, which must keep pace with each harness's hook names.
- The first Codex launch in each fleet stops on the hook review dialog. The 10-second rule shows that launch as needs-you.
- A startup dialog that clears in under 10 seconds is never flagged, and a slow first hook can flag a ship briefly. Both are accepted.
- A harness that drops or renames a hook event breaks needs-you for that harness until the bundle is updated. Admiral never falls back to screen reading.
