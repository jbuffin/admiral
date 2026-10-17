# Admiral: product spec

Admiral is a playful, game-like dashboard for launching and watching coding-agent sessions. It starts each session itself, in the background, under one of the engineer's fleets (ADR 0003), with Commodore as the default skill. It runs locally on the engineer's Mac and is opened in a browser on the same Mac or on a tablet over the home network or VPN. Read `GLOSSARY.md` first; this spec uses its words.

The look and behavior are defined by the clickable prototype in `designs/prototype/index.html`. When this spec and the prototype disagree on a visual detail, the prototype wins; when they disagree on behavior, this spec wins. Screenshots of each scene are in `designs/screenshots/`.

## Goals

- See every running session at a glance, grouped by fleet, and know immediately which one needs attention.
- Start work by dragging an order onto a dock, with mouse or touch.
- Read and type into any session's terminal, including each teammate or subagent, without hunting for the terminal window it runs in.
- Feel like playing a game: colorful, animated, cartoony, but never childish or slow.

## Non-goals (for now)

- Hosting on the internet, accounts, or multi-user access. Admiral is private and local.
- A native macOS app (signing, notarization). A web app served from the Mac covers the use cases.
- Working with Commodore Launcher. Admiral does not call `commodore` or read its configuration or state; a session opened with the launcher and one opened in Admiral are unrelated.

## Screen layout

One screen, three zones, left to right (see `designs/screenshots/harbor-1440x900.png`):

1. **Header** (navy): the Admiral mark and name, a count of ships at sea, a "N needs you" button (only when at least one ship needs you; it opens the first one), a sound toggle, and the number of fleets loaded.
2. **Orders sidebar** (cream, collapsible to a narrow rail): a filter field, then panels of orders, then "Add source plugin".
3. **Open water** (turquoise ocean) with ships, and on its right edge **land**: a beach, grass, trees, a lighthouse and one **marina** per fleet, stacked from the top.

At narrower widths the zones wrap; at iPad landscape (1194×834) all three fit (see `designs/screenshots/tablet-1194x834.png`). Touch targets are at least 44 px.

## Behavior

Each item ends with **Done when**: the check an implementation must pass.

### 1. Fleets and docks come from Admiral's configuration

Admiral's own configuration lists the fleets. Each fleet sets, for each harness it allows (`claude`, `codex`, `copilot`), the executable, extra arguments and environment the harness starts with (for example its own `CLAUDE_CONFIG_DIR`), so work and personal setups never mix. Admiral shows one marina per fleet with one dock per harness. A fleet may set its color; otherwise the first two are blue `#3366E0` and orange `#F28C28`, and further fleets take the next colors in `designs/tokens.json`. Fleets also list their search roots (ADR 0009).

The configuration is `~/.admiral/config.json`, written by `admiral setup` and editable by hand; Admiral watches it (ADR 0010). With no fleets, the land shows an empty "Set up a fleet" marina that opens a terminal running `admiral setup`; the header's fleet count opens it again later. An invalid file keeps the last good configuration and shows a banner with the error. A running ship keeps the settings it launched with.

**Done when:** adding a fleet or a harness to the configuration shows the new marina or dock without a reload or code changes; a first run with no configuration can be set up entirely from the browser; a harness the fleet does not list never appears; a session launched from a dock starts with exactly that fleet's settings for that harness.

### 2. Orders sidebar

- Each task source contributes panels. A panel header shows a chevron (collapse), its title, overlapping source avatars, a count, and for combinable panels a combine/split icon button.
- Combining merges panels into one list (cards keep their source avatar); splitting restores one panel per source.
- The filter field filters every panel by title or ID as you type.
- The sidebar collapses to a rail of panel buttons with counts.
- A launched order leaves its panel.

**Done when:** combine/split, collapse of a panel, collapse of the sidebar and filtering all work, and survive a page reload (remember per browser).

### 3. Launching: drag or tap

- **Drag:** press an order card and drag it (mouse or touch). A tilted copy of the card follows the pointer. While dragging, every dock is highlighted and the dock under the pointer turns gold. Releasing over a dock opens launch orders for that fleet and harness. Releasing elsewhere cancels.
- **Tap:** tapping a card selects it; docks then show "Tap a dock to launch"; tapping a dock opens launch orders. Tapping the card again deselects.
- Touch drag must not fight vertical scrolling of the sidebar: a vertical swipe on a card scrolls, a horizontal drag picks it up (the prototype uses `touch-action: pan-y` and an 8 px threshold).

**Done when:** both flows work with mouse, trackpad and touch on iPad Safari; dragging never selects text; scrolling the sidebar on a tablet still works.

