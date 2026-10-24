# Admiral designs

The design reference for building Admiral. Start with the prototype, then read the [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45).

| Path | What |
| --- | --- |
| `prototype/index.html` | The clickable prototype. Open it in a browser (no server needed; React is vendored in `prototype/vendor/`). It is the reference for look, layout and motion. |
| `screenshots/` | The prototype rendered at desktop (1440×900), iPad landscape (1194×834) and iPad portrait (834×1194) and, for the phone layout, phone (402×874: inbox, ships, bridge, launch, chat). The chat and the settings are rendered too. Not every scene is rendered at every size. |
| `tokens.json` | Colors, fonts, sizes and motion timings taken from the prototype. |
| `assets/` | The ship, crew boat, flag, Admiral mark and scenery as standalone SVGs. |
| `canvas/` | The source design files (`.dc.html`, a Design Component format: HTML with `{{holes}}`, `<sc-if>`, `<sc-for>` and a `Component` class) and the canvas layout. `Main.dc.html` also uses `<sc-def>` and `<sc-use>` partials (one question card for the inbox and the chat), which only `runtime.js` knows. |
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
- `chat`: the chat with AdmiralBot open at every size (the Chat tab on a phone, a full-screen sheet on iPad portrait, a floating panel on desktop and iPad landscape), with the sample conversation and the unanswered question card.
- `chat-terminal`: the chat's terminal view, "Chat session".
- `chat-new`: just after New chat, with the fresh greeting.
- `settings`: the Settings modal open, with its General and Chat sections.

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
- On a phone there is no open water or marina. A bottom tab bar switches between **Inbox** (the one-at-a-time inbox, with its count), **Chat** (see below), **Ships** (a list: needs-you ships first, then the rest newest first, crew boats indented under their ship) and **Orders** (the Orders sidebar full width). Tap a ship, or a teammate crew boat, to open its bridge full screen; Back returns to the tab you came from. A subagent crew boat cannot be boarded, so its row is dimmed.
- Launching from a phone: tap an order, pick a dock from the sheet (grouped by fleet), then Hoist sails in the launch orders dialog. The Ships tab opens with the new ship highlighted. Dragging is off on a phone.
- Chat with AdmiralBot, one long-lived session. On a phone it is the Chat tab, with a dot on the tab when AdmiralBot has replied since you looked. On iPad portrait, the **Chat** button in the header opens it as a full-screen sheet. On desktop and iPad landscape, the round chat button at the bottom right of open water opens a floating panel; with the inbox drawer open, the button and panel sit left of it. Escape or the close button closes it.
- In the chat: type a message (Enter sends, Shift+Enter is a new line), AdmiralBot types for a moment and answers. AdmiralBot only adds orders, never launches a ship. The question card is the inbox's: one tap on **Freeform** or **Work queue** sends it and folds the card to "Answered: Freeform"; Other or a note shows Send. The order then appears in the Freeform panel (or the Work queue), where you can launch it as usual. **New chat** asks first, then clears the conversation. **Open terminal** swaps the chat for the chat session's terminal; **Back to chat** returns. Chat questions stay in the chat, not in the inbox.
- Settings: the gear button in the header opens it (on a phone it is full screen and the only place for Sound). The Chat section writes the `chat` key of config.json: fleet (default: the first fleet with a Claude dock), model (default sonnet) and effort (Harness default writes nothing). **Chat settings** in the chat opens the same modal. Save only closes it here.
- Open the inbox with the **Needs you** button in the header; its badge counts every item, live stops included. On a window at least 1024px wide and in landscape it is a drawer on the right that sits beside the bridge, and the Orders sidebar collapses while it is open. On an iPad in portrait or a narrow desktop window it is a full-screen sheet with one item at a time, Back and Next. Resize or rotate to see both.
- In the inbox, answer a question (pick an option, or Other to write your own, then Send answers), submit a review (choose COMMENT, APPROVE or REQUEST_CHANGES, then confirm), discard a review, or approve, edit or reject each finding and Send. The item folds to one line for about 5 seconds and then slides out. The badge drops as soon as you act. A live stop has Attach, which opens that ship's bridge and types nothing into it.
- Collapse panels or the whole sidebar; combine or split the Work queue (not in the MVP); filter.

## What is fake

Every order, ship, crew boat, terminal line and skill in the prototype is sample data. Harness flags use lettered placeholders (Cl, Co, Cx) where the official logos go. The fonts load from Google Fonts; offline, system fallbacks show.

## Regenerating the prototype

```bash
python3 designs/prototype/build/build.py designs/canvas/Main.dc.html designs/prototype/build/runtime.js designs/prototype/index.html
```
