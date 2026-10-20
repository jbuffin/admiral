# The command line

`admiral` with no command starts the server. On a terminal with no fleets it first asks whether to run setup (not under launchd, where stdin is not a terminal). The other commands work on `$ADMIRAL_HOME`. Exit codes: 0 ok, 1 error, 2 usage (unknown command, bad flags, `setup --yes` without `--answers`).

| command | what it does |
| --- | --- |
| `admiral setup [--answers <file> --yes] [--dry-run]` | Finds your harnesses, wrappers (`claude-work`...), the names of `~/.claude*`, `~/.codex*` and `~/.copilot*` (never what is inside them) and folders under `~` that hold git checkouts, asks for each fleet, shows the change as a diff and asks before writing. It only adds or updates: fleets it isn't told about and top-level keys it doesn't know stay as they are. Interactively, a re-run lists the fleets already in the file, takes each default from the fleet you name, offers a wrapper named `<harness>-<key>` as the executable, and ends on an empty key. If `config.json` changes while setup waits to write, it plans again on the new file and asks again. It refuses a fleet that shares an identity directory with another fleet for the same harness. |
| `admiral fleet list \| add \| edit \| check [<key>]` | `add <key> --name N` and `edit <key>` take `--color`, `--harness`, `--executable <harness>=<path>`, `--arg <harness>=<arg>`, `--env <harness>=<NAME>=<value>`, `--unset-env <harness>=<NAME>`, `--search-root` and `--remove-search-root`; every flag may repeat, and the key comes first. On `edit`, the `--arg` values for a harness replace its arguments. `~` is stored as typed. `check` resolves each executable without running it and exits 1 when one is missing; with a key it checks only that fleet. |
| `admiral config show \| validate \| edit` | `edit` opens a copy in `$EDITOR` (`vi` without one) and saves it only if it loads, so it also fixes a file that is invalid now. |
| `admiral token rotate [--port N]` | Writes a new token and prints the new "On other devices" URL. A running server notices and signs out every device that isn't on this Mac, dashboard and bridge alike. `--port` (default 7420) is the port of the URL and of the server it looks for (`GET /api/ships` carries a `home` field, a hash of `ADMIRAL_HOME`, which the CLI compares); a server on that port that serves another `ADMIRAL_HOME` doesn't count, and the command says there is no running server for this home. |
| `admiral ships [--port N] [--names]` | Lists ships (`NAME SESSION FLEET HARNESS STATUS REPO`, tab-separated) as the running server on `--port` sees them, if it serves this `ADMIRAL_HOME`. Otherwise it reads tmux and says so on the first line. `--names` prints display names only. |
| `admiral attach <ship>` | Attaches this terminal to the ship's tmux session (`Ctrl-b d` detaches), by display name or session name. Read-only for an anchored ship. `_admiral` and `_setup` are refused. |
| `admiral completion zsh \| bash` | Completes ship names for `attach`. |
| `admiral service install \| uninstall \| status` | `install` writes the LaunchAgent `~/Library/LaunchAgents/dev.jasonbuffin.admiral.plist`, points it at the stable path (`$(brew --prefix)/bin/admiral` under Homebrew, never the versioned Cellar path), gives ships the `PATH` of your login shell as it is when you run `install` (run it again after changing `PATH`), logs to `$ADMIRAL_HOME/logs/admiral.log`, loads it and prints the "On other devices" URL; `uninstall` unloads it and removes the plist; `status` says `Service: not installed`, `installed, not loaded` or `installed, loaded` (from the exit code of `launchctl print`) and prints the URL (`No token yet; Admiral creates it the first time it starts.` when there is no token). |
| `admiral --version` | Prints `admiral <version> (<commit>)`. |

The answers file for `setup --answers` is JSON holding the same `fleets` object as `config.json`, in the order the fleets should have:

```json
{"fleets": {"work": {"name": "Work", "search_roots": ["~/code"],
                     "claude": {"env": {"CLAUDE_CONFIG_DIR": "~/.claude-work"}},
                     "codex": {}}}}
```

An `executable` left out is looked up on `PATH` (and `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`) and written as the absolute path found, without following symlinks. The identity variable (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `COPILOT_HOME`) is always written, with the harness's default directory when the file leaves it out; two fleets that leave it out share that default and are refused.
