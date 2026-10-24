# Admiral designs

The design reference for building Admiral. Start with the prototype, then read the [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45).

| Path | What |
| --- | --- |
| `prototype/index.html` | The clickable prototype. Open it in a browser (no server needed; React is vendored in `prototype/vendor/`). It is the reference for look, layout and motion. |
| `screenshots/` | The prototype rendered at desktop (1440×900), iPad landscape (1194×834) and iPad portrait (834×1194) and, for the inbox, phone (390×844). Not every scene is rendered at every size. |
| `tokens.json` | Colors, fonts, sizes and motion timings taken from the prototype. |
| `assets/` | The ship, crew boat (live and anchored), subagent bot, flag, Admiral mark and scenery as standalone SVGs. |
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
- `crew`: every crew boat state at once. Saltpaw's teammate checker needs you, Brass Kettle's teammate api has anchored, and Little Bramble is anchored with two crew boats still working.
- `crew-bridge`: boarded on the anchored crew boat api, with the ⋯ menu open on Close crew boat.
- `crew-attn`: boarded on checker, the crew boat that needs you, with it shaking in the flotilla.
- `crew-dismiss`: Dismiss on Little Bramble, asking first because its crew is still working.

## Crew boats and subagents

- **Crew boats** are a ship's teammates. Up to three raft along the ship's stern, small and without name tags, at the spots in `tokens.json` (`size.crewRaft`). The rest go behind a "+N crew" pill, which boards the ship. Every crew boat shows in the flotilla with its name and its role ("Test writer · needs you"), or "Teammate" when it has none. Every role draws the same boat for now (#157).
- **A crew boat that needs you** gets a "!" badge, and its ship shakes and waves its flag fast as if it needed you itself. The ship's plaque names the teammate ("checker needs you"). In the flotilla, the crew boat's row shakes and turns coral. The inbox lists it as a live stop of its own, `Ship › teammate`, and Attach boards that crew boat.
- **An anchored crew boat** furls its sail, turns its stripe grey, stops bobbing and gets the red ✕. Boarded, it shows its last screen with no input. "Close crew boat" in the bridge's ⋯ menu removes it. The ⋯ menu shows only on a crew boat; on a live one, Close crew boat is greyed out with the reason.
- **Subagents** are small bots on their ship's deck, bow side of the mast, part of the ship's drawing so they bob and shake with it. Up to four stand on deck; past that, three and a "+N". They have no terminal, so they can't be boarded: one 44 px button over the deck crew lists each subagent's type and age for a few seconds. The flotilla lists them under "Subagents · no terminal", as rows that can't be tapped.
- **Dismissing a ship with live crew** asks first: "2 crew boats are still working", naming each with its state, then Keep it or Dismiss ship and crew. A ship with no live crew dismisses at once, as before.
- A ship with crew needs more room than a ship alone: the rafted crew reach about 75 px to the left of the ship. Placement in open water has to leave that room.

## Try it

- Drag an order card onto a dock (or tap a card, then tap a dock), pick a skill, Hoist sails. The moored ship leaves the marina, a ship sails into open water, a new ship fades in at the dock, crew boats join, and after a few seconds a Commodore ship starts needing you.
- Tap a ship or a crew boat to board it, or tap a ship's deck crew to see its subagents. Type into the terminal (Shift+Enter for a new line), or use the key buttons on a touch screen; a ship that needs you is answered in its terminal. Full view gives the terminal the whole screen, and the chip under the ship's name copies `admiral attach <ship>`.
- Drag ships around open water.
- Open the inbox with the **Needs you** button in the header; its badge counts every item, live stops included. On a window at least 1024px wide and in landscape it is a drawer on the right that sits beside the bridge, and the Orders sidebar collapses while it is open. On a phone, or an iPad in portrait, it is a full-screen sheet with one item at a time, Back and Next. Resize or rotate to see both.
- In the inbox, answer a question (pick an option, or Other to write your own, then Send answers), submit a review (choose COMMENT, APPROVE or REQUEST_CHANGES, then confirm), discard a review, or approve, edit or reject each finding and Send. The item folds to one line for about 5 seconds and then slides out. The badge drops as soon as you act. A live stop has Attach, which opens that ship's bridge and types nothing into it.
- Collapse panels or the whole sidebar; combine or split the Work queue (not in the MVP); filter.

## What is fake

Every order, ship, crew boat, subagent, terminal line and skill in the prototype is sample data. Harness flags use lettered placeholders (Cl, Co, Cx) where the official logos go. The fonts load from Google Fonts; offline, system fallbacks show.

## Regenerating the prototype

```bash
python3 designs/prototype/build/build.py designs/canvas/Main.dc.html designs/prototype/build/runtime.js designs/prototype/index.html
```
