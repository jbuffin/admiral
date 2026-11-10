# Admiral redesign: UI pattern references

> Research for the Admiral redesign map, 2026-11-09. The direction later chosen is a Linear-clean, chat-first shell (the design canvas's direction D), which differs from this note's recommendation of B.

Researched 2026-11-09. Every URL below returned HTTP 200 to a browser user agent on that date, unless it is marked otherwise. Pages behind logins or bot walls (Mobbin app pages, Land-book, Dribbble search, Cursor, OpenAI Codex) are flagged. Their content was not read, so check them by hand.

Admiral's pieces, as used below: **Projects** (repos, left rail), **Orders** (work items from GitHub/Jira/Linear), **Sessions** (running agents: running / needs-you / exited-with-error / idle), **Inbox** (questions, PR reviews, findings to approve), and the **Terminal drawer** (live attach).

---

## 1. Dev dashboards: list, detail and drawer

| Reference | URL | Borrow | How it applies to Admiral |
| --- | --- | --- | --- |
| Linear: "How we redesigned the Linear UI" (2024) | <https://linear.app/now/how-we-redesigned-the-linear-ui> | Theme generated in LCH from three inputs (base, accent, contrast). Chrome forms an "inverted L" (sidebar plus header) around the content. Separate app-level and view-level headers. Inter Display for headings, Inter for body. Elevation shown by surface tone, not borders. | Generate the navy ramp from one base hue in OKLCH so panels, drawer and modals step evenly. Put Projects in the L's vertical bar, and the view header (filters, harness, group-by) over the Sessions/Orders list. Add a contrast setting for long sessions on the iPad. |
| Linear: "Behind the latest design refresh" (Mar 2026) | <https://linear.app/now/behind-the-latest-design-refresh> | Sidebar "dimmed a few notches" so content leads. Fewer and softer separators ("structure should be felt, not seen"). Smaller icons. Header actions in the same place in every view. Colors moved from cool blue toward a less saturated gray. | Make the Projects rail one step darker or quieter than the Sessions list. Drop most row dividers and use a 1px subtle border only between panes. Desaturate the navy neutrals a little so teal and amber stand out. |
| Linear changelog: UI refresh | <https://linear.app/changelog/2026-03-12-ui-refresh> | Short visual summary of the refresh above, with before/after screenshots. | Quick visual check of how much "calm" to aim for. |
| Linear Inbox docs | <https://linear.app/docs/inbox> | Priority/Other tabs. `J/K` to move, `U` read/unread, `H` snooze, `Backspace` remove. Detail view opens beside the list. Show-unread-first option and a badge count. | Admiral's Inbox: a **Needs you** tab (questions, approvals) and an **Other** tab (FYI findings, finished runs). Same keys. Snooze maps cleanly to "remind me when this PR's CI finishes". |
| Linear Triage docs | <https://linear.app/docs/triage> | One-key verdicts: `1` Accept, `2` Decline, `3` Duplicate, `H` Snooze. | Approving findings and orders: `1` approve and launch, `2` dismiss, `3` merge into an existing order, `H` later. Show these as a fixed action bar in the detail pane, which gives touch users large targets. |
| Linear mobile redesign (Oct 2025) | <https://linear.app/changelog/2025-10-16-mobile-app-redesign> | Bottom toolbar for core workflows on phones. | Phone layout: a bottom tab bar (Inbox, Sessions, Orders, Projects) instead of a hamburger. |
| Linear agent interaction (developer docs) | <https://linear.app/developers/agent-interaction> | Agent session states: `pending`, `active`, `awaitingInput`, `error`, `complete`, `stale`. Activity types: thought, action ("Searching" changes to "Searched"), elicitation, response, error. Ephemeral activities replace each other. | A vocabulary that matches Admiral's ship statuses. Show only the latest *ephemeral* action as a one-line subtitle on each session row ("Running tests…"), and keep elicitations (questions) as persistent Inbox items. Treat `stale` (no heartbeat) as a separate state from idle. |
| Vercel Geist: colors | <https://vercel.com/geist/colors> | A 10-step scale per hue, each step with a fixed job: 100 bg, 200 hover, 300 active, 400 border, 500 border hover, 600 border active, 700 solid, 800 solid hover, 900 secondary text, 1000 primary text. | Name Admiral's tokens by job, not by color, so the dark and light themes swap cleanly. |
| Vercel Geist: status dot | <https://vercel.com/geist/status-dot> | Queued and Building animate. Ready, Error and Canceled are static. Each dot always carries a sentence-case label, so color is never the only signal. | Session status chip: animate only "running". Needs-you, error, idle and exited are static. Always show the label on desktop, and on phone at least in the row's accessible name. |
| Vercel deployments and logs | <https://vercel.com/docs/deployments> · <https://vercel.com/docs/deployments/logs> | Deployment list rows: status dot, branch, commit message, age, author avatar. The log view filters by level. | Session row: harness logo, status, branch and worktree, order title, age. Use the same row anatomy for Orders. |
| Railway: the basics | <https://docs.railway.com/overview/the-basics> | A canvas of services. Clicking one opens a large **right-side panel** with tabs (Deployments, Variables, Metrics, Settings) while the canvas stays visible behind it. | The model for opening a session: a right panel with tabs (**Terminal**, **Activity**, **Diff/PR**, **Order**) over the list, which stays in view. |
| Render logging | <https://render.com/docs/logging> | Service log stream with search and time filters. | A pattern for the non-interactive "Activity" tab (a structured event log beside the raw terminal). |
| Fly.io monitoring | <https://fly.io/docs/monitoring/> | Live log tail beside machine state. | Minor reference for the live-tail behavior. |
| GitHub Actions run graph | <https://docs.github.com/en/actions/how-tos/monitor-workflows/use-the-visualization-graph> | Job nodes with status icons to the left of each name. Clicking a job opens its log. Run summary on top. | Crew boats and lead/worker sessions: draw a small dependency strip (lead to workers) at the top of the session panel. |
| Buildkite build page | <https://buildkite.com/docs/pipelines/dashboard-walkthrough> | The **primary action depends on state** ("Retry failures" when retryable, "Rebuild" when failed, "New build" otherwise). Exit status sits in the job footer. | One primary button per session that follows its status: Needs you leads to **Answer**, Error to **Resume** or **Restart**, Idle to **Send order**, Running to **Attach**. Show the exit code in the row footer for errored sessions. |
| Temporal Web UI | <https://docs.temporal.io/web-ui> | Saved Views (up to 20), a built-in "Task Failures" view, time shown as UTC/Local/Relative, and history as Timeline / Compact / JSON. | Built-in saved views: **Needs you**, **Failing**, **Running**, **Idle > 1h**. Activity tab with a Compact/Raw toggle. |
| Trigger.dev run states | <https://trigger.dev/docs/runs> | A complete, well-named lifecycle: Queued → Executing → Waiting → Completed / Failed / Crashed / Timed out / Canceled / Expired. | Check Admiral's ship statuses against this list. "Crashed" (the process died) and "Failed" (the agent reported failure) deserve different copy even if they share coral. |
| Inngest run inspection | <https://www.inngest.com/docs/platform/monitor/inspecting-function-runs> | Step timeline with expandable failed steps and retries. "Open in new tab" for a full-page run. | Let the drawer pop out to full page (`⌘⏎`), which matters on iPad split view. |
| Sentry issues | <https://docs.sentry.io/product/issues/> | Triage tabs (Unresolved / **For Review** / Regressed / Escalating / Archived). Hover a title to preview the stack trace. Saved searches in the header. | Inbox findings: a "For review" tab for new agent findings. Hover (or long-press on touch) previews the agent's last message without opening it. |
| PostHog docs | <https://posthog.com/docs> | Dense left nav with product groups, and a friendly but technical tone. | Secondary. Useful for the settings and docs areas, not the main dashboard. |
| Raycast | <https://www.raycast.com/> · <https://manual.raycast.com/> | The command palette as the main surface: fuzzy search, a sectioned result list, an action panel (`⌘K` within a result), keyboard hints on the right. | `⌘K` palette: "Attach to…", "New order in…", "Approve…", "Stop…". On iPad with a keyboard this is the fastest path. On touch, give it a floating search button. |
| Superhuman | <https://superhuman.com/> · <https://superhuman.com/blog> | Split Inbox (separate streams by rule), "Inbox zero" ritual, keyboard-first triage, instant optimistic UI. (The help-center article on Split Inbox returns 403 to bots.) | Inbox split by kind: Questions / Reviews / Findings. Celebrate an empty "Needs you" state with a calm empty state, not a game reward. |

## 2. Terminal-forward UIs

| Reference | URL | Borrow | How it applies to Admiral |
| --- | --- | --- | --- |
| Warp: Blocks | <https://docs.warp.dev/terminal/blocks> | Each command plus its output is a unit you can copy, bookmark and share. Sticky command header. Jump to the start of the output. | Over the live xterm, show a slim "turn header" (the last prompt sent) that sticks while you scroll. Copy-output and jump-to-latest buttons in the drawer's toolbar. |
| Warp: Vertical tabs | <https://docs.warp.dev/terminal/windows/vertical-tabs/> | **The closest analogue to Admiral's session list.** Each row shows cwd, git branch, diff stats, PR badge, worktree, an **agent brand icon plus a status badge overlay** (in progress / done / errored / cancelled / blocked), and an accent **unread dot** that clears on focus. Compact (one line) and Expanded density. Hover sidecar with full metadata. | Copy it closely: harness logo with a status badge overlaid in the corner, title, branch and diff stats on line 2 in Expanded density. Unread dot = "finished a turn since you last looked". Hover sidecar on desktop, long-press on touch. |
| Warp: Agent notifications | <https://docs.warp.dev/agent-platform/local-agents/agent-notifications> | Three notification kinds: **Complete**, **Request** (blocked, needs input), **Error**. At most two toasts at once, the oldest replaced. A mailbox with All / Unread / Errors filters. Attention badge on tabs. | Toast policy: no more than 2, only for Request and Error. Complete goes quietly into the Inbox. A tab badge on the Projects rail per project. |
| Warp: Run multiple agents | <https://docs.warp.dev/guides/agent-workflows/how-to-run-multiple-ai-coding-agents/> | An Agent Management Panel listing running / waiting / finished across tabs. | Confirms that the "fleet list plus attach" model is where terminals are heading. |
| Ghostty features | <https://ghostty.org/docs/features> | Native tabs and splits. A **Quick Terminal** that slides down from the menu bar. Hundreds of themes that follow the system light/dark setting. | The Quick Terminal is the motion model for the attach drawer: a fast slide from one edge, dismissed by the same key. Ship a curated set of terminal themes, defaulting to an Admiral navy. |
| Zed: Terminal | <https://zed.dev/docs/terminal> | Terminal as a dock panel (bottom/left/right) or as a center tab. Toggled with ``⌘` ``. | Let the drawer dock right (default on desktop and iPad landscape) or bottom (iPad portrait), with a "promote to full tab" action. |
| Zed: panel system (blog) | <https://zed.dev/blog/new-panel-system> | Left, right and bottom docks hold panels. A newer "Agentic" layout puts the agent panel and threads sidebar together on the left. | Shows that one app can offer "Classic" and "Agentic" layouts. Admiral could ship two presets (see the directions below). |
| VS Code terminal basics | <https://code.visualstudio.com/docs/terminal/basics> | Tab list at the side of the panel. Splits. Maximize chevron. Move the terminal to the editor area. **Status icons beside the tab title** (check / X / bell), hover for details. Command decorations in the gutter and scrollbar. | Scrollbar marks in the drawer for each prompt or turn, and red marks for errors. Maximize chevron in the drawer header. |
| xterm.js | <https://xtermjs.org/> · <https://github.com/xtermjs/xterm.js> | The renderer behind VS Code's terminal. Theming via an `ITheme` object. WebGL/canvas addons. | Terminal palette tokens (section 6) map straight onto `ITheme`. |

**Browser tmux-style split UIs:** no single canonical, polished web product turned up. The closest reference patterns are VS Code's terminal splits and Warp's pane-as-row model. For Admiral, allowing two attached sessions side by side (desktop only) is enough.

## 3. Design systems and tokens to build on

| Reference | URL | Borrow | How it applies to Admiral |
| --- | --- | --- | --- |
| Radix Colors | <https://www.radix-ui.com/colors> | 12-step scales, each step with a job (1 app bg, 2 subtle bg, 3–5 component bg normal/hover/pressed, 6–8 borders, 9–10 solid, 11–12 text). Matched light and dark scales, plus alpha variants. | The best ready-made structure. Radix's `slate`, `cyan`/`teal`, `amber` and `tomato` are close to Admiral's hues. |
| Radix: Understanding the scale | <https://www.radix-ui.com/colors/docs/palette-composition/understanding-the-scale> | The step-by-step usage table. | The cheat sheet for whoever writes the CSS variables. |
| Radix Colors: custom palette generator | <https://www.radix-ui.com/colors/custom> | Generates full 12-step light and dark scales from an accent, a gray and a background hex. | Feed it accent `#29B2C2`, gray `#5B7590` and background `#0A1828` to get a navy ramp tuned for accessibility, then hand-pin the brand amber and coral. |
| Radix Themes playground | <https://www.radix-ui.com/themes/playground> | Live controls for accent, gray, radius, scaling (90–110%) and panel background (solid/translucent). | Use it to try density and radius quickly. Scaling 95% fits the desktop list, and 105% or more suits touch. |
| shadcn/ui: Drawer | <https://ui.shadcn.com/docs/components/drawer> | Now built on Base UI (no longer Vaul). `swipeDirection` (up/right/down/left), a swipe handle, **snap points** (fractions or px), and a documented iOS Safari fix (`body{position:relative}`). Responsive Dialog-on-desktop / Drawer-on-mobile recipe. | The terminal drawer on phones: a bottom sheet with snap points at 40% (peek: last output plus reply box) and 100% (full terminal). On desktop and iPad, a right **Sheet**. |
| shadcn/ui: Sheet, Resizable, Command | <https://ui.shadcn.com/docs/components/sheet> · <https://ui.shadcn.com/docs/components/resizable> · <https://ui.shadcn.com/docs/components/command> | Side sheet, draggable split panes, and a cmdk palette. | Sheet for the session panel, Resizable for the Projects / list / detail columns, Command for `⌘K`. |
| shadcn/ui Blocks | <https://ui.shadcn.com/blocks> | Sidebar and dashboard layouts ready to copy. | A starting skeleton for the app shell. |
| cmdk | <https://cmdk.paco.me/> | Unstyled, fast command menu. | The palette primitive. |
| Vaul and Emil Kowalski's drawer write-up | <https://vaul.emilkowal.ski/> · <https://emilkowal.ski/ui/building-a-drawer-component> | Animate `transform` directly (not CSS variables). Velocity-based flick to dismiss. Damping past the limits. Drag only when content is scrolled to the top, with a 100ms guard. Visual Viewport API for the on-screen keyboard. Ionic easing at ~500ms. Test on a real device. | Exactly the iPad and phone pitfalls Admiral's drawer will hit, especially the keyboard covering the reply box and scroll fighting the drag inside xterm. Make only the drawer's header the drag handle so xterm keeps its own scrolling. |
| Vercel Geist intro and font | <https://vercel.com/geist/introduction> · <https://vercel.com/font> | Geist Sans and Geist Mono (OFL, 9 weights, Swiss-restrained). | One option for the type pairing. |
| GitHub Primer: color usage | <https://primer.style/product/getting-started/foundations/color-usage/> | Functional roles: `accent`, `success`, **`attention` (warnings *and active processes*, e.g. queued PRs and running tests)**, `danger`, `done`. Each has fg, muted bg, emphasis bg and border. | Naming model: `--status-needs-you` (attention/amber), `--status-error` (danger/coral), `--status-running` (accent/teal), `--status-done` (success). Primer uses amber for *in progress*. Admiral should not, because amber has to mean "needs you" alone. |
| Primer UI patterns | <https://primer.style/product/ui-patterns/> | Loading, empty states, navigation and degraded experiences. | Copy for an order source being offline (degraded) and an empty Inbox. |
| IBM Carbon: Data table usage | <https://carbondesignsystem.com/components/data-table/usage/> | Row heights xs 24 / sm 32 / md 40 / lg 48 / xl 64 px. Toolbar height matches the rows. A batch-action bar replaces the toolbar on selection. Expandable rows. | Session and Order rows: 32px compact on desktop, **44–48px on touch** (iOS minimum target). Multi-select orders, then a batch bar ("Launch 3 on Claude Code"). |
| IBM Carbon: Status indicator pattern | <https://carbondesignsystem.com/patterns/status-indicator-pattern/> | Use at least two of color, shape and icon. 3:1 contrast for non-text. Groups take the **highest-attention** status. Dot badge for "new" and a numbered badge capped at 99+. Don't show more than 5–6 kinds of indicator. | The Projects rail shows each project's worst status. Amber gets a distinct shape (a filled triangle or "?" glyph), coral an "×" or octagon, so they stay apart for color-blind users. Limit Admiral to five session states visually. |
| Inter / JetBrains Mono / Geist on Google Fonts | <https://rsms.me/inter/> · <https://www.jetbrains.com/lp/mono/> · <https://fonts.google.com/specimen/Geist> · <https://fonts.google.com/specimen/Geist+Mono> | | See section 6. |

## 4. Inspiration galleries

| Gallery | URL | Note |
| --- | --- | --- |
| Refero: Linear | <https://refero.design/apps/linear> | Loads (200) but renders client-side, so its content could not be read. Refero indexes real product screens by pattern (Inbox, Command palette, Side panel). Search there for "inbox", "side sheet", "status". |
| Refero home | <https://refero.design/> | Search by pattern and app. Good apps to pull up: Linear, Vercel, Railway, Raycast, Height, Attio. |
| Mobbin: browse web apps | <https://mobbin.com/browse/web/apps> · <https://mobbin.com/discover/apps/web/latest> | Loads, but app detail pages need a login (the `/apps/linear-web` slug 404s anonymously). Mobbin has full real flows for Linear, Vercel, GitHub and Sentry on web and iOS, which is useful for the phone layout. |
| Nicely Done: Linear | <https://nicelydone.club/apps/linear> | Real Linear flows (onboarding, inbox, settings), screen by screen. |
| SaaSFrame: Dashboards | <https://www.saasframe.io/categories/dashboard> | Real SaaS dashboard screenshots by category. |
| SaaS UI | <https://www.saasui.design/> | Curated SaaS UI screens and patterns. |
| Godly: SaaS | <https://godly.website/websites/saas> | Mostly marketing sites. Useful for motion and polish, less so for app density. |
| Dribbble tags and searches | <https://dribbble.com/tags/developer-dashboard> · <https://dribbble.com/search/terminal-dashboard> | Returned 202 (bot challenge). Open them in a browser. Concept shots, so treat as mood rather than patterns. |
| Land-book | <https://land-book.com/> | 403 to bots. Marketing-site gallery, low priority. |

## 5. "Needs a human" status and inbox patterns in agent tools

| Reference | URL | Borrow | How it applies to Admiral |
| --- | --- | --- | --- |
| **Claude Code: Agent view** | <https://code.claude.com/docs/en/agent-view> | Groups in this order: Pinned → **Ready for review** (open PR or failing checks) → **Needs input** → Working → Completed (folded to "… N more"). Icons: Working animated, **Needs input yellow**, Idle dimmed, Completed green, Failed red, Stopped grey. Icon *shape* also encodes state (`✻`/`✽` live, `∙` exited, `✢` sleeping loop). **`Space` = peek** (latest output or the pending question, with inline reply), `→`/`Enter` = attach, `←` = detach. A number key answers a multiple-choice question. | The closest analogue, from a harness Admiral already drives. Use the same group order on the Sessions list. **Add a Peek state between the row and the full terminal**: a compact card with the question plus quick replies, so most "needs you" items get handled without the terminal drawer. Keyboard: Space peek, Enter attach, Esc detach. |
| AgentsRoom: Needs-input inbox | <https://agentsroom.dev/docs/needs-input-inbox> | A **red** "needs input" pill in the title bar only while someone waits. Click it to jump to the *oldest* waiting agent (`⌘⇧I`). Chips per project (idle / working / needs input). Waiting agents sort first. Single-choice questions answerable in a popover. Looking at an agent **does not** clear the state, only answering does. Unread (green) for "finished a turn while you were away". On mobile, push plus a tinted row instead of the popover. | Global "N need you" pill in the header that jumps to the oldest. Clear needs-you only when the human responds, not when they view. Separate *unread* from *needs you*. |
| Warp vertical tabs and notifications | (section 2) | Status badge over the brand icon. Unread dot. Complete / Request / Error toasts. | Same as above. |
| GitHub Copilot: track agent sessions | <https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/track-copilot-sessions> | Sessions are reachable from every surface (an Agents panel on any page, the repo Agents tab, mobile, CLI `/resume`). The session view puts the overview (progress, tokens, length) over the log, with a **steering prompt box under the log**. Stop and Archive. | Steering box pinned at the bottom of the session panel, outside xterm, so iPad users get a native text field with autocorrect and dictation, then send it to the session. Archive for exited sessions. |
| GitHub Agent HQ (mission control) | <https://github.blog/news-insights/company-news/welcome-home-agents/> | One "mission control" across web, VS Code, CLI and mobile. Assign, steer and track agents in parallel. | Positioning: Admiral is a local, multi-harness mission control. Avoid a "game" metaphor in the copy. |
| GitHub Copilot agents | <https://github.com/features/copilot/agents> | Marketing screenshots of the agents panel. | Visual reference for a GitHub-native session list. |
| Conductor | <https://conductor.build/> | Parallel Claude Code, Codex and Cursor agents in isolated worktrees. "See at a glance what they're working on, then review and merge." | The direct competitor in spirit. Look at its screenshots for diff-review placement. |
| Vibe Kanban | <https://www.vibekanban.com/> | Kanban where card status updates automatically when an agent starts, opens a PR or merges. A worktree per task. Review view with inline comments sent back to the agent. | A source for the "Board" direction below. Columns follow order state, and session status shows on the card. |

Not verified (403 to bots): <https://cursor.com/agents> and <https://openai.com/codex/>. Both are worth a manual look for background-agent list UIs.

---

## 6. Proposed layout directions

### A. "Inbox-first" (Linear Inbox × Superhuman × Claude Code agent view)

Three columns: a slim **Projects rail** (icons plus a status badge per project), a **unified queue** (Needs you → Running → Idle → Exited, grouped like agent view), and a **detail pane**. The detail pane opens as Peek (question, quick replies, last output, linked PR). "Attach" turns the detail pane into the terminal. Orders live behind a tab or `⌘K` "New order". Best when the main job is answering agents. Phone: a bottom tab bar, a full-screen queue, and Peek as a 40% bottom sheet.

### B. "Mission control" (Warp vertical tabs × Railway panel × VS Code panel)

Left: **Sessions as vertical tabs** (brand logo, status overlay, branch, diff stats) grouped by project. Center: an **Orders** list for the selected project, Linear-dense. Right: a **session sheet** with tabs Terminal / Activity / Diff / Order, resizable and dockable to the bottom on iPad portrait. A global "N need you" pill in the header. Best for the engineer who launches a lot and watches several at once. Closest to today's Admiral information architecture.

### C. "Board" (Vibe Kanban × GitHub Actions graph)

Per-project kanban: **Orders** (backlog) → **Underway** (session running) → **Needs you** → **In review** (PR open) → **Done**. Cards carry the harness logo and live status, and crew boats show as stacked cards with a mini lead/worker graph. Clicking a card opens the right drawer. Fits touch and drag well (it keeps the existing drag-to-launch flow), but is less dense. A good iPad landscape default.

### D. "Console" (Ghostty Quick Terminal × Raycast × tmux)

The terminal is the canvas. A top strip of session "pills" (status-colored) plus a `⌘K` palette for everything else. Inbox and Orders slide in as overlays. Peek cards float from the strip. Best for keyboard power users on desktop and weak on phones. Could ship as an alternate "focus mode" preset over A or B, following Zed's Classic/Agentic precedent.

Recommendation: build **B** as the default shell, steal A's **Peek** and grouped queue for the Sessions column, and offer C as a per-project view toggle.

---

## 7. Palette tokens

Built by keeping Admiral's hues and desaturating the neutrals a little (per Linear's 2026 refresh). Contrast ratios are WCAG 2.x, computed against the listed backgrounds.

