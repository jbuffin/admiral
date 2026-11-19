# How each harness runs on native Windows 11

Research for [#873](https://github.com/jbuffin/admiral/issues/873), a child of [Map: Admiral runs natively on Windows](https://github.com/jbuffin/admiral/issues/872). It extends §2 of [`windows-and-native-apps.md`](windows-and-native-apps.md), which only said that all three harnesses run natively. This note does not pick a design.

Research notes, 2026-11-19. Sources are the vendors' docs as published that day, the Codex source at `openai/codex` main (`65454c7b99`, latest release `rust-v0.170.0`), and the Copilot CLI 1.0.114 package's own files. Nothing here was run on Windows: there is no Windows machine in this research. Claims marked **unconfirmed** come from inference, not from a source.

## Summary

| | Claude Code | Codex CLI | Copilot CLI |
| --- | --- | --- | --- |
| **Shell for tool calls** | Two shell tools. `Bash` runs in Git Bash when Git for Windows is installed. `PowerShell` runs `pwsh.exe` (7+), else `powershell.exe` (5.1). With Git Bash, both tools are on and PowerShell is "the primary shell"; without it, only PowerShell exists | PowerShell: `pwsh` on PATH or at `C:\Program Files\PowerShell\7\pwsh.exe`, else `powershell.exe`, else `cmd.exe`. `$SHELL` is ignored on Windows | The `powershell` tool (the `bash` tool's Windows twin). `pwsh` 6+, falling back to `powershell.exe` since 1.0.45 |
| **Can the shell be pinned?** | Yes, to Bash: install Git for Windows and set `CLAUDE_CODE_USE_POWERSHELL_TOOL=0`. To PowerShell: leave Git Bash out. Both are documented | No setting. The model may name a shell per `exec_command` call, which Codex maps to a detected shell type | No. `powershellFlags` sets flags only |
| **Hook form on Windows** | Shell form runs in Git Bash, or PowerShell without it. `"shell": "powershell"` forces PowerShell. Exec form (`command` + `args`) spawns a real `.exe` with no shell | The session's shell: `pwsh -NoProfile -Command "<command>"`. With no session shell, `%COMSPEC% /C`. `commandWindows` overrides the command on Windows | The `powershell` field, else the `command` fallback. A `bash`-only entry has no Windows command. `exec` + `args` spawns with no shell |
| **Hook env** | Inherits Claude's env, plus `CLAUDE_PROJECT_DIR`, `CLAUDE_CODE_SESSION_ID`, `CLAUDECODE=1`, `CLAUDE_CODE_CHILD_SESSION=1`, plugin vars | Starts empty (`env_clear`), then replays the session's environment snapshot, minus non-inheritable names. Plugin hooks also get `PLUGIN_ROOT`, `PLUGIN_DATA`, `CLAUDE_PLUGIN_*` | Not documented beyond the hook's own `env` field and `COPILOT_PLUGIN_DATA`. Inheriting the CLI's env is **unconfirmed** |
| **Config, transcripts** | `%USERPROFILE%\.claude` (or `CLAUDE_CONFIG_DIR`). Transcripts in `projects\<slug>\<session-id>.jsonl` | `%USERPROFILE%\.codex` (or `CODEX_HOME`). Rollouts in `sessions\…` (layout **unconfirmed** on Windows), hooks in `hooks.json` | `%USERPROFILE%\.copilot` (or `COPILOT_HOME`). Session events in `session-state\<id>\events.jsonl` (**unconfirmed** on Windows, inferred from `COPILOT_HOME`) |
| **Status line** | `statusLine` command, run through Git Bash or PowerShell | Built-in items only (`tui.status_line`); no command | Built-in items only (`/statusline`) |
| **Sandbox** | None on native Windows ("runs commands unsandboxed") | Native: `mxc` (Win 11 24H2+), `elevated` (admin setup, sandbox users, firewall rules), `unelevated` | Native OS sandbox, opt-in (`/sandbox enable`, `--sandbox`). Hooks run inside it |
| **Permission rules** | `PowerShell(...)` rules, AST-parsed, aliases canonicalised, case-insensitive. Bash deny rules turn the PowerShell tool off | Approval policy plus rules. PowerShell safety checks use tree-sitter, then PowerShell's own AST in a helper process | `shell(...)` rules cover both `bash` and `powershell` |
| **Install** | `irm https://claude.ai/install.ps1 \| iex`, `winget install Anthropic.ClaudeCode`, or npm (`win32-x64`, `win32-arm64`). Git for Windows is optional | `irm https://chatgpt.com/codex/install.ps1 \| iex` or `npm i -g @openai/codex`. Needs ConPTY and `winget`; Windows 11 recommended | `winget install GitHub.Copilot` or `npm i -g @github/copilot` (Node 22+). PowerShell 6+ is listed as a prerequisite. npm ships `win32-x64` and `win32-arm64` |

## Claude Code

### Shell for tool calls

From [Advanced setup](https://code.claude.com/docs/en/setup#set-up-on-windows) and [Tools reference: PowerShell tool](https://code.claude.com/docs/en/tools-reference#powershell-tool):

- "Without Git for Windows, Claude Code runs shell commands via the PowerShell tool." "With Git for Windows, Claude Code uses Git Bash for the Bash tool." `CLAUDE_CODE_GIT_BASH_PATH` points at `bash.exe` if detection fails.
- With Git Bash installed, the PowerShell tool is "on by default for claude.ai and Console accounts". `CLAUDE_CODE_USE_POWERSHELL_TOOL=0` turns it off; `=1` turns it on for Bedrock, Agent Platform and Foundry.
- "On Windows, Claude Code auto-detects `pwsh.exe` for PowerShell 7+ with a fallback to `powershell.exe` for PowerShell 5.1. When the tool is enabled, Claude treats PowerShell as the primary shell. The Bash tool remains available for POSIX scripts when Git Bash is installed."
- PowerShell is spawned with `-ExecutionPolicy Bypass` at process scope (`CLAUDE_CODE_POWERSHELL_RESPECT_EXECUTION_POLICY=1` stops that). By default the PowerShell tool is started through a `cmd.exe` launcher so background commands survive a backgrounded session (`CLAUDE_CODE_DISABLE_WINDOWS_SHELL_LAUNCHER`, [env vars](https://code.claude.com/docs/en/env-vars)). Profiles are not loaded.
- The Monitor tool exists on Windows only with Git Bash. Glob and Grep are default tools on Windows; elsewhere search goes through Bash.
- "On Windows with Git Bash installed, denying Bash also turns the PowerShell tool off for the session", silently, unless `CLAUDE_CODE_USE_POWERSHELL_TOOL=1` or a scoped `PowerShell(...)` rule is present.

**Pinning.** Bash only: Git for Windows plus `CLAUDE_CODE_USE_POWERSHELL_TOOL=0`. PowerShell only: no Git Bash. The env var can go in the `--settings` file's `env` block, which Admiral already passes per launch.

### Hooks

From the [hooks reference](https://code.claude.com/docs/en/hooks), "Command hook fields" and "Exec form and shell form":

- **Shell form** (no `args`): "The `command` string is passed to a shell: `sh -c` on macOS and Linux, Git Bash on Windows, or PowerShell when Git Bash isn't installed." The `shell` field (`"bash"` or `"powershell"`) chooses explicitly.
- **Exec form** (`args` present): no shell. "On Windows, exec form requires `command` to resolve to a real executable such as a `.exe`"; `.cmd` and `.bat` shims cannot be spawned.
- **Failure without Git Bash.** A shell-form `PreToolUse` or `PermissionRequest` hook with `onFailure` unset blocks the call on any exit code other than 0 or 2 ("blocking because Git Bash wasn't found…"). Admiral's guard already sets `onFailure: "block"`, so it is not affected by this default.
- **Matchers.** Hooks that inspect shell commands must match `Bash|PowerShell`. "On Windows without Git Bash, the tool is enabled automatically and Claude Code doesn't register the Bash tool at all. A hook that matches only `Bash` never fires there." The PowerShell tool's `tool_input` has the same fields as Bash (`command`, `description`, `timeout`, `run_in_background`).
- **Paths.** `tool_input.file_path` for Write, Edit and Read "arrives with backslash separators, even when your hook runs under Git Bash". Permission rules normalise Windows paths to POSIX form (`C:\Users\alice` → `/c/Users/alice`) before matching ([permissions](https://code.claude.com/docs/en/permissions)).
- **No TTY.** "Windows has no `/dev/tty`"; use `terminalSequence` for escape sequences.
- **Env.** The hook gets `CLAUDE_PROJECT_DIR`, the plugin variables, `CLAUDE_CODE_SESSION_ID` (matches `session_id`, updated on `/clear`), `CLAUDECODE=1` and `CLAUDE_CODE_CHILD_SESSION=1` ([env vars](https://code.claude.com/docs/en/env-vars)). `CLAUDE_ENV_FILE` is not given to a hook that runs in PowerShell. The PowerShell tool only picks up its assignments when every line is a plain assignment, and never one whose value contains `/` or `\`.
- **Input.** The stdin JSON is unchanged: `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name` and so on.

### Status line

[Status line: Windows configuration](https://code.claude.com/docs/en/statusline#windows-configuration): "Claude Code runs status line commands through Git Bash when Git Bash is installed, or through PowerShell when Git Bash is absent." Under Git Bash, backslashes in the command path are eaten, so use forward slashes. The status line runs only after the workspace trust dialog has been accepted.

### Files

- `~/.claude` is `%USERPROFILE%\.claude`, or `CLAUDE_CONFIG_DIR` ([settings](https://code.claude.com/docs/en/settings)).
- Transcripts are at `~/.claude/projects/<project>/<session-id>.jsonl`, where `<project>` is the working directory "with non-alphanumeric characters replaced by `-`", truncated to 200 characters plus a hash when longer ([sessions](https://code.claude.com/docs/en/sessions)). `C:\Users\me\repo` therefore becomes `C--Users-me-repo`. Admiral's `ProjectSlug` already does this, but not the 200-character truncation.
- `.claude/settings.local.json` is saved next to `.claude/settings.json` on Windows, not at the repository root.

### Sandbox, prompts, install

- Sandbox: "On native Windows, Claude Code runs commands unsandboxed" ([sandboxing](https://code.claude.com/docs/en/sandboxing)).
- Permission prompts: `PowerShell(...)` rules use the Bash rule shape. "Common aliases are canonicalized before matching… Matching is case-insensitive. Claude Code parses the PowerShell AST and checks each command in a compound command independently", splitting on `|`, `;`, and on 7+ `&&` and `||`. A command with a UNC path always prompts.
- Install: `irm https://claude.ai/install.ps1 | iex`, the `install.cmd` variant, `winget install Anthropic.ClaudeCode` (no auto-update), or npm (`win32-x64`, `win32-arm64`). Windows 10 1809+, x64 or ARM64. The binary is Authenticode-signed. The native install lives in `%USERPROFILE%\.local\bin\claude.exe`.

## Codex CLI

### Shell for tool calls

From the source, [`codex-rs/shell-command/src/shell_detect.rs`](https://github.com/openai/codex/blob/main/codex-rs/shell-command/src/shell_detect.rs):

- `default_user_shell_from_path`: "if cfg!(windows) { get_shell(ShellType::PowerShell).unwrap_or_else(ultimate_fallback_shell) }". PowerShell is looked up as `pwsh` on PATH, then `C:\Program Files\PowerShell\7\pwsh.exe`, then `powershell`, then `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`. The last resort is `cmd.exe`. `get_user_shell_path` returns `None` on Windows, so `$SHELL` plays no part.
- Store-installed PowerShell (under `WindowsApps`) "cannot run under MXC and can be inaccessible to the elevated sandbox account", so Codex swaps in an unpackaged one for sandboxed runs.
- The unified exec tool accepts a shell path from the model, and `get_shell_by_model_provided_path` turns it into a detected shell type. A model could ask for `bash` or `cmd` on a given call. Whether models do that on Windows is **unconfirmed**.
- `derive_exec_args` (in [`core/src/shell.rs`](https://github.com/openai/codex/blob/main/codex-rs/core/src/shell.rs)) runs PowerShell as `<pwsh> [-NoProfile] -Command <script>` and cmd as `cmd /c <script>`.
- The [config reference](https://learn.chatgpt.com/docs/config-file/config-reference) has no key that picks the shell, only `allow_login_shell`, `shell_environment_policy.*` and `features.shell_tool`.

**Pinning.** Not possible by configuration. Install PowerShell 7 to get `pwsh` rather than 5.1.

### Hooks

From the [hooks docs](https://learn.chatgpt.com/docs/hooks) and [`hooks/src/engine/command_runner.rs`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/engine/command_runner.rs):

- The config form is the same on Windows. User hooks are in `%USERPROFILE%\.codex\hooks.json` (or `$CODEX_HOME`), system hooks in `%ProgramData%\OpenAI\Codex\hooks.json`, plus inline `[hooks]` and `-c hooks.<Event>=…`. "`commandWindows` is an optional Windows-only command override."
- The shell is the session's own. `build_hooks_config` in [`core/src/session/mod.rs`](https://github.com/openai/codex/blob/main/codex-rs/core/src/session/mod.rs) takes the turn environment's shell and its `derive_exec_args("", false)`, so a hook runs as `pwsh -NoProfile -Command "<command>"`. With no shell, `build_command` uses `%COMSPEC%` (default `cmd.exe`) with `/C` and the command quoted raw. The process gets `CREATE_NO_WINDOW`.
- Env: "Both launchers start with an empty environment. Replay the session snapshot before hook overrides, filtering restricted names." On Windows the hook's env is the session's environment snapshot, not Codex's live process env. Whether a variable set on the pane at launch (such as `ADMIRAL_PANE`) reaches that snapshot is **unconfirmed**; it is likely, since the snapshot is taken from Codex's own environment.
- Tool names: shell commands and `exec_command` match as `Bash` on Windows too ("Match as `Bash`").
- Input carries `session_id` (the thread id), `transcript_path` (the rollout, "if any") and `turn_id`.
- Hook trust works as it does on macOS: non-managed hooks need review, `--dangerously-bypass-hook-trust` skips it, and plugin hooks are re-reviewed when their scripts change.

### App-server socket (codexlive)

`--listen unix://PATH` and `--remote unix://PATH` are accepted on every platform. The `codex-uds` crate uses `uds_windows` on Windows, where "`uds_windows` represents the rendezvous as a regular path", and protects the directory with a user-only DACL ([`codex-rs/uds/src/lib.rs`](https://github.com/openai/codex/blob/main/codex-rs/uds/src/lib.rs)). The shared-daemon directory helpers are `#[cfg(unix)]` only. Admiral's `codexpane` treats the listen path as a symlink into Codex's daemon directory; on Windows it is a plain file, so that logic needs a Windows branch. Go's `net.Dial("unix", …)` supports AF_UNIX on Windows 10 1803+.

### Status line, sandbox, prompts, install

- Status line: `tui.status_line` is a list of built-in item ids, with no command. Admiral does not set one for Codex.
- Sandbox ([Windows sandbox](https://developers.openai.com/codex/windows)): `mxc` is recommended where available (Windows 11 24H2 build 26100.9278 or 25H2 build 26200.9278; `features.prefer_mxc = true` in the CLI). `elevated` "uses dedicated lower-privilege sandbox users, filesystem permission boundaries, firewall rules, and local policy changes" and needs admin-approved setup (`codex sandbox setup --elevated`). `unelevated` uses a restricted token and ACLs, has weaker network isolation and no denied reads. Both legacy modes use a private desktop. MXC kills leftover child processes when the foreground command exits. Logs go to `CODEX_HOME/.sandbox/sandbox.log`.
- Permission prompts: same approval policy and TUI. PowerShell commands are judged by [`command_safety`](https://github.com/openai/codex/tree/main/codex-rs/shell-command/src/command_safety). A tree-sitter lowering "recognize[s] a deliberately small literal PowerShell subset and fail[s] closed for everything else". `powershell_parser.rs` keeps "one long-lived parser process per executable path", which runs PowerShell's own AST parser.
- Install ([CLI](https://learn.chatgpt.com/docs/codex/cli)): `powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 | iex"` or `npm install -g @openai/codex`. Windows 11 is recommended; Windows 10 needs 1809+ for ConPTY. `winget` should be present. arm64 builds are **unconfirmed** from the docs.

## GitHub Copilot CLI

### Shell for tool calls

- The shell tools come in pairs, `bash` / `powershell`, plus `list_`, `read_`, `stop_` and `write_` ([CLI command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference)). On Windows the agent uses `powershell`. That the `bash` tool is absent there is **unconfirmed**, but the paired names and the prerequisite point that way.
- The prerequisite is "(On Windows) PowerShell v6 or higher" ([install](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/install-copilot-cli)). The 1.0.45 changelog (in the package's `changelog.json`) adds "Fall back to Windows PowerShell (powershell.exe) when PowerShell 7+ (pwsh) is not available on Windows". Later entries mention "avoiding && chaining instructions when using legacy PowerShell 5.x".
- `powershellFlags` (e.g. `copilot config powershellFlags -- -NoProfile`) passes flags to the shell on win32 only. Nothing selects the shell itself.
- The `$` shortcut opens `%COMSPEC%` on Windows.

### Hooks

From the [CLI hooks reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-hooks-reference):

- Fields: `bash` ("Shell command for Unix"), `powershell` ("Shell command for Windows"), and `command` ("Cross-platform fallback. Copied to both `bash` and `powershell` when those fields are absent"). Alternatively `exec` + `args` "runs the executable directly without a shell"; it cannot be combined with the others. Also `cwd`, `env` and `timeoutSec` (default 30).
- **Admiral's Copilot hooks set only `bash`** (`copilotHooks` in `internal/launcher/bundle.go`). On Windows they would define no command and never run. They need `command`, `powershell`, or `exec`/`args`.
- Which binary runs the `powershell` field, `pwsh` or `powershell.exe`, and with which flags, is **unconfirmed**. The docs don't say, and the Windows binary's embedded code is compressed. The bundled `browser-use` plugin's Windows hook uses PowerShell syntax (`$ErrorActionPreference = 'Stop'; … $env:…; exit $LASTEXITCODE`).
- User hooks live in `%USERPROFILE%\.copilot\hooks\` (or `$COPILOT_HOME/hooks/`), policy hooks in `C:\ProgramData\GitHub\Copilot\policy.d\*.json` or under `HKLM\Software\Policies\GitHub\Copilot`. Hook files with a UTF-8 BOM load since 1.0.106.
- Input: camelCase payloads carry `sessionId`, `timestamp` and `cwd`. `transcriptPath` arrives only on `agentStop`, `subagentStart`, `subagentStop` and `preCompact`. PascalCase `PreToolUse` reports both `bash` and `powershell` as `tool_name: "Bash"`.
- Env: no general list is documented. When the session sandbox is on, user, repository and plugin hooks "run inside it, under the same sandbox policy rules as the agent's shell commands". Policy hooks run on the host. "A hook's `cwd` and `env` fields don't widen that access."

### Status line, sandbox, prompts, install

- Status line: `/statusline` picks built-in items. Admiral sets none for Copilot.
- Sandbox: a native OS sandbox on Windows, opt-in per session (`/sandbox enable`, `--sandbox`) or forced by an enterprise floor. It is deny-by-default on the filesystem and grants `PATH` directories read-only ([local sandboxing](https://docs.github.com/en/copilot/concepts/security-governance-and-network-settings/understanding-local-sandboxing)). Whether it is on by default for new users is **unconfirmed**.
- Permission prompts: `shell(...)` rules, such as `shell(git:*)`, cover both shell tools. Approvals for PowerShell can require "full-script review" (changelog).
- Install: `winget install GitHub.Copilot` or `npm install -g @github/copilot` (Node 22+). npm has `@github/copilot-win32-x64` and `@github/copilot-win32-arm64` (1.0.114); each ships a single `copilot.exe`.

## What this means for Admiral

These are inputs to the grilling tickets, not decisions.

1. **The guard parses bash, and only Claude Code can be held to bash.** Codex and Copilot always run PowerShell on Windows. Claude Code runs Bash only when Git for Windows is installed and `CLAUDE_CODE_USE_POWERSHELL_TOOL=0` is set. A guard that keeps parsing bash must either drop PowerShell for those two harnesses (not possible) or learn PowerShell. Codex's own answer is a literal-subset parser that fails closed, with PowerShell's AST parser as a helper process.
2. **Matchers must name PowerShell.** The guard's `Bash|Monitor` group misses Claude's `PowerShell` tool. A Bash deny rule silently turns the PowerShell tool off.
3. **Hook commands should be exec form, not a shell string.** `admiral hook <event>` as a shell string runs in Git Bash, PowerShell or cmd depending on the harness and the machine. Exec form avoids all of that: Claude Code's `command` + `args`, and Copilot's `exec` + `args`. Codex has no exec form, so `command` (or `commandWindows`) runs under `pwsh -NoProfile -Command`, where a bare `admiral hook Stop` works if `admiral.exe` is on PATH or given as an absolute path with `&`. Copilot's `bash`-only entries must change in any case.
4. **Pane state from hooks needs a variable that reaches them.** Claude Code passes its env through. Codex replays a session snapshot. Copilot is undocumented. `TMUX_PANE` becomes something like `ADMIRAL_PANE` set by the ConPTY host at spawn. A Windows probe should confirm it arrives in all three, and inside Copilot's sandbox.
5. **Status line.** Only Claude's `statusLine` matters. It runs `admiral statusline` through Git Bash or PowerShell, and the wrapper's own `sh -c` for the engineer's line has no Windows equivalent yet.
6. **Transcript paths** keep their layout under `%USERPROFILE%`. Claude's slug turns `C:\` into `C--`, and Codex's app-server socket is a plain file on Windows.

## Not confirmed

- Which PowerShell runs Copilot's `powershell` hook field, its flags, and whether hooks inherit the CLI's environment.
- Whether Copilot registers the `bash` tool at all on Windows.
- Codex rollout layout under `%USERPROFILE%\.codex\sessions\` on Windows, and Codex arm64 Windows builds.
- Whether Codex models pick a non-PowerShell shell on their own through the unified exec `shell` argument.
- Whether Copilot's sandbox is on by default.
- Everything above about Windows behaviour comes from docs and source, not a run. A probe on the arm64 VM, like the macOS probes in [`harness-waiting-signals.md`](harness-waiting-signals.md), should settle the hook env and shell questions before the spec relies on them.
