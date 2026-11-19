# Installing Admiral on Windows and keeping it running

> Research for "Map: Admiral runs natively on Windows" ([#872](https://github.com/jbuffin/admiral/issues/872)), answering [#874](https://github.com/jbuffin/admiral/issues/874). Written 2026-11-19 from primary sources, listed at the end. Items marked **Unconfirmed** still need checking on the arm64 VM or the x64 PC.

## Answer

- **Channels:** two, both built from one per-architecture zip on the GitHub release:
  - **WinGet**, as a `zip` installer with a `portable` nested installer. This is the "usual Windows package manager" in the map's destination. It ships with Windows 11.
  - **A PowerShell `irm … | iex` script**, the twin of `install.sh`. It needs no admin and gives `admiral update` a path it fully controls.
  - **Scoop** comes later and is optional: an owned bucket is cheap, but it is a smaller audience, and Scoop refuses to update an app while any process is running from the app's folder.
- **Unsigned is fine for the channels.** WinGet's policies and validation pipeline don't require a signature, and unsigned Go zips such as fzf are in the catalog today. Neither `irm` nor WinGet's download leaves a Mark of the Web, so SmartScreen's download prompt doesn't apply. The one real block is **Smart App Control in enforcement mode**: it stops unknown unsigned code whatever the channel. That is the only reason to come back for code signing.
- **Logon start:** use a **per-user Task Scheduler task** with a logon trigger for the current user and an interactive token (no password, no admin). Override three defaults: the 72-hour `ExecutionTimeLimit`, the "don't start on battery" setting and the "stop on battery" setting. Avoid the HKCU `Run` key: it has no restart, and Windows may start its entries late.
- **No console window:** the process the task starts must not be a console-subsystem program, or it opens a terminal window at every logon. Either build a small GUI-subsystem (`-H windowsgui`) launcher, or start the server with `CREATE_NO_WINDOW`/`DETACHED_PROCESS`.
- **Upgrades without ending ships:** Windows lets you rename a running `.exe` but not overwrite or delete it. Two consequences:
  - The host process must not run from the package's own folder. Copy it to a versioned path under Admiral's data directory and start it from there. Otherwise every package manager's upgrade either fails (WinGet's overwrite, Scoop's running-process check) or would need to stop the host.
  - The `admiral` front binary is replaced by renaming it aside, then stopping and restarting the server. The ships stay alive in the host.

## 1. WinGet

**Manifest types.** A package is one *singleton* YAML file only if it has one installer and one locale. Anything with both x64 and arm64 needs the multi-file form: a `version` file, a `defaultLocale` file and an `installer` file, each with `ManifestVersion` (1.12.0 in the Learn docs, 1.28.0 in manifests merged this month). Files go in `manifests/<letter>/<Publisher>/<Package>/<version>/`, and the identifier is `Publisher.Package` (for example `jbuffin.Admiral`). [manifest]

**Installer types.** WinGet accepts exe, msi, msix, inno, wix, nullsoft, burn, appx, font, `portable` (since 1.3) and `zip` (since 1.5). A zip must declare a `NestedInstallerType`. For a CLI that is `portable`, with `NestedInstallerFiles[].RelativeFilePath` and an optional `PortableCommandAlias`. Only a `portable` nested type may list more than one file, so the zip could hold two executables, such as `admiral.exe` plus a windowless launcher. [installer-schema, winget]

**What portable does.** Per the portable spec and the settings doc:
- Files go to `%LOCALAPPDATA%\Microsoft\WinGet\Packages\<PackageIdentifier>…` by default (user scope, no admin).
- A symlink named after the alias goes in `%LOCALAPPDATA%\Microsoft\WinGet\Links\`, which WinGet adds to the user's PATH. `ArchiveBinariesDependOnPath: true` puts the install directory on PATH instead.
- WinGet writes an Apps & Features entry.
- WinGet doesn't update the PATH of the running shell, so users open a new terminal after installing. [portable-spec, settings, installer-schema]

**Architectures.** `Architecture` is one of x86, x64, arm, arm64 or neutral. The fzf manifest is the pattern to copy: x64 and arm64 entries, each a `zip` with `NestedInstallerType: portable` pointing at `fzf.exe`. [installer-schema, fzf-manifest]

**`winget upgrade` and a running exe.** The portable upgrade copies the new exe over the old one in the same place, then rewrites the symlink. If the file is in use, the spec says the user is told to close the program or pass `--force`. Two more data points:
- Anthropic's Claude Code docs say "On WinGet the upgrade may fail while Claude Code is running because Windows locks the executable."
- winget-cli #5190 was a bug where a failed in-use upgrade still recorded the new version. PR #5214 fixed it in March 2025.
- **Unconfirmed:** whether `--force` actually terminates the process in today's client. The spec describes it, but I didn't find it on the command reference page.

So `winget upgrade jbuffin.Admiral` will fail while the server runs from the WinGet folder, unless Admiral is stopped first or runs from elsewhere (§5). [portable-spec, claude-code-setup, winget-cli-5235/5214]

**Submission, validation and latency.**
- **How you submit:** a PR to `microsoft/winget-pkgs`, one package version per PR. `wingetcreate`, Komac, or the `winget-releaser` GitHub Action (which fzf uses) can open it from a release.
- **What the pipeline checks:**
  - Schema and hash.
  - The URL must be the publisher's own release location, so GitHub release assets qualify, and it must not redirect.
  - Installs and uninstalls run silently for admins and non-admins.
  - Several antivirus engines scan the installer, Defender among them. A Defender or PUA hit gets `Validation-Defender-Error` or `Binary-Validation-Error`.
  - Then a moderator review.
- **Signing:** none of the policies (1.1–1.10) or validation labels mention a code signature.
- **fzf proves an unsigned binary passes:** `fzf.exe` from v0.75.0, merged this month, has an empty Authenticode certificate table (checked locally by reading its PE header).
- **Latency:** in a sample of 40 "New package" PRs merged 7–19 Nov 2026, the time from open to merge ranged from about 50 minutes to 12 days. Most took 1–3 days. Version updates of an existing package usually auto-approve once `Azure-Pipeline-Passed` is set. [repository, policies, winget-pkgs PR sample]

**Reach.** WinGet comes in App Installer, which the Store installs and updates on Windows 11. It is there after the user's first logon, though there are documented cases where it needs a manual registration. Uv, Claude Code, fzf and Copilot CLI all list it. [winget, uv-install, claude-code-setup]

## 2. Scoop (owned bucket)

**A tap analog.** Any git repo of JSON manifests is a bucket: `scoop bucket add admiral https://github.com/jbuffin/scoop-admiral`, then `scoop install admiral/admiral`. The BucketTemplate repo includes CI and an "Excavator" job that updates manifests from `checkver` and `autoupdate` every four hours. The `main` bucket wants at least 500 stars and 150 forks for a GitHub project, a standard install and a non-GUI tool, so an owned bucket is the realistic route. [scoop-buckets, scoop-main-criteria]

**Manifest:**
- `architecture.64bit` and `architecture.arm64` each carry a `url` and `hash`.
- `bin` lists executables to shim onto PATH.
- `persist` lists dirs/files kept in `~/scoop/persist/<app>` and linked back with junctions or hard links.
- Admiral keeps its state in `$ADMIRAL_HOME`, so it needs no `persist`. [scoop-manifests, scoop-persist]

**Shims.** `~/scoop/shims/admiral.exe` reads `admiral.shim`, starts the real exe as a child, waits for it and passes its exit code back. A logon task must point at the real exe, not the shim, or every server carries a shim parent. [scoop-shim]

**Updates and a running exe.**
- Scoop installs each version in its own folder, `apps/<app>/<version>`, with a `current` junction pointing at the active one.
- `scoop update` calls `test_running_process`, which refuses with "The following instances of "admiral" are still running. Close them and try again" if any process's image path is under the app folder.
- The user can override that with `scoop config ignore_running_processes true`.
- So the host process has to live outside `~/scoop/apps/admiral`, for the same reason as with WinGet. [scoop-source]

**Install without admin.** `irm get.scoop.sh | iex` from a non-admin PowerShell; Scoop's installer refuses admin unless given `-RunAsAdmin`. Scoop itself runs `.ps1` scripts, so its README requires an execution policy of RemoteSigned, Unrestricted or ByPass and tells users to run `Set-ExecutionPolicy RemoteSigned -Scope CurrentUser`. That is one more step than the plain script install in §3 needs. [scoop-install]

**Reach.** Scoop has about 24.8k stars and the WinGet CLI about 26.6k (both read 2026-11-19). Developer tools that support Scoop list it next to WinGet, never instead of it (uv, for one). Scoop users have chosen it on purpose. WinGet users mostly have it because Windows ships it.

## 3. PowerShell `irm | iex` script

**Prior art for developer CLIs:**
- uv: `powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"`, installing to `~\.local\bin`.
- Claude Code: `irm https://claude.ai/install.ps1 | iex` (no admin), plus a `curl … install.cmd` for CMD.
- Both tools self-update only when installed through their script. On WinGet or Homebrew installs they show the package-manager command, which is what Admiral's `update.InstallFor` already does on the Mac. [uv-install, claude-code-setup]

**Execution policy:**
- When every scope is Undefined, a Windows client's effective policy is `Restricted`.
- `Restricted` "permits individual commands, but doesn't allow scripts", and `iex` on a downloaded string isn't a script file.
- `Invoke-RestMethod` doesn't add the Mark of the Web to what it downloads.
- So `irm | iex` runs under the default policy. Uv adds `-ExecutionPolicy ByPass` for the case where a policy blocks it.
- Microsoft calls execution policy "not a security boundary". [execution-policies]

**What the script should do** (no sources needed, this mirrors `install.sh`):
- Detect `$env:PROCESSOR_ARCHITECTURE` (AMD64 or ARM64).
- Download the same release zip WinGet uses and check its SHA-256 against the release's checksums.
- Unpack into `%LOCALAPPDATA%\Programs\Admiral\` or `~\.local\bin`.
- Add that folder to the *user* PATH in `HKCU\Environment` and broadcast the change. No admin, no machine PATH.
- Print `admiral service install` as the next step rather than running it.

**SmartScreen.** SmartScreen checks files that are *downloaded* against reputation lists of well-known files and certificates. Its download prompt keys off the Mark of the Web, which `irm` doesn't add, so a script install doesn't trigger it. A user who downloads the zip in a browser and runs `admiral.exe` from Explorer would see the "unrecognized app" warning. [smartscreen, execution-policies]

## 4. Code signing: what its absence blocks

| Channel | Blocked without a signature? |
|---|---|
| WinGet community repo | No. No signing requirement in policies or validation, and unsigned zips are in the catalog. An antivirus false positive would block it (signing doesn't fix that either). |
| Scoop (own bucket) | No. |
| `irm \| iex` script | No. No Mark of the Web, so no SmartScreen prompt. |
| Browser download + double-click | Shows a SmartScreen warning that the user can click through. |
| **Any channel on a PC with Smart App Control *On* (enforcement)** | **Yes.** Smart App Control blocks "unknown, unsigned code" unless Microsoft's cloud service predicts it safe. It turns itself off for developers during evaluation mode, and is on only on some clean installs. **Unconfirmed:** whether either test machine has it on. Check Settings › Windows Security › App & browser control. |

[policies, repository, smart-app-control, smartscreen]

## 5. Per-user start at logon

| | Task Scheduler (logon trigger, per user) | HKCU `…\CurrentVersion\Run` |
|---|---|---|
| Admin needed | No. A non-admin may register a task for their own account with an interactive or S4U logon and no password. | No |
| Restart if the server dies | `RestartOnFailure` (Count plus Interval) "if the task fails for any reason". **Unconfirmed:** whether a process that exits non-zero counts as failed, or only one that fails to launch (forum reports conflict). | None |
| When it starts | At the user's logon (plus `schtasks /run` or `Start-ScheduledTask` on demand) | "No guarantees about how promptly"; Windows may delay Run entries |
| Defaults to override | `ExecutionTimeLimit` stops the task after 72 h unless set to `PT0S`. `DisallowStartIfOnBatteries` defaults to true. `StopIfGoingOnBatteries` too. | 260-character command line |
| Session and environment | Runs in the user's interactive session with the user's token (`TASK_LOGON_INTERACTIVE_TOKEN`, `RunLevel` LUA) | Started by Explorer in the user's session |
| On-demand stop and restart | `Stop-ScheduledTask` / `Start-ScheduledTask`: the twin of `launchctl kickstart -k` | Admiral would have to find and kill its own process |

[security-contexts, restartonfailure, executiontimelimit, disallowstartifonbatteries, tasksettings, run-keys]

**Recommendation:** for `admiral service install` on Windows, register a task in Admiral's own task folder, with:
- a `LogonTrigger` with `UserId` set to the current user
- the principal set to that user, `InteractiveToken`, `LeastPrivilege`
- `ExecutionTimeLimit PT0S`, both battery flags false, `MultipleInstances IgnoreNew`, and `RestartOnFailure` (for example 1 minute × 999)
- an `Exec` action pointing at the windowless launcher (or the server, if it detaches itself)

`schtasks /create /xml` or the Task Scheduler COM API both register a task from XML. **Unconfirmed:** that registering a logon-trigger task from an *unelevated* prompt succeeds on Windows 11. Microsoft's security-context doc says a non-admin can do it for their own account, but forum reports disagree. It is the first thing to probe on the VM.

**Console window.**
- A console-subsystem process started with no parent console gets a new console window. Go builds console binaries unless linked with `-H windowsgui`.
- So the program the task runs should be a GUI-subsystem binary. It could be a tiny `admiralw.exe` launcher that starts `admiral.exe serve` with `CREATE_NO_WINDOW`. Or one binary could re-exec itself with `DETACHED_PROCESS`, but then the console flashes briefly before the re-exec.
- The CLI itself must stay a console binary, or it loses stdout in a terminal. [creation-of-a-console, process-creation-flags, go-link]

**Restart responsibility (suggestion).** Task Scheduler's restart semantics are uncertain, and a separate host process must exist anyway. The host could watch the server and restart it on a crash, the way launchd's `KeepAlive { Crashed = true }` does today. The task then only has to start whichever of the two is missing at logon.

**Job objects (risk, Unconfirmed).** Task Scheduler runs a task's process inside a job object. Reports conflict on whether that job allows breakaway on Windows 10/11, and whether ending the task kills its children:
- `CREATE_BREAKAWAY_FROM_JOB` works only if the job sets `JOB_OBJECT_LIMIT_BREAKAWAY_OK`.
- A Mozilla bug saw no breakaway on Windows 10 tasks.
- A uv-windows source comment says children die when Task Scheduler kills the parent.

If the server started the ConPTY host as a child, stopping or restarting the task could end every ship. The safe options:
- Start the host through its own scheduled task (the server calls `Run` on it), so Task Scheduler is the host's parent.
- Or confirm breakaway on the VM before relying on a plain child.

**PATH and environment.**
- On Windows, PATH lives in the registry (`HKCU\Environment` plus the machine key), not a shell profile, so the macOS "read the login shell's PATH" step has no equivalent.
- A long-running server holds the environment it started with, so tools installed later (a `winget install` that adds the Links folder, for example) would be missing from ships' PATH.
- **Suggestion:** build each ship's environment fresh from the registry at launch, for example with `CreateEnvironmentBlock`, instead of copying Admiral's own.
- **Unconfirmed:** whether Task Scheduler-started processes see PATH edits made after logon.

## 6. Replacing a running `.exe`

- **What Windows allows:** you can't overwrite or delete an image that is running, but you can rename it, because the running process holds the file, not its name.
- **The Go pattern:** `minio/selfupdate` (`apply.go`) renames `target` to `.target.old`, renames `.target.new` to `target`, and hides `.old` because deleting it "always fails" while the old process runs. It removes the `.old` file at the next update.
- **Why not delay until reboot:** `MoveFileEx(…, MOVEFILE_DELAY_UNTIL_REBOOT)` is the other classic route, but it needs an admin or LocalSystem context, so a per-user tool can't use it. [selfupdate, movefileex]

What this means for Admiral, by install method:

| Installed by | Who replaces `admiral.exe` | Running server | Host (ships) |
|---|---|---|---|
| Script | `admiral update` downloads the zip and renames the old exe aside, then restarts the task (`Stop`/`Start-ScheduledTask`) | Restarts on the new version | Keeps running from its own copy |
| WinGet | `winget upgrade` overwrites in place, so `admiral update` must stop the server first and start it after, or say so in its printed commands | Must be stopped for the overwrite | Unaffected only if it isn't running from the WinGet folder |
| Scoop | `scoop update` writes a new version folder but refuses while any process runs from `apps\admiral\…` | Must be stopped | Same: must run from elsewhere |

**Host process location.** On first start, and whenever the host's protocol version changes, Admiral copies its host binary to `<ADMIRAL_HOME>\host\<version>\admiral-host.exe` and starts it from there. That keeps the host out of every package manager's way. Ships stay alive across ordinary upgrades, and the new server reattaches to the old host as long as the protocol stays compatible. Retiring an old host when no ships remain is the session-host grilling's call.

## Recommendation

1. **Release artifacts:** `admiral_<ver>_windows_amd64.zip` and `…_arm64.zip` on the GitHub release, each holding `admiral.exe` and a windowless launcher, plus SHA-256 checksums. They come from the same tag, published by hand after the Mac release as the map says.
2. **WinGet:** a `jbuffin.Admiral` multi-file manifest, `zip` with nested `portable` for x64 and arm64. Submit the first version by hand with `wingetcreate` or Komac. Automate later versions with `winget-releaser` in the hand-started Windows publish job. Expect 1–3 days for the first review and usually hours for updates.
3. **Script:** `install.ps1` on the release (`irm https://github.com/jbuffin/admiral/releases/latest/download/install.ps1 | iex`). It installs per user, adds a user PATH entry, needs no admin and checks the checksum.
4. **Scoop:** later and optional. An owned `jbuffin/scoop-admiral` bucket with `checkver` and `autoupdate`, maintained by the BucketTemplate's Excavator.
5. **Service:** `admiral service install` registers a per-user logon task as in §5. The host is started separately and outside the install folder, and the host or the task restarts the server.
6. **Update:** extend `update.InstallFor` with `winget` and `scoop` methods (detected from the exe's path under `WinGet\Packages` or `scoop\apps`) that print the package-manager command plus the stop and start steps. Only script installs self-replace.
7. **Signing:** not needed for any channel. Smart App Control in enforcement mode is the trigger to revisit it.

## Unconfirmed, to check on the VM or PC

- Registering a logon-trigger task from an unelevated prompt (`schtasks /create /xml`) on Windows 11 arm64 and x64.
- Whether `RestartOnFailure` restarts a process that exits non-zero or crashes, as opposed to one that fails to launch.
- Whether a child of a Task Scheduler task survives `Stop-ScheduledTask`, with and without `CREATE_BREAKAWAY_FROM_JOB`.
- Whether `winget upgrade --force` stops a running portable exe in the current client.
- Whether either machine has Smart App Control on.
- Whether Defender flags the Go binary (common for unsigned Go executables, but not documented). It would surface as `Validation-Defender-Error` on the WinGet PR.
- Whether processes started by a logon task pick up registry PATH changes made after logon.

## Sources

- [manifest] Microsoft Learn, "Create your package manifest" (2026-09-14): https://learn.microsoft.com/en-us/windows/package-manager/package/manifest
- [installer-schema] winget-pkgs, installer manifest schema 1.12.0: https://github.com/microsoft/winget-pkgs/blob/master/doc/manifest/schema/1.12.0/installer.md
- [winget] Microsoft Learn, "Use WinGet to install and manage applications" (2026-07-19): https://learn.microsoft.com/en-us/windows/package-manager/winget/
- [portable-spec] winget-cli spec "#182 Support for installation of portable standalone apps": https://github.com/microsoft/winget-cli/blob/master/doc/specs/%23182%20-%20Support%20for%20installation%20of%20portable%20standalone%20apps.md
- [settings] winget-cli `doc/Settings.md` (portablePackageUserRoot): https://github.com/microsoft/winget-cli/blob/master/doc/Settings.md
- [repository] Microsoft Learn, "Submit your manifest to the repository": https://learn.microsoft.com/en-us/windows/package-manager/package/repository
- [policies] Microsoft Learn, "Windows Package Manager repository policies": https://learn.microsoft.com/en-us/windows/package-manager/package/windows-package-manager-policies
- [fzf-manifest] https://github.com/microsoft/winget-pkgs/blob/master/manifests/j/junegunn/fzf/0.75.0/junegunn.fzf.installer.yaml
- [winget-pkgs PR sample] `gh pr list -R microsoft/winget-pkgs --state merged --search "New package in:title" --limit 40`, run 2026-11-19
- [winget-cli-5235/5214] https://github.com/microsoft/winget-cli/issues/5235, https://github.com/microsoft/winget-cli/pull/5214
- [claude-code-setup] Claude Code "Advanced setup": https://code.claude.com/docs/en/setup
- [uv-install] uv installation docs: https://docs.astral.sh/uv/getting-started/installation/
- [scoop-install] https://github.com/ScoopInstaller/Install
- [scoop-buckets] https://github.com/ScoopInstaller/Scoop/wiki/Buckets
- [scoop-main-criteria] https://github.com/ScoopInstaller/Scoop/wiki/Criteria-for-including-apps-in-the-main-bucket
- [scoop-manifests] https://github.com/ScoopInstaller/Scoop/wiki/App-Manifests
- [scoop-persist] https://github.com/ScoopInstaller/Scoop/wiki/Persistent-data
- [scoop-shim] https://github.com/ScoopInstaller/Shim
- [scoop-source] Scoop `libexec/scoop-update.ps1` (calls `test_running_process`) and `lib/install.ps1` (`function test_running_process`): https://github.com/ScoopInstaller/Scoop
- [execution-policies] Microsoft Learn, about_Execution_Policies (PowerShell 7.6, 2026-08-31): https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_execution_policies
- [smartscreen] Microsoft Learn, Microsoft Defender SmartScreen (2026-11-03): https://learn.microsoft.com/en-us/windows/security/operating-system-security/virus-and-threat-protection/microsoft-defender-smartscreen/
- [smart-app-control] Microsoft Learn, Smart App Control (2026-09-30): https://learn.microsoft.com/en-us/windows/apps/develop/smart-app-control/overview
- [security-contexts] Microsoft Learn, Security Contexts for Tasks: https://learn.microsoft.com/en-us/windows/win32/taskschd/security-contexts-for-running-tasks
- [tasksettings] https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings
- [executiontimelimit] https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-executiontimelimit
- [restartonfailure] https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-restartonfailure-settingstype-element
- [disallowstartifonbatteries] https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-disallowstartifonbatteries-settingstype-element
- [run-keys] Microsoft Learn, Run and RunOnce Registry Keys: https://learn.microsoft.com/en-us/windows/win32/setupapi/run-and-runonce-registry-keys
- [creation-of-a-console] https://learn.microsoft.com/en-us/windows/console/creation-of-a-console
- [process-creation-flags] https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags
- [go-link] `cmd/link` (`-H windowsgui`): https://pkg.go.dev/cmd/link
- [movefileex] https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexa
- [selfupdate] minio/selfupdate `apply.go`: https://github.com/minio/selfupdate/blob/master/apply.go
- Job-object breakaway under Task Scheduler (secondary, unconfirmed): https://bugzilla.mozilla.org/show_bug.cgi?id=1330718