### Dark (default)

```css
:root[data-theme="dark"] {
  /* surfaces: deepest to highest */
  --bg-canvas:   #0A1828;  /* app backdrop, Projects rail */
  --bg-panel:    #0E2236;  /* list panes */
  --bg-raised:   #14304A;  /* detail pane, drawer, popovers (existing navy-2) */
  --bg-hover:    #1A3A57;
  --bg-selected: #21486B;  /* existing navy-3 */
  --terminal-bg: #07131F;  /* a step below canvas so the drawer reads as "inside" */

  --border-subtle: #1A3550;
  --border:        #2A4B6B;
  --border-strong: #3B6489;
  --focus-ring:    #29B2C2;

  --text-1: #E8EFF6;  /* 15.4:1 on canvas */
  --text-2: #A7BBCF;  /*  9.1:1 */
  --text-3: #8399B0;  /*  4.6:1 on raised: meta, timestamps */

  /* brand + status */
  --accent:            #29B2C2;  /* teal: primary actions, focus, "running" (7.0:1 on canvas) */
  --accent-hover:      #3CC4D3;
  --accent-muted-bg:   rgb(41 178 194 / 0.14);
  --on-accent:         #04161A;  /* 7.3:1 on teal */

  --status-running:    #29B2C2;  /* teal, animated dot */
  --status-needs-you:  #FFC23D;  /* amber, static, distinct glyph (11.1:1) */
  --status-needs-you-bg: rgb(255 194 61 / 0.14);
  --on-amber:          #1A1300;
  --status-error:      #FF7A59;  /* coral, x glyph (7.0:1) */
  --status-error-bg:   rgb(255 122 89 / 0.14);
  --status-done:       #3DD68C;  /* green check: PR merged / completed */
  --status-idle:       #6F87A0;  /* dimmed hollow dot */
  --unread-dot:        #29B2C2;
}
```

