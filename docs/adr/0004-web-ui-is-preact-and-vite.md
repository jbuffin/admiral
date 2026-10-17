# 4. The web UI is Preact, TypeScript and Vite

Status: accepted

## Context

Admiral's UI is a single page served as static files by the local server (ADR 0001). It has to match the prototype's look and motion, embed xterm.js for the bridge, support touch drag on iPad Safari, and work on a LAN with no internet. The prototype is built on React, but only through a small custom renderer for its design components. Its motion is all CSS keyframes and transitions, and its drag is plain Pointer Events, so none of its code needs to carry over. Only the design does.

The UI is mostly live state the server pushes: ships changing state, needing you, docking. The art changes with that state. For example, a ship's flag waves faster when it needs you.

## Decision

- **Preact with `@preact/signals`**, without `preact/compat` unless a dependency needs it.
- **TypeScript, built with Vite.** The source is in `web/` in this repo. `vite build` outputs hashed static files that the server embeds. In development, Vite forwards HTTP and WebSocket requests to the local server.
- **Custom CSS.** Plain CSS with custom properties generated from `designs/tokens.json`. No Tailwind, no CSS-in-JS and no component library.
- **Ships, crew boats, flags and scenery are Preact SVG components** (`<Ship fleet bot harness state/>` and so on), so each part of a drawing can follow the ship's state. The art comes from `designs/assets/`, cleaned up (C2PA metadata removed, run through SVGO) and turned into components. `designs/assets/` remains the design reference only.
- **Harness logos are a separate `<symbol>` set** that the flag draws with `<use>`. Builds we share include lettered placeholders under the same ids until Anthropic and GitHub grant written permission to use their logos ([#11](https://github.com/jbuffin/admiral/issues/11)).
- **Drag is our own Pointer Events code**, with `touch-action: none` on draggable elements. No drag library.
- **The terminal is `@xterm/xterm` with `@xterm/addon-fit`**, loaded the first time a bridge opens. It uses `@xterm/addon-webgl` where available and falls back to the DOM renderer.
- **Fonts are bundled.** Bricolage Grotesque, Archivo and JetBrains Mono are included as WOFF2 subsets with their OFL licences. The UI never loads Google Fonts.
- **Tests** use Vitest for logic, plus Playwright on WebKit with iPad emulation for the drag and bridge flows, following the testing rule in `AGENTS.md`. Before a release, check on a real iPad by hand.
- **No size budget.** First-load size is not a constraint.

## Considered options

- **React.** It matches the prototype and agents know it best, but it is about ten times Preact's size and gives no benefit here, since the prototype's code is not reused.
- **Svelte 5 or Solid.** Both are about as small as Preact, but they use a different component model, and Preact keeps the familiar React hooks and JSX style.
- **Vanilla TypeScript.** The smallest option, but every view would have to update itself by hand across the harbor, bridge and launch orders.
- **An SVG `<symbol>` sprite sheet for every drawing.** It has the lightest DOM, but every per-ship state difference would have to be passed in through CSS variables, and Safari has had bugs with animations and gradients inside `<use>` copies. Admiral shows tens of ships, so DOM size doesn't matter.
- **A drag library (dnd-kit).** It needs React or `preact/compat`. The drags Admiral needs are simple enough to write directly.

## Consequences

- None of this depends on the back-end stack. If the server is Bun, it and the UI share TypeScript.
- Swapping in the official logos changes only the logo symbol set.
- The prototype guides design only. Where its structure is awkward for a real app, the app is built differently.
