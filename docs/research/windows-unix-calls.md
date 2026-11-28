# Unix-only file and process calls on Windows

Question ([Research: what does each Unix-only file and process call need on Windows?](https://github.com/jbuffin/admiral/issues/986)): what each Unix-only call outside `internal/sessionhost` needs on Windows, whether the guarantee holds, and where paths compare as strings.

Sources: this repo at `origin/main` (30efe9d0), Go 1.26.5's standard library source (`syscall`, `os`, `path/filepath`, `internal/syscall/windows`), `golang.org/x/sys/windows` v0.47.0, Microsoft Learn pages linked inline. Claims marked **probed** were run on the Windows 11 VM (build 26200, elevated SSH session) with a Go 1.26.5 `GOOS=windows` probe binary.

## Summary

- `GOOS=windows go build ./...` reports 9 packages, but it stops at the first broken dependency. By grep, the compile breaks are in 17 packages, and the tables below list every site. They fall into six kinds: `Flock` (2), process-group kill (8), `O_NOFOLLOW` opens (16), owner checks through `Stat_t` (3), `x/sys/unix` splits (2 files), and signal forwarding (3).
- Four kinds are a swap with the same guarantee: `Flock` to `LockFileEx`, no-follow opens to `FILE_FLAG_OPEN_REPARSE_POINT` plus an `Fstat` check, `renameat` no-replace to `MoveFileEx` with no flags, and `TIOCGETA` to `GetConsoleMode`.
- Process-group kill needs a job object, but `os/exec` cannot put a child in a job at creation. The swap is not mechanical.
- Signals mostly do not exist. `Process.Signal` supports only `Kill` on Windows (**probed**: `SIGTERM` and `os.Interrupt` return "not supported by windows").
- Owner and mode checks have no direct equivalent. `os.Getuid()` is `-1` and every file's `Perm()` is `0666` or `0444` (**probed**), so the codexpane check refuses every directory and the three executable-bit PATH lookups never find anything.
- Several things compile cleanly but behave differently: junctions break `filepath.EvalSymlinks`, `rename` fails over an open file, `errors.Is(err, syscall.ENOTEMPTY)` is false, and string-prefix path checks that use `/` stop matching.

## Call sites

"Holds" means the Windows version gives the same guarantee the Mac code relies on.

### Exclusive locks

| Package | Site | Guards | Windows equivalent | Holds |
| --- | --- | --- | --- | --- |
| capabilities | `store.go:90`, `:93` | Read-modify-write of `capabilities.json` under `<file>.lock` | `windows.LockFileEx(h, LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ol)` and `UnlockFileEx` on the `.lock` file's handle | Yes |
| allowrules | `store.go:202`, `:205` | Same pattern for the allow-rules file | Same | Yes |

[LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex) blocks until granted when the handle is synchronous (Go's `os.OpenFile` handles are), and "locking a region that goes beyond the current end-of-file position is not an error", so locking one byte of an empty `.lock` file works. The lock is mandatory for that byte range, which doesn't matter because nothing reads the `.lock` file. The OS releases locks on close or exit, but the page warns that "the time it takes ... depends upon available system resources", so keep the explicit unlock. The guarantee is per handle, as `flock` is per open file description, and both stores open a fresh handle each time.

The data write after the lock is a temp file plus `os.Rename`. See [Replacing a file another handle has open](#replacing-a-file-another-handle-has-open).

### Killing a child's whole tree

| Package | Site | Guards | Windows equivalent | Holds |
| --- | --- | --- | --- | --- |
| capabilities | `proc.go:11`, `:13` (`InOwnGroup`, used by probes, judge, harnessmodels, version) | A timeout kills the harness and its helpers | Job object, `TerminateJobObject` on cancel | Yes, once the child is in the job before it can spawn |
| resources | `resources.go:157`, `:169` | A check's timeout kills its children | Same | Same |
| sources | `rpc.go:100`, `:119`, `:323` | Kill the order source's group on stop, and kill whatever it left behind after the leader exits | Same; the job outlives its first process, so `TerminateJobObject` after `Wait` still reaches orphans | Same |
| gitleaks | `gitleaks.go:140`, `:142` | A wrapper script's forked scanner dies on timeout | Same | Same |
| commodore | `commodore.go:153`, `:154` | The Commodore CLI's git and ssh children die with it | Same | Same |
| cli | `statusline.go:250`, `:251` | Timeout or cancel kills the engineer's line; a normal exit leaves a background refresher alone | Job without `KILL_ON_JOB_CLOSE`, terminated only on cancel | Yes, but only if the job is not kill-on-close |
| codexpane | `codexpane.go:252`, `:154`, `:160` | `codex app-server` runs in its own group so terminal signals miss it, and the stop sends SIGTERM then SIGKILL to the group | Inside a pane the session host's job already covers it. For the "own group", use `CREATE_NEW_PROCESS_GROUP` (Ctrl+C is off in the new group). For the stop, `TerminateJobObject` or `windows.GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid)` first | Kill: yes. Graceful SIGTERM: no equivalent for a process without a console handler |

Facts that shape the swap:

- `syscall.SysProcAttr` on Windows has `CreationFlags`, `ParentProcess` and handle fields, but no attribute list, so `os/exec` cannot pass `PROC_THREAD_ATTRIBUTE_JOB_LIST` (Go source, `syscall/exec_windows.go:292`). The session host gets around this with its own `CreateProcess`.
- Assigning with `AssignProcessToJobObject` after `cmd.Start()` races: a child spawned before the call escapes. "After a process is associated with a job, any child processes it creates ... are also associated" ([Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)), but not ones made earlier.
- There are three race-free routes:
  1. `CREATE_SUSPENDED` in `CreationFlags`, assign, then resume the main thread. Go discards the thread handle, so finding it means `CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD)` plus `OpenThread` and `ResumeThread` (all in x/sys).
  2. A small `CreateProcess` wrapper with `PROC_THREAD_ATTRIBUTE_JOB_LIST`, the session host's helper reused. It gives up `exec.Cmd`'s pipes and `Wait`, which then have to be rebuilt.
  3. `SysProcAttr.ParentProcess` set to a process already in the job. [UpdateProcThreadAttribute](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute) says attributes inherited from that parent include the "job object". This needs a placeholder process per job, so it is clumsy.
- All 8 sites do the same thing. One helper (`procgroup.Start(cmd)` / `Kill`) with a Unix and a Windows file replaces 8 copies.

### Refusing a symlink (and a FIFO)

Every site below passes `syscall.O_NOFOLLOW`, which does not exist on Windows. Most also pass `syscall.O_NONBLOCK`, which does exist and is ignored. The Windows form is `os.O_RDONLY|syscall.FILE_FLAG_OPEN_REPARSE_POINT`: `syscall.Open` passes the high 12 bits of the flag through as `FILE_FLAG_*` (`syscall/syscall_windows.go:408`), and `os.Root.OpenFile` does the same through `NtCreateFile`.

The behavior differs. On the Mac, `O_NOFOLLOW` on a link **fails** with `ELOOP`. On Windows the flag **opens the link itself**, and reading returns 0 bytes (**probed**, for both `os.OpenFile` and `Root.OpenFile`). The guarantee (never read the target) holds only when the site then checks the open handle with `f.Stat()` and refuses anything that isn't regular. That check sees a symlink as `ModeSymlink` and every other reparse point as `ModeIrregular`.

FIFOs: Windows named pipes live in `\\.\pipe\`, not in NTFS folders, and a link to one is refused by the no-follow open. Unix-socket files are reparse points (`ModeSocket`). So the reason for `O_NONBLOCK` goes away, and the flag can be dropped.

For writes, `O_CREATE|O_EXCL` already gets `FILE_FLAG_OPEN_REPARSE_POINT` added by Go (`syscall_windows.go:438`, `at_windows.go:131`) and `CREATE_NEW` fails on any existing name. On an existing symlink the probe got "The file exists" with `errors.Is(err, os.ErrExist)` true. Those sites only need `O_NOFOLLOW` dropped on Windows.

| Package | Site | Guards | Fstat after open | Holds with the swap |
| --- | --- | --- | --- | --- |
| ships | `outcomes.go:410` | Ship's report file | Yes | Yes |
| ships | `hand.go:425` | Hand ref file | Yes | Yes |
| launcher | `launcher.go:934` | Fresh ship context file (`O_CREATE\|O_EXCL`) | n/a | Yes |
| inbox | `resume.go:49`, `remove.go:81`, `read.go:106`, `question.go:125` | Inbox reports, answers, items, questions (via `os.Root`) | Yes | Yes |
| inbox | `crew_items.go:283` | Ad hoc crew file (via `os.Root`) | **No** (Lstat before only) | Target never read, but a swapped-in link reads as 0 bytes and is parsed as an empty member. Add the Fstat |
| chat | `questions.go:209` | Chat question file (via `os.Root`) | Yes | Yes |
| moor | `feedback.go:91` | Feedback file (`O_CREATE\|O_EXCL`, via `os.Root`) | n/a | Yes |
| moor | `shipcopy.go:139` | Copy of a ship's inbox item; treats `ELOOP` as "skip" | Yes | Yes; the `ELOOP` case never fires, and the Fstat's `copyable` check does the work |
| moor | `shipcopy.go:157` | Copy destination (`O_CREATE\|O_EXCL`) | n/a | Yes |
| crew | `notes.go:78` | Memory note; `ELOOP` gives "is a symlink, skipped" | **No** | Target never read, but the link is read as an empty note and the "symlink" message is lost. Add the Fstat |
| findings | `findings.go:97` | Ship's findings file | Yes | Yes |
| cli | `campaigns.go:233` | Ship context for campaigns | Yes | Yes |
| crewapi | `files.go:65` | Crew file read; `ELOOP` gives `errLink` | **No** | Same as crew/notes. Add the Fstat |

The `Lstat` before the open at most of these sites works the same on Windows. One difference: a **junction** reports as `ModeIrregular`, not `ModeSymlink` or `ModeDir` (**probed**). The walks in `crewapi/files.go:28`, `launcher/launcher.go:960` and `transcriptfs/transcriptfs.go:56` follow the symlink test with an `IsDir()` or `IsRegular()` test, so they refuse a junction. `moor/wakestate.go:21`, `crew/adhoc.go:17`, `push/vapid.go:45` and `cli/guard_scout.go:69` test only `ModeSymlink`, so a junction gets past them. For `crew/adhoc.go` that matters (see [junctions](#paths-case-and-separators)). The other three then do something that fails on a folder anyway.

`os.Root` keeps its escape guarantee on Windows. Every component is opened with `OBJ_DONT_REPARSE`, and reparse points are resolved in user space (`os/root_windows.go:147`). The probe got "path escapes from parent" both for an absolute symlink and for a path through a junction.

### Owner and mode checks

| Package | Site | Guards | Windows equivalent | Holds |
| --- | --- | --- | --- | --- |
| codexpane | `codexpane.go:65` (`checkDir`) | The socket folder is ours (uid) and mode 0700 | Owner SID through `windows.GetNamedSecurityInfo(path, SE_FILE_OBJECT, OWNER_SECURITY_INFORMATION\|DACL_SECURITY_INFORMATION)`, compared with the token's user SID (`windows.GetCurrentProcessToken().GetTokenUser()`), plus a DACL walk that allows only that SID (and decided-on system principals) | Different: see below |
| codexpane | `codexpane.go:236` (`removeSocket`) | Delete only our own socket and `.lock` | Same owner check | Different |
| codexlive | `codexlive.go:369` (`dialTarget`) | Dial only a socket of this user's | Same owner check. A Windows AF_UNIX socket file shows as `ModeSocket` (`os/types_windows.go:210`) | Different |

- `fi.Mode().Perm()&0o077` is always non-zero on Windows: files and folders report `0666`, or `0444` when read-only (**probed**). Kept as is, `checkDir` refuses every folder. The DACL is the real check.
- The paths are Unix ones: `/tmp/admiral-<uid>/`, `/tmp/codex-daemon-<uid>`, and `os.Getuid()` returns `-1` (`syscall_windows.go:1352`, **probed**). The harness note says Codex's Windows socket is a plain file in a user-only DACL folder ([windows-harnesses](https://github.com/jbuffin/admiral/blob/research/windows-harnesses/docs/research/windows-harnesses.md#app-server-socket-codexlive)), so the symlink-into-daemon-dir logic does not apply there.
- Other `Getuid` callers that build paths or names: `launcher/launcher.go:462`, `launcher/restricted.go:101` (`claude-<uid>`), `probes/restricted.go:470`, `:554`, `cli/codexpane.go:29`, `cli/service.go:52` (launchd domain; the install note covers the Windows service).

### Executable-bit PATH lookups

| Package | Site | Guards | Windows equivalent | Holds |
| --- | --- | --- | --- | --- |
| config | `derive.go:135` (`isExecutable`) | Find a harness binary on a PATH | `exec.LookPath` semantics: try each `PATHEXT` extension, no mode check | No as written: no Windows file has `0o111`, so nothing is ever found |
| cli | `detect.go:137` (`executable`) | Same, for setup's detection | Same | Same |
| gitleaks | `gitleaks.go:188` | Find `gitleaks` on the fleet env's PATH, skipping relative entries | Same | Same |

### Renames that must not replace

| Package | Site | Guards | Windows equivalent | Holds |
| --- | --- | --- | --- | --- |
| launcher | `rename_darwin.go:7` (`RENAME_EXCL`), `rename_linux.go:7` (`RENAME_NOREPLACE`) | The clone move refuses a target that appeared while `gh` ran (`clone.go:127`) | Windows builds `rename_other.go`: `Lstat`, then `os.Rename`, which leaves a race. Use `windows.MoveFileEx(old, new, 0)` instead: without `MOVEFILE_REPLACE_EXISTING` it fails when the target exists. [MoveFileEx](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw) also says "if lpNewFileName names an existing directory, an error is reported" even with the flag | Yes, for a same-volume folder move |

### Terminal check

| Package | Site | Guards | Windows equivalent | Holds |
| --- | --- | --- | --- | --- |
| cli | `offer.go:13`, `termios_darwin.go:5`, `termios_linux.go:6` | Offer setup only when stdin is a terminal | `windows.GetConsoleMode(handle, &mode) == nil` | Yes for a console or a ConPTY. Git Bash's mintty presents a pipe, so it reads as "not a terminal", which only skips the offer |

### Signals

| Package | Site | Guards | Windows | Holds |
| --- | --- | --- | --- | --- |
| cli | `lock.go:109`, `:130`, `:139` | `admiral lock` passes SIGTERM to the command and reports 128+signal | `signal.Notify` gets `SIGTERM` for `CTRL_CLOSE/LOGOFF/SHUTDOWN_EVENT` and `os.Interrupt` for Ctrl+C or Ctrl+Break ([os/signal](https://pkg.go.dev/os/signal#hdr-Windows)). `SIGHUP` and `SIGQUIT` never arrive. `Process.Signal(SIGTERM)` fails. `WaitStatus.Signaled()` is always false | No forwarding. A console child already gets the same console event, so it is the "would arrive twice" case and passing nothing on is right |
| codexpane | `codexpane.go:123`, `:275`, `:303`, `:321` | Pass SIGHUP and SIGTERM to the TUI, then stop the server | Same as above. In a pane, `ClosePseudoConsole` sends `CTRL_CLOSE_EVENT` to every attached client, the TUI included | The TUI gets the event from the console, not from Admiral |
| cli | `statusline.go:240` | Cancel on SIGTERM, SIGINT or SIGHUP | Works for SIGTERM and Interrupt | Yes |

`cmd/admiral/main.go:827` (`os.Interrupt`, `SIGTERM`) works. Whether a windowless server started by Task Scheduler gets any of these is a question for the service ticket.

### Errno comparisons that compile but don't match

Windows `syscall` defines `ENOTEMPTY`, `ELOOP`, `EADDRINUSE` and others as invented values (`zerrors_windows.go`). The OS returns Win32 or WinSock codes instead, so `errors.Is` against the invented value is false.

| Site | Check | Windows returns | Fix |
| --- | --- | --- | --- |
| `crewapi/members.go:457`, `crewapi/sharing.go:126` | `errors.Is(err, syscall.ENOTEMPTY)` | `ERROR_DIR_NOT_EMPTY` (145). **Probed**: `Is ENOTEMPTY` false, `Is ErrExist` true | `errors.Is(err, fs.ErrExist)` on both platforms |
| `cmd/admiral/rebind.go:113` | `errors.Is(err, syscall.EADDRINUSE)` | `WSAEADDRINUSE` (10048) | `windows.WSAEADDRINUSE` in a Windows file |
| `crew/notes.go:85`, `crewapi/files.go:67`, `moor/shipcopy.go:141` | `syscall.ELOOP` | Never returned by a reparse-point open | Fstat check (above) |
| `crew/resolve.go:187`, `crew/notes.go:42`, `crew/shared.go:87`, `crewapi/*` | `syscall.ENOTDIR` | `ENOTDIR` is defined as `ERROR_PATH_NOT_FOUND`, which also matches `ErrNotExist` | Works |

### Symlinks Admiral creates

`launcher/bundle.go:216` (`bin/admiral` link), `launcher/chat_codex.go:85` (`root.Symlink`) and `launcher/restricted_temp.go:66` create symlinks. Go passes `SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE`, which works only with Developer Mode on. Otherwise [CreateSymbolicLink](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createsymboliclinkw) needs `SeCreateSymbolicLinkPrivilege`, which standard users don't have. The probe's `os.Symlink` succeeded, but that session was elevated, so it does not settle the unelevated case. `bin/admiral` without `.exe` is also not runnable through PATH. The install note covers shims.

### Modes Admiral sets

`os.Chmod` and the `0o600`/`0o700` arguments only toggle the read-only attribute on Windows. Private files (the VAPID key in `push/vapid.go`, `state.go`, `capabilities`, allow rules) are private only because of the ACL they inherit from their folder. Under `%LOCALAPPDATA%` that ACL is the user, SYSTEM and Administrators. The `home.go:23` chmod of `$ADMIRAL_HOME` does nothing.

## Replacing a file another handle has open

Every atomic write in Admiral is a temp file plus `os.Rename`. On Windows, `os.Rename` is `MoveFileEx(..., MOVEFILE_REPLACE_EXISTING)` (`internal/syscall/windows/syscall_windows.go:366`). It fails with "Access is denied" while any other handle has the target open (**probed**), because Go opens files with `FILE_SHARE_READ|FILE_SHARE_WRITE` and no `FILE_SHARE_DELETE` (`syscall_windows.go:395`).

On the Mac a reader holding the old file never blocks a writer. On Windows any reader blocks it: a watcher that polls, a ship's `admiral` hook, an editor, or an antivirus scan. This is the widest behavior change outside the build errors. It covers config, state, capabilities, allow rules, chat logs and crew files.

## Paths: case and separators

The Mac's default APFS volume is already case-insensitive, so some code handles it: `allowrules/never.go:239` folds segments, `crewapi/sharing.go:351` folds a segment, and `cli/guard_scout.go:89` falls back to `os.SameFile`. Windows adds `\`, drive letters (`C:` vs `c:`), forward-slash spellings that Windows accepts (`C:/Users/...`, which is what `git rev-parse --path-format=absolute` prints), 8.3 short names (`JASONB~1`), and junctions.

What the standard library does on Windows:

- `filepath.Clean` and `Join` turn `/` into `\`. `filepath.Rel` compares components with `strings.EqualFold` (`path/filepath/path_windows.go:116`). `filepath.IsLocal` also rejects reserved names such as `NUL`.
- `filepath.EvalSymlinks` returns one canonical spelling: an upper-case drive letter, each component's on-disk case and the long name (`symlink_windows.go:12`, `toNorm`). **Probed**: `...\real\F.TXT` came back as `...\Real\f.txt`.
- `filepath.EvalSymlinks` **fails on any path that passes through a junction**. Since Go 1.23 a junction is `ModeIrregular`, not a symlink and not a folder, so `walkSymlinks` returns `ENOTDIR` midway (`path/filepath/symlink.go:90`). On the junction itself it returns the path unresolved. **Probed**: "The system cannot find the path specified", `ENOTDIR` true. Ordinary opens still follow the junction.
- So where a failed `EvalSymlinks` falls back to the cleaned input, **a junction defeats "resolve, then check it is inside"**. The fallback path sits lexically inside the root while the file lives elsewhere. This is from reading the code, not probed. `crew/resolve.go:262` (`realPath`) falls back this way, and its `within` check guards fleet, shared, ad hoc and pinned members. `crew/pin.go:35` would not catch it either: `Lstat` on the unresolved path follows the junction to the same file the open reached, so `os.SameFile` agrees. `repokey.go:44` and `home/home.go:36` fall back the same way, but they only name things and guard nothing.
- `os.SameFile` compares volume serial and the 64-bit file index. Microsoft says the 64-bit identifier "is not guaranteed to be unique on ReFS" ([BY_HANDLE_FILE_INFORMATION](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/ns-fileapi-by_handle_file_information)), and Dev Drive is ReFS.

By area:

| Area | Code | How it compares | What breaks when two spellings name one folder |
| --- | --- | --- | --- |
| repofinder | `repofinder.go:62` `Contains` | `filepath.Clean(dir) == e.Path`, exact | `c:\code\repo` or `C:\Code\Repo` is "not listed", so launch, launch orders and drafting refuse a repo the picker showed (`launcher.go:322`, `launchorders.go:296`, `:463`, `server/drafting.go:38`). Fails closed. `List`'s `seen` map lists a root twice if two search roots differ in case |
| launch orders | `launchorders.go:120`, `:159`, `:168`, `:253` | `sh.Dir == e.Path` and `e.Path == dir` | A ship launched under one spelling doesn't count as busy in, or the latest in, the other. Busy checks fail open |
| repo keys | `repokey/repokey.go:44` | `EvalSymlinks` of the main checkout, else `filepath.Clean` | Canonical when `EvalSymlinks` succeeds. Through a junction it falls back to the raw spelling, and git's `C:/...` becomes `C:\...` with git's case. Keys stored in allow rules then miss (`allowrules/store.go:126`, `r.Repo == repo`), which fails closed (the rule doesn't apply) |
| allow rules | `allowrules/never.go:226`–`:243` | `filepath.Rel` (case-safe), then `strings.HasPrefix(rel, "../")` and `strings.Split(rel, "/")` | On Windows `Rel` returns `..\x` and `inbox\ship\x`. Outside-home paths pass the `../` test, and the `/` split yields one segment, so a ship's own report and inbox paths are never recognized. Every path rule counts as Admiral's own, which over-refuses. Fails closed |
| allow rules | `allowrules/candidates.go:191`, `:238`, `:245`, `never.go:261`, `:264` | Claude's `//abs` and `/rel` forms, `path.IsAbs`, `HasPrefix(p, cwd+"/")` | A Windows absolute path is not `path.IsAbs`, so Copilot `write(C:\...)` is refused. How Claude and Copilot spell Windows paths in rules is not documented in what I read. Unconfirmed |
| crew files | `crew/resolve.go:273` `within` (used by `adhoc.go:30`, `member.go:164`, `pin.go:22`, `shared.go:92`) | `strings.HasPrefix(path, root+"/")` on `EvalSymlinks` results | Never true on Windows, so **every fleet, shared and ad hoc member is refused as outside its layer**. Fails closed and breaks crew completely. Fix with `filepath.Rel` plus `IsLocal`, as `ships/hand.go:493` does |
| crew files | `crew/pin.go:35` | `os.SameFile` after open | Works on NTFS. Weaker on ReFS/Dev Drive (64-bit index) |
| crew files | names | Lower-case slugs (`crew/member.go:14`) | No case collisions. But `con`, `nul`, `aux`, `prn`, `com1`–`com9` and `lpt1`–`lpt9` are valid slugs and reserved device names, even with `.md` ([Naming Files](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)). The same applies to ship names (`launcher/names.go:52`) |
| privacy checks | `crewapi/sharing.go:256` `checkPaths` (publish check 3) | Case-sensitive `strings.Index` for home, `$ADMIRAL_HOME`, clone root and search roots; shorthands `~/`, `$HOME/`, `${HOME}/`; `hasSegment` scans only words starting with `/` | **Weaker on Windows.** It misses `c:\users\jason` against `C:\Users\Jason`, `C:/Users/jason/...`, `%USERPROFILE%`, `%LOCALAPPDATA%`, `$env:USERPROFILE` and `~\`. `hasSegment` never fires, because `:` splits `C:\Users\jason` into `C` and `\Users\jason`, which starts with `\`. A member naming a Windows home path gets published. Fails open |
| scout guard | `cli/guard_scout.go:55`–`:100` | Own symlink walk on `/`, `inside` by prefix then `os.SameFile` walking up to `/` | `strings.Split(dest, "/")` and `cur = "/"` mis-handle a `C:\` link target. The `inside` loop stops only at `/` or `.`, but `filepath.Dir("C:\\")` is `C:\`, so **it loops forever** when `p` is not inside `root` |
| hand refs | `ships/hand.go:474`–`:493` | `EvalSymlinks` plus `filepath.Rel`/`IsLocal` | Case-safe, but fails through a junction |
| plugins, skill index, config migrate | `sources/plugin.go:82`, `skillindex/plugins.go:155`, `config/migrate.go:466` | `EvalSymlinks`, then a `Separator` prefix or `Rel` | Separator-correct. `sources/plugin.go:82` is a case-sensitive prefix on two `EvalSymlinks` results, which works because both are canonical. All fail through a junction |
| detect | `cli/detect.go:52` | `HasPrefix(p, id+Separator)` | Separator-correct, case-sensitive |
| memory dirs | `launcher/member.go:107` | `HasPrefix(file, name+"/")` | Crew-relative names, so separator only matters if they come from `filepath` |

## Places that need a decision, not a swap

1. **Process trees outside panes.** Pick how a child gets into a job before it can fork: `CREATE_SUSPENDED` plus thread resume, the session host's `CreateProcess` wrapper, or accepting the post-`Start` race. Put all 8 sites behind one helper. The status line also needs a job that is not kill-on-close, so its background refresher survives a normal exit.
2. **Graceful stop.** Windows has no SIGTERM for a child. Decide whether codexpane's server stop and `admiral lock` go straight to `TerminateJobObject`, or send `CTRL_BREAK_EVENT` first, which needs `CREATE_NEW_PROCESS_GROUP` and a shared console.
3. **What "private" means for a folder.** codexpane's owner and 0700 check becomes an owner-SID and DACL check. Decide which extra ACEs are acceptable (SYSTEM, Administrators: `%LOCALAPPDATA%` grants both), and where the socket folder lives instead of `/tmp/admiral-<uid>`. The same decision covers files Admiral writes 0600, since modes do nothing on Windows.
4. **Replace while open.** `os.Rename` over a file any process has open fails. Choose a retry with backoff in one shared atomic-write helper, readers that open with `FILE_SHARE_DELETE` (which needs `windows.CreateFile`, since `os.Open` can't set it), or both.
5. **Junctions and other reparse points.** `EvalSymlinks` fails through a junction. Every "resolve then compare" site (crew layers, hand refs, repo keys, plugins, transcripts, the restricted temp dir) then either refuses, or falls back to the raw spelling, which lets a junction escape crew's layer check. Decide whether Admiral refuses any path with a reparse point, or resolves paths from an open handle with `GetFinalPathNameByHandle`. Also decide whether every `ModeSymlink`-only check becomes "not a plain dir or file".
6. **One path identity.** Decide on one canonicalizer (resolve, then compare with `filepath.Rel`/`IsLocal`, or with `EqualFold` on canonical strings) and use it in repofinder, launch orders, repo keys, crew `within` and the scout guard. Fix the scout guard's endless loop and crew's `/`-only `within` before anything runs on Windows.
7. **Privacy check spellings.** Check 3 needs Windows forms: case-folded, either separator, `%USERPROFILE%`-style variables and the drive-letter home segment. Until then it fails open on Windows.
8. **Symlinks Admiral creates.** Without Developer Mode or elevation, `os.Symlink` fails. Decide on a copy, hard link or junction for `bin/admiral`, the Codex chat link and the restricted temp link, or require Developer Mode.
9. **Reserved names.** Decide whether ship and crew slugs refuse `con`, `nul`, `aux`, `prn`, `comN` and `lptN` on every platform, so a fleet stays portable, or only on Windows.

The swaps that need no decision: `LockFileEx`, `FILE_FLAG_OPEN_REPARSE_POINT` plus an Fstat (add it at the three sites without one), `MoveFileEx(old, new, 0)`, `GetConsoleMode`, `exec.LookPath`-style PATH lookups, and `fs.ErrExist`/`WSAEADDRINUSE` in place of the invented errnos.
