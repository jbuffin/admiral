# 4. The server is written in Go and shipped as a CLI

Status: accepted

## Context

The Admiral server runs all day on the engineer's Mac, so it must be very light on memory, and other people must be able to install it. Most of its work is glue: serving the UI over HTTP and WebSocket, starting harnesses in tmux, reading config and running order-source plugins. Measurements of a minimal HTTP + WebSocket server in each candidate stack (see [Server stack options for a lean local server](https://github.com/jbuffin/admiral/issues/18)) put idle memory at about 3 MB for Rust, 11 MB for Go, 12 MB for Bun and 70 MB for Node. Talking to tmux in control mode (`tmux -C`) means no stack needs a PTY, and a `tmux attach` client per browser would have cost about 5 MB on its own, so the gap between Rust and Go is small next to the rest of the system. No stack has a maintained tmux control-mode parser.

## Decision

The server is written in Go. It ships as one `admiral` binary for macOS on Apple Silicon, with the built UI embedded, and talks to Admiral's tmux socket in control mode through a parser of its own.

It installs from a Homebrew tap or a `curl` install script. `admiral service install` writes a LaunchAgent so the server runs at login. The binary is ad-hoc signed, and neither install path is quarantined, so the MVP needs no Developer ID or notarization.

## Considered options

- **Rust.** The lightest (about 3 MB idle, 1 MB binary) and could build a menu-bar app in the same language, but needs the most code and iterates slowest.
- **Go** (chosen). About 11 MB idle and a 5.6 MB binary. The standard library covers HTTP, embedding the UI and running processes.
- **Bun.** Least code and shares TypeScript with the UI, but a 71 MB binary, a young PTY API, a JIT entitlement when signed, and a second technology for any menu-bar app.
- **Node.** Bun's model with five to six times the memory.

## Consequences

- The MVP targets macOS on Apple Silicon only. Linux is not a target but stays possible: launchd lives behind the `service` command, and the server core uses no macOS-only APIs.
- A menu-bar Mac app comes after the MVP, as a small Swift shell that registers the same binary with `SMAppService`. That is when a Developer ID and notarization are needed.
- The UI's build output is embedded in the binary, whatever the front-end stack.
