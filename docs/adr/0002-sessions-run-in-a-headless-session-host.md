# 2. Sessions Admiral launches run in a headless session host

Status: proposed

## Context

Commodore Launcher runs a harness as a child of `commodore` in the terminal it was typed in, and knows whether it runs by the task's session lock (launcher ADR 0022, `docs/session-host.md`). A dashboard cannot attach to a terminal it does not own. The launcher's `SessionHost` interface (`running`, `claim`, `run`) was made as the seam for a second host, and names tmux as the first candidate: it keeps detached sessions and lets any terminal attach.

## Decision (proposed)

Sessions Admiral launches run in a headless host that Admiral can attach to, keeping the launcher's session lock so liveness means the same thing for every host. Preferred: add a tmux `SessionHost` to Commodore Launcher (for example `commodore open <url> --host tmux`) and have Admiral attach to the tmux session it names. Fallback if that is not wanted in the launcher: Admiral runs `commodore open <url>` inside a PTY or tmux session it owns.

## Considered options

- **Admiral owns the PTY.** Works without launcher changes, but sessions die with the Admiral server and cannot be attached from another terminal.
- **tmux host in the launcher.** Sessions survive Admiral restarts, can also be attached from any terminal (`tmux attach`), and keep one implementation of liveness. Needs a launcher change.

## Consequences

Admiral depends on tmux on the Mac. The tmux session name must be derivable from the task so Admiral can find it after a restart. ADR 0022 notes tmux behaves differently when the engineer is already inside tmux; the host should use its own socket.
