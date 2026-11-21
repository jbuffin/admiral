# The old web UI

The frozen Preact UI under `web/src/` other than `web/src/app/`. Read it before touching old-UI code.

- `web/`: the Preact UI. `web/embed.go` embeds the built `web/dist` into the binary.
  - The phone layout: `web/src/sizeClass.ts` is the one place that decides the size class.
    - `phone` (`(max-width: 760px), (max-height: 499px)`, so a phone turned sideways too) and `wide` (1024px and up in landscape, and not phone) are signals, and a `phone` class on the root is set from them; phone CSS keys off `:root.phone`, never its own media query.
    - On a phone `App.tsx` renders `PhoneShell.tsx` instead of open water and the marinas: the phone header (the mark, "N at sea", the Settings gear), one tab body (Inbox, Chat, Ships, Orders) and a bottom tab bar that hides while a bridge is open.
    - Opening or closing a bridge never changes the tab, so Back returns to where it was opened.
    - The Inbox tab is `InboxPanel.tsx`'s `Sheet` in place (a region, no Close).
    - The Ships tab lists `sortShips` (`shipList.ts`, pure): needs-you ships first by `stop_since`, then the rest newest first, sailing-off ships left out.
    - The Orders tab is the Orders sidebar expanded, with no Collapse, no grips and no dragging.
    - The bridge is full screen with a phone top bar (Back, flag, name, status, harness · order).
    - Tapping an order (`.order-hit`, a button over the row) opens `DockSheet.tsx`, a bottom sheet with a button per dock of every fleet (the marinas' `marks` and `restingStatus`); it is `launch.ts`'s `dockSheet`, not `selected`, so no dock arms.
    - A dock opens Launch orders, which on a phone is a top-aligned scrolling card.
    - Dismissing either returns focus to the order's button (`returnFocus`).
    - After a launch the app shows Ships, focuses the new ship's row and highlights it for 2.6 s (`newShip`).
  - Crew boats on the phone Ships list: each ship's `crew[]` shows as indented buttons under its row (`CrewRow` in `PhoneShell.tsx`, text from `crewRowText`, named `Board <crew>, teammate, crew of <ship>[, needs you|, anchored]`), and a tap runs `board(session, crew)`.
    - A ship is in the needs-you group (`asksYou` in `shipList.ts`) when its status is `needs_you` or any crew boat's is: an anchored ship stays `anchored` on the wire, so its boat is the only sign.
    - The Ships tab badge and hint (`askingCount`), the row's asking style and the sort (by `stop_since`, else the earliest needing boat's `last_activity ?? launched`; an unparseable stamp sorts last) all use that set.
    - A ship that needs you only through its crew reads `Needs you · <boats> · waiting <age>` (`shipStatusLine`).
    - Its subagents show as a `role="note"` block under it (not a button, no terminal).
    - The phone bar of a boarded crew boat has the same ⋯ menu as the wide head (`crewMenu` in `Bridge.tsx`), so Close crew boat works on a phone.
  - The inbox UI: `web/src/inbox.ts` is the pure queue (served items and needs-you stops merged oldest first, the cursor, the 5 s hold after an action), `inboxState.ts` wires it to the signals, the `inbox` WebSocket message, the clock and the answer request, and `InboxPanel.tsx` draws the header button, the wide layout's drawer (a flex child of `.harbor`; the bridge ends at `main`'s right edge beside it, and Orders collapses while it is open without changing the stored state) and the narrow layout's modal sheet.
    - Item files are untrusted: item prose (a question, a review summary, a notice body, and a finding's quote, reason and reply) renders through `renderMarkdown` (no raw HTML, safe links only); titles and every other field render as text, linking only what `safeHref` allows.
    - Review items (`ReviewBody`) and findings items (`FindingsBody`) share the action path in `inboxState.ts` (`act`): the badge drops at the tap and returns on a refusal, which shows inline and keeps the choices.
    - `findings.ts` is the pure decision state of a findings item: Save makes a card `edited`, Approve after a Save keeps it, Reject drops it. A PR link must be `https:` (`prLink`).
    - "Remove from inbox" on a review sends `discard {"forget":true}` (`FORGET_SUPPORTED` in `inboxState.ts` hides it when false).
  - Boarding a crew boat: `board(session, crew?)` and the `boarded` signal (`{ session, crew? }`, `ships.ts`) are the one way in (the sea and the inbox call it too); `window.__admiral.boarded` records `<session>/<crew>`. `Bridge.tsx` keys the view by ship and passes the crew name to `useBridgeTerminal`, which reconnects to `/ws/bridge/<session>/crew/<name>` (`bridgePath` in `crew.ts`). Typing, key buttons, pill and footer follow the boarded boat's status (`takesInput`), not the ship's. A crew link's reconnect probe asks whether the boat is still in the ship's `crew[]`, and a boat gone from a `ships` push sends the bridge back to the ship.
  - Crew boats and subagents on the sea: `.ship-slot` in `OpenWater.tsx` is a positioned `<div>` that owns the drag (`press`); the ship button, one button per rafted crew boat (up to three, `crewRaft` spots), the `+N crew` pill and the deck button are its siblings, and `press` ignores presses that start on the last three, so a tap on a boat boards it and never drags the ship.
    - `crewSea.ts` holds the pure rules (plaque `<crew> needs you`, the raft and pill, `shouldChime` for ships and crew boats, the deck tip and label); `CrewBoat.tsx` draws the boat for the sea and the flotilla.
    - Every new ship keeps `REACH` free on its left in `placeShip`, so its crew fits; ships already placed don't move.
    - A crew boat that joins after the first push pops (`popping` in `ships.ts`).
