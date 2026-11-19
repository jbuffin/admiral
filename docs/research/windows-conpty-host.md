# Go building blocks for a ConPTY session host

Research for [#875](https://github.com/jbuffin/admiral/issues/875), a child of [Map: Admiral runs natively on Windows](https://github.com/jbuffin/admiral/issues/872). It picks up §2 option C of [`windows-and-native-apps.md`](windows-and-native-apps.md) and checks each building block against the tmux contract in §1 of that note and the bridge in [`docs/bridge-protocol.md`](../bridge-protocol.md).

Research notes, 2026-11-19. Library facts come from the module source fetched from `proxy.golang.org` on that date. "Probed" means a throwaway Go program on this Mac (Go 1.26.5) imported the library: it ran on darwin and cross-compiled with `CGO_ENABLED=0` for `windows/amd64` and `windows/arm64`. Nothing here was run on Windows.

## Summary

| Need | Use | Why |
| --- | --- | --- |
| ConPTY | Admiral's own small package on `golang.org/x/sys/windows`, seeded from `charmbracelet/x/conpty` (MIT) | The host needs three things no library gives together: `ReleasePseudoConsole`, a job in the same attribute list as the pseudoconsole, and the choice to load a bundled `conpty.dll` later. x/sys is already a direct dependency. |
| Headless terminal | `tuios.dev/vt` v0.3.3, behind an Admiral interface | It reports every mode the bridge's `join` carries, has a bell callback, scrollback, an ANSI `Render`, a snapshot package, and a conformance, fuzz and tmux-differential test suite. It is pure Go by default. |
| Host IPC | `github.com/Microsoft/go-winio` v0.6.4 named pipe with an SDDL that grants only the user's SID | It rejects remote clients and refuses to open a pipe name that already exists, which blocks squatting. Docker and containerd use it. |
| Ending a process tree | A job object per pane, from x/sys (`CreateJobObject`, `TerminateJobObject`), assigned at creation with `PROC_THREAD_ATTRIBUTE_JOB_LIST` | Children that leave the pseudoconsole still die with the ship. |
| Outliving its starter | A separate `admiral host` process started from its own logon task, or by the server with `DETACHED_PROCESS \| CREATE_NEW_PROCESS_GROUP \| CREATE_BREAKAWAY_FROM_JOB` | Closing the pseudoconsole kills its processes, so the process that holds the handles must outlive Admiral's server. |
| Minimum Windows | Windows 11 build 26100 (24H2) | Every Windows 11 release still serviced on 2026-11-19 is at or above it, and 26100 is the build that adds `ReleasePseudoConsole`. |

The main ConPTY risks, all for the prototype (#876) to measure:

1. **ConPTY sits between the harness and the host's emulator.** It answers some queries itself (DA1), drops some sequences (DCS and APC graphics on the inbox host), and passes mouse modes only after the app turns on VT input mode.
2. **Bracketed paste is not reliable on Windows.** Codex has a paste-burst heuristic because pastes arrive there as separate key events. The chat, resume and crew-send prompts must not depend on `ESC[200~`.
3. **Host death kills every ship.** That is no worse than a tmux server crash. But the host binary is locked while it runs, which affects upgrades.
4. **Inbox vs bundled ConPTY.** Windows Terminal's newer `conpty.dll` + `OpenConsole.exe` fixes several of the problems above. Bundling it is an option to keep open, not a day-one need.

## 1. ConPTY libraries

| | `UserExistsError/conpty` | `charmbracelet/x/conpty` | `aymanbagabas/go-pty` | raw `golang.org/x/sys/windows` |
| --- | --- | --- | --- | --- |
| Latest | v0.1.4, 2024-07-09 | v0.2.0, 2025-11-17 | v0.2.3, 2026-05-17 | v0.49.0 (Admiral requires v0.47.0) |
| Licence | MIT | MIT | MIT | BSD-3-Clause |
| Dependencies | x/sys (`go 1.15` in its go.mod) | x/sys only | creack/pty, u-root, x/crypto, x/sys | none |
| API | `Start(cmdline)`; `Wait` polls every 1 s | `New`/`NewWithPipes`, `Spawn(name, args, *syscall.ProcAttr)`, `Resize`, `Read`/`Write`, raw handles | `exec.Cmd`-like `pty.Command`, one API for Unix and Windows | `CreatePseudoConsole`, `ResizePseudoConsole`, `ClosePseudoConsole`, `NewProcThreadAttributeList`, `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`, job functions |
| Creation flags | no | yes, from `attr.Sys.CreationFlags` | yes, from `SysProcAttr.CreationFlags` | yes |
| State | quiet for 2 years | maintained in the Charm monorepo | active | the base the others use |

Sources: each module's zip at `proxy.golang.org`, `gh api repos/<owner>/<repo>`.

- The older note said go-pty "pulls in hcsshim". That is no longer true at v0.2.3. Its go.mod pulls u-root and x/crypto instead, for its SSH helpers.
- `charmbracelet/x/conpty`'s `NewWithPipes` creates the attribute list with room for exactly one attribute (the pseudoconsole). `Spawn` returns the pid and process handle, closes the thread handle, and passes `bInheritHandles = FALSE`. Its I/O is plain synchronous `ReadFile`/`WriteFile` on anonymous pipes, which is what ConPTY requires: "This is currently restricted to synchronous I/O" ([CreatePseudoConsole](https://learn.microsoft.com/en-us/windows/console/createpseudoconsole)).
- x/sys v0.49.0 has no `ReleasePseudoConsole` and no `PROC_THREAD_ATTRIBUTE_JOB_LIST` constant. Both are a few lines with `NewLazySystemDLL("kernel32.dll")` and the constant `0x2000D`.

**Recommendation.** Write `internal/conpty` (Windows only, about 300 lines) on x/sys and copy the process-creation helpers from `charmbracelet/x/conpty` with its MIT notice. That gives:

- an attribute list holding both `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` and `PROC_THREAD_ATTRIBUTE_JOB_LIST`, so the child starts inside its job with no `CREATE_SUSPENDED` race;
- `ReleasePseudoConsole` when `kernel32` exports it (look it up with `GetProcAddress`, not by build number: microsoft/terminal#19112, via the conpty-oxide notes below);
- one function table, so the same code can later call `ConptyCreatePseudoConsole` and the rest from a bundled `conpty.dll` ([`conpty-static.h`](https://github.com/microsoft/terminal/blob/main/src/inc/conpty-static.h) lists the exports: `Create`, `Resize`, `Clear`, `ShowHide`, `Reparent`, `Release`, `Close`, `Pack`).

For the prototype alone, `charmbracelet/x/conpty` as-is is fine.

## 2. Headless terminal emulators

| | `charmbracelet/x/vt` | `tuios.dev/vt` |
| --- | --- | --- |
| Latest | v0.1.0, 2026-10-12 (first and only tag; last commit to `vt/` 2026-10-12) | v0.3.3, 2026-11-16 (commits to `vt/` on 2026-11-12, 13, 16 and 17) |
| Licence | MIT | MIT. It started as a copy of charmbracelet/x/vt and keeps Charm's notice in `NOTICE`. |
| Go | 1.24.2 | 1.26.0 (Admiral is on 1.26.5) |
| cgo | none | none by default. The `ghostty` build tag swaps in libghostty-vt through cgo. |
| Scrollback | `Scrollback()`, `SetScrollbackSize`, `ScrollbackCellAt` | `NewWithScrollback`, scrollback with reflow on resize, `AppendScrollbackText`, soft-wrap flags |
| Screen with SGR | `Render()` | `Render()`; `snapshot.Capture` plus `snapshot.Replay` (bytes that repaint the state on any xterm) |
| Cursor | `CursorPosition`, `CursorHidden` | `CursorPosition`, `IsCursorHidden`, `CursorStyle`, pending wrap |
| Modes the `join` needs | `IsAltScreen()`. The rest only through `EnableMode`/`DisableMode` callbacks, so the host has to track them. | `IsAltScreen`, `ApplicationCursorKeys`, `BracketedPasteEnabled`, `GetModes()` (mouse modes by number) |
| Bell | `Callbacks.Bell` | `Callbacks.Bell` |
| Query replies | `Read` | `Read`, or `TakeResponses` after each `Write` with no extra goroutine |
| Tests | a handful | conformance corpus, fuzzing, differential tests against libghostty and against **tmux** (`differential_tmux_test.go`, behind a build tag) |
| Bus factor | Charm (company) | one main author (6,249 of about 6,700 commits to tuios) |

Probed: `tuios.dev/vt` fed `line1\r\nline2\r\n\x1b[?2004h\x1b[31mred\x07` reported `BracketedPasteEnabled() == true`, fired `Bell`, and `Render()` returned `\x1b[31mred\x1b[m` padded with spaces to 80 columns. The snapshot needs trailing blanks trimmed to match `capture-pane -p -e`. Both libraries compile for windows/amd64 and windows/arm64 with cgo off.

Other candidates in the older note: `gitpod-io/xterm-go` was not evaluated this time. `hinshun/vt10x` and similar are older and have no scrollback or alt screen worth comparing.

**Recommendation.** `tuios.dev/vt`, behind a small Admiral interface (`Write`, `Resize`, `Snapshot() (screen, scrollback, cursor, modes)`, `OnBell`, `Responses`). It answers every field of the bridge's `join` directly, and its tmux differential tests match the emulator Admiral uses on the Mac. The single-maintainer risk is real. Because both libraries share an ancestor and the interface is small, swapping to `charmbracelet/x/vt` costs one file.

## 3. IPC: named pipes with per-user ACLs

`github.com/Microsoft/go-winio` v0.6.4 (2026-10-13, MIT):

- `ListenPipe(path, &PipeConfig{SecurityDescriptor: sddl})` takes an SDDL string. Without one it builds the default named-pipe DACL (`RtlDefaultNpAcl`), which is not per-user. Always pass `D:P(A;;GA;;;<user SID>)`. Add SYSTEM only if something needs it.
- It always sets `FILE_PIPE_REJECT_REMOTE_CLIENTS`.
- The first instance is created with `FILE_CREATE`, so listening on a name another process already owns fails instead of sharing it. That is the squatting guard.
- `DialPipeContext` on the client. `admiral hook` would dial it from inside a pane.

Probed: compiles for windows/amd64 and windows/arm64 with cgo off.

Alternative: Go's `net.Listen("unix", …)` works on Windows 10 1803 and later with no new dependency, and the ACL then comes from the socket file's directory under `%LOCALAPPDATA%`. It is a reasonable fallback. Named pipes are the Windows-native choice and have the explicit DACL, so go-winio is the recommendation.

## 4. Ending a process tree: job objects

From [Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects):

- After a process is associated with a job, "any child processes it creates using CreateProcess are also associated with the job". The exceptions are processes created through WMI and breakaway, when the job allows it.
- Jobs nest from Windows 8 on, so a harness that makes its own job (Codex's Windows sandbox may) still sits inside Admiral's.
- `TerminateJobObject` ends every process in the job. With `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, closing the last handle does the same.

From [Creating a Pseudoconsole session](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session): "Any attached client character-mode applications … will be terminated when the session is closed … any related attached processes in the tree will also be terminated." A child that detaches from the console or starts with its own (`DETACHED_PROCESS`, `CREATE_NEW_CONSOLE`) escapes that, and the job catches it.

The conpty-oxide notes say the console host itself (`conhost.exe` or `OpenConsole.exe`) is not in the child's job. That is unconfirmed by Microsoft. If it holds, `TerminateJobObject` alone leaves a conhost behind, so the pseudoconsole still has to be closed.

**Design.** One job per pane, with `KILL_ON_JOB_CLOSE`, assigned at creation through `PROC_THREAD_ATTRIBUTE_JOB_LIST`.

- **Pane death:** wait on the process handle, then `GetExitCodeProcess`. Windows has no signals, so `exit_signal` stays empty. An NTSTATUS such as `0xC000013A` (`STATUS_CONTROL_C_EXIT`) is the nearest thing to one.
- **remain-on-exit:** keep the emulator and the record after the process exits, then release and close the pseudoconsole.
- **Kill:** `ClosePseudoConsole` first. It sends `CTRL_CLOSE_EVENT` to each attached client ([ClosePseudoConsole](https://learn.microsoft.com/en-us/windows/console/closepseudoconsole)). Then `TerminateJobObject` after a short grace period.

## 5. A host that outlives its starter

Closing the pseudoconsole, or the death of the process that holds its handles, ends the session. So, as the map already decided, a separate `admiral host` process owns the pseudoconsoles. It needs to survive:

- **The server exiting or restarting.** Start it with `DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP` so it has no console to lose and gets no Ctrl+C from the server's group ([Process Creation Flags](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)).
- **The starter's job.** `CREATE_BREAKAWAY_FROM_JOB` works only if the starter's job sets `JOB_OBJECT_LIMIT_BREAKAWAY_OK`. Otherwise `CreateProcess` fails, and the host would die with whatever job the server runs in (a terminal's job, or the scheduled task's).
- **The logon task's restart.** Task Scheduler stops a task after 72 hours by default. `ExecutionTimeLimit` must be `PT0S` ([TaskSettings.ExecutionTimeLimit](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-executiontimelimit)). I found no Microsoft doc saying whether ending a task also kills processes the task's process started. **Unconfirmed.**

**Recommendation.** Register the host as its own logon task (no time limit, `IgnoreNew` for multiple instances), separate from the server's task. The server dials the pipe, and if no host is there, it starts the host's task (`schtasks /run` or the COM API) rather than a child process. That removes job inheritance from the question.

- **Fallback:** a direct `CreateProcess` with the three flags above, retried without `CREATE_BREAKAWAY_FROM_JOB` if it fails, and a log line saying the host may share the server's fate.
- **Console window:** a console-subsystem exe started by Task Scheduler opens a console window. Either the host is built with `-H windowsgui`, or the task starts a GUI-subsystem shim. The service ticket should decide which.
- **Upgrades:** Windows locks a running exe. `winget upgrade` replacing `admiral.exe` while the host runs needs either a host started from a versioned copy, or a host protocol stable enough that an old host can keep serving a new server. The packaging ticket should decide.

## 6. What ConPTY does to the VT stream

**Translation, not passthrough.**

- Before Windows Terminal 1.22, ConPTY "rendered" a point-in-time snapshot of its own buffer. In 1.22 it was "rewritten … to be based on direct API->VT translation rather than 'rendering' … plus an ability for applications to send unmodified VT directly to the terminal" ([v1.22.2362.0 release notes](https://github.com/microsoft/terminal/releases/tag/v1.22.2362.0), PR microsoft/terminal#17510).
- The old `PSEUDOCONSOLE_PASSTHROUGH_MODE` flag no longer appears in `conpty-static.h`. The flags today are `INHERIT_CURSOR`, the `GLYPH_WIDTH_*` group and `AMBIGUOUS_IS_WIDE`.
- Known gaps after the rewrite are tracked in microsoft/terminal#17643 (open).
- **Unconfirmed: which builds carry the rewrite inbox.** A third-party issue says 24H2 (26100) ships a conhost with #17510. A psmux report measured inbox conhost 10.0.26100.8875 dropping DCS (Sixel) that a bundled OpenConsole 1.24 forwarded. Either way, the host must treat the stream as conhost's output, not the harness's bytes. That is why the snapshot has to come from Admiral's own emulator.

**Queries.**

- The inbox ConPTY answers DA1 (`CSI c`) itself and forwards `CSI ? u` (the kitty keyboard query). The two replies can then arrive out of order. Claude Code uses DA1 as its end marker, so it intermittently never turns on the kitty protocol. It loses Shift+Enter in about 4 of 10 launches in Zellij ([anthropics/claude-code#111141](https://github.com/anthropics/claude-code/issues/111141)). Loading the newer `conpty.dll` fixed the order in that report.
- On a headless host, Admiral's emulator is the only "terminal", so it must answer every forwarded query (CPR, XTVERSION, OSC 10/11 colours, `CSI ? u`) promptly, through `TakeResponses`. Codex defers rendering on a default-colour probe ([openai/codex#43837](https://github.com/openai/codex/issues/43837)).
- The 1.22 notes add that if a terminal never answers `DSR CPR`, ConPTY continues after 3 s.

**Resize.**

- `ResizePseudoConsole` makes the host repaint, so the output re-emits the current screen (conpty-oxide notes). The emulator absorbs that.
- Watch for a repaint pushing duplicate rows into scrollback. Claude Code's agent view has a stale-width-after-resize bug on Windows ([anthropics/claude-code#80123](https://github.com/anthropics/claude-code/issues/80123)).
- The bridge's per-window sizing maps to one `ResizePseudoConsole` per pane. The pane keeps its size after the last browser leaves, as tmux's does.

**Input, bracketed paste and mouse.** conhost parses the input pipe's VT into console input records. When the client has turned on VT input mode, conhost passes unknown sequences through, and it consumes SGR mouse reports when the client has not ([`InputStateMachineEngine.cpp`](https://github.com/microsoft/terminal/blob/main/src/terminal/parser/InputStateMachineEngine.cpp)). In practice:

- **Mouse modes** (`?1000h`, `?1006h`) reach the terminal on Windows 11 only after the child has turned on VT input mode before writing them. On Windows 10 and Server 2019 they never do ([oven-sh/bun#43457](https://github.com/oven-sh/bun/pull/43457), probed on 26100). Claude Code on Windows 10 turns the mouse wheel into arrow keys for this reason (anthropics/claude-code#103327, #104277).
- **Bracketed paste:** Codex's TUI (crossterm, which reads console input records on Windows) ships [`paste_burst.rs`](https://github.com/openai/codex/blob/main/codex-rs/tui/src/bottom_pane/paste_burst.rs) because "on some platforms (notably Windows), pastes often arrive as a rapid stream of `KeyCode::Char`, `KeyCode::Enter` … rather than as a single paste event". Claude Code loses Shift+Enter modifiers through libuv's console-to-VT translation ([anthropics/claude-code#92771](https://github.com/anthropics/claude-code/issues/92771)).
- So Admiral's `load-buffer` + `paste-buffer -p` equivalent must send the text, then wait, then send Enter as its own write. A newline inside the text may submit early. **Each harness needs a measurement.**
- Do not close the input pipe to signal EOF. It closes the console, and the client exits with `0xC000013A` (conpty-oxide notes; openai/codex#60911 hits the same thing).

**Alternate screen and SGR.** These come through as VT. The emulator tracks them. No problems reported beyond the translation caveats above.

**BEL.** BEL in the output stream reaches the host, either as a bare `0x07` or as the terminator of an OSC. The emulator's `Bell` callback distinguishes the two. 1.22 changed `OSC 0` titles to end in `ST` rather than `BEL` (microsoft/terminal#17643), which removes one false positive. This replaces tmux's `alert-bell` hook as the Codex needs-you signal. **Needs a measurement on Windows.**

**Teardown and threads.**

- Service the input and output pipes on separate goroutines. Microsoft warns that a single thread deadlocks ([Creating a Pseudoconsole session](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session)).
- From 24H2 (26100), `ClosePseudoConsole` returns immediately. Earlier builds wait until the output pipe is drained or closed ([ClosePseudoConsole](https://learn.microsoft.com/en-us/windows/console/closepseudoconsole)).
- `ReleasePseudoConsole` (26100+, or a bundled `conpty.dll`) lets the output pipe reach EOF once clients disconnect. Without it, a reader waiting for EOF after the child exits waits forever.
- Closing a session while the client is still starting can show a `0xc0000142` dialog.

**arm64.**

- ConPTY is a kernel32 API on both architectures. Go supports windows/arm64.
- Probed: all chosen libraries cross-compile with cgo off.
- Harness arm64 builds are outside this note. Copilot CLI's missing `win32-arm64` `runtime.node` (github/copilot-cli#3307, closed) shows it is worth checking per release.

**Minimum build.** On 2026-11-19, the serviced Windows 11 releases are 24H2 (26100, Enterprise until 2027-10-12, LTSC until 2029), 25H2 (26200) and 26H1 (28000, new devices only). 23H2 is out of updates ([release information](https://learn.microsoft.com/en-us/windows/release-health/windows11-release-information)). Requiring 26100 costs nothing and guarantees `ReleasePseudoConsole` and the non-blocking `ClosePseudoConsole`.

## 7. Inbox vs bundled ConPTY

Windows Terminal publishes `conpty.dll` and `OpenConsole.exe` as the NuGet package `Microsoft.Windows.Console.ConPTY` (1.24.260710001 at the time of writing; MIT, from microsoft/terminal). Bundling it fixes the DA1 ordering, forwards DCS, and gets conhost fixes years before Windows ships them. The costs:

- two more files per architecture in the package;
- `conpty.dll` and `OpenConsole.exe` must be a matched pair (wezterm#7774 crashed on a stale pair, per the conpty-oxide notes);
- one more thing to update.

zed, wezterm, psmux and node-pty all offer it.

**Recommendation.** Start on the inbox ConPTY, keep the function table from §1, and bundle only if the prototype's harness runs hit a bug that only `OpenConsole.exe` fixes.

## 8. Mapping to the tmux contract (§1 of the older note)

| tmux contract | ConPTY host |
| --- | --- |
| 1. Server outlives Admiral | `admiral host` as its own logon task, on a per-user pipe (§3, §5) |
| 2. Sessions, windows, panes; per-session env | The host's own records. A pane is one pseudoconsole, one job and one emulator. The environment is passed at `CreateProcess`. |
| 3. remain-on-exit with dead status and time | Keep the emulator and record after exit, plus the exit code from `GetExitCodeProcess` and a timestamp. No signal (§4). |
| 4. `@admiral-*` options | A key/value store in the host, read and written over the pipe by `admiral hook` using `ADMIRAL_PANE` |
| 5. Control mode output stream | The host fans out each pane's output bytes to subscribers over the pipe |
| 6. Snapshot: screen, 2,000 lines of scrollback, SGR, cursor, modes | `tuios.dev/vt` (§2) |
| 7. Input, literal and hex; bracketed paste | Write to the input pipe. Paste needs a per-harness strategy (§6). |
| 8. Sizing that sticks after detach | `ResizePseudoConsole` per pane |
| 9. Bell | Emulator `Bell` callback (§6) |
| 10. `admiral attach` | Parity exception for the first release (map decision) |

## Not confirmed

- Whether Task Scheduler kills processes a task started when the task is ended or restarted, and whether a logon task's process sits in a job without `BREAKAWAY_OK`.
- Which Windows builds carry the post-1.22 ConPTY inbox, and exactly what the inbox host on 26100 and 26200 drops or rewrites.
- Whether bracketed paste reaches Claude Code and Copilot CLI (both Node) through the inbox ConPTY, and what each harness does with a pasted newline.
- Whether ConPTY's resize repaint duplicates rows in the emulator's scrollback.
- That the console host is outside the child's job (third-party claim).
- Harness behaviour on Windows arm64.
- Nothing was run on Windows; all of the above is for #876.

## Sources

- Microsoft Learn: [CreatePseudoConsole](https://learn.microsoft.com/en-us/windows/console/createpseudoconsole), [ClosePseudoConsole](https://learn.microsoft.com/en-us/windows/console/closepseudoconsole), [Creating a Pseudoconsole session](https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session), [Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects), [Process Creation Flags](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags), [TaskSettings.ExecutionTimeLimit](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-executiontimelimit), [Windows 11 release information](https://learn.microsoft.com/en-us/windows/release-health/windows11-release-information)
- microsoft/terminal: [v1.22.2362.0 release](https://github.com/microsoft/terminal/releases/tag/v1.22.2362.0), [#17510](https://github.com/microsoft/terminal/pull/17510), [#17643](https://github.com/microsoft/terminal/issues/17643), [#4856](https://github.com/microsoft/terminal/pull/4856), [`src/inc/conpty-static.h`](https://github.com/microsoft/terminal/blob/main/src/inc/conpty-static.h), [`InputStateMachineEngine.cpp`](https://github.com/microsoft/terminal/blob/main/src/terminal/parser/InputStateMachineEngine.cpp), [NuGet Microsoft.Windows.Console.ConPTY](https://www.nuget.org/packages/Microsoft.Windows.Console.ConPTY/)
- Go modules (source read from proxy.golang.org): [charmbracelet/x/conpty v0.2.0](https://pkg.go.dev/github.com/charmbracelet/x/conpty), [UserExistsError/conpty v0.1.4](https://github.com/UserExistsError/conpty), [aymanbagabas/go-pty v0.2.3](https://github.com/aymanbagabas/go-pty), [charmbracelet/x/vt v0.1.0](https://pkg.go.dev/github.com/charmbracelet/x/vt), [tuios.dev/vt v0.3.3](https://pkg.go.dev/tuios.dev/vt) ([repo](https://github.com/Gaurav-Gosain/tuios)), [Microsoft/go-winio v0.6.4](https://github.com/microsoft/go-winio), [golang.org/x/sys/windows](https://pkg.go.dev/golang.org/x/sys/windows)
- Harness reports: [anthropics/claude-code#111141](https://github.com/anthropics/claude-code/issues/111141), [#92771](https://github.com/anthropics/claude-code/issues/92771), [#80123](https://github.com/anthropics/claude-code/issues/80123), [#103327](https://github.com/anthropics/claude-code/issues/103327), [#104277](https://github.com/anthropics/claude-code/issues/104277); [openai/codex `paste_burst.rs`](https://github.com/openai/codex/blob/main/codex-rs/tui/src/bottom_pane/paste_burst.rs), [openai/codex#60911](https://github.com/openai/codex/issues/60911), [#43837](https://github.com/openai/codex/issues/43837); [github/copilot-cli#3307](https://github.com/github/copilot-cli/issues/3307)
- Secondary, used only where marked: [conpty-oxide pitfalls](https://docs.rs/crate/conpty-oxide/0.1.2/source/docs/conpty-pitfalls.md), [oven-sh/bun#43457](https://github.com/oven-sh/bun/pull/43457), [psmux/psmux#914](https://github.com/psmux/psmux/issues/914), [deblasis/wintty#263](https://github.com/deblasis/wintty/issues/263)
