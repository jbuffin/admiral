# 10. Fleets are set up by `admiral setup` in a JSON config

Status: accepted

## Context

Fleets come from Admiral's own configuration, not Commodore Launcher's profiles (ADR 0003). Each fleet decides, per harness, the executable, arguments and environment a session starts with, and since ADR 0009 the search roots its ships run in. The engineer changes this a few times a year. Commodore Launcher already solved the same problem for its profiles: a setup wizard that detects installed harnesses and writes `config.json`, `profile add/edit` commands, and validation strict enough that a misspelled `env` never runs a harness as the wrong identity. Admiral may run as a launchd service (ADR 0005), which has no terminal and a minimal `PATH`, and is often opened from an iPad.

## Decision

Follow Commodore Launcher's configuration model, adapted to fleets ([How the engineer sets up fleets](https://github.com/jbuffin/admiral/issues/13)).

- **One file, `~/.admiral/config.json`.** JSON, with `"version": 1` required; a newer version is refused. `ADMIRAL_HOME` replaces `~/.admiral/`, which is also Admiral's config dir: plugins live in `~/.admiral/plugins/`. The engineer may edit the file by hand. Admiral watches it and updates marinas and docks live.
- **`admiral setup` writes it.** It detects the harness executables, wrappers on `PATH` and the *names* of `~/.claude*`, `~/.codex*` and `~/.copilot*` directories (it never reads inside them), and asks the engineer to confirm each choice: fleet key, name, color, harnesses, each harness's executable (written as an absolute path), identity directory and extra variables, and search roots (suggested from folders under `~` that hold git checkouts, none picked for you). Re-running it only adds or updates, shows a diff and asks before writing. `--answers file --yes` and `--dry-run` run it unattended.
- **Setup runs in the browser too.** With no fleets, the land shows one empty "Set up a fleet" marina. Tapping it opens a bridge-style terminal on Admiral's tmux socket running `admiral setup`; when the file is written the terminal closes and the marina sails in. Tapping the header's fleet count opens the same terminal later. It is not a ship. On a terminal, `admiral` with no config offers setup.
- **Commands as in Commodore Launcher:** `admiral fleet list | add | edit | check` (with `--executable`, `--arg`, `--env`, `--unset-env`, `--search-root`, `--remove-search-root`; `check` resolves the executable without running it) and `admiral config show | validate | edit` (`edit` saves only a result that validates). Every write keeps unknown top-level keys.
- **A fleet** is keyed by letters, digits, `-` and `_` (`fleets.work`), and holds `name`, optional `color`, `search_roots` (absolute or `~/` paths) and one entry per harness it allows (`claude`, `codex`, `copilot`), each with `executable`, `args` and `env`. The executable defaults to the harness's command name; `env` is merged over Admiral's environment with `~` expanded; the harness is started as an argument list, never through a shell. File order is marina order. There is no default harness (the dock picks it), working directory (the checkout is it), resume arguments (resuming is out of the MVP) or harness types (the three are built in).
- **Safety rules carry over.** An unknown key inside a fleet or a harness is an error, not a warning. Two fleets using the same harness must set different `CLAUDE_CONFIG_DIR` / `CODEX_HOME` / `COPILOT_HOME`: `setup` and `fleet add/edit` refuse a shared one, and a hand edit that shares one loads with a warning on both docks. Nothing clears inherited identity variables, so each harness should set its own.
- **A bad file never takes Admiral down.** An invalid file keeps the last good config in use (none at startup) and shows a banner naming the file and the error. A missing executable is flagged on its dock. A fleet with no search roots shows its docks but cannot launch.
- **Running ships keep the settings they launched with.** A change applies to the next launch. A ship whose fleet or harness is removed stays at sea in its recorded fleet color.
- **The UI writes two parts of the file:** `favorite_skills` (up to five skill names, starred in launch orders) and `sources` (the order sources and their settings, from their forms). Fleets are never edited in the UI except through the setup terminal.

## Considered options

- **Setup wizard plus hand edits, in JSON** (chosen). The wizard gets a new engineer going and the file stays plain. JSON round-trips without loss when setup, `fleet edit` or the UI rewrite it.
- **TOML.** Nicer to edit by hand and allows comments, but Go cannot rewrite it without losing the engineer's comments and layout, and the file is rewritten often.
- **A settings screen.** Lots of UI for something changed rarely, and it would turn a LAN browser into an editor for executables and environment. The setup terminal gives the browser the same reach with no new UI.
- **Hand edits only.** Leaves a new engineer facing an empty file, and the launchd service has no terminal to help them.

## Consequences

- Fleets can be set up from an iPad, behind the LAN token, because the setup terminal is a shell-equivalent the token already allows.
- The engineer who knows Commodore Launcher recognises the commands, flags and error codes; its profiles are not imported (ADR 0003).
- A configured skill list is gone: favorites replace it, and launch orders always offers Commodore first.
- Where Admiral's data dir is (state, the freeform list's file) is still open.
