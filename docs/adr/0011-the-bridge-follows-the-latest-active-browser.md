# 11. The bridge follows the latest active browser

Status: accepted

## Context

The bridge streams a ship's tmux pane to xterm.js over a WebSocket (ADR 0002). The engineer often has the same ship open on the Mac and an iPad at once, and a pane has only one size. Admiral keeps one tmux control-mode client per watched ship, shared by every browser on that bridge ([Server stack options for a lean local server](https://github.com/jbuffin/admiral/issues/18)), so tmux sees a single client and Admiral must choose the size itself. The server can also restart under `admiral service` or an upgrade while ships keep running in tmux, and every open bridge loses its WebSocket when it does. The setup terminal (ADR 0010) uses the same bridge.

## Decision

Decided in [Terminal bridge: several browsers and reconnects](https://github.com/jbuffin/admiral/issues/35).

- **The latest active browser sets the size.** Opening the bridge, tapping or clicking on it, typing, reloading or resizing makes a browser the active one, and Admiral sizes the pane to it (`refresh-client -C`). Every other browser on that bridge shows the same grid scaled to fit its space, never reflowed. Open Saltpaw on the iPad and the iPad takes over; click the Mac's bridge again and the Mac does.
- **Only a visible bridge counts.** A hidden tab or a locked iPad stops receiving the stream and cannot become active. When it is visible again it catches up as a joining browser does. About 30 s after the last browser leaves a ship, Admiral detaches that ship's control client; the pane keeps its last size.
- **A joining browser gets the current screen and the last 2,000 lines of scrollback** with their colors (`capture-pane -e`), then the live stream. Admiral's sessions set `history-limit` to 10,000, so older output is still reachable with `tmux attach`.
- **No input lock and no presence.** Keystrokes from every browser go straight to the pane, as with two `tmux attach` clients.
- **A ship that needs you is answered in its terminal.** The bridge has the prompt and key buttons (Enter, Esc, Tab, ↑, ↓, ^C) but no Approve or Deny buttons: harness prompts are menus (Claude Code's trust prompt ignores a bare `y`, and with no prompt up a `y` is typed as text), so a single key can't answer them safely.
- **When the server goes away**, an open bridge keeps its last screen, dims and shows "Reconnecting…". The prompt and key buttons are disabled and nothing typed is queued: a buffered `y` replayed later could approve something the engineer never saw. The browser retries after 1 s, doubling to a 10 s cap, until the server is back, then rejoins as a joining browser and replaces the screen. A ship that anchored meanwhile reopens read-only (ADR 0008); a ship that is gone closes the bridge. The rest of the dashboard reconnects the same way, behind one header banner.
- **The LAN token survives restarts**, so device cookies keep working after an upgrade. `admiral token rotate` replaces it and signs every device out. A 401 on reconnect stops retrying and shows "Open the URL Admiral printed".
- **The setup terminal follows the same rules.** If setup finished while the server was down, the reconnecting browser closes the terminal and re-reads the config.

## Considered options

- **Smallest browser wins** (tmux's old default). A forgotten iPad tab would keep the Mac's terminal cramped.
- **Largest browser wins.** The iPad would scroll sideways or scale down to unreadable.
- **One fixed size.** Wastes the Mac's screen and overflows the iPad's.
- **Latest active** (chosen, and tmux's own default since 3.1). The device in the engineer's hands always gets a full-size terminal.
- **Queueing input while disconnected.** Rejected: keystrokes to an agent are not safe to replay against a screen the engineer can't see.

## Consequences

- An onlooking browser can show a small, scaled terminal until it is touched.
- One control client per watched ship, not per browser, keeps memory flat as devices are added.
- Where the token is stored is decided with the rest of Admiral's state ([What survives a restart and where it lives](https://github.com/jbuffin/admiral/issues/36)).

Amended 2026-10-18 (MVP build, [spec #45](https://github.com/jbuffin/admiral/issues/45)): no Approve or Deny buttons; story 106 changed to match. A real Claude Code run showed its trust prompt ignores a bare `y` and that a `y` sent with no prompt up lands in the input as text.
