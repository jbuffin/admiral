# Idle memory and the setup terminal

Idle memory figures and the setup terminal. Read it before changing memory use or the setup terminal.

One fleet, one source, no ships, on an M-series Mac (macOS 27, Go 1.26, tmux 3.7c, python 3.14), resident set size from `ps -o rss`:

| process | RSS |
| --- | --- |
| `admiral` | about 16 MB |
| a Python plugin process (`python3 plugin.py`, measured with the former freeform plugin, a minimal one) | about 19 MB |
| Admiral's tmux server and its control client | about 4 MB and 5 MB |

Each Claude or Codex ship has a transcript follower (`shipconv.Tails`): one small `conversation.Reader`, polled every 250 ms with a stat that skips the read when the file is unchanged. A ship with no readable transcript is looked up again every 2 s, backing off to a minute.

Each ship adds its harness, and a pane in tmux.

## The setup terminal

Tapping the "Set up a fleet" marina or the header's fleet count opens `admiral setup` in a terminal over open water. It runs in the tmux session `_setup` on Admiral's socket (never in `ships`, never a ship) and uses the bridge's terminal, so several browsers can share it and a dropped connection rejoins. `POST /api/setup` starts it or leaves the running one alone; `GET /api/setup` says whether it runs. The bridge accepts `/ws/bridge/_setup` while the session exists. The terminal closes when the session ends, whether setup wrote the file or not, and the page then re-reads `/api/config`; a change to `config.json` alone never closes it.