### 4. Launch orders

The dialog shows the fleet's color in its header, the harness flag, the order's ID and title, and the choices:

- **Commodore** (default, labeled "Task force"): launches the session on Commodore, which plans, assembles teammates and sees it through to a merged PR.
- **Favorite skills** (Wayfinder, Triage in the mockup): up to five skills the engineer starred in the type-ahead, shown on every dock where they are installed. Favorites are global by skill name and kept in Admiral's configuration. Starring a sixth is refused ("Unstar one first"). The mockup's "Edit skill list" goes away. Commodore is always shown and never counts; if the skill index does not find it on a dock, it shows disabled with the tooltip "Commodore not found" (shown on tap on iPad), cannot launch, and the default moves to the first favorite, or to Just launch.
- **Custom skill**: a text field with type-ahead over the skills installed for that harness and fleet, each row with a star. Arrow keys move, Enter picks, Escape closes; tapping a row picks it. If nothing matches, the session launches with the name exactly as typed.
- **Hoist sails** launches; **Stay in port** cancels.

Hoist sails starts the dock's harness in the background with the fleet's settings, in the order's repository checkout, on the chosen skill with the order's URL as the launch prompt.

Installed skills come from the places each harness loads skills from, listed in `docs/architecture.md` (for Claude Code: the fleet's `CLAUDE_CONFIG_DIR/skills`, the project's `.claude/skills`, and enabled plugins), read with the fleet's environment and the chosen directory each time launch orders opens. A skill is `<dir>/<name>/SKILL.md`.

**Done when:** each choice launches the session with that skill; the type-ahead lists real installed skills for the chosen fleet and harness; keyboard and touch both work.

### 5. Leaving the marina

When launched, the ship moored at that dock pulls away, sails toward the marina mouth and fades out; about 0.7 s later the same ship sails into open water. About a second after it left, a new ship fades in at the dock. Commodore sessions gain crew boats (one per teammate) as they appear; other sessions gain a crew boat per subagent.

**Done when:** the sequence plays on every launch; the moored ship is always back within ~2 s; crew boats appear as the session reports them, not on a timer.

### 6. Open water

- Ships sail to a free spot in their fleet's area: Work toward the top, Home toward the bottom (generally, not a hard lane; no dividing line or label).
- Ships bob gently and their flags ripple. When a ship's harness exits cleanly, the ship shows a green check, sails off and disappears. If it exits with an error, the ship is anchored: it sits still with a red mark and the exit code on its plaque until the engineer dismisses it from the bridge, which opens read-only on its last screen (ADR 0008).
- Any ship (with its crew) can be dragged to a new spot and stays there; a tap without movement boards it.
- Each ship has a plaque: generated ship name, then task ID and skill.

**Done when:** new ships never overlap existing ones on launch; dragged positions persist across reloads; a tap opens the bridge and a drag does not.

### 7. Needs you

When a session waits on the engineer, its ship shakes, its flag waves fast, a red "!" badge pulses, and a short chime plays (unless muted). The header shows "N needs you". Answering in the bridge clears it.

**Done when:** a real harness waiting for input or an approval flips its ship to needs-you within a couple of seconds, and back once answered. See `docs/open-questions.md` on how to detect it.

### 8. The bridge (attached terminal)

Tapping a ship opens the bridge over open water:

- **Flotilla** on the left: the ship and each crew boat fly in; picking one switches the terminal to that agent.
- **Terminal** on the right: a header with the harness flag, ship name › agent, harness · fleet · skill · task, a status pill and a close button; the live terminal output; an inline `❯` prompt that sends a line on Enter; a row of keys sent straight to the session (Enter, Esc, Tab, ↑, ↓, ^C), plus **Approve · y** and **Deny · n** when the lead is asking.
- The terminal must be a real terminal emulator (ANSI colors, cursor movement, resizing), not a log view.

**Done when:** output streams live with no visible lag on the LAN; typed text and keys reach the harness; switching agents in the flotilla switches the stream; closing the bridge leaves the session running.

### 9. Sound and motion

A sound toggle in the header mutes the chime. `prefers-reduced-motion` turns off bobbing, shaking, sailing and flag animations.

### 10. Logos

Flags carry each harness's official logo. The prototype uses lettered placeholders (Cl, Co, Cx); the build must load the vendors' official logo files into the same flag shape. Do not redraw the logos.

## Known prototype issues to fix in the build

- Crew boats can overlap their ship's plaque; lay crew out around the ship without covering the plaque.
- Ship slots are fixed percentages; real placement should avoid collisions and respect the fleet's general area.
- Sample orders, ships and terminal lines are fake; nothing in them is real data.
