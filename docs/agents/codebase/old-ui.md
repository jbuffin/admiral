# The old web UI

The Preact UI under `web/src/` other than `web/src/app/`, served at `/classic/`. It is watch-only: it shows the sea, the marinas, ships, crew boats and subagents, live from `/ws`, and takes no input. Read it before touching old-UI code.

- `web/`: `web/embed.go` embeds the built `web/dist` into the binary.
- Nothing in it is interactive: no button, link, `tabindex`, pointer, click or key handler, no global listener (it does not import `chime.ts`, which arms audio listeners on import). `.app` is `pointer-events: none`. `ClassicListeners.test.tsx` guards the listeners and `App.test.tsx` the rest.
- There is no frame: no header, Orders sidebar, inbox, chat, bridge, setup terminal, launch card, phone shell or hint chip. `App.tsx` renders `.stage > main` holding the water, the land (shore, lighthouse, trees, marinas) and the ships.
- It never scrolls. `useStage` lays the scene out at least `STAGE_MIN_WIDTH` wide and scales it down to the window (`--stage`); `useFit` scales the marinas so every dock fits (`--fit`). A phone sees the same scene, just scaled further; there is no phone layout.
- Ships, crew boats, the `+N crew` pill, deck crew and docks are `role="img"` elements with an `aria-label`. Ship labels end in the status words (`statusWords`), crew boats say `crew boat of <ship>`, deck crew use `deckLabel` (`crewSea.ts`).
- A ship or crew boat that needs you looks like a working one: no shake, frantic flag, bang, asking plaque, "needs you" wording or chime. `ships.ts` does not import `chime.ts`; `/app/` still does.
- `ships.ts` keeps the placement rules (`placeShip`, `putPosition`: the page still writes a position for an unplaced ship, and every new ship keeps `REACH` free on its left for its crew), the `leaving`, `arrivals` and `popping` animations, and the `window.__admiral` global that `useBridgeTerminal.ts` writes.
- `crewSea.ts` is the pure rules: the raft (up to `RAFT_MAX` crew boats beside the ship), the `+N crew` pill and `deckLabel`. `CrewBoat.tsx` draws a boat.
- With no fleets, the "Set up a fleet" marina is inert and says to run `admiral setup` in a terminal. The new shell's "New fleet…" says the same; setup is never started from a browser.
- `styles.css` is classic-only; `/app/` imports `shell-tokens.css`. Everything `/app/` imports from `web/src/` (`chime`, `crew`, `findings`, `inbox`, `keys`, `questions`, `useBridgeTerminal`, `connection`, `bridgeLink`, `bridgeReplay`, `terminal`) stays.
