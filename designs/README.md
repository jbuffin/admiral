# Admiral designs

The design reference for building Admiral. Start with the prototype, then read the [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45).

| Path | What |
| --- | --- |
| `prototype/index.html` | The clickable prototype. Open it in a browser (no server needed; React is vendored in `prototype/vendor/`). It is the reference for look, layout and motion. |
| `screenshots/` | The prototype rendered at desktop (1440×900), iPad landscape (1194×834) and iPad portrait (834×1194), one per scene. |
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

## Try it

- Drag an order card onto a dock (or tap a card, then tap a dock), pick a skill, Hoist sails. The moored ship leaves the marina, a ship sails into open water, a new ship fades in at the dock, crew boats join, and after a few seconds a Commodore ship starts needing you.
- Tap a ship or a crew boat to board it. Type at the `❯` prompt, use the key buttons, or Approve / Deny.
- Drag ships around open water.
- Collapse panels or the whole sidebar; combine or split the Work queue (not in the MVP); filter.

## What is fake

Every order, ship, crew boat, terminal line and skill in the prototype is sample data. Harness flags use lettered placeholders (Cl, Co, Cx) where the official logos go. The fonts load from Google Fonts; offline, system fallbacks show.

## Regenerating the prototype

```bash
python3 designs/prototype/build/build.py designs/canvas/Main.dc.html designs/prototype/build/runtime.js designs/prototype/index.html
```
