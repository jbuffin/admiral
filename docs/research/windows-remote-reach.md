# Remote reach and web pushes from a Windows host

> Research for "Map: Admiral runs natively on Windows" ([#872](https://github.com/jbuffin/admiral/issues/872)), answering [#987](https://github.com/jbuffin/admiral/issues/987). Written 2026-11-27 from primary sources listed at the end. It takes as given the map's settled points: on Windows, Admiral's server is started by a per-user logon task through a windowless `admiralw.exe`, the server gives every child a fresh user environment, and the binaries are unsigned. Install, ConPTY and harness questions are in `windows-install.md`, `windows-conpty-host.md` and `windows-harnesses.md` on their research branches and are not repeated here.
>
> Two checks ran on the Windows 11 25H2 arm64 VM over SSH. (1) The Tailscale 1.104.7 MSI's tables were read through the `WindowsInstaller.Installer` COM object, without installing it. (2) A small Go 1.26.5 probe tested binds, a LAN connection from the Mac, and file link/rename. Nobody was signed in to the VM's desktop, so the probe ran in the SSH session (elevated, session 0), not as a limited logon task, and no firewall prompt could show. Where that matters, the text says so.

## Answer

ADR 0031, 0032 and 0033 carry over to Windows with no change to their design. Tailscale Serve, `status --json`, the VAPID key, the subscription store, RFC 8291 encryption, pairing codes and the e2e fake LAN are all plain Go, or the same Tailscale CLI, on both systems. Four things work differently on Windows:

1. **Tailscale only runs while the GUI does.** By default, Windows Tailscale works in "client mode": tailscaled serves only while a user is signed in and the Tailscale tray app is running. Admiral also runs only while the engineer is signed in, so the two usually line up. But Admiral can start before the tray app connects, and quitting the tray app turns off remote access.
2. **Admiral finds `tailscale.exe` through its own `PATH`.** The MSI appends `C:\Program Files\Tailscale` to the *machine* `PATH`. But `exec.Command("tailscale")` searches the server's own environment, not the fresh one it builds for children, so a Tailscale installed after Admiral started is "not installed" until Admiral restarts.
3. **The firewall blocks the LAN path.** Windows Defender Firewall blocks inbound connections to an app that has no rule, and only an admin's "Allow" creates one. Rules are keyed on the full path of the program. Tailscale mode needs no rule at all, because `tailscale serve` reaches Admiral on loopback.
4. **Two small OS quirks.** `os.Rename` over a file that another process holds open fails with "Access is denied", which can hit `devices.json`, where subscriptions are kept. A failed bind is error 10048, `WSAEADDRINUSE`, which `errors.Is(err, syscall.EADDRINUSE)` does not match.

No elevation is needed for anything Admiral runs: `tailscale serve` to a port, `status --json`, `serve status --json`, the listener, pushes.

## `tailscale serve` (ADR 0031)

**Where the CLI lives.**
- The MSI installs `tailscale.exe`, `tailscaled.exe` (the `Tailscale` service, auto-start, no account named, so LocalSystem) and `tailscale-ipn.exe` (the tray GUI) into `INSTALLDIR`, which defaults to `C:\Program Files\Tailscale`. [msi-kb] [msi-tables]
- Its Environment table has the row `=-*PATH` = `[~];[INSTALLDIR]` on the `ClientCmd` component (the one holding `tailscale.exe`). Per the MSI Environment-table spec, this appends the install directory to the **system** `PATH` on install and removes it on uninstall. Two more rows (`!*PATH`) strip the old NSIS installer's `Tailscale IPN` paths. [msi-tables] [msi-env]
- The KB only says "you can access the CLI by executing the `.exe` from the Command Prompt". [cli-kb] The MSI table is the primary source for the `PATH` change.

**Whether the server that the logon task starts can find it.**
- `internal/tailscale/tailscale.go` runs `exec.CommandContext(ctx, "tailscale", …)`. Go resolves that against the **calling process's** `PATH`, with `PATHEXT` adding `.exe`.
- The settled "fresh user environment" applies to children Admiral launches, not to Admiral's own lookups. So whether `tailscale` is found depends on the environment Task Scheduler gave Admiral at logon.
- The MSI spec says new environment values reach "a new process, or another process that checks for these messages". Admiral doesn't listen for `WM_SETTINGCHANGE`. [msi-env]
- So, if the engineer installs Tailscale while Admiral runs, the checks say "tailscale is not installed or not on PATH" until the next logon or restart. Setup's **Set up** fails the same way.
- **Unconfirmed** (also open in `windows-install.md`): whether a logon task started at the next logon sees a machine `PATH` edited during the previous session. It should, since the environment is rebuilt from the registry at logon.
- Fix (a decision): resolve `tailscale` from the fresh registry `PATH` that ships already get, then fall back to `%ProgramFiles%\Tailscale\tailscale.exe`.

**`serve --bg --https=443 http://127.0.0.1:<port>` and `status --json` behave the same.**
- It is the same Go CLI on every platform. Serve's target form, `--bg` and `--https` are platform-neutral, and "only `http://127.0.0.1` is supported for proxies", which is exactly what `ServeArgs` sends. [serve-cli]
- `--bg` config persists and "resumes automatically after a reboot or after `tailscale down` and `tailscale up`". [serve-cli]
- `status --json` is the same `ipnstate.Status` struct, so `Evaluate`'s fields (`BackendState`, `Self.DNSName`, `Self.UserID`, `User`, `CurrentTailnet.MagicDNSEnabled`, `CertDomains`) are there.
- Nothing in `Evaluate`, `findAdmiral` or `ServeArgs` reads the OS.

**Elevation: not needed.**
- The Serve KB's "On Windows, run the command from an Administrator console" sits in its Limitations section and covers only serving a file, directory or Unix socket. The same section says "Forwarding a port does not require root." [serve-kb]
- The LocalAPI code confirms it. `authorizeServeConfigForGOOSAndUserContext` returns early unless the config `HasPathHandler()` or serves a Unix socket. Only then does it refuse with "must be a Windows local admin to serve a path or Unix socket". [ts-localapi-serve]
- On Windows, `actor.Permissions` returns read and write for any user allowed to connect ("Windows users always have read/write access to the local API if they're allowed to connect"). [ts-ipnserver]
- So the limited-token server can run all three commands Admiral uses.

**Who may connect: one Windows user at a time.**
- `checkConnIdentityLocked` refuses a second Windows user with "Tailscale already in use" while another user's client is connected. SYSTEM is exempt. [ts-ipnserver]
- The server and the tray app run as the same user, so this only matters on a PC with several signed-in users (fast user switching). The other user's Admiral would see `status` fail.
- Unattended mode gives "the user who enabled it exclusive control of the Tailscale configuration", so it must be the engineer's own account that turns it on. [unattended-kb]

**The GUI must be running.**
- `ForceDaemon` (Run Unattended) is documented as: "whether a platform that normally operates in 'client mode' (that is, requires an active user logged in with the GUI app running) should keep running after the GUI ends and/or the user logs out. The only current applicable platform is Windows." [ts-prefs]
- The KB says: "runs within the user login session by default". When the user signs out, Tailscale disconnects until someone signs in again. [unattended-kb]
- What this means for Admiral:
  - **Normal case: fine.** The MSI starts the GUI for all users at install (`TSCAStartApp`/`StartAppForAllUsers`) [msi-tables], and both it and Admiral live in the signed-in session.
  - **Logon race.** The logon task can start Admiral before the tray app has connected. Admiral's startup `Inspect` may then see tailscaled not running, or time out after 4 s. Admiral still starts on loopback and warns, as ADR 0031 says, and Settings rechecks live. But the startup Funnel check reads "unknown" rather than stopping a Funnel'd start. That check is defence in depth: proxied requests without `Tailscale-User-Login` are refused anyway.
  - **Quitting the tray app turns remote access off.** The fix is Tailscale's own Run Unattended (`tailscale set --unattended=true`, or the `UnattendedMode` policy). [unattended-kb] [msi-kb] Settings could name it when the running check fails on Windows.
  - Admiral's CLI calls are short connections. On Windows, when the last request finishes, the server clears the current user (`SetCurrentUser(nil)`) [ts-ipnserver], so the CLI is no substitute for the GUI.

**No console flash, as long as children inherit a hidden console.**
- `tailscale.exe` is a console program, and `Inspect` runs on every `GET /api/remote`.
- If the server runs with a hidden console (started with `CREATE_NO_WINDOW`), its console children inherit it. If it has no console at all (`DETACHED_PROCESS`, or a GUI-subsystem server), each call opens a console window. [process-creation-flags] [creation-of-a-console]
- This decides how `admiralw.exe` starts the server. Otherwise the CLI calls need `CREATE_NO_WINDOW` through `SysProcAttr`.

**Tailscale's own Windows firewall work.**
- Tailscale sets its adapter's network category to Private and "modifies Windows networking settings such as routes, DNS, and firewall rules". [ts-netcategory]
- Serve traffic ends in tailscaled and is proxied to `127.0.0.1`, so Admiral needs no rule for it (see Firewall).

## Web push (ADR 0033)

Nothing in sending pushes, the VAPID key or the subscription store assumes macOS, with one small exception (rename).

- **No library.** `go.mod` has no Web Push dependency. `internal/push` does VAPID (RFC 8292, ES256 JWT), RFC 8291 encryption and the HTTP POST with `crypto/*` and `net/http` alone, so it builds and runs the same on Windows.
- **TLS roots.** `SafeClient` passes `nil` roots to use the system pool. On Windows, Go verifies chains through the platform verifier (CryptoAPI), as it does on macOS. [go-x509] Push services (FCM, Apple, Mozilla, WNS for Edge) are public HTTPS hosts.
- **The dial guard.** `DialControl` uses `net.Dialer.Control` with a `syscall.RawConn`, which Go supports on Windows. It decides from the resolved address only.
- **The VAPID key file.** `LoadKey` writes through `os.CreateTemp` plus `os.Link`, so it never replaces an existing key.
  - On NTFS the probe found `os.Link` works, and a second link to an existing name fails with `errors.Is(err, os.ErrExist) == true`, the error the retry loop relies on.
  - The `// created 0600` comment is POSIX-only: on Windows Go uses only the read-only bit [go-os-chmod], so the key's privacy comes from the ACL that `$ADMIRAL_HOME`'s directory passes down. Under `%LOCALAPPDATA%` that is the user, SYSTEM and Administrators, the same reach as root on macOS.
  - The dangling-symlink guard in `LoadKey` still works, since Go reports Windows symlinks as `ModeSymlink`.
- **The subscription store** is `devices.json` (subscriptions sit on their device), saved by `os.CreateTemp` plus `os.Rename`.
  - The probe found that `os.Rename` onto a file another handle holds open (Go's `os.Open`) fails with **"Access is denied"**, and succeeds once it is closed.
  - Admiral itself reads `devices.json` only at load, but an antivirus scan, a backup or an editor can hold it briefly. A failed save loses a new subscription, a revoke or a `lastSeen`.
  - A short retry on `ERROR_ACCESS_DENIED`/`ERROR_SHARING_VIOLATION` is the usual Windows answer. The same applies to every temp-and-rename save in Admiral, not just this one.
- **The engineer's own browser on the host.**
  - A loopback browser counts as a secure context, so it can subscribe the way This Mac does.
  - Edge's subscriptions point at WNS (`*.notify.windows.com`), which reportedly answers `406` when it throttles. [edge-push-policy] [wns-406] The sender drops only on 404/410, so a 406 is just a failed push.
  - The device is still named "This Mac" (`thisMacName` in `internal/auth/push.go`), and Settings and CLI text say "this Mac" throughout. Naming is a map-wide wording question, not a push one.

## Pairing (ADR 0032)

- **The flow is OS-free.** Codes, redemption, the cookie and bearer, binding to a login, and the 401 back to **Pair this app** are all in `internal/auth`, with no OS calls.
- **The LAN address it offers.** `server.LANIPv4` asks the routing table which source address reaches `192.0.2.1`, without sending a packet, then falls back to `net.InterfaceAddrs`. On the VM it picked `172.16.165.134`, the default-route adapter, ahead of IPv6 link-local and loopback. The method is the same on both systems; `ADMIRAL_LAN_IP` still overrides it.
  - A Windows PC is likelier to have Hyper-V, WSL or VPN adapters, but the route lookup already prefers the default route.
- **The LAN URL is firewalled.** `http://<LAN IP>:7420` reaches the PC only once there is an inbound allow rule (see Firewall). A pairing QR in LAN mode shows a URL the iPad can't open, with no error on the PC.
- **The e2e fake LAN carries over.** `cmd/admiral/listen_tcp_e2e.go` adds a second listener on `127.0.0.1:<port>` and relabels its connections (`192.0.2.20` reaching `ADMIRAL_LAN_IP`).
  - On the VM, loopback connections need no firewall rule, and binding `127.0.0.1:P` beside `0.0.0.0:P'` works.
  - The fake `tailscale` (`e2e/testdata/faketailscale/tailscale`) is a `#!/bin/sh` script, so `exec` can't start it on Windows. It needs a Go or `.cmd` twin, or the Tailscale tests stay on macOS. That is part of the map's test-suite question, not this one.
- **Binds.** On the VM, with Go's defaults (no `SO_EXCLUSIVEADDRUSE`), one user can bind `127.0.0.1:P` while `0.0.0.0:P` is held, and the reverse. A second wildcard bind fails with `errno=10048`. This matches Microsoft's same-user table: wildcard, then specific, succeeds. [winsock-reuse]
  - **Rebind.** `cmd/admiral/rebind.go`'s retire-and-retry branch keys on `errors.Is(err, syscall.EADDRINUSE)`, which is **false** for `WSAEADDRINUSE` on Windows. The overlapping move it handles succeeds outright on Windows, so the branch isn't needed for that move. A same-address rebind would just log and keep the old listener.
  - **Port hijack.** The same table means that, under `--listen 0.0.0.0:7420`, another process of the *same user* could bind `127.0.0.1:7420` and take loopback traffic, including what `tailscale serve` proxies. A different user gets `WSAEACCES`. Microsoft recommends `SO_EXCLUSIVEADDRUSE` for all servers. [winsock-reuse] A same-user process can already read `$ADMIRAL_HOME`, so this adds little, but setting the option on Windows through `net.ListenConfig.Control` is cheap.
- **`hostAllowed`** uses `os.Hostname` (the computer's DNS host name; `WTG_Win11` on the VM) and interface IPs. It is cross-platform. Only the `.local` alias assumes Bonjour, and nothing in pairing depends on it.

## Firewall

- **Default inbound is block.** "Since there's a default *block* action in Windows Firewall, you must create inbound exception rules to allow the traffic." [fw-rules]
  - On the VM, the probe's `0.0.0.0:7420` answered loopback and the VM's own IP from inside the VM, but a `curl` from the Mac to `172.16.165.134:7420` timed out. The VM's Ethernet was on the Public profile, all three profiles had `NotifyOnListen` on, and no rule was created.
  - SSH answered on the same address through its own allow rule.
- **The prompt.** With no allow rule, "a dialog box prompts the user to either allow or block an application's packets the first time the app is launched or tries to communicate":
  - "If the user has admin permissions, they're prompted. If they respond *No* or cancel the prompt, block rules are created."
  - "If the user isn't a local admin and they are prompted, block rules are created. It doesn't matter what option is selected."
  - Once created, rules "must be deleted to generate the prompt again". [fw-rules]
  - **Unconfirmed:** whether the prompt shows for a windowless process started by a logon task. The VM had no desktop session. Microsoft ties it to the listen call, not to a window.
- **Rules are per full path.** "You can only create rules using the full path to the application." [fw-rules] Two consequences:
  - `windows-install.md` has Admiral copy its host to a versioned path under the data directory, so every upgrade is a new program to the firewall: a new prompt, and the old rule left behind.
  - A dismissed prompt leaves block rules that silently break the LAN path until someone deletes them.
- **Tailscale mode needs no rule.** It listens on `127.0.0.1:7420` (ADR 0031). tailscaled (LocalSystem) terminates the tailnet connection and proxies to loopback, which the firewall doesn't filter (confirmed on the VM). Only `--listen 0.0.0.0:7420`, plain LAN mode, or opening the tailnet IP by hand (ADR 0031 option A, over the Tailscale adapter, which Tailscale marks Private [ts-netcategory]) need an inbound rule.
- **Unsigned `admiral.exe`.**
  - The firewall prompt and rules key on the path, not the signature, so being unsigned doesn't change the firewall's behaviour. The prompt shows no verified publisher.
  - SmartScreen and Smart App Control are covered in `windows-install.md` and don't involve the listener.
  - Outbound (pushes, `gh`, harness APIs) is allowed by default. [fw-rules]
- **Making a rule ahead of time** takes an admin, for example `New-NetFirewallRule -Direction Inbound -Protocol TCP -LocalPort 7420 -Profile Private -Action Allow`. A rule by port, not program, survives upgrades. Microsoft recommends providing rules "before the application's first launch" and scoping home-network apps to the Private profile or Local Subnet. [fw-rules]

## Differences that need a decision

1. **Default Windows to Tailscale mode, or to loopback?** On macOS, LAN mode is the zero-setup default. On Windows it is blocked by the firewall until an admin allows it, and its rule breaks on each upgrade. Options:
   - (a) Windows listens on loopback unless remote access is set up.
   - (b) `admiral setup` offers an elevated, port-scoped Private-profile rule for LAN mode.
   - (c) Keep the macOS default, and when Admiral sees no allow rule for its port, tell the engineer the LAN URL won't work.
2. **How Admiral finds `tailscale.exe`.** Resolve it from the fresh registry `PATH`, then `%ProgramFiles%\Tailscale\tailscale.exe`, instead of the server's frozen `PATH`. The same applies to `gh` and anything else the server itself runs.
3. **The Tailscale GUI and unattended mode.** Decide whether Remote access on Windows checks for and recommends Run Unattended, and what the running check says when the tray app isn't running ("start Tailscale" versus "not running"). Decide whether startup should retry `Inspect` after logon, rather than treating the first, often too early, read as final for the startup line and the Funnel check.
4. **How `admiralw.exe` starts the server** (`CREATE_NO_WINDOW` versus `DETACHED_PROCESS`, already open in the install research). It now also decides whether every `tailscale` call needs `CREATE_NO_WINDOW` to avoid console flashes.
5. **Retrying temp-and-rename saves on Windows.** `devices.json` holds subscriptions and device tokens. Decide on a bounded retry on access-denied and sharing violations, everywhere Admiral replaces a file. Small, but it touches every store.
6. **Small code fixes, no design needed:**
   - Match `WSAEADDRINUSE` in `rebind.go`, for example with `errors.Is(err, windows.WSAEADDRINUSE)` or a portable helper.
   - Set `SO_EXCLUSIVEADDRUSE` on Windows listeners.
   - Give the fake `tailscale` a Windows form if the Tailscale e2e tests should run there.
   - Fix the "This Mac" wording, which goes with the map's naming.

## Sources

- [msi-tables] Tailscale 1.104.7 amd64 MSI (`https://pkgs.tailscale.com/stable/tailscale-setup-1.104.7-amd64.msi`), Environment, File, ServiceInstall and CustomAction tables, read on the VM with `WindowsInstaller.Installer` `OpenDatabase` (read-only) on 2026-11-27.
- [msi-env] Microsoft Learn, Environment Table: https://learn.microsoft.com/en-us/windows/win32/msi/environment-table
- [msi-kb] Tailscale KB, Install Tailscale on Windows with MSI: https://tailscale.com/kb/1189/install-windows-msi
- [cli-kb] Tailscale KB, Tailscale CLI: https://tailscale.com/kb/1080/cli
- [serve-kb] Tailscale KB, Tailscale Serve: https://tailscale.com/kb/1312/serve
- [serve-cli] Tailscale KB, `tailscale serve` command: https://tailscale.com/kb/1242/tailscale-serve
- [unattended-kb] Tailscale KB, Keep Tailscale running when I'm not logged in: https://tailscale.com/kb/1088/run-unattended
- [ts-prefs] tailscale/tailscale `ipn/prefs.go` (`ForceDaemon`): https://github.com/tailscale/tailscale/blob/main/ipn/prefs.go
- [ts-ipnserver] tailscale/tailscale `ipn/ipnserver/server.go` (`checkConnIdentityLocked`, `Permissions`, `addActiveHTTPRequest`) and `ipn/ipnserver/actor.go` (`IsLocalAdmin`): https://github.com/tailscale/tailscale/tree/main/ipn/ipnserver
- [ts-localapi-serve] tailscale/tailscale `ipn/localapi/serve.go` (`authorizeServeConfigForGOOSAndUserContext`): https://github.com/tailscale/tailscale/blob/main/ipn/localapi/serve.go
- [ts-netcategory] Tailscale docs, "Windows network configuration failed": https://tailscale.com/docs/reference/messages/client/set-network-category-failed
- [fw-rules] Microsoft Learn, Windows Firewall rules (updated 2026-10-08): https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/rules
- [winsock-reuse] Microsoft Learn, Using SO_REUSEADDR and SO_EXCLUSIVEADDRUSE: https://learn.microsoft.com/en-us/windows/win32/winsock/using-so-reuseaddr-and-so-exclusiveaddruse
- [process-creation-flags] Microsoft Learn, Process Creation Flags: https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags
- [creation-of-a-console] Microsoft Learn, Creation of a Console: https://learn.microsoft.com/en-us/windows/console/creation-of-a-console
- [go-x509] Go `crypto/x509`, `Certificate.Verify` (platform verifier on Windows and macOS): https://pkg.go.dev/crypto/x509#Certificate.Verify
- [go-os-chmod] Go `os.Chmod` (on Windows only the read-only bit is used): https://pkg.go.dev/os#Chmod
- [edge-push-policy] Microsoft Learn, Edge policy ForceBuiltInPushMessagingClient (Edge's WNS client): https://learn.microsoft.com/en-us/deployedge/microsoft-edge-browser-policies/forcebuiltinpushmessagingclient
- [wns-406] Microsoft Q&A, 406 from Edge web push endpoints (secondary, user report): https://learn.microsoft.com/en-us/answers/questions/940324/406-not-acceptable-may-be-returned-when-sending-a
- Repo: `internal/tailscale/tailscale.go`, `internal/tailscale/route.go`, `internal/push/{vapid,endpoint,sender}.go`, `internal/auth/{auth,pair,push,devices}.go`, `internal/server/lan.go`, `cmd/admiral/{main,rebind,listen_tcp_e2e}.go`, `docs/agents/testing/fakes.md`, ADR 0031/0032/0033.
- VM probe (Go 1.26.5, `GOOS=windows GOARCH=arm64`, Windows 11 25H2 build 26200), 2026-11-27: binds on 7420/7422, a LAN `curl` from the Mac, `os.Link` and `os.Rename` against an open handle.
