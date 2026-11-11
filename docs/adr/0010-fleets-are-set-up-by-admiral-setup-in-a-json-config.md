# 10. Fleets are set up by `admiral setup` in a JSON config

Status: accepted, amended below (sources belong to a fleet, fleets gain `owners` and `clone_root`, the config migrates itself at startup, and the HTTP API may edit four fleet fields)

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
- **The UI writes two parts of the file:** `favorite_skills` (up to five skill names, starred in launch orders) and `sources` (the order sources and their settings, from their forms, and each panel's autopilot entry under `sources.<key>.autopilot`, from the panel's Autopilot form: [#181](https://github.com/jbuffin/admiral/issues/181), [ADR 0022](0022-a-ui-save-of-autopilot-args-passes-the-crew-allowlist.md)). Fleets are never edited in the UI except through the setup terminal. The top-level `autopilot` and `resources` keys stay hand-edited.

## Considered options

- **Setup wizard plus hand edits, in JSON** (chosen). The wizard gets a new engineer going and the file stays plain. JSON round-trips without loss when setup, `fleet edit` or the UI rewrite it.
- **TOML.** Nicer to edit by hand and allows comments, but Go cannot rewrite it without losing the engineer's comments and layout, and the file is rewritten often.
- **A settings screen.** Lots of UI for something changed rarely, and it would turn a LAN browser into an editor for executables and environment. The setup terminal gives the browser the same reach with no new UI.
- **Hand edits only.** Leaves a new engineer facing an empty file, and the launchd service has no terminal to help them.

## Consequences

- Fleets can be set up from an iPad, behind the LAN token, because the setup terminal is a shell-equivalent the token already allows.
- The engineer who knows Commodore Launcher recognises the commands, flags and error codes; its profiles are not imported (ADR 0003).
- A configured skill list is gone: favorites replace it, and launch orders always offers Commodore first. A Commodore the skill index cannot find is shown disabled ("Commodore not found") and cannot launch.
- Admiral's data dir is `~/.admiral/` too (ADR 0012).

## Amendment (2026-11-10)

From [Grilling: how do order sources work per fleet, with GitHub built in?](https://github.com/jbuffin/admiral/issues/227) ([ADR 0006](0006-order-sources-are-subprocess-plugins.md)):

- **Sources belong to a fleet.** `sources` stays top-level and written by the UI, and each entry has a required `fleet`. Built-in GitHub is a source with `"type": "github"`, one per fleet. The autopilot entry's `fleet` field goes, since a view's fleet is its source's; [#240](https://github.com/jbuffin/admiral/issues/240) later removed the panel autopilot entries altogether.
- **Two new fleet fields.** `owners` lists the orgs and user accounts whose GitHub repos route to the fleet. `clone_root` is where a missing repo is cloned on launch, by default the fleet's first search root ([ADR 0009](0009-ships-run-in-a-checkout-picked-from-the-fleets-search-roots.md)).

From [Grilling: how does the redesign roll out, and how do today's sources migrate?](https://github.com/jbuffin/admiral/issues/247), **the config migrates itself at startup**, in the first release whose server needs it, whatever the state of the new UI. It writes `config.json.pre-<version>` first, is safe to run twice, and posts one Inbox item saying what changed. The release notes say so too.

- **GitHub.** Each fleet gets a built-in GitHub source with the default views. Each custom panel query from the old global `github` plugin (anything beyond Assigned to me and Review requested) is copied as a custom view onto every fleet. Routing then shows each fleet only its share, so nothing is lost or shown twice; empty views can be deleted by hand.
- **Other plugin sources** go to the default fleet and can be moved in Settings.
- **Freeform.** The text of pending orders goes into the migration's Inbox item, and then they are removed. Nothing becomes a launch or a commitment on its own.
- **`state.json`.** Ship positions and paused autopilot panels are deleted with the old UI, or with the server change that retires them. Each fleet's last-used directories and the kept inbox records stay.

## Amendment (2026-11-11)

From [Grilling: settings for fleets and their order sources](https://github.com/jbuffin/admiral/issues/271):

- **The setup terminal is no longer the only way to edit a fleet.** The HTTP API, and so the UI, may edit a fleet's `search_roots`, `owners`, `clone_root` and `color` (`PUT /api/fleets/{key}`). This supersedes the Decision bullet "Fleets are never edited in the UI except through the setup terminal" and the rejected "settings screen" option, for those four fields only.
- **Docks and env stay in the setup terminal.** Executables, args and env, and the fleet's name and key, are not writable through the settings API; changing them still goes through the setup terminal (or `admiral fleet`).
- **A built-in GitHub source stays with its fleet.** Other plugin sources can be moved to another fleet (`PUT /api/sources/{key}` with `fleet`); a GitHub source is refused, and the engineer adds one to the other fleet instead.
- **The UI writes three parts of the file**, not two: `favorite_skills`, `sources`, and now the four fleet fields above.
- **`clone_root` must be an absolute path or start with `~/`**, like a search root; nothing read it before, so no stored config breaks.
