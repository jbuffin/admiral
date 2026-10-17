# 13. Each harness starts with the prompt on its command line

Status: accepted

## Context

Admiral starts each ship's harness itself (ADR 0003) as an argument list, with no shell, from the fleet's executable, `args` and `env` (ADR 0010), in the checkout picked in launch orders (ADR 0009). Launch orders picks Commodore, a favorite skill, a custom skill or no skill ("Just launch"), and the order is freeform text. ADR 0007 adds Admiral's needs-you hooks to every launch. Commodore Launcher verified that all three harnesses submit a prompt given at launch and run a skill named in it. A probe against Claude Code 2.1.302, Codex CLI 0.162.1 and Copilot CLI 1.0.98 checked how each one parses that prompt, stores folder trust and keys Codex's hook trust ([Launch command line per harness](https://github.com/jbuffin/admiral/issues/37)).

## Decision

**The command line** is the fleet's executable, the fleet's `args`, Admiral's flags, then the prompt:

```
claude  <args> --settings ~/.admiral/harness/claude-settings.json -- '<prompt>'
codex   <args> -c 'hooks.<Event>=[…]' … -c 'tui.notification_method="bel"' -- '<prompt>'
copilot <args> --plugin-dir ~/.admiral/harness/copilot --interactive='<prompt>'
```

`--` lets a prompt start with `-` on Claude Code and Codex, and Copilot only takes such a prompt in the `--interactive=` form.

**The prompt** is `/<skill> <order text>` on Claude Code and Copilot and `$<skill> <order text>` on Codex, for Commodore, favorites and custom skills alike. The order text is one argument, newlines kept, with only leading and trailing whitespace trimmed. "Just launch" sends the order text exactly as written.

**A custom skill name** is used as typed, except that one leading `/` or `$` is stripped. A name containing whitespace is refused in the field ("Skill names have no spaces").

**Every hook is `tmux set -p @admiral-state <state>`.** tmux finds Admiral's socket and the pane from `$TMUX` and `$TMUX_PANE`, so the command names no ship, socket or install path, and the Codex hook definitions stay identical across ships, fleets and upgrades. Admiral appends the directory of the tmux it uses to the end of each ship's `PATH`.

**The bundle lives in `~/.admiral/harness/`:** Claude Code's settings file and Copilot's plugin folder. Admiral rewrites them at every start and all fleets share them. Codex's hooks need no file.

**Admiral skips no harness dialog.**
- Codex hooks are trusted through Codex's own review dialog, once per fleet, as in ADR 0007. Admiral never passes `hooks.state`, although a per-launch `-c 'hooks.state={…}'` with Codex's hash would skip the dialog.
- Search roots are not pre-trusted. Each harness asks for folder trust the first time it runs in a checkout, and the engineer answers on the bridge.
- Codex's "Update available" dialog is left on. A fleet that wants it off adds `-c check_for_update_on_startup=false` to its `args`.

The no-first-hook rule (ADR 0007) shows a ship stopped on any of these dialogs as needs-you.

## Considered options

- **Pre-trusting search roots.** It would save one dialog per checkout per fleet, but the harnesses differ. Claude Code has no per-launch skip, so Admiral would write `hasTrustDialogAccepted` into the fleet's `.claude.json`, inside the identity directory Admiral never touches (ADR 0010). Copilot only skips it with `COPILOT_ALLOW_ALL=true`, which also approves every tool. Only Codex has a clean per-launch override (`-c 'projects={…}'`).
- **Pre-trusting Codex hooks with `hooks.state`.** It works for one launch and writes nothing, but it forges the input of a safety check from an undocumented hash format read in Codex's source.
- **Guarding "Just launch" text.** Text starting with `/` runs a slash command on Claude Code and Copilot, and on Claude Code a prompt that is exactly a subcommand name (`doctor`, `update`) runs that subcommand even after `--`. A leading space would prevent both, but "Just launch" promises the text unchanged.
- **Hooks that call the Admiral binary.** A Homebrew upgrade changes its path, which would change the hook definitions and bring back Codex's review dialog in every fleet.

## Consequences

- An order whose whole text is a Claude Code subcommand name, launched with "Just launch" on Claude Code, runs that subcommand. Accepted.
- On Codex, a fleet's own `-c hooks.<Event>=…` for an event Admiral hooks is replaced by Admiral's, because the last `-c` wins. Repeated `--settings` and `--plugin-dir` combine with the fleet's own.
- The first launch in each checkout stops on folder trust, on every harness and in every fleet.
- A hook bundle change that alters a Codex hook definition brings back the review dialog once per fleet.
