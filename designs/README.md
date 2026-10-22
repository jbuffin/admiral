# Admiral designs

The design reference for building Admiral. Start with the prototype, then read the [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45).

| Path | What |
| --- | --- |
| `prototype/index.html` | The clickable prototype. Open it in a browser (no server needed; React is vendored in `prototype/vendor/`). It is the reference for look, layout and motion. |
| `screenshots/` | The prototype rendered at desktop (1440×900), iPad landscape (1194×834), iPad portrait (834×1194) and, for the inbox, phone (390×844), one per scene. |
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

## Try it

- Drag an order card onto a dock (or tap a card, then tap a dock), pick a skill, Hoist sails. The moored ship leaves the marina, a ship sails into open water, a new ship fades in at the dock, crew boats join, and after a few seconds a Commodore ship starts needing you.
- Tap a ship or a crew boat to board it. Type into the terminal (Shift+Enter for a new line), or use the key buttons on a touch screen; a ship that needs you is answered in its terminal. Full view gives the terminal the whole screen, and the chip under the ship's name copies `admiral attach <ship>`.
- Drag ships around open water.
- Open the inbox with the **Needs you** button in the header; its badge counts every item, live stops included. At 1024px wide and up it is a drawer on the right that sits beside the bridge, and the Orders sidebar collapses while it is open. Below 1024px it is a full-screen sheet with one item at a time, Back and Next. Resize the window to see both.
- In the inbox, answer the questions (pick an option, or Other for your own answer, and send), submit a review (Submit, choose COMMENT, APPROVE or REQUEST_CHANGES, then confirm), discard a review, or approve, edit or reject each finding and send. The item folds to one line for about 5 seconds, then slides out and the badge has already dropped by one. A live stop has Attach, which opens that ship's bridge and never types into it.
- Collapse panels or the whole sidebar; combine or split the Work queue (not in the MVP); filter.

## What is fake

Every order, ship, crew boat, terminal line and skill in the prototype is sample data. Harness flags use lettered placeholders (Cl, Co, Cx) where the official logos go. The fonts load from Google Fonts; offline, system fallbacks show.

## Regenerating the prototype

```bash
python3 designs/prototype/build/build.py designs/canvas/Main.dc.html designs/prototype/build/runtime.js designs/prototype/index.html
```
