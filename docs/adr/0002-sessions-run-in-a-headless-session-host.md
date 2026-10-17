# 2. Sessions run in tmux sessions Admiral owns

Status: accepted

## Context

A dashboard can only show and type into a terminal it can attach to. Sessions must keep running in the background, survive the Admiral server restarting, and let several browsers (the Mac and an iPad) attach to the same session. Admiral launches sessions itself (ADR 0003), so it chooses its own session host.

## Decision

Each ship runs in its own tmux session on a tmux socket that belongs to Admiral. The tmux session's name is derived from the ship's task, so Admiral finds its sessions again after a restart. The terminal bridge attaches to the session's pane, streams its output to the browser and sends keystrokes back. A ship is alive while its tmux session and harness process are.

## Considered options

- **Admiral owns a PTY per session.** Needs no tmux, but sessions die with the Admiral server and nothing else can attach to them.
- **tmux on Admiral's own socket** (chosen). Sessions survive Admiral restarts and can also be attached from any terminal (`tmux -L <socket> attach`).

## Consequences

Admiral depends on tmux on the Mac. Using its own socket keeps Admiral's sessions apart from the engineer's own tmux and avoids the quirks of running inside an existing tmux session. Admiral, not tmux, decides what counts as needs-you (ADR 0007) or anchored (ADR 0008).

Revised by ADR 0003: this ADR first proposed a tmux host inside Commodore Launcher, with an Admiral-owned PTY or tmux session as the fallback.
