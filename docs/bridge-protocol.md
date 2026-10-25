# The bridge protocol

The bridge is the terminal a browser opens on a ship. Each ship has its own WebSocket, `GET /ws/bridge/<the ship's tmux session name>`, behind the same token and Origin checks as `/ws`. The bridge also accepts `_setup` (the setup terminal) and `_chat` (the AdmiralBot chat, ADR 0019) while those sessions exist, each as a stand-in ship that is underway and has no needs-you state to clear. A session that is none of these (gone, a strict prefix of a live one, `_admiral`) is refused at the handshake with 404.

Each crew boat (a teammate, an entry of the ship's `crew`) has a WebSocket too, `GET /ws/bridge/<session>/crew/<name>`, behind the same checks. The handshake answers 404 when `<session>` is not a current ship (`_setup` and `_chat` have no crew) or the ship has no crew boat named exactly `<name>`. The name never reaches tmux: the bridge looks the boat up by name at the handshake and from then on holds its window and pane ids, so a boat relaunched under the same name is a new boat. The messages are the ship bridge's, message for message; the rest of this page says where they differ on a crew boat. Subagents have no pane and no bridge.

- **One control client per session.** Admiral attaches one tmux control client per watched ship, shared by the ship's bridge and the bridge on each of its crew boats, and by every browser on them. It starts with the first browser on any of them and detaches 30 s after the last one leaves the ship and all its crew boats (`ADMIRAL_BRIDGE_IDLE` sets another Go duration for tests). Each watch takes only the output of its own pane (filtered by pane id), and the client pins every window to the size it has when the client attaches, so attaching restores each window's size at once. A window opened later keeps the client's 80x24 until a browser on its bridge sizes it.
- **Pane and window.** The ship's bridge always targets the ship's own pane and window (`=<session>:^`), never the session's current window, which moves when anyone attaches to or selects a crew window. A crew boat's bridge targets the boat's pane and window ids. Every tmux command the bridge sends targets `=<session>:`, `=<session>:^`, or a pane or window id, never a bare name.
- **Sizing is per window** (`refresh-client -C @<window>:CxR`). The latest active browser on a bridge sizes that bridge's window alone: a browser on the ship never resizes a crew window, and the reverse. The pane keeps its last size after the detach.
- **Gates follow the boat.** On a crew boat's bridge, input, resize and `refused` follow the crew boat's status, never the ship's: an anchored ship keeps its live crew boats, and they stay typeable and sizable. Input to an anchored crew boat is refused with its status as the reason. Input on a crew boat's bridge clears that boat's needs you (it writes `@admiral-state` `working` on its pane by pane id) and leaves the ship's alone; input on the ship's bridge clears only the ship's.
- **The `ship` object** on a crew boat's bridge (in `join` and the `ship` message) describes the crew boat: `status` (`underway`, `needs_you` or `anchored`), and `exit_code` or `exit_signal` once it is anchored. The field keeps the name `ship`, so a client reads both bridges the same way.
- **It closes with the boat.** When the crew boat's window is gone (the close route, `admiral crew close`, the harness's window closed by hand, or the ship ending), the bridge on it closes at once (on tmux's `%window-close`, so a boat closed and relaunched under the same name inside one poll still ends the old bridge) with a normal close; the ship's bridge and the other crew boats' stay open.

Browser to server, JSON text frames:

| message | meaning |
| --- | --- |
| `{"type":"join","cols":C,"rows":R}` | First message. The browser's size; it becomes the active browser. |
| `{"type":"resize","cols":C,"rows":R}` | A new size. The browser becomes active and the pane takes its size. |
| `{"type":"active"}` | The browser was tapped, clicked or typed in. It sets the pane to the size it last reported. |
| `{"type":"visible","visible":false}` | The tab is hidden: it stops receiving output and can't become active. `true` makes it rejoin like a joining browser. |
| `{"type":"input","data":"..."}` | Bytes for the pane, exactly as given. Each input clears needs you first, then goes out as `send-keys -H`. |

Server to browser:

| message | meaning |
| --- | --- |
| `{"type":"join","screen","scrollback","cursor":{"x","y","visible"},"modes":{"alternate","application_cursor","bracketed_paste","mouse"},"size":{"cols","rows"},"ship":{"status","exit_code","exit_signal"}}` | Sent after `join`, and again after `visible: true`. `screen` and `scrollback` (at most 2,000 lines) come from `capture-pane -p -e`, with SGR colours, lines joined by `\n`. A pane in the alternate screen, or with no history yet, has an empty `scrollback`. Output frames follow with nothing missing or repeated. |
| binary frame | The pane's output bytes, decoded from `%output`. Write them to xterm.js as bytes: tmux can split a UTF-8 character across two frames. |
| `{"type":"size","cols","rows"}` | The pane's size changed. The browsers that didn't set it resize xterm.js to this grid and scale it, never reflowing. |
| `{"type":"ship","status","exit_code","exit_signal"}` | The ship's status changed (the crew boat's, on a crew boat's bridge). (Not `status`: that is the ship's own field.) |
| `{"type":"refused","reason":"anchored"}` | Input to a ship, or a crew boat, that has exited. The reason is its status. |

The page itself takes the ship's status from the main `/ws` socket; the `ship` and `refused` messages are for other clients of the bridge.

The first line of an order's title rides the ship as `title` (the eleventh session option, `@admiral-title`, set at launch with control characters, bidi controls and line separators dropped). A dock launch takes the first line of its text instead; a launch with an order ID but no source has none.

The order's web URL rides the ship as `url` (the twelfth session option, `@admiral-url`, taken at launch from the source's order (sources keep only http and https URLs); empty for a launch with no source, an order without one, or a ship launched before this option existed).

When the server goes away an open bridge keeps its last screen, dims, says "Reconnecting…", disables input and queues nothing. The page's own socket and the bridge retry after 1 s, doubling to 10 s, and the bridge rejoins as a new browser. Browsers don't expose why a WebSocket handshake failed, so each failed reconnect asks `GET /api/ships`: a 401 stops every retry and shows "Open the URL Admiral printed"; a ship missing from the list closes the bridge.

The terminal is xterm.js (MIT) with the fit and WebGL addons, loaded when the first bridge opens.