### Light

```css
:root[data-theme="light"] {
  --bg-canvas:   #EDF1F6;  /* rail */
  --bg-panel:    #F5F7FA;
  --bg-raised:   #FFFFFF;
  --bg-hover:    #E6ECF3;
  --bg-selected: #D9E6F2;
  --terminal-bg: #0E2236;  /* keep the terminal dark in light mode; see below */

  --border-subtle: #E2E8EF;
  --border:        #CDD8E4;
  --border-strong: #A9BBCD;
  --focus-ring:    #0B7A88;

  --text-1: #0B1F33;  /* existing navy as ink: 15.6:1 */
  --text-2: #3E5670;  /*  7.1:1 */
  --text-3: #566D86;  /*  5.0:1 */

  --accent:          #0B7A88;  /* darkened teal for text/links on white (5.1:1); white on it 5.1:1 */
  --accent-hover:    #096A76;
  --accent-muted-bg: #DDF3F5;
  --brand-teal:      #29B2C2;  /* decorative fills only, never text */

  --status-running:   #0B7A88;
  --status-needs-you: #8F5B00;  /* amber ink for text (5.3:1) */
  --status-needs-you-fill: #FFC23D; /* chip fill, with --text-1 on it */
  --status-needs-you-bg: #FFF4D6;
  --status-error:     #B33C1E;  /* coral ink (5.2:1) */
  --status-error-fill:#FF7A59;
  --status-error-bg:  #FFE8E1;
  --status-done:      #177A47;
  --status-idle:      #8095AB;
}
```

