# What in the jbuffin/skills package fails on native Windows

Research for [#988](https://github.com/jbuffin/admiral/issues/988), a child of [Map: Admiral runs natively on Windows](https://github.com/jbuffin/admiral/issues/872). It builds on [`windows-harnesses.md`](https://github.com/jbuffin/admiral/blob/research/windows-harnesses/docs/research/windows-harnesses.md) (branch `research/windows-harnesses`), which covers how each harness runs tool calls on Windows, and does not repeat it. Settled before this note: Git for Windows is required on Windows, and every Claude ship there is pinned to Git Bash. This note does not pick a design.

Research notes, 2026-11-27. Sources:

- The package: `jbuffin/skills` at `1cfe42f` (plugin `jasonbuffin` 3.6.0), read from a local clone.
- Admiral at `origin/main` `30efe9d`: [ADR 0040](../adr/0040-admiral-installs-commodore-as-a-harness-plugin.md), `internal/skillindex`, `internal/commodore`, `internal/cli/crew.go`.
- Vendor docs as published that day: Claude Code [plugin loading reference](https://code.claude.com/docs/en/plugins/loading.md), [plugin manifest reference](https://code.claude.com/docs/en/plugins-reference), [agent view](https://code.claude.com/docs/en/agent-view.md); GitHub [Copilot CLI plugin reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-plugin-reference); the Codex source at `openai/codex` main `8cae463a69` (`codex-rs/core-plugins`).
- Git for Windows: [`make-file-list.sh`](https://github.com/git-for-windows/build-extra/blob/main/make-file-list.sh) (which packages the installer ships, `build-extra` at `ba8198790c`) and [`installer/install.iss`](https://github.com/git-for-windows/build-extra/blob/main/installer/install.iss) (defaults); release `v2.56.1.windows.1`.
- Python [Using Python on Windows](https://docs.python.org/3/using/windows.html) and [`os` open flag constants](https://docs.python.org/3/library/os.html#open-flag-constants); the [cmux README](https://github.com/manaflow-ai/cmux).
- **Read-only probes on the Windows 11 arm64 test VM** (25H2, Git for Windows 2.56.1, Claude Code, Codex and Copilot CLI installed, no plugin installed), run over SSH: tool lookups in PowerShell and in Git Bash, a throwaway `git clone` of `jbuffin/skills` to read its line endings, and small Git Bash behaviour checks. Marked **(VM)** below. No harness, plugin or script from the package was run on Windows.

Claims marked **unconfirmed** come from inference, not from a source or a probe.

## Summary

- **Nothing in the package runs on Windows today.** Every shell script is zsh (`#!/bin/zsh` or a sourced `.zsh`), and Git for Windows has no zsh. In Git Bash a script fails at once with `/bin/zsh: bad interpreter: No such file or directory` **(VM)**. That covers Commodore, agent-teammates (every driver, the admiral host driver included), crew's `context.sh`, run-preflight and setup-commodore.
- **The scripts are zsh, not POSIX sh with a zsh shebang.** They use `${0:A:h}`, `${(P)v}`, `${(f)…}`, glob qualifiers `(N)` `(Nom[1])`, `zstat`, `zsystem flock` and `<1->` patterns throughout. Changing the shebang to bash is a port, not an edit.
- **Python is a second missing runtime.** Windows and Git for Windows ship no Python. On the VM, `python3` resolves to the Microsoft Store alias, which prints "Python was not found; run without arguments to install from the Microsoft Store" **(VM)**. Even with Python installed, `crew.py` and `convert-roles.py` use `os.O_NOFOLLOW` and `crew.py` and `mission.py` use `os.O_NONBLOCK`, neither of which exists on Windows, and `crew.py` runs zsh scripts (`has-skill`, `harness-available.sh`) as programs, which Windows can't do.
- **jq, tmux, cmux, `security`, `uuidgen`, `setsid` and `ps -o` are missing too**; Git for Windows does provide bash, coreutils, sed, gawk, grep, findutils, perl, curl, `column`, and an MSYS `ps` without `-o` **(VM)**.
- **All three harnesses install the plugin by cloning it with the system git**, so on Windows the files come out with Git for Windows' default `core.autocrlf=true`. The package's `.gitattributes` pins LF only for `*.sh`, `*.zsh` and `*.py`. The extensionless driver operations (`drivers/*/launch`, `status`, …), every `driver.env`, the `.md` and the `.json` files check out CRLF **(VM)**.
- **`skillindex` needs nothing new to find the plugin**: it builds for Windows and reads the three install records by `filepath`. `internal/commodore`, which runs the install, does not build for Windows (`syscall.SysProcAttr.Setpgid`, `syscall.Kill`).
- **The teammate viewer has no Windows form.** cmux is "macOS only, for now", and Git for Windows ships no tmux. Windows Terminal's `wt` is on the VM **(VM)**.
- **A Codex or Copilot ship told to run a `.sh` path runs it from PowerShell, which has no `bash` on PATH** with Git for Windows' default PATH choice **(VM)**. The `.sh` file association opens `git-bash.exe` instead (**unconfirmed** how PowerShell hands it over), and the zsh shebang fails inside that anyway.

## Inventory

The package's `scripts/` folders hold 84 files besides docs: 71 zsh files (`#!/bin/zsh` executables, and `.zsh` or `lib.zsh`/`skills.zsh` files they source), 9 Python scripts and 4 `driver.env` data files. `target-evaluation` has no scripts. Commodore's `SKILL.md` and the briefs run them by literal absolute path; crew's runs `zsh <skill>/scripts/context.sh`.

What Git for Windows provides, from `make-file-list.sh`: the installer takes `git`, `git-extra`, `openssh`, `msys2-runtime`, `sed awk grep findutils coreutils` and, in the full build, `mintty vim nano winpty less gnupg tar diffutils patch dos2unix which` and `perl-JSON` (so perl), plus their dependencies. It excludes `util-linux` and `curl` (the MinGW curl ships instead), and adds `tmux` only under a build flag (`INCLUDE_TMUX`) that `installer/release.sh` never sets. The VM's Git Bash matches: `bash sed awk grep mktemp stat sort timeout perl column ps tail date cygpath curl` present; `zsh python3 jq gh tmux flock uuidgen setsid` absent; `ps` takes only `-aefls -u -p` **(VM)**.

`gh` is not part of Git for Windows; it installs separately (as it does on macOS). Commodore needs it, so it is listed as a dependency below but is not a Windows-specific gap.

| Script (under `skills/engineering/`) | Interpreter | Tools it calls beyond coreutils, sed, awk, grep | Git for Windows provides? |
| --- | --- | --- | --- |
| `agent-teammates/scripts/launch-teammate.sh` | zsh | git | No: zsh |
| `agent-teammates/scripts/team-env.zsh`, `advisor-family.zsh` (sourced by every script) | zsh | default `CMUX=/Applications/cmux.app/…` | No: zsh |
| `agent-teammates/scripts/close-teammate.sh` | zsh | tmux, cmux | No: zsh, tmux, cmux |
| `agent-teammates/scripts/detect-harness.sh`, `harness-available.sh`, `retask.sh`, `retasked.sh`, `teammate-tail.sh` | zsh | (none) | No: zsh |
| `agent-teammates/scripts/evidence.sh` | zsh | `zsh -c` for the proof command, `zsystem flock`, git | No: zsh (the lock is zsh's own module; `flock` is absent too) |
| `agent-teammates/scripts/verify-report.sh`, `watch-teammates.sh` | zsh | `zstat`, git | No: zsh |
| `agent-teammates/scripts/drivers/admiral/*` (11 zsh files) | zsh | admiral, jq, python3, `/usr/bin/python3` (hard path in `status`), `/bin/sleep`, git (from Python) | No: zsh, jq, python3. `/bin/sleep` resolves in MSYS; `/usr/bin/python3` never does |
| `agent-teammates/scripts/drivers/claude/*` (11 zsh files) | zsh | claude (`--bg`, `agents`, `attach`, `stop`), jq, python3, `zstat`, git, tmux or cmux for the viewer | No: zsh, jq, python3, tmux, cmux |
| `agent-teammates/scripts/drivers/codex/*` (12 zsh files) | zsh | codex `exec`, jq, python3 (`os.setsid` to start a turn, `os.replace`), `ps -o pid,pgid,args`, `kill -- -<pgid>`, `/bin/sleep`, `tail -c` | No: zsh, jq, python3; `ps -o` unsupported; `os.setsid` absent on Windows |
| `agent-teammates/scripts/drivers/copilot/*` (11 zsh files) | zsh | copilot, `perl -e 'setpgrp(0,0)'`, `uuidgen` (fallback `/proc/sys/kernel/random/uuid`), `ps -o`, `kill -- -<pgid>`, jq, `zsh -f -c`, git | No: zsh, jq, uuidgen (and the `/proc` fallback is absent **(VM)**); `ps -o` unsupported. perl is present, but MSYS process groups don't reach native `copilot.exe` children (**unconfirmed**) |
| `commodore/scripts/admiral-io.sh` | zsh | admiral (`advise`), runs `schema-check.py` by path | No: zsh, python3 |
| `commodore/scripts/advisor-probe.sh`, `review-round.sh`, `push-branch.sh`, `state-set.sh` | zsh | git, mktemp | No: zsh |
| `commodore/scripts/pr-ready.sh` | zsh | gh, jq | No: zsh, jq; gh separate |
| `commodore/scripts/team-init.sh` | zsh | python3, `cmux identify`; writes `REQUIRED_TOOLS="git gh python3 jq security curl"` into every team.env | No: zsh, python3, cmux; and that list includes `security` |
| `commodore/scripts/team-preflight.sh` | zsh | gh (`extension list`), git, `sort -V`, tmux/cmux checks, runs `crew.py` and run-preflight | No: zsh. `sort -V` works **(VM)** |
| `commodore/scripts/unit-worktree.sh` | zsh | git, gh (`pr view`, `stack`), `cp -cR` (APFS clone, falls back to `cp -R`), `sort -V` | No: zsh; the clone fallback works as written |
| `commodore/scripts/watch-ci.sh` | zsh | gh (`run list/watch/view`), git | No: zsh |
| `commodore/scripts/crew.py`, `mission.py` | python3 | `os.O_NOFOLLOW`, `os.O_NONBLOCK`, `os.link`; runs `drivers/<h>/has-skill` and `harness-available.sh` with `subprocess` | No Python. With Python: `AttributeError` on the two flags, and `OSError` running a zsh script, which `has_skill` turns into "not installed", so every named skill is dropped |
| `commodore/scripts/schema-check.py`, `cards-draft.py`, `answer-check.py`, `advise-check.py` | python3 | stdlib only (`os.link`, `os.replace`, `tempfile`) | No Python; otherwise portable (`os.link` works on NTFS) |
| `crew/scripts/context.sh` | zsh | git | No: zsh |
| `retro/scripts/retro-analyze.py` | python3 | stdlib only | No Python; otherwise portable |
| `run-preflight/scripts/preflight.sh` | zsh | `security find-generic-password` (Keychain), gh, curl, git, runs `ci-branch-check.py` | No: zsh, python3, `security` (macOS only) |
| `run-preflight/scripts/secret-run.sh` | zsh | `security find-generic-password -w` | No: zsh, `security` |
| `run-preflight/scripts/ci-branch-check.py` | python3 | stdlib only | No Python; otherwise portable |
| `setup-commodore/scripts/crew-draft.sh` | zsh | jq, git, `ln -s`, `env -u`, mktemp | No: zsh, jq. Git Bash's `ln -s` makes a copy, not a link **(VM)**, so `check` keeps the first draft it copied |
| `setup-commodore/scripts/convert-roles.py` | python3 | `os.O_NOFOLLOW` | No Python; with Python, `AttributeError` |

Python's docs list `O_NOFOLLOW` among the flags "not present if they are not defined by the C library" and `O_NONBLOCK` as Unix-only; the Windows CRT defines neither. Python's install manager puts `python`, `py` and a compatibility `python3` on PATH, and its `py` launcher handles `#!/usr/bin/env python3`, but none of that is there until the engineer installs Python.

### Line endings after a plugin install

A throwaway `git clone https://github.com/jbuffin/skills` with the VM's Git for Windows (system `core.autocrlf=true`, the installer's default "Checkout Windows-style, commit Unix-style") gives, from `git ls-files --eol` **(VM)**:

```text
i/lf    w/crlf  attr/text=auto   skills/engineering/agent-teammates/scripts/drivers/claude/driver.env
i/lf    w/crlf  attr/text=auto   skills/engineering/agent-teammates/scripts/drivers/claude/launch
i/lf    w/crlf  attr/text=auto   skills/engineering/commodore/SKILL.md
```

The `.gitattributes` comment says "Scripts must keep LF endings or their shebangs break", but the rule covers only `*.sh`, `*.zsh` and `*.py`, and 38 of the zsh executables (the driver operations) have no extension. Git Bash itself is forgiving: a CRLF bash script ran correctly, and gawk read `SEND=tool\r` as `tool`, but bash's `read` kept the `\r` **(VM)**. A zsh from MSYS2 would not be forgiving (**unconfirmed**), and any port that reads `driver.env` or `team.env` line by line inherits the `\r`.

## How each harness installs the plugin on Windows

ADR 0040's commands (`internal/commodore`: `<harness> plugin marketplace add jbuffin/skills`, then `claude|copilot plugin install jasonbuffin@jbuffinskills` or `codex plugin add jasonbuffin@jbuffinskills`) are the same on Windows; nothing in the vendor docs or the Codex source makes them platform-specific. All three clone with the system git, so the CRLF note above applies to all three.

| | Claude Code | Codex CLI | Copilot CLI |
| --- | --- | --- | --- |
| **Fetch** | "The clone or download of a marketplace added from GitHub" in `marketplaces/<name>/` (loading reference). On macOS that copy holds a `.git` directory | `clone_git_source` in `core-plugins/src/marketplace_add/install.rs` runs `git clone` (and `sparse-checkout`) through PATH lookup, passing only `-c` `SAFE_BARE_REPOSITORY_CONFIG` and `GIT_TERMINAL_PROMPT=0`: no `core.autocrlf` override | Not documented. On macOS the marketplace cache `~/Library/Caches/copilot/marketplaces/jbuffin-skills/` holds a `.git` directory, so it is a clone |
| **Marketplace copy on Windows** | `%USERPROFILE%\.claude\plugins\marketplaces\<name>` (the VM's `known_marketplaces.json` records `C:\\Users\\jason\\.claude\\plugins\\marketplaces\\claude-plugins-official`) **(VM)** | In `CODEX_HOME` (layout not checked) | `%LOCALAPPDATA%\copilot\marketplaces\` **(VM)**; the docs give only the Linux and macOS cache paths and `COPILOT_CACHE_HOME` |
| **Where the plugin lands** | `%USERPROFILE%\.claude\plugins\cache\jbuffinskills\jasonbuffin\3.6.0\` (`cache/<marketplace>/<plugin>/<version>/`, or under `CLAUDE_CODE_PLUGIN_CACHE_DIR`), recorded in `installed_plugins.json` with an absolute `installPath` | `%USERPROFILE%\.codex\plugins\cache\jbuffinskills\jasonbuffin\<version>\` (`store.rs` joins `CODEX_HOME`, `plugins/cache`, marketplace, plugin, version; the VM has `C:\Users\jason\.codex\plugins\cache\openai-curated-remote`) **(VM)**. Codex copies the tree and writes a generated `.codex-plugin/plugin.json` beside the `.claude-plugin/` one | `%USERPROFILE%\.copilot\installed-plugins\jbuffinskills\jasonbuffin\` (docs: `~/.copilot/installed-plugins/MARKETPLACE/PLUGIN-NAME`), listed in `config.json` `installedPlugins[].cache_path` |
| **Manifest read** | `.claude-plugin/plugin.json` | `.claude-plugin/plugin.json` (Codex generates its own beside it) | Docs: `.plugin/plugin.json`, `plugin.json`, `.github/plugin/plugin.json`, then `.claude-plugin/plugin.json`; the package has only the last |
| **Old versions** | Marked `.orphaned_at`, removed 14 days later | Replaced in place (`store.rs` backs up and swaps the version directory) | Not documented |
| **Paths handed to the skill** | "On Windows, most substituted paths use forward slashes, as in `C:/Users/you/...`" in skill content (manifest reference) | Not documented | Not documented |

Admiral's install runner is the part that breaks, not the harness: `internal/commodore/commodore.go` sets `SysProcAttr{Setpgid: true}` and cancels with `syscall.Kill(-pid, SIGKILL)`, and `GOOS=windows go build ./internal/commodore/` fails on both lines. It also writes `extraKnownMarketplaces.jbuffinskills.autoUpdate` into the identity's `settings.json`, which is path-neutral.

## What skillindex needs

`GOOS=windows go build ./internal/skillindex/` succeeds, and the reading is by `filepath`, so Windows separators and drive letters work:

- **Identity dir.** `config.IdentityDir` expands the defaults `~/.claude`, `~/.codex`, `~/.copilot` with `os.UserHomeDir`, which is `%USERPROFILE%` on Windows, or from a dock's own `HOME`. A dock whose `HOME` or `CLAUDE_CONFIG_DIR` is written in MSYS form (`/c/Users/me`) would not resolve (**unconfirmed** whether any setup path writes one).
- **Claude.** `claudePlugins` reads `installed_plugins.json` and takes `installPath` when `filepath.IsAbs`, which a `C:\…` path is. `enabledPlugins` keys are path-free. A project-scoped install is matched with `in.ProjectPath != dir`, an exact string compare; on Windows the same folder can differ by drive-letter case or separator (**unconfirmed** what Claude writes there), which only matters for a project-scope Commodore.
- **Codex.** `codexPlugins` reads `config.toml` `[plugins."jasonbuffin@jbuffinskills"]` and the highest version directory under `plugins\cache\jbuffinskills\jasonbuffin` holding `.claude-plugin\plugin.json`. The Codex copy keeps that file, so it is found.
- **Copilot.** `copilotPlugins` takes `cache_path` from `config.json` when it is absolute and `within` `installed-plugins` after `EvalSymlinks`. That a Windows install writes an absolute `C:\…` `cache_path` there is **unconfirmed**; the key itself is undocumented.
- **Junctions.** The Codex standalone install is reached through junctions under `%USERPROFILE%\.codex\packages`; if a harness ever puts a plugin copy behind a junction, `within` resolves it with `filepath.EvalSymlinks` (whether that follows a junction is **unconfirmed**). Nothing seen so far does.

So `skillindex` finds the plugin on Windows without changes, pending one probe of each install record. Inside the package, the Claude, Codex and Copilot drivers' own `skills.zsh` (the `has-skill` and `list-skills` operations `crew.py cast` and setup-commodore rely on) search the harness's skill locations with zsh globs and are part of the port. (Not Windows-specific: the Copilot driver's `skills.zsh` reads `skills/`, `~/.agents/skills` and Copilot's built-ins but no `installed-plugins`, so it doesn't see the plugin's own skills on any platform.)

## The admiral host driver

`drivers/admiral/*` is where a ship's teammates go: inside a ship (`ADMIRAL_SHIP` set) every Claude teammate, and every Codex or Copilot teammate outside autopilot, is a crew boat launched with `admiral crew launch|send|list|close`. From the harness it needs no more than any other script:

- **A shell that runs it**: zsh today. The driver is 11 zsh files that also call jq, python3 (one call by the hard path `/usr/bin/python3`, which no Windows install provides) and `/bin/sleep`.
- **Environment through to the tool call**: `admiral crew` refuses without `ADMIRAL_SHIP`, and finds Admiral's home from `ADMIRAL_HOME` or `ADMIRAL_REPORT` (`internal/cli/crew.go`). The driver also reads `ADMIRAL_AUTOPILOT`. Claude passes its environment to tool calls; for Codex and Copilot see `windows-harnesses.md` (Codex snapshots it; Copilot is undocumented).
- **`admiral` on the tool shell's PATH**: Admiral already prepends `$ADMIRAL_HOME/bin`. Git Bash finds `admiral.exe` there by its bare name; PowerShell does too.
- **Its harness's driver** for `has-skill`, `list-skills`, `advisor-probe` and the transcript read in `tail`, and the harness driver for a read-only Codex or Copilot teammate, which runs headless outside Admiral. Those are the Codex and Copilot drivers' process-group code (`setsid`, `perl setpgrp`, `ps -o`, `kill -- -<pgid>`), which has no Git Bash equivalent for native processes.

Admiral's side of these commands is out of scope here: `admiral crew` talks to tmux on `$ADMIRAL_HOME/tmux.sock` today, which the map's session-host work replaces.

Two details of the arguments the driver hands Admiral, for whoever ports it:

- A read-only Claude teammate gets `--disallowedTools "Edit(/$W/**)"`. In Git Bash `$W` is `/c/Users/…`, giving `Edit(//c/Users/…/**)`, an absolute-path rule in the POSIX form Claude normalises Windows paths to before matching (`windows-harnesses.md`, permissions). It should match (**unconfirmed**).
- A Copilot teammate gets `--available-tools bash view edit create glob grep` (the admiral driver and `cp_flags` in the Copilot driver). On Windows Copilot's shell tool is `powershell` (`windows-harnesses.md`), so this list would leave a Copilot teammate with no shell at all (**unconfirmed** until run).

## The teammate viewer

`VIEWER` in team.env opens a pane attached to each Claude teammate outside Admiral (`drivers/claude/launch`, `close-teammate.sh`):

- `VIEWER=tmux` needs `$TMUX` and runs `tmux split-window … "claude attach <id>"`. Git for Windows ships no tmux.
- `VIEWER=cmux` calls `/Applications/cmux.app/Contents/Resources/bin/cmux`. cmux is "macOS only, for now" (README).
- Inside a ship the admiral driver ignores `VIEWER`: Admiral is the viewer.

What the viewer attaches to does work on Windows: Claude's background sessions run under a supervisor on Windows too (agent view lists a Windows-only stop failure, `supervisor (pid=<pid>) is still running`, and a Windows note for `claude attach`). A Windows viewer could open `claude attach <id>` in a Windows Terminal split: `wt` is on the VM **(VM)**. Codex and Copilot teammates have no viewer on any platform.

## What a Codex or Copilot ship does with a `.sh` instruction

Commodore's `SKILL.md` and its briefs tell the agent to run each script "bare: one command, run directly by its literal absolute path". On Windows a Codex or Copilot ship runs that line in PowerShell (`windows-harnesses.md`). What follows:

1. **No bash on PowerShell's PATH.** The installer's recommended PATH choice ("Git from the command line and also from 3rd-party software") adds only Git's wrappers; the VM's PowerShell finds no `bash`, `sh`, `sed`, `awk`, `perl` or `jq` **(VM)**. Where WSL is installed, `bash` instead resolves to `C:\Windows\System32\bash.exe`, the WSL launcher (absent on the VM), which would run the script in Linux against `/mnt/c` paths (**unconfirmed**).
2. **The `.sh` association.** The default installer component "Associate .sh files to be run with Bash" registers `sh_auto_file` as `"C:\Program Files\Git\git-bash.exe" --no-cd "%L" %*`, and the VM has it **(VM)**. PowerShell invoking a file that isn't an executable hands it to its association, so the likely result is a new Git Bash window that the tool call doesn't wait for and whose output never reaches the agent (**unconfirmed**: not run, because it opens a window on the desktop).
3. **The shebang fails anyway.** In that window, or if the model calls `& 'C:\Program Files\Git\bin\bash.exe' <script>` itself, bash reads `#!/bin/zsh` and stops with "bad interpreter" **(VM)**.
4. **Paths and allow rules.** The scripts print and expect POSIX paths; a PowerShell model passing `C:\…` paths to a Git Bash script would need them converted (`cygpath` is there **(VM)**). Copilot allow rules are `shell(<command>)` over both shell tools, and Admiral's guard parses bash (`windows-harnesses.md`), so a wrapped `bash.exe <script>` line would need its own rule shape.

The result is the same for every script: the run stops at `team-init.sh`, its first step.

## Decisions this raises

1. **What the scripts become on Windows.** Options: a bash port that runs in Git Bash (keeps one shell language, still leaves Codex and Copilot calling from PowerShell); Python throughout (needs Python on Windows, which is not settled); moving the run machinery into `admiral.exe` subcommands (one binary on every platform and every harness, but Commodore is meant to work without Admiral, ADR 0040); or PowerShell twins (two copies to keep in step).
2. **Whether Python becomes a Windows requirement**, beside Git for Windows. If yes, `crew.py`, `mission.py` and `convert-roles.py` need their `O_NOFOLLOW` / `O_NONBLOCK` paths guarded, and `crew.py` must run driver operations through an interpreter instead of executing them. If no, the nine Python scripts and the inline Python in the drivers move to whatever replaces them.
3. **What replaces jq**, which a Windows engineer would otherwise have to install (13 zsh files use it).
4. **How a ship's harness calls a script.** For Codex and Copilot, the instructions need a form PowerShell runs and the guard and allow rules can match, such as an explicit `bash.exe <script>`, `admiral <subcommand>`, or `python <script>`. For Claude pinned to Git Bash, the current bare-path form works once the interpreter exists.
5. **Line endings.** Either `.gitattributes` gains `* text=auto eol=lf` (or rules for the extensionless driver files and `driver.env`), or the driver operations get extensions. Without one of them every Windows install gets CRLF driver files whatever the scripts are ported to.
6. **Process control for Codex and Copilot teammates outside Admiral** (`setsid`, process-group kill, `ps -o`, `uuidgen`): Windows needs job objects or a different supervisor. Decide whether those drivers are ported, or whether on Windows teammates run only as Admiral crew boats (which still leaves the read-only Codex and Copilot teammates that the admiral driver hands to their own drivers).
7. **Keychain secrets.** `run-preflight`'s `security` calls and team.env's `REQUIRED_TOOLS="… security …"` have no Windows counterpart; Windows Credential Manager is the nearest.
8. **The teammate viewer on Windows**: none, a Windows Terminal split running `claude attach`, or Admiral only.
9. **Copilot's tool list.** `--available-tools bash …` must name `powershell` on Windows, or drop the list, before a Copilot teammate can run a command there.
10. **Admiral's installer runner** needs a Windows build of its process-group handling (`internal/commodore`), so Install Commodore can run at all.

## Not confirmed

- How PowerShell hands a `.sh` path to the Git Bash association, and what the agent sees.
- Whether Copilot writes an absolute Windows `cache_path` into `config.json`, and what Claude writes as a project install's `projectPath`.
- What Codex's Windows marketplace copy looks like under `CODEX_HOME`.
- Whether an MSYS process group (`perl setpgrp`, `kill -- -<pgid>`) reaches a native `copilot.exe` and its children.
- Whether the Copilot teammate's `--available-tools bash` leaves it shell-less on Windows.
- A real `claude|codex|copilot plugin install` on Windows. The probes cloned the repository with the VM's git, which is what each harness runs, but no harness install was run, to leave the VM's identities untouched. One install per harness on the VM, then `git ls-files --eol` in the landed copy and a `skillindex` read, would settle the install rows and the `skillindex` section.
