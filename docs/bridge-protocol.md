# The bridge protocol

The bridge is the terminal a browser opens on a ship. Each ship has its own WebSocket, `GET /ws/bridge/<the ship's tmux session name>`, behind the same token and Origin checks as `/ws`. A session that is not a current ship (gone, a strict prefix of a live one, `_admiral`) is refused at the handshake with 404. Admiral attaches one tmux control client per watched ship, shared by every browser on it. It starts with the first browser, and detaches 30 s after the last one leaves (`ADMIRAL_BRIDGE_IDLE` sets another Go duration for tests); the pane keeps its last size. Every tmux command the bridge sends targets `=<session>:` or a pane id, never a bare name.

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
| `{"type":"ship","status","exit_code","exit_signal"}` | The ship's status changed. (Not `status`: that is the ship's own field.) |
| `{"type":"refused","reason":"anchored"}` | Input to a ship that has exited. The reason is the ship's status. |

The page itself takes the ship's status from the main `/ws` socket; the `ship` and `refused` messages are for other clients of the bridge.

An order's first line rides the ship as `title` (the eleventh session option, `@admiral-title`, set at launch with control characters, bidi controls and line separators dropped; empty for a launch with no source).

The order's web URL rides the ship as `url` (the twelfth session option, `@admiral-url`, taken from the source's order at launch, which keeps only http and https; empty for a launch with no source, an order without one, or a ship launched before this option existed).

When the server goes away an open bridge keeps its last screen, dims, says "Reconnecting…", disables input and queues nothing. The page's own socket and the bridge retry after 1 s, doubling to 10 s, and the bridge rejoins as a new browser. Browsers don't expose why a WebSocket handshake failed, so each failed reconnect asks `GET /api/ships`: a 401 stops every retry and shows "Open the URL Admiral printed"; a ship missing from the list closes the bridge.

The terminal is xterm.js (MIT) with the fit and WebGL addons, loaded when the first bridge opens.
