# Server stack options for a lean local server

Research for [#18](https://github.com/jbuffin/admiral/issues/18), feeding the back-end stack decision in [#19](https://github.com/jbuffin/admiral/issues/19). This note compares stacks and does not pick one.

Admiral's server controls tmux on its own socket (ADR 0002), bridges tmux panes to browsers over a WebSocket, serves a static web UI, watches a config file and keeps a little state. The standing preference is that it be very light on memory and shippable to other people as a CLI, a service or a Mac app.

Researched 2026-10-17 on macOS 27.0.1 (arm64) with Go 1.26.5, Rust 1.99.0, Bun 1.4.3, Node 22.22.0 and tmux 3.7c.

## Answer in one table

| | Rust | Go | Bun | Node |
| --- | --- | --- | --- | --- |
| Idle RSS, HTTP + WebSocket server (measured) | **2.6 MB** | 11.3 MB | 12.1 MB | 69.7 MB |
| RSS with 50 streaming WebSockets (measured) | 9.9 MB | 17.2 MB | 17.0 MB | 72.0 MB |
| Release binary (measured) | **0.97 MB** | 5.6 MB | 70.9 MB | about the size of `node` itself (SEA copies the binary) |
| WebSocket | axum `ws` / tokio-tungstenite | coder/websocket (zero deps) or gorilla | built into `Bun.serve` (uWebSockets) | `ws` package |
| PTY, if needed | portable-pty (from WezTerm) | creack/pty | built in: `Bun.spawn({ terminal })` since 1.3.5 | node-pty (native addon, compiled) |
| tmux control | write a control-mode parser (`tmux_interface` crate has it unimplemented) | write a control-mode parser | write a control-mode parser | write a control-mode parser |
| Config watching | `notify` (FSEvents by default) | fsnotify (kqueue on macOS) | `fs.watch` | `fs.watch` |
| Single-file CLI | yes, native | yes, native | `bun build --compile` | SEA, "Stability 1.1, active development" |
| Signing a shared binary | Developer ID + hardened runtime | same | same, plus a JIT entitlement | same, plus JIT entitlements |
| Menu-bar app in the same language | `tray-icon` (Tauri) or Tauri | `fyne-io/systray` (needs cgo) or Wails | none native; Electron or a Tauri/Swift shell with the server as sidecar | same as Bun |
| Order-source plugins, most natural form | subprocess or WASM (Extism, wasmtime); no stable in-process ABI | subprocess (hashicorp/go-plugin) or WASM (Extism on wazero); `plugin` package is fragile | in-process `import()` of JS/TS; subprocess; WASM via the built-in `WebAssembly` | same as Bun |

Bold marks a clear lead. Every stack can do every job; the differences are memory, binary size, how much is built in, and which plugin model feels native.

## Memory

### What was measured

The same minimal server in each stack: serve `/`, upgrade `/ws`, push 1 KB to each socket every 50 ms (a busy terminal) and echo what the client sends. Clients were 10, then 50, WebSockets each sending a keystroke a second. RSS from `ps`, and physical footprint from macOS `footprint`, about 10 seconds after each step. Sources are in the appendix.

| Stack | Idle RSS / footprint | 10 sockets | 50 sockets |
| --- | --- | --- | --- |
| Rust (axum 0.8, tokio current-thread, release, LTO) | 2.6 MB / 1.5 MB | 4.1 MB / 3.0 MB | 9.9 MB / 8.8 MB |
| Go (net/http + coder/websocket 1.8.15, `-s -w`) | 11.3 MB / 5.0 MB | 13.2 MB / 6.8 MB | 17.2 MB / 10 MB |
| Bun 1.4.3 (`bun build --compile`) | 12.1 MB / 4.9 MB | 15.1 MB / 5.1 MB | 17.0 MB / 5.2 MB |
| Node 22 (`node:http` + `ws`) | 69.7 MB / 32 MB | 70.6 MB / 32 MB | 72.0 MB / 31 MB |

Reading it:

- Rust is the lightest by a wide margin at idle, which is where Admiral spends most of its life.
- Go and Bun idle at about the same RSS. Go grows by about 0.1 to 0.15 MB per socket here; Bun grows less per socket in this test, but a garbage-collected JS heap grows with real application code (state, plugin code, JSON), which a 20-line benchmark does not exercise. Expect a real Bun or Go server to sit in the tens of MB, not the low teens.
- Node starts near 70 MB RSS before any application code.
- These are short, synthetic runs on one machine. They rank the stacks; they do not predict Admiral's final footprint.

### Per attached terminal: tmux dominates, not the language

How the bridge attaches to tmux matters more than the language:

| tmux piece (measured, tmux 3.7c) | RSS |
| --- | --- |
| tmux server, 1 session | 4.2 MB |
| tmux server, 10 sessions | 4.3 MB |
| one control-mode client (`tmux -C attach`) | 5.0 MB |
| one normal client (`tmux attach`) | 5.4 MB, plus a PTY the server must own |

Two ways to bridge a pane:

1. **A `tmux attach` client per browser terminal, inside a PTY the server opens.** Simple: bytes in, bytes out. Costs a tmux client process (about 5 MB) per open bridge per browser, plus a PTY library in the server.
2. **tmux control mode.** The server runs `tmux -C attach` (no PTY needed) and reads `%output %<pane> <data>` lines, where bytes below ASCII 32 and backslash are octal-escaped; it sends keys with `send-keys` (`-H` for hex) and sizes the client with `refresh-client -C`. Flow control is `refresh-client -f pause-after=N`, which switches to `%extended-output` and `%pause` / `%continue` notifications ([tmux wiki, Control Mode](https://github.com/tmux/tmux/wiki/Control-Mode)). iTerm2 is built on it. Tested here: a control client sees output only for panes in the session it is attached to, so it is one control client per ship being watched (ADR 0002 gives each ship its own session), shared by every browser watching that ship.

Control mode needs the server to parse a small line protocol in any stack, and makes the "several browsers, who wins on resize" question explicit through `refresh-client -C`. No stack has a maintained control-mode parser: Rust's `tmux_interface` 0.4 lists its control-mode module as "unimplemented, draft" ([docs.rs](https://docs.rs/tmux_interface/latest/tmux_interface/)). It is a few hundred lines in any language.

For scale: each ship's harness process (Claude Code, Codex CLI, Copilot CLI; not measured here) is typically far larger than either the server or tmux, so the server's share of Admiral's total memory is small whichever stack wins. The preference for a light server still holds for the idle case, when no ships run.

## Libraries for each job

### Rust

- HTTP and WebSocket: axum with the `ws` feature on tokio (used in the benchmark), or tokio-tungstenite directly. A `current_thread` runtime keeps it to one thread.
- PTY: `portable-pty` 0.9, from the WezTerm project: "a cross platform API for working with the psuedo terminal (pty) interfaces" ([docs.rs](https://docs.rs/portable-pty/latest/portable_pty/)).
- tmux: no maintained control-mode client; `tmux_interface` wraps the CLI and is pre-1.0 ([docs.rs](https://docs.rs/tmux_interface/latest/tmux_interface/)).
- File watching: `notify` uses FSEvents on macOS by default and warns that editors save in different ways (truncate vs replace) ([docs.rs](https://docs.rs/notify/latest/notify/)).
- Static UI: embed with a crate such as `rust-embed` or `include_dir`, so the binary carries the UI.
- Cost: slowest to write and compile; async Rust plus a protocol parser is the most code of the four.

### Go

- HTTP: the standard library. WebSocket: `coder/websocket`, "Minimal and idiomatic", "Zero dependencies", `context.Context` throughout, full permessage-deflate ([GitHub](https://github.com/coder/websocket)); or gorilla/websocket, "mature and widely used".
- PTY: `creack/pty`, "a Go package for using unix pseudo-terminals" ([GitHub](https://github.com/creack/pty)).
- tmux: `os/exec` plus a control-mode parser; goroutines suit one reader per control client.
- File watching: `fsnotify` uses kqueue on macOS and says to watch the parent directory, not the file, because "many programs (especially editors) update files atomically" ([pkg.go.dev](https://pkg.go.dev/github.com/fsnotify/fsnotify)).
- Static UI: `embed` in the standard library.
- Cost: least friction of the compiled options; most of the server is standard library.

### Bun

- HTTP and WebSocket: built into `Bun.serve`, "built on uWebSockets", with backpressure (`send()` returns -1, then a `drain` handler; `pause()`/`resume()`) and native topic pub/sub, which fits fanning one ship's output to several browsers ([Bun docs](https://bun.com/docs/api/websockets)).
- PTY: built in. `Bun.spawn` takes a `terminal` option that attaches a PTY ([Bun docs](https://bun.com/docs/api/spawn)); added in Bun 1.3.5, Windows (ConPTY) in 1.3.14, and the Bun 1.4 post says it drives terminal programs "without node-pty" ([Bun 1.3.5](https://bun.com/blog/bun-v1.3.5), [Bun 1.4](https://bun.com/blog/bun-v1.4)). Bun 1.4.3 fixed a leak of the PTY's file descriptors after a child exits on macOS and Linux, a 1.4.0 regression ([Bun 1.4.3](https://bun.com/blog/release-notes/bun-v1.4.3)): young code.
- tmux: `Bun.spawn` plus a control-mode parser.
- File watching: `fs.watch`. Static UI: embedded by `bun build --compile`.
- Cost: least code; TypeScript shared with the UI; the binary is about 71 MB because it carries the runtime, and the Bun docs say "Bun's binary is still way too big" ([Bun docs](https://bun.com/docs/bundler/executables)).

### Node

- WebSocket: `ws`. PTY: `node-pty`, `forkpty(3)` bindings that compile C++ on install ([GitHub](https://github.com/microsoft/node-pty)), which complicates a single-file binary.
- Single executable applications are "Stability: 1.1 - Active development", with the assets VFS at "1.0 - Early development" ([Node docs](https://nodejs.org/api/single-executable-applications.html)).
- Node has Bun's development story with five to six times the idle memory and a harder PTY. It is in this note for completeness; nothing here favors it over Bun.

## Shipping to other people

### What Apple requires

- Developer ID software distributed outside the Mac App Store must be notarized: "Beginning in macOS 10.15, all software built after June 1, 2019, and distributed with Developer ID must be notarized." Notarization needs a Developer ID certificate, the hardened runtime "for your app and command line targets", and a secure timestamp ([Apple](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)).
- A bare binary can be notarized (zip it and submit) but not stapled: "Although tickets are created for standalone binaries, it's not currently possible to staple tickets to them." Gatekeeper then checks the ticket online. Apps, disk images and flat installer packages can be stapled ([Apple](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)).
- A paid Apple Developer Program membership (US$99 a year) is needed; a free account can sign for testing but cannot notarize ([Tauri macOS signing](https://v2.tauri.app/distribute/sign/macos/)).
- Gatekeeper only checks files carrying the quarantine attribute. Checked here: a file fetched with `curl`, and Homebrew's `tmux`, have `com.apple.provenance` but no `com.apple.quarantine`, and Homebrew's `tmux` is only ad-hoc signed. All three toolchains (Go, Rust, `bun build --compile`) ad-hoc sign their output by default (`flags=0x20002(adhoc,linker-signed)`), which arm64 Macs require to run anything.

### CLI

| | Without an Apple Developer account | Signed and notarized |
| --- | --- | --- |
| How others install | Homebrew formula in a tap (built from source or a bottle), `go install`, `cargo install`, or `curl \| sh`. None of these set quarantine, so the ad-hoc signature is enough. | Download from a browser (quarantined) works without a warning. |
| Rust, Go | Works as is. | `codesign --options runtime --timestamp`, zip, `notarytool submit`. No entitlements. |
| Bun | Works as is. | Same, plus an entitlements file: the Bun docs recommend `com.apple.security.cs.allow-jit` (and list `allow-unsigned-executable-memory`, `disable-library-validation`); codesign support needs Bun 1.2.4+ ([Bun docs](https://bun.com/docs/bundler/executables)). |
| Node SEA | Works; must `codesign --remove-signature` before injection and re-sign after ([Node docs](https://nodejs.org/api/single-executable-applications.html)). | JIT entitlements as for Bun. |

A Homebrew formula is the low-cost path for sharing a CLI in every stack. Casks are different: Homebrew 5.0.0 deprecated unsigned casks and said "We will disable all Homebrew/homebrew-cask casks that fail Gatekeeper checks in September 2026" ([Homebrew 5.0.0](https://brew.sh/2025/11/12/homebrew-5.0.0/)). That policy covers the official cask repository; the post says nothing about formulae.

### launchd service

Language-neutral: a `~/Library/LaunchAgents/<label>.plist` pointing at the binary, which the CLI can write and `launchctl bootstrap` itself (an `admiral service install` subcommand). No extra signing beyond the CLI's. A LaunchAgent, not a LaunchDaemon, because sessions must run as the engineer with their tmux and harness config.

### Menu-bar Mac app that runs the server

- Any app downloaded by a browser is quarantined, so sharing a Mac app in practice means Developer ID signing and notarization: the cost ADR 0001 rejected a native app over. The difference now is that the app would only be a launcher around the same server, and the CLI path stays available without it.
- macOS 13+ `SMAppService` registers a LaunchAgent bundled inside the app (`Contents/Library/LaunchAgents/`) and replaces installing plists in `~/Library/LaunchAgents` ([Apple](https://developer.apple.com/documentation/servicemanagement/smappservice)). The app can therefore run the server binary as a helper agent that keeps running when the menu-bar app quits.
- Options by stack:
  - **Rust:** `tray-icon` (tauri-apps) needs an event loop on the main thread ([docs.rs](https://docs.rs/tray-icon/latest/tray_icon/)); or a Tauri app, which signs and notarizes during the build from `APPLE_*` environment variables ([Tauri](https://v2.tauri.app/distribute/sign/macos/)). The server can live in the same process.
  - **Go:** `fyne-io/systray` "requires cgo" and on macOS needs an `.app` bundle with `LSUIElement` to stay out of the Dock ([GitHub](https://github.com/fyne-io/systray)). Same process is possible.
  - **Bun / Node:** no native tray. Use Electron (heavy, against the memory preference), or a Tauri app running the compiled server as a sidecar binary (`bundle.externalBin`, `Command.sidecar(...)`) ([Tauri](https://v2.tauri.app/learn/sidecar-nodejs/)), or a small Swift menu-bar app.
  - **Any stack:** a small Swift menu-bar app that registers the server binary with `SMAppService`. This decouples the Mac app from the server language entirely, at the cost of a second, tiny codebase.

## Order-source plugins

Plugins are a stated MVP need (map #17), with the freeform list as the first one. Each loading model, by stack:

| Model | Rust | Go | Bun / Node |
| --- | --- | --- | --- |
| In-process, native code | Rust has no stable ABI, so dylib plugins must be built with the same compiler or go through a C ABI | `plugin` package: "supported only on Linux, FreeBSD, and macOS", needs "exactly the same version of the toolchain, the same build tags", "poorly supported by the Go race detector" ([pkg.go.dev](https://pkg.go.dev/plugin)) | n/a |
| In-process, script | embed a JS or Lua engine (extra weight) | embed a JS or Lua engine (goja, gopher-lua) | native: `import()` a `.ts`/`.js` file. No isolation: a plugin can read the engineer's files and the bridge. |
| Subprocess | spawn and speak JSON over stdio | `hashicorp/go-plugin`: subprocess over net/rpc or gRPC, "Plugins can't crash your host process", any language via gRPC, used by Terraform, Vault, Nomad ([GitHub](https://github.com/hashicorp/go-plugin)) | spawn and speak JSON over stdio; `Bun.spawn` has IPC between Bun processes |
| WASM | Extism host SDK, or wasmtime | Extism Go SDK runs on wazero ([GitHub](https://github.com/extism/go-sdk)) | Extism JS SDK, or the built-in `WebAssembly` |

Extism host SDKs exist for Rust, JS, Go and others, and plugins can be written in Rust, JS, Python, Go, AssemblyScript, C, Zig and more ([GitHub](https://github.com/extism/extism)). Apple's note on hardened-runtime hosts applies to whichever is chosen: plug-ins "inherit the entitlements of the host process", so a notarized host that runs a JavaScript engine needs the JIT entitlement ([Apple](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)).

A subprocess speaking JSON over stdio is the one model every stack supports equally, lets a plugin be written in any language, and isolates crashes. An order source (fetch a list, refresh) is low-frequency, so subprocess overhead does not matter. Bun's in-process `import()` is the easiest to write against but gives up isolation.

## Trade-offs for the back-end stack decision (#19)

- **Rust**: lightest by far (2.6 MB idle, 1 MB binary), and a natural path to a menu-bar app (tray-icon, Tauri) in one language. Most code and slowest iteration; no stable in-process plugin ABI, so plugins are subprocess or WASM.
- **Go**: about 11 MB idle, 6 MB binary; standard library covers HTTP, embedding and process control; best subprocess-plugin library (go-plugin) and pure-Go WASM (wazero). Tray needs cgo; native `plugin` is not viable for third parties.
- **Bun**: about the same idle memory as Go in this test, but a 71 MB binary and a JS heap that grows with real code; built-in WebSocket pub/sub and PTY mean the least code, TypeScript shared with the UI, and in-process plugins for free. Its PTY API is young (a 1.4.0 fd leak fixed in 1.4.3), and a signed build needs the JIT entitlement. A menu-bar app needs a second technology.
- **Node**: Bun's model at five to six times the idle memory, with a compiled native PTY addon. Dominated by Bun for this server.
- **Independent of stack**: tmux control mode avoids a PTY and a tmux client per browser; a Homebrew formula ships a CLI with no Apple account; a menu-bar app means Developer ID and notarization in every stack; a Swift menu-bar shell plus `SMAppService` works with any server binary.

## Appendix: benchmark sources

Run from a scratch directory, not committed. Each server listened on 127.0.0.1, served `/` and upgraded `/ws`.

- Go: `net/http` with `github.com/coder/websocket` v1.8.15, a 50 ms ticker writing 1 KB per socket, echo of reads. Built with `go build -ldflags="-s -w"`.
- Rust: axum 0.8.9 (`ws`), tokio `current_thread`, `tokio::select!` over a 50 ms interval and `recv()`. `cargo build --release` with `strip = true`, `lto = true`.
- Bun: `Bun.serve` with a `websocket` handler and a `setInterval` per socket. `bun build --compile`.
- Node: `node:http` with `ws` and a `setInterval` per socket.
- Client: Node `ws`, 10 sockets then 40 more, each sending one byte a second.
- tmux: `tmux -L <socket> new-session -d` for 1 and 10 sessions; `tmux -C attach` with stdin held open; `script -q /dev/null tmux attach` for a normal client in a PTY. The cross-session test attached a control client to session `a`, echoed in sessions `a` and `b`, and saw `%output` only for `a`.
