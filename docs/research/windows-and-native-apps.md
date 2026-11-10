# Admiral on Windows, and native apps

> Research for the Admiral redesign map. Native apps and a native Windows host are out of scope for that map; it only keeps the API and pairing ready for them.

Research notes, 2026-11-09. Read-only on the repo.

## 1. The tmux portability contract

What Admiral uses tmux for today (from `internal/sessionhost`, `internal/bridge`, `internal/launcher`, `internal/cli/hook.go`, `internal/chat`, `internal/ships/resume.go`; ADRs 0002, 0005, 0007, 0012, 0014, 0019). A replacement host has to supply all of it:

1. **A long-lived server that outlives Admiral**, on a private socket (`$ADMIRAL_HOME/tmux.sock`). After a restart Admiral rebuilds every ship from it (ADR 0012).
2. **Sessions with windows and panes**: `new-session -d` with a placeholder command, then `respawn-pane -k` to the harness argv with `-c dir`; `new-window` for crew boats; `kill-session`; `has-session`; per-session environment (`set-environment`, `-r`).
3. **`remain-on-exit`**: a dead pane stays readable, with `pane_dead_status`, `pane_dead_signal` and `pane_dead_time` (ADR 0008, anchored ships).
4. **A metadata store on the sessions themselves**: about 25 `@admiral-*` user options at session, window and pane scope. These are the only record of a ship. Harness hooks write them from inside the pane with `tmux set -p -t $TMUX_PANE` (ADR 0007, 0014), and hooks read them back with `show-options` and `display -p`.
5. **Control mode** (`tmux -C`): one client for the poll (`list-panes -a -F <format>` every 500 ms, plus `%sessions-changed`) and one per watched ship for the bridge (`%output` per pane, `%window-close`, `%begin`/`%end` framing, and a parser Admiral wrote).
6. **Snapshots**: `capture-pane -p -e` gives the screen and up to 2,000 lines of scrollback with SGR colours. The cursor and modes (alternate screen, application cursor, bracketed paste, mouse) come from format variables. `history-limit 10000`.
7. **Input**: `send-keys -H` (hex bytes), `send-keys -l`, and `load-buffer` plus `paste-buffer -p` for bracketed-paste prompts (chat, resume, crew send).
8. **Sizing**: per-window `refresh-client -C @win:CxR`, and the pinned size stays after detach.
9. **Bell**: a global `alert-bell` hook stamps `@admiral-bell`. This is the Codex needs-you signal.
10. **Engineer attach**: `admiral attach` runs `tmux attach` (read-only `-r` when anchored) from any terminal.

Points 1, 4 and 10 are the hard part. Points 5 to 9 are terminal plumbing that any PTY host can do.

## 2. Windows options

**What the harnesses support.** All three run natively on Windows:

- Claude Code supports Windows 10 1809+. Git for Windows is optional (without it, Claude Code uses PowerShell). Sandboxing works only in WSL2.
- Codex runs natively in PowerShell with its own Windows sandbox, and recommends WSL2 only for Linux tooling.
- Copilot CLI needs PowerShell 6+, and installs through WinGet or npm.

So a native Windows Admiral is viable on the harness side.

**A. WSL2 with Admiral and tmux inside it.** Almost no code changes: Linux is already "possible" per ADR 0005. The costs:

- The repos and harness CLIs have to live in the Linux filesystem.
- LAN access from the iPad needs either mirrored networking (Windows 11 22H2+, plus a Hyper-V firewall rule) or `netsh portproxy` in NAT mode.
- WSL has to be running for ships to stay alive.
- Windows-native projects and tools are out of reach.

This is the cheapest way to "support Windows", but it supports Linux-on-Windows, not Windows.

**B. A tmux clone on Windows (psmux and similar).** psmux is a Rust, ConPTY-based tmux with about 83 commands, a format engine and hooks. Its docs do not mention control mode (`-C`), `@user` options, `-S` sockets, `capture-pane -e` or `send-keys -H`, and those are exactly what Admiral needs. A third-party clone also tends to lag tmux's behaviour. Not recommended as the foundation, though `admiral attach` could use it later.

**C. Admiral's own session host on Windows (ConPTY), with tmux kept on macOS and Linux.** The building blocks:

- ConPTY: `CreatePseudoConsole`, `ResizePseudoConsole`, `ClosePseudoConsole` (Windows 10 1809+). Closing the pseudoconsole kills its processes, so whatever holds the handles must outlive Admiral.
- Go libraries: `aymanbagabas/go-pty` (creack/pty on Unix, ConPTY on Windows, but it pulls in hcsshim), `UserExistsError/conpty` (small), `charmbracelet/x/conpty`. creack/pty itself does not do Windows: its `pty.Start` returns ErrUnsupported there.
- Headless terminal for snapshots: `charmbracelet/x/vt` (v0.1, MIT, has scrollback, alt-screen and an ANSI `Render()`). Alternatives are `tuios.dev/vt` and `gitpod-io/xterm-go`.

The design is a small `admiral-host` daemon, started per user (Task Scheduler at logon, or a Windows service) and reached over a named pipe. It owns the ConPTYs, keeps a vt emulator per pane for `join` snapshots, stores the `@admiral-*` key/value records per session, window and pane, and exposes `ADMIRAL_PANE` plus a pipe path so `admiral hook` can write state without tmux. Bell detection becomes scanning for BEL in the output stream. ConPTY rewrites VT output, so snapshots would come from Admiral's own emulator rather than tmux's grid.

**D. One own session host everywhere, replacing tmux.** This removes the tmux dependency and the control-mode parser, and gives one code path to test. The costs:

- `tmux attach` from a real terminal goes away. It needs a replacement `admiral attach` client that speaks the host protocol, raw-mode stdin and resize included.
- Admiral loses tmux's battle-tested emulator and history.
- Claude Code's agent teams, which use a tmux backend, can no longer reuse the session.
- ADR 0002's main argument ("survives restarts, attachable from any terminal") has to be met by Admiral's own code.

**Recommendation.** Put a `SessionHost` interface at the seam in item 1 (spawn, respawn, kill, list, options get/set, output stream, snapshot, input, paste, resize, death, bell). Keep tmux as the macOS and Linux backend, and document WSL2 as the stopgap for Windows now. Build the ConPTY daemon (C) only when there is real Windows demand, and test it against the same interface. Don't do D unless keeping two backends proves expensive. The interface leaves that door open.

## 3. Native apps

**The baseline is a PWA.** iOS and iPadOS 16.4+ allow web push only for a Home Screen web app with a manifest (`display: standalone`), and only after a user gesture. Service workers and push also need a secure context, and Admiral serves plain HTTP on the LAN with no manifest today (`web/public` has only a favicon). The cheap route to a secure context is `tailscale serve` (a valid tailnet TLS certificate, proxying to localhost) or the Cloudflare Tunnel origin that `auth.go` already allows. Even with push, the server still needs a VAPID sender. The cost is small: a manifest, a service worker, a VAPID push sender, and `apple-mobile-web-app` meta tags.

**iOS and iPadOS, plus macOS: a SwiftUI client.**

- Uses the existing REST API, `/ws` and `/ws/bridge`.
- SwiftTerm (MIT; AppKit and UIKit views, Metal, headless core, used by Secure ShellFish and La Terminal) renders the terminal from the bridge's binary frames. The bridge protocol is already client-agnostic.
- Native push needs APNs, and APNs needs the developer's key, which can't ship inside every user's Admiral. That means a relay. Copy ntfy's pattern: the local server sends a content-free poll ping through a small hosted relay, and the app fetches the details over the tailnet.
- Local notifications only fire while the app is running.

**macOS menu-bar app.** As ADR 0005 already plans: a small Swift shell that registers the bundled `admiral` binary with `SMAppService` (macOS 13+). It shows status and needs-you counts and opens the UI. This step needs a Developer ID and notarization.

**Wrappers.** Tauri 2 targets desktop, iOS and Android, and has a WebSocket plugin. It could wrap the existing Preact UI for every platform, but it adds Rust. Wails v3 (beta, API stable, has a system tray) keeps everything in Go for desktop only. Electron is the heaviest. Capacitor wraps the web UI for iOS and gives native push plugins.

**Windows.** Use the browser or a PWA first, then a Tauri or Wails tray app that supervises the server. WinUI is not worth it for a single developer.

**What native clients need from the server:**

- **A versioned API**: `/api/v1`, a `GET /api/version` capability probe, and a `version` field in the bridge `join`.
- **Header auth**: `Authorization: Bearer`. Today auth is a cookie, or `?token=` on WebSocket upgrades only.
- **An Origin rule for clients that send no Origin**: native WebSocket clients do not send `Origin: http://<host>`.
- **Pairing**: a QR code of the URL and token, then a per-device token that can be revoked instead of the one shared token.
- **A push registration endpoint.**

**Sequence.**

1. API versioning, bearer auth, and per-device pairing (needed by everything else).
2. A PWA over Tailscale HTTPS with web push.
3. The `SessionHost` interface refactor, plus a WSL2 guide.
4. The macOS menu-bar app.
5. The SwiftUI iPad and iPhone client with SwiftTerm and the APNs relay, only if the PWA falls short (background push reliability, keyboard handling).
6. The Windows ConPTY host and a Windows tray app. Deferred.

## 4. Open decisions (ADR candidates)

1. Session host abstraction: tmux only, tmux plus ConPTY, or own host everywhere. This revisits ADR 0002 and 0012 ("tmux is the only record of a ship").
2. Windows support level: WSL2 documented, or native.
3. Platform scope in ADR 0005 (macOS only today).
4. API versioning and stability policy for non-browser clients.
5. Auth model: per-device tokens and pairing instead of one startup token (ADR 0001).
6. TLS and remote reach: Tailscale serve, a tunnel, or built-in TLS.
7. Push: web push only, or APNs through a hosted relay. A hosted relay is a first exception to ADR 0001's "nothing is hosted elsewhere".
8. Native client tech: SwiftUI with SwiftTerm, Tauri, or Capacitor.

## Sources

- <https://code.claude.com/docs/en/setup>
- <https://learn.chatgpt.com/docs/windows/windows-sandbox>
- <https://docs.github.com/en/copilot/how-tos/set-up/install-copilot-cli>
- <https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session>
- <https://learn.microsoft.com/en-us/windows/wsl/networking>
- <https://github.com/aymanbagabas/go-pty>
- <https://github.com/UserExistsError/conpty>
- <https://github.com/creack/pty>
- <https://pkg.go.dev/github.com/charmbracelet/x/vt>
- <https://pkg.go.dev/tuios.dev/vt>
- <https://docs.rs/crate/psmux/3.3.6/source/README.md>
- <https://github.com/anthropics/claude-code/issues/34150>
- <https://github.com/migueldeicaza/SwiftTerm>
- <https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/>
- <https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts>
- <https://tailscale.com/kb/1312/serve>
- <https://docs.ntfy.sh/config/>
- <https://developer.apple.com/documentation/servicemanagement/smappservice>
- <https://v2.tauri.app/plugin/websocket/>
- <https://v3alpha.wails.io/whats-new/>