Notes:

- **Amber means "needs you" and nothing else.** Don't reuse it for warnings, queued or in-progress (Primer does that, and Admiral shouldn't). Running is teal, error is coral.
- Pair each status with a glyph as well as a color (Carbon): running ● (pulsing), needs-you ◆ or "?", error ✕, done ✓, idle ○, exited ∙.
- Keep the terminal dark in both themes (most CLI agents' TUIs assume a dark background). Map xterm `ITheme` to: background `--terminal-bg`, foreground `#D6E2EE`, cursor `#29B2C2`, selection `rgb(41 178 194 / .30)`, ANSI yellow `#FFC23D`, ANSI red `#FF7A59`, ANSI cyan `#29B2C2`, ANSI green `#3DD68C`, ANSI blue `#5AA9F0`, ANSI magenta `#C792EA`.
- To get full 12-step ramps, run `#29B2C2` / `#5B7590` / `#0A1828` through <https://www.radix-ui.com/colors/custom> and pin the values above where they differ.

### Type

- **Primary pairing: Inter (UI) + JetBrains Mono (terminal, IDs, branches).** Inter is what Linear uses (Inter Display for headings). JetBrains Mono has excellent box-drawing glyphs and ligature control for agent TUIs. Use `font-feature-settings: "tnum", "cv11"` on Inter for counts and timers.
- **Alternative: Geist + Geist Mono** (OFL, 9 weights each). Tighter and more Swiss. Pick it if you want the Vercel feel.
- Sizes: 13px body and rows on desktop, 15px on touch (`@media (pointer: coarse)`), 12px meta, 11px uppercase-tracked group headers, 13px mono in the terminal (14px on iPad).

### Motion and touch

- Drawer: transform-only, about 300ms on desktop and about 450ms on touch, with Ionic-style easing `cubic-bezier(0.32, 0.72, 0, 1)` (Vaul). Flick to dismiss. Drag only from the drawer header so xterm keeps its own scrolling. Use `visualViewport` to keep the steering box above the iPad keyboard.
- Only "running" animates (a slow 2s pulse). Needs-you is static but high-contrast, so motion stays meaningful (Geist status dot, Claude Code agent view).
- Touch rows are 44–48px (Carbon lg). Desktop rows are 32px (Carbon sm). Switch on `pointer: coarse`, not viewport width, because an iPad with a keyboard and trackpad should get the dense layout.
