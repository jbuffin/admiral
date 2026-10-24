# Admiral designs

The design reference for building Admiral. Start with the prototype, then read the [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45).

| Path | What |
| --- | --- |
| `prototype/index.html` | The clickable prototype. Open it in a browser (no server needed; React is vendored in `prototype/vendor/`). It is the reference for look, layout and motion. |
| `screenshots/` | The prototype rendered at desktop (1440×900), iPad landscape (1194×834) and iPad portrait (834×1194) and, for the inbox, phone (390×844). Not every scene is rendered at every size. |
| `tokens.json` | Colors, fonts, sizes and motion timings taken from the prototype. |
| `assets/` | The ship, crew boat, flag, Admiral mark and scenery as standalone SVGs. |
| `canvas/` | The source design files (`.dc.html`, a Design Component format: HTML with `{{holes}}`, `<sc-if>`, `<sc-for>` and a `Component` class) and the canvas layout. |
| `prototype/build/` | `build.py` turns `canvas/Main.dc.html` into `prototype/index.html`; `runtime.js` is the small React renderer it embeds. |

## Scenes

Add `?scene=` to the prototype URL:

- `harbor` (default): the dashboard.
- `launch`: launch orders open, with the custom-skill type-ahead showing.
- `bridge`: boarded on a ship that needs you; terminal and flotilla.
- `tablet`: an order selected for tap-to-launch, docks armed.
- `inbox`: the Needs You inbox open over the harbor, with six items waiting.
- `inbox-bridge`: the inbox open beside a boarded ship (Brass Kettle).
- `inbox-empty`: nothing needs you anywhere, so the inbox shows its empty state.
- `ships`: on a phone, the Ships tab. At the other sizes it is the same as `harbor`.
- `orders`: on a phone, the Orders tab. At the other sizes it is the same as `harbor`.
- `phone-launch`: on a phone, the dock sheet open for MOB-1183. At the other sizes it is the same as `harbor`.

On a phone, `harbor` and `inbox` show the Inbox tab and `bridge` shows Brass Kettle's bridge full screen.

## Size classes

The component decides the size class in one place (`phoneQuery()` and `wideQuery()` in `canvas/Main.dc.html`):

- **phone**: `(max-width: 760px), (max-height: 499px)`. The width matches `web/src/styles.css`. The height part keeps a phone in landscape (874×402) in the phone layout, so rotating never brings back open water.
- **wide**: 1024px and up, in landscape, unless phone matches. Desktop and iPad landscape.
- **middle**: everything else, mainly iPad portrait and desktop windows from 761px to 1023px wide. It keeps the harbor.

## Try it

- Drag an order card onto a dock (or tap a card, then tap a dock), pick a skill, Hoist sails. The moored ship leaves the marina, a ship sails into open water, a new ship fades in at the dock, crew boats join, and after a few seconds a Commodore ship starts needing you.
- Tap a ship or a crew boat to board it. Type into the terminal (Shift+Enter for a new line), or use the key buttons on a touch screen; a ship that needs you is answered in its terminal. Full view gives the terminal the whole screen, and the chip under the ship's name copies `admiral attach <ship>`.
- Drag ships around open water.
- On a phone there is no open water or marina. A bottom tab bar switches between **Inbox** (the one-at-a-time inbox, with its count), **Chat** (a placeholder for now), **Ships** (a list: needs-you ships first, then the rest newest first, crew boats indented under their ship) and **Orders** (the Orders sidebar full width). Tap a ship, or a teammate crew boat, to open its bridge full screen; Back returns to the tab you came from. A subagent crew boat cannot be boarded, so its row is dimmed.
- Launching from a phone: tap an order, pick a dock from the sheet (grouped by fleet), then Hoist sails in the launch orders dialog. The Ships tab opens with the new ship highlighted. Dragging is off on a phone.
- Open the inbox with the **Needs you** button in the header; its badge counts every item, live stops included. On a window at least 1024px wide and in landscape it is a drawer on the right that sits beside the bridge, and the Orders sidebar collapses while it is open. On an iPad in portrait or a narrow desktop window it is a full-screen sheet with one item at a time, Back and Next. Resize or rotate to see both.
- In the inbox, answer a question (pick an option, or Other to write your own, then Send answers), submit a review (choose COMMENT, APPROVE or REQUEST_CHANGES, then confirm), discard a review, or approve, edit or reject each finding and Send. The item folds to one line for about 5 seconds and then slides out. The badge drops as soon as you act. A live stop has Attach, which opens that ship's bridge and types nothing into it.
- Collapse panels or the whole sidebar; combine or split the Work queue (not in the MVP); filter.

## What is fake

Every order, ship, crew boat, terminal line and skill in the prototype is sample data. Harness flags use lettered placeholders (Cl, Co, Cx) where the official logos go. The fonts load from Google Fonts; offline, system fallbacks show.

## Regenerating the prototype

```bash
python3 designs/prototype/build/build.py designs/canvas/Main.dc.html designs/prototype/build/runtime.js designs/prototype/index.html
```
