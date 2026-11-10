# Coding-agent orchestration apps: design references for Admiral

> Research for the Admiral redesign map, 2026-11-09.

Researched 2026-11-09. Every URL below loaded for me unless marked **(not verified)** or **(blocked to fetcher)**. Third-party reviews are labelled as such. Where I could not see a screenshot, the layout description comes from the vendor's text.

---

## 1. Which "-bot" product to model on

**Best identification: Grok Bot (also spelled "Grokbot"), xAI's managed agent app, which shares a Cursor account.** The timing also fits: it launched in mid-August 2026 and is the most talked-about agent-fleet product of the last three months. It is pitched as a fleet of named agents that delegate to each other, run on schedules and triggers, and hand coding work to Cursor cloud agents.

**Runner-up: Clawdbot, renamed Moltbot and then OpenClaw** (launched Nov 2025, renamed twice in late Jan 2026). It has a large following, and its new web Control UI is the closer design reference for Admiral. I cover both in depth below.

Evidence:
- Grok Bot: xAI's guide https://x.ai/bot/guides/grok-bot-101 (dated Sep 11, 2026); Cursor staff explain it at https://forum.cursor.com/t/confused-over-grok-bot/171745 (Sep–Nov 2026); review at https://latent.space/p/grok-bot; launch coverage at https://www.mindstudio.ai/blog/grokbot-xai-agent-app (Aug 15, 2026). Cursor's own changelog mentions Grok Bot as a consumer of apps and API keys (https://cursor.com/changelog, Nov 5, 2026).
- Clawdbot to OpenClaw rename: https://anotherwrapper.com/blog/clawdbot-is-now-openclaw (third-party; most sources say Moltbot on Jan 27, 2026 and OpenClaw on Jan 30, 2026).

The owner confirmed Grok Bot is the model. OpenClaw's Control UI stays the closer reference for a local web dashboard.

### 1a. Grok Bot (xAI / Cursor)

- **What it is:** a managed "agent with a computer". You create named Bots, each with a name, title and description, and each gets a persistent cloud computer (browser, terminal, filesystem). You talk to them like colleagues in an iMessage-style chat on desktop, iOS and Android.
- **Pages:**
  - Guide: https://x.ai/bot/guides/grok-bot-101
  - Product page: https://x.ai/bot **(blocked to fetcher, 403)**
  - Changelog: https://x.ai/changelog/bot **(blocked to fetcher)**
  - Reviews: https://latent.space/p/grok-bot, https://www.chatprd.ai/how-i-ai/how-i-ai-my-hands-on-review-of-grokbot-cursor-origin-and-the-grok-46-model, https://www.mindstudio.ai/blog/grok-bot-ai-agent-fleet/
- **Layout:** a list of Bots (left), each Bot is a chat thread (centre), and you can open the Bot's computer like a remote desktop and take over (for CAPTCHA or 2FA). There is a plugin panel, a "Teach a task" button (screen-record yourself and it becomes a skill), and a Routines section. Settings sit top right; permissions are under Settings > General > Agent.
- **Features worth borrowing:**
  - **Agents as named teammates.** Each has a role description, and a Bot that gets an out-of-scope task forwards it to the Bot whose description matches. A "Chief of Staff" Bot manages the others, and several Bots can share a group chat.
  - **Routines:** cron-like schedules plus event triggers (Slack message, GitHub event, Teams). They run while your devices are off.
  - **Permissions written as a prompt.** Rules are natural language. A separate reviewer agent allows, blocks or escalates each proposed action, with allow and block lists.
  - **Human handoff.** You can take over the desktop, or the agent sends you a secure form for a secret.
  - **Coding is delegated, not done in-app.** The Bot starts a Cursor cloud agent and opens a PR. A reviewer built an "Agentic Engineer" Bot that routes frontend work to Claude Code, debugging to Codex and simple tasks to Grok CLI (latent.space). That is close to Admiral's multi-harness idea.
  - **The same state on phone and desktop.**
- **Weaknesses that Admiral beats:**
  - No model picker, and usage and context are hidden.
  - All Bots share one computer and set of logins: "organizational boundaries, not security boundaries".
  - Unrelated tasks pile into one Bot thread.
  - It cannot drive local CLIs or TUIs: a Cursor staffer confirmed there is no CLI or TUI integration (Oct 12–13 in the forum thread).
  - Cloud-only, and it requires a Cursor plan (Ultra at launch).
  - Admiral runs real harness CLIs locally, has explicit fleets (Work vs Home credentials), concurrency caps and resource locks, and a live terminal you can attach to.

### 1b. OpenClaw (formerly Clawdbot, then Moltbot)

- **What it is:** an open-source (MIT), self-hosted assistant. A **Gateway** process on your machine routes 29 chat channels (WhatsApp, Telegram, Slack, iMessage and more) to agents, keeps sessions and memory, and runs cron jobs, hooks and webhooks. It ships a browser **Control UI** at `http://127.0.0.1:18789/`.
- **Pages:**
  - Homepage: https://openclaw.ai
  - Docs: https://docs.openclaw.ai/
  - Control UI docs: https://docs.openclaw.ai/web/control-ui
  - The best UI page, the 2026.8.1 "new web UI" release notes: https://docs.openclaw.ai/releases/2026.8.1/the-new-web-ui
- **Layout of the new Control UI (2026.8.1):**
  - Chat-first. The conversation is the main screen and the old Overview page is retired.
  - A resizable left sidebar remembers its width. It can be flat or grouped by project, person or custom group, supports Pinned chats and personal Sections, and folds worktrees back under their project.
  - Chat, Details and Discussion sit in resizable columns or merge into tabs. The dashboard, terminal and desktop open as focused views.
  - A docked Browser panel can annotate a page and attach the screenshot to the chat.
- **Features worth borrowing:**
  - **Session catalog.** Interactive **Claude Code sessions are listed** next to native ones, with sidebar groups for Codex and Claude Code. The new-session picker can start CLI agent sessions in a terminal. This is the closest any product comes to Admiral's model.
  - **New-session setup screen:** pick agent, model, reasoning level, workspace and **the machine that runs it**. That last one maps onto Admiral's fleets.
  - **Child-session tree in the sidebar.** Child sessions expand under the parent, the agent's own session shows as "Main Thread", and large trees stay compact. Compare Admiral's crew boats and subagents.
  - **Approvals appear inside the conversation that raised them**, with "a quiet indicator elsewhere" for attention. Closing the approval queue leaves a request pending instead of deciding it.
  - **Session actions:** Snooze (hide until a time), Fork, New child, Archive with undo. A failed remote start keeps the prompt visible with Retry.
  - **"Worked for …" collapse:** a finished turn folds its narration and tool calls under one line.
  - **Usage page:** context breakdown and provider plan limits.
  - **Mobile:** a compact header, a draggable drawer for navigation, and a reserved activity row so arriving subagent status does not shift the transcript.
  - Team mode shows presence ("Online", with running-session counts).
- **What Admiral does better:** work intake. OpenClaw has no order queue from GitHub, Jira or Linear, no autopilot with caps and locks, and no fleet credential separation. Also, according to the docs, a session's terminal is a view rather than the primary surface. Admiral's live, attachable tmux terminal is closer to how engineers actually drive CLIs.

---

## 2. Product-by-product

### Conductor (Melty Labs, YC)
- **What it is:** a Mac app that runs Claude Code, Codex, Cursor and OpenCode agents in parallel, one isolated workspace (git worktree and branch) each. There is now also Conductor Cloud and an iOS app.
- **Pages:** https://www.conductor.build · docs https://www.conductor.build/docs · workflow https://www.conductor.build/docs/concepts/workflow · changelog https://www.conductor.build/changelog (0.94.0 on Nov 9, 2026) · Dispatcher https://www.conductor.build/changelog/0.63.0-cursor-support-dispatcher · Sections and routines https://www.conductor.build/changelog/0.85.0-sections-routines-and-a-new-model-picker · iOS https://www.conductor.build/changelog/0.90.0-conductor-for-ios
- **Layout:** a left sidebar of workspaces grouped by repo, with user-defined **Sections** since 0.85. The centre is the agent chat; the right side has a terminal, a Diff Viewer (⌘⇧D) and a **Checks** tab (git status, CI, deployments, PR comments, todos).
- **Borrow:**
  - **Dispatcher** (⌘N): a quick-launch prompt box with a "create more" toggle, so you can fire off several tasks in a row.
  - Create a workspace from a branch, PR, GitHub issue or Linear issue.
  - Inline diff comments become attachments for the agent.
  - The PR lifecycle lives in-app: draft the PR, fix failing checks, answer review comments, merge, then archive with restore from History.
  - Routines (schedule or GitHub Action; cloud only).
  - A keyboard model picker.
  - The iOS app chats with agents and merges PRs (cloud workspaces only).
- **Admiral is better at:** multiple harnesses including Copilot CLI, external order sources and an autopilot queue, Linux and iPad web access with no cloud required, and fleets with separate credentials.

### Vibe Kanban (BloopAI), sunsetting
- **What it is:** an open-source (Apache-2.0) kanban board where each issue card launches a coding agent in its own worktree workspace, with diff review, inline comments and a built-in preview browser. BloopAI shut down on **April 10, 2026**. The project continues community-maintained and local-only.
- **Pages:** https://github.com/BloopAI/vibe-kanban · shutdown post https://vibekanban.com/blog/shutdown · screenshots https://github.com/BloopAI/vibe-kanban/blob/main/packages/public/vibe-kanban-screenshot-overview.png and .../vibe-kanban-screenshot-workspace.png **(not verified)**
- **Layout:** a kanban board of issues (custom status columns, priorities, sub-issues). Opening a card gives a workspace view with the agent log, diff and preview.
- **Borrow:** issue → workspace linkage, custom status columns, a preview browser with device emulation and inspect mode, AI-written PR descriptions.
- **Lesson:** the team blamed the shutdown on its business model ("couldn't find a business model"), not the UX. They also say they shipped multi-agent support and diff commenting before anyone else, and both are now table stakes.

### Claude Squad (smtg-ai)
- **What it is:** a terminal TUI (`cs`) that manages several Claude Code, Codex, Gemini, Aider or Amp sessions in tmux plus git worktrees. AGPL, about 8.8k stars.
- **Pages:** https://github.com/smtg-ai/claude-squad
- **Layout:** a session list on the left, with Preview and Diff tabs on the right (switched with `tab`) and a key-hint bar along the bottom. `↵` attaches to the tmux session and `ctrl-q` detaches.
- **Borrow:**
  - A single-key action vocabulary: n/N new, D delete, s commit and push, c checkout (pause), r resume.
  - An "autoyes" mode.
  - Profiles: named launch commands per harness.
- **Admiral is better at:** everything beyond one machine and one terminal. Claude Squad is the closest ancestor of Admiral's tmux model.

### Crystal, now Nimbalyst (Stravu)
- **What it is:** Crystal (a desktop multi-session Claude Code and Codex manager on worktrees) was deprecated in Feb 2026 and replaced by **Nimbalyst**. Nimbalyst is an open-source (MIT) "visual workspace" with a session kanban, a Monaco editor, diff review, visual editors (markdown, Excalidraw, mockups, data models) and a native iPhone app.
- **Pages:** https://github.com/stravu/crystal · https://nimbalyst.com · mobile https://nimbalyst.com/mobile-agent-management/ · vendor comparison https://nimbalyst.com/compare/nimbalyst-vs-conductor-vs-vibe-kanban/
- **Layout:** each session has a transcript, a kanban card and an optional worktree; plans, tracker items and sessions are cross-linked.
- **Borrow (mobile especially):**
  - A phone dashboard of active, completed and paused sessions, color-coded and sorted by recent activity.
  - Red/green diffs formatted for mobile, with **per-change approve or reject**.
  - **Resume a stalled session by voice note.**
  - **Push notifications** for completion, error and approval requests.
  - Queue work from the phone for the desktop.
- **Admiral is better at:** order sources, autopilot, and multi-harness support beyond Claude and Codex.

### Sculptor (Imbue)
- **What it is:** a desktop UI for Claude Code (Codex added Dec 2025) where every agent runs in its **own Docker container** instead of a worktree.
- **Pages:** https://imbue.com/sculptor · containers doc https://docs.imbue.com/features/containers **(not verified)**
- **Borrow:**
  - **Pairing Mode:** one click syncs an agent's container both ways with your local checkout, so you can test in your own IDE, then switch back.
  - The merge flow flags likely conflicts and sends them back to the agent.
  - "Suggestions" (beta) flag fixes the agent's code needs.
  - Fork-from-any-point is planned.
- **Admiral is better at:** it does not need Docker. Sculptor is Mac (Apple Silicon) and Linux only.

### Terragon, shut down
- **What it is:** a cloud background-agent orchestrator (Claude Code, Codex, Amp, Gemini) with sandboxes, auto-PRs, a `terry` CLI for local takeover, an MCP server, automations and Slack/GitHub @-mention triggers. **Shut down and open-sourced on Jan 16, 2026.**
- **Pages:** https://github.com/terragon-labs/terragon-oss
- **Borrow:** @-mention triggers, event automations ("on new issue or PR"), an MCP server so other agents can create tasks, and local takeover of a cloud task.

### Omnara, pivoted
- **What it is:** it started as a mobile and web command centre for Claude Code and Codex (YC S25; iOS app still listed at https://apps.apple.com/app/id6748426727). It has **pivoted** to an open-source durable-agent runtime and API, with approvals (Allow/Deny with who approved), an event timeline, and Slack/Discord/GitHub integrations.
- **Pages:** https://omnara.com · https://github.com/omnara-ai/omnara
- **Borrow:** per-tool policy (run / require approval / block), an audit trail of who approved what, and a streaming event timeline.

### Superset, now **Paradev**
- **What it is:** superset.sh now 308-redirects to paradev.com, and the page says "Superset is now Paradev". It is an MIT-licensed desktop app for running "100+" CLI agents in parallel (Claude Code, Codex, OpenCode, Cursor Agent, Gemini, Devin, Kiro), each in its own worktree, locally or in cloud sandboxes.
- **Pages:** https://paradev.com/ · https://paradev.com/agent-orchestration · docs https://docs.paradev.com **(not verified)** · code https://github.com/paradevai/paradev **(not verified)**
- **Layout** (the clearest match for the user's "project list + pane + terminal" idea):
  - **Left sidebar:** Workspaces, Automations and Pages, with workspaces grouped by *host* (desktop, cloud, gpu-box, mobile, cli). Each row shows status and diff counts.
  - **Centre:** the agent terminal.
  - **Right panel:** Files, Changes and Review tabs.
- **Borrow:**
  - Host grouping maps onto Admiral fleets.
  - A **terminal daemon owns sessions**, so restarting the app does not restart agents. This is the same idea as Admiral's long-lived tmux server.
  - A fleet view of running, waiting, done and what changed.
  - Automations are prompt + agent + host + schedule, and they fail clearly when the host is offline.
  - A CLI, SDK and MCP server.
  - "Pages": publish HTML output from an agent.
  - iPhone and iPad app.
- **Admiral is better at:** order intake from Jira, Linear and GitHub, autopilot caps and resource locks, and an inbox. Paradev's own docs warn that a "created" run only means the workspace exists.

### Cursor: Agents Window (Cursor 3)
- **What it is:** Cursor's agent-first window, GA on Apr 2, 2026 with Cursor 3. It runs many agents across local, worktree, cloud and SSH environments. 3.2 (Apr 24) added async subagents (/multitask) and multi-root workspaces. Recent changelog items: voice chat with agents (Nov 6), live apps (Nov 5), Agent SDK with cron and webhook triggers (Oct 20), and Origin (a code host).
- **Pages:** https://cursor.com/docs/agent/agents-window · https://cursor.com/changelog · guide (third-party) https://www.digitalapplied.com/blog/cursor-3-agents-window-complete-guide
- **Layout:** one sidebar holding every agent, whether started from local, cloud, mobile, web, Slack, GitHub or Linear. It is multi-workspace and has a diffs/PR view. The classic editor is a separate window that can sit alongside.
- **Borrow:**
  - `/best-of-n`: run one task on several models and compare.
  - Cloud↔local handoff.
  - `/autopilot` on a PR.
  - Cloud agents attach demo screenshots or video as proof.
  - Voice check-ins ("what changed in this diff?").
- **Admiral is better at:** being vendor-neutral and running harness CLIs as they are.

### OpenAI Codex app (now folded into the ChatGPT/Codex desktop app)
- **What it is:** a macOS "command center" for parallel Codex agents (Feb 2026). Each thread sits in a project and can use a worktree. Developer docs now redirect to learn.chatgpt.com.
- **Pages:** https://learn.chatgpt.com/docs/app · scheduled tasks https://learn.chatgpt.com/docs/automations?surface=app · features https://learn.chatgpt.com/docs/features · launch post https://openai.com/index/introducing-the-codex-app/ **(blocked to fetcher, 403)**
- **Layout:** a sidebar with New chat, Pinned, Projects, Recents and **Scheduled**. A thread is the main pane.
- **Borrow:**
  - **The Scheduled view works as an inbox.** Runs with findings land there with an unread dot, and each has "Run now".
  - Each scheduled task chooses "standalone (new chat each run)" or "inside a chat (keeps context)", and "local checkout" or "background worktree". The docs warn about worktree pile-up and suggest archiving.
  - RFC 5545 rrule editing.
  - Event triggers (Gmail, Slack, GitHub PR), on web and mobile only.

### GitHub: Agent HQ, mission control and the Agents panel
- **What it is:** GitHub's cross-vendor agent layer, announced Oct 28, 2025. Copilot plus Claude, Codex and others are assigned from the Agents tab or panel on github.com, VS Code, CLI and Mobile.
- **Pages:** https://github.blog/news-insights/company-news/welcome-home-agents/ · https://docs.github.com/en/copilot/how-tos/copilot-on-github/use-copilot-agents/manage-and-track-agents · https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent
- **Layout:** an agents panel reachable from any GitHub page, plus a repo **Agents** tab ("All sessions"). Selecting a session shows its log and an overview (progress, **token usage, duration**) with a steering box below.
- **Borrow:**
  - Steering is applied after the current tool call.
  - Stop keeps the commits already pushed.
  - Archive.
  - Cloud sessions are shared by default and local ones by opt-in.
  - "Agent Merge / Watch and fix" on a PR.
  - Remote control of a local CLI session.
  - Repo automations from the Agents tab.
  - A governance and audit control plane.
- **Note:** steering is not available for third-party agents.

### Google Jules, shutting down
- **What it is:** Google's async cloud coding agent: plan approval, diff, PR, scheduled tasks, suggested tasks, CLI and API. **The Oct 29, 2026 changelog announces a wind-down on Dec 31, 2026; it goes read-only after Nov 11.**
- **Pages:** https://jules.google · docs https://jules.google/docs · changelog https://jules.google/docs/changelog/
- **Borrow:**
  - **Plan-approval gate**, with a "Planning Critic" agent that reviews plans running without human approval.
  - **Suggested Tasks**: the agent proposes work (TODOs, performance issues) from the repo.
  - CI Fixer.
  - A commit-authorship setting.
  - Edit, pause and resume scheduled tasks.
  - Daily task caps and concurrency caps by plan (15/3, 100/15, 300/60): a concurrency model like Admiral's.

### Devin (Cognition)
- **What it is:** a cloud agent with a session view (chat plus shell, IDE and browser tools), Devin Review, automations, a CLI with `/handoff`, Devin Desktop and mobile.
- **Pages:** https://docs.devin.ai · release notes https://docs.devin.ai/release-notes/overview.md · https://app.devin.ai
- **Layout:** a session sidebar. Since Sep 25, 2026 its **status groups are Working / Ready / Blocked / Inactive**. Hovering a row shows a preview card (status, latest reply, PRs, working directory). Child sessions are hidden under the parent behind "Reveal child sessions". You can **filter by Origin (Slack, Web, API, Jira, Linear)**.
- **Borrow:**
  - Those four status groups.
  - Hover preview cards.
  - The origin filter, which maps directly onto Admiral's order sources.
  - A "PRs needing you" list.
  - Automations with a preflight script.
  - Analytics: ACUs per merged PR, a cost-per-outcome metric.

### Factory (Droids)
- **What it is:** the Droid CLI plus the Factory app (desktop, web and mobile). **Missions** turn a goal into a milestone plan, and **Mission Control** runs it with orchestrator, worker and validator agents. Domain moved to factory.com.
- **Pages:** https://docs.factory.com/ · https://docs.factory.com/missions/overview
- **Borrow:**
  - Plan → approve → Mission Control.
  - **Per-feature validation by automated QA, with self-correction.**
  - You can pause, redirect or ask the orchestrator to recover.
  - Troubleshooting states (frozen mission, stuck milestone, retry-limit warning).
  - Sacra (third-party) reports missions running up to 16 days.

### Warp (Oz)
- **What it is:** a terminal plus Oz, a cloud agent platform that runs Warp Agent, **Claude Code and Codex** under one control plane ("a single pane of glass", May 19, 2026).
- **Pages:** https://www.warp.dev/blog/multi-harness-cloud-agent-orchestration · https://docs.warp.dev/agent-platform/cloud-agents/managing-cloud-agents
- **Layout:** an Agent Management Panel in the app and a Runs page on oz.warp.dev (works on mobile), with Personal and All tabs.
- **Borrow:**
  - A clean **status set: Working, Blocked 🟨, Canceled, Failed 🔺, Success ✅**.
  - A **Source column** (Interactive, CLI, API, Slack/Linear, Scheduled).
  - Duration.
  - Filters by source, day, creator and status.
  - Child agents show as pills above the parent (local) or as their own rows or a Sub-agents tab (cloud).

### Amp (Sourcegraph / Amp Inc.)
- **What it is:** a coding agent. Its 2026 work is on multi-agent UI: the Agents Panel (Jan 9), Agents Everywhere for web, CLI and mobile (Jun 4), agent-to-agent messaging (Jul 17), iOS/macOS (Aug 28), Android (Oct 27), and **Spaces (Nov 6)**.
- **Pages:** https://www.ampcode.com/news · https://ampcode.com/news/agents-panel · https://ampcode.com/news/spaces
- **Borrow:**
  - **Spaces**: a shared, team chat-style feed with *one coordinating agent* that creates threads, delegates, and reports back. A **side panel lists the space's threads grouped by status**, and the agent keeps a "ledger" artifact current. This is very close to Admiral's chat assistant plus ships, made team-visible.
  - Agents Panel keys: j/k move, Space peeks at the last message, Enter opens, e archives, Esc toggles list and input.

### Charlie (Charlie Labs), shutting down
- **What it is:** a TypeScript cloud agent for GitHub, Linear and Slack that pivoted to **Daemons**: always-on, role-scoped agents defined in `.agents/daemons/<name>/DAEMON.md` (frontmatter for watch conditions, cron schedule and deny rules; body for policy and escalation). The homepage now says Charlie is shutting down.
- **Pages:** https://www.charlielabs.ai
- **Borrow:** describing a standing agent role as a checked-in Markdown file with triggers and caps (for example "add labels, never remove, max 20 per sweep").

### Claude Code: desktop app, web, Projects
- **What it is:**
  - The redesigned desktop Code tab (Apr 14, 2026) for parallel sessions.
  - Cloud sessions at claude.ai/code.
  - Scheduled tasks (formerly routines).
  - Remote Control of local sessions.
  - **Projects** (public beta): one coordinating conversation that starts and tracks parallel threads.
- **Pages:**
  - Redesign: https://claude.com/blog/claude-code-desktop-redesign
  - Desktop docs: https://code.claude.com/docs/en/desktop
  - Cloud and web: https://code.claude.com/docs/en/claude-code-on-the-web
  - Projects: https://code.claude.com/docs/en/claude-projects
  - Scheduled tasks: https://code.claude.com/docs/en/routines
- **Desktop layout:**
  - A sidebar of sessions you can group and sort, with filters by status, project and environment.
  - Option-click for split view.
  - Drag-and-drop panes: chat, diff, terminal, browser/preview, file, plan, tasks, subagent, iOS Simulator.
  - A details rail with cards (where it runs, Changes +/−, tasks, context usage).
  - **Auto-archive when the PR merges or closes.**
  - PR CI status with Auto-fix and Merge-when-ready.
  - Side chat (⌘;).
  - Verbose / Normal / Summary view modes.
  - OS notification when a session finishes and you aren't looking at it.
- **Projects Overview pane:** threads grouped as **Ready for review / Waiting on you / Working / Landing / Idle / Resolved**. Thread cards in the conversation carry next-step buttons: **Resolve conflicts, Fix CI, Address comments, Merge it, Review PR, Create PR**. Other features:
  - "Suggested threads" with Start all.
  - A dot on Overview when something waits on you.
  - A coordinator model and thread model set separately.
  - Per-thread usage.
  - Preferences such as "run at most two threads at a time" are kept in memory but explicitly **not a hard cap**.
- **Admiral is better at:** **hard** concurrency caps and resource locks (Projects is soft only), local multi-harness support, external order sources, and fleets.

### Zed agent panel
- **What it is:** Zed's agent panel. It runs Zed Agent or external ACP agents, has a Threads Sidebar grouped by project, Worktree Isolation per thread, and Terminal threads.
- **Pages:** https://zed.dev/docs/ai/agent-panel
- **Borrow:**
  - Review Changes as a multi-buffer with accept or reject per hunk.
  - "New From Summary" to restart a thread lean.
  - Messages queue while the agent works, with a Steer toggle.
  - `notify_when_agent_waiting`, a sound when done, and `prevent_idle_sleep`.

### Linear agents and coding sessions
- **What it is:** delegate an issue to an agent from the Assignee field (A); the human stays the owner and the agent is the "delegate". Since Jun 11, 2026, **Linear Agent coding sessions** run Claude Code or Codex in the cloud and return a draft PR.
- **Pages:** https://linear.app/changelog/2026-06-11-coding-sessions · https://linear.app/docs/coding-sessions **(404 to fetcher; the search index lists it)**
- **Borrow:**
  - **Owner vs delegate** semantics.
  - A session card with a status row on the issue.
  - Steer by replying in the thread.
  - A completion notification to the delegator.
  - Agent activity types (thought, tool call, clarification prompt, final response, error) from the developer API.

### Others discovered
- **Gas Town (Steve Yegge, Jan 1, 2026).** A Go and tmux orchestrator for 20–30 Claude Code or Codex agents, very close to Admiral's architecture.
  - Pages: https://github.com/steveyegge/gastown · hosted beta https://blog.kilo.ai/p/gas-town-by-kilo
  - Roles: Mayor (coordinator), Polecats (workers), Witness (health), Refinery (**merge queue**).
  - Work is tracked in Beads (a git-backed issue tracker) and grouped into Convoys.
  - `gt dashboard`: an htmx web page of agents, convoys, queues and escalations with a command palette.
  - `gt feed` TUI: Agent Tree, Convoy Panel and Event Stream, plus a **Problems view grouping agents as Stalled / Zombie / Working / Idle** with nudge and handoff keys.
  - **Borrow the Problems view and the merge queue.**
- **Kilo, Happy and Grass** are mentioned as mobile Claude Code remotes in third-party roundups. I did not verify them.

---

## 3. Synthesis

### Recurring layout patterns
1. **Three columns: sessions, agent, review** (Conductor, Paradev, Claude desktop, OpenClaw, Codex). A left sidebar lists sessions or workspaces grouped by project, host or user-defined section. The centre holds the agent chat or terminal. A right rail holds Changes/Diff, Checks/CI, Files and a terminal tab. This is the dominant professional pattern, and it matches the user's "project list + pane + fly-out terminal" idea.
2. **Status-grouped lists instead of free-form canvases.** These products converge on roughly five buckets:
   - Devin: Working / Ready / Blocked / Inactive.
   - Claude Projects: Ready for review / Waiting on you / Working / Landing / Idle / Resolved.
   - Warp: Working / Blocked / Failed / Success / Canceled.
   - Gas Town: Stalled / Zombie / Working / Idle.
3. **A coordinator chat above workers** (Claude Projects, Amp Spaces, Grok Bot Chief of Staff, Factory Mission Control, Gas Town Mayor). You talk to one agent, it spawns threads, and thread cards with status and next-step buttons appear inline in the chat.
4. **Kanban is fading.** Vibe Kanban (the kanban-first product) shut down its company. Nimbalyst keeps a board, but the leaders use status-grouped lists. A board view is a secondary mode at most.
5. **Approvals live in the thread that raised them**, plus one global "needs you" indicator (OpenClaw, Claude Projects dot, Devin "PRs needing you", Codex Scheduled unread dot).
6. **Mobile is a triage surface, not a workstation.** The phone apps (Nimbalyst, Conductor iOS, Amp, GitHub Mobile, Claude app) offer a session list, mobile diffs with approve or reject, a reply or voice note, push notifications and merge.
7. **Collapse the transcript by default** ("Worked for…", Verbose / Normal / Summary). The raw terminal is one click away rather than the default view.

### Feature gap list for Admiral, ranked by value
1. **A status-grouped ship list with a single "Needs you" count.** Suggested buckets: Needs you / Working / Ready for review / Landing / Idle / Done. Replace the ocean map with this as the home view, and fold the inbox items into each ship's row as badges. (Devin, Claude Projects, Warp.)
2. **The PR lifecycle on each ship.** A Changes card (+/−), CI checks, next-step buttons (Fix CI, Address comments, Resolve conflicts, Merge), auto-fix watch, and auto-archive when the PR merges. (Claude desktop and Projects, Conductor Checks, GitHub Agent Merge.)
3. **A diff pane with inline comments sent back to the harness** as the next message, plus per-hunk accept or reject. (Conductor, Claude, Zed, Vibe Kanban.)
4. **Push and OS notifications** for needs-you, error and done, with a per-fleet or per-project setting. Admiral works on iPad and phone but has no push yet. Web Push or a PWA would cover it. (Nimbalyst, Zed, Claude.)
5. **Fly-out terminal plus a summarized transcript.** Default to a collapsed activity summary; the live tmux terminal opens as a resizable right or bottom drawer. Keep Admiral's attach as the escape hatch, since no competitor offers a real attachable TUI this cleanly.
6. **Origin and source filter, and a Dispatcher-style quick launch.** Filter ships by order source (GitHub, Jira, Linear, freeform); launch with ⌘N and a "create more" toggle. (Devin origin filter, Warp Source column, Conductor Dispatcher.)
7. **Scheduled and triggered orders** (routines): cron or event triggers create orders that autopilot picks up, with a Scheduled inbox for runs that have findings. (Codex, Claude, Conductor, Grok Bot.)
8. **A coordinator mode for AdmiralBot.** Let the chat assistant spawn ships, show inline ship cards with status, and keep a ledger. Unlike Claude Projects, Admiral can enforce the caps. (Claude Projects, Amp Spaces.)
9. **Cost and usage per ship and per fleet**: tokens, duration, plan-limit headroom. (GitHub overview, Claude Projects usage, OpenClaw Usage, Devin ACUs/PR.)
10. **Best-of-N and compare**: launch one order on 2–3 harnesses and compare the diffs side by side. (Cursor /best-of-n, Crystal.)
11. Smaller items: hover preview cards; snooze; fork or "new from summary"; a stalled/zombie detector (Gas Town Problems view); a plan-approval gate before execution (Jules, Factory).

### What Admiral already does better than nearly all of them
- **Hard concurrency caps and resource locks in autopilot.** Claude Projects says outright that its caps are not enforced.
- **Pluggable order sources** (GitHub, Jira, Linear, freeform) feeding a queue. Others only start from an issue one at a time.
- **Fleets as named credential and setup sets** (Work vs Home). Grok Bot and OpenClaw share logins across agents.
- **Three harnesses, including Copilot CLI, running locally in tmux, with a live attachable terminal.** Survives app restarts, which Paradev only recently matched.
- **Local-first web UI that works on desktop, iPad and phone with no cloud account.** Conductor's mobile app only works with its cloud workspaces.

### The 9 links most worth the user's time
1. https://code.claude.com/docs/en/claude-projects: the Overview pane's status groups, inline thread cards and next-step buttons. The best model for "orders → ships → inbox".
2. https://docs.openclaw.ai/releases/2026.8.1/the-new-web-ui: a professional local web Control UI with a sidebar, child-session tree, in-thread approvals and a mobile drawer. Likely the "-bot" product.
3. https://x.ai/bot/guides/grok-bot-101: Grok Bot's named-agent fleet, routines, prompt-based permissions and reviewer agent. The other "-bot" candidate.
4. https://paradev.com/: Superset, now Paradev. The cleanest picture of a sidebar grouped by host, a centre terminal and a right-hand Changes/Review layout.
5. https://claude.com/blog/claude-code-desktop-redesign: drag-and-drop panes, status filters, auto-archive on merge.
6. https://www.conductor.build/docs/concepts/workflow: the workspace → diff comments → Checks → PR → archive loop.
7. https://docs.devin.ai/release-notes/overview.md: dense, dated UI decisions (status groups, origin filter, hover cards, child-session reveal).
8. https://ampcode.com/news/spaces: a coordinator agent feed with threads grouped by status and a ledger artifact.
9. https://github.com/steveyegge/gastown: the nearest architectural sibling (Go, tmux, merge queue, Problems view).
