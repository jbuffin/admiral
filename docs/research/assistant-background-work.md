# Assistants that run work in the background and report back

Research for [#656](https://github.com/jbuffin/admiral/issues/656), on the map [#653](https://github.com/jbuffin/admiral/issues/653), November 2026.

The question: how do ChatGPT, Claude Duet and similar assistants hand work to background agents, run several at once, and bring the results back through the main agent? Which of their patterns fit AdmiralBot, a coordinator that does no work itself and acts by launching ships ([ADR 0023](../adr/0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md))?

This adds to [`redesign-similar-products.md`](redesign-similar-products.md), which covers Grok Bot, the Codex app and other agent dashboards, and to [`admiralbot-context.md`](admiralbot-context.md), which covers the ledger and the brief. Neither is repeated here.

Marks: **primary** is the vendor's own page, read in full. **primary (snippet)** is the vendor's own page, seen only as a search-result excerpt, because `help.openai.com` and `openai.com` returned 403 to the fetcher. **secondary** is press or a third party.

## Short answer

Every product that does this well has the same shape: **one conversation with a coordinator, many child runs under it, each child with its own status row, and results that come back as a short message plus a link to a real thing** (a file, a thread, a transcript). The closest matches to AdmiralBot are Claude's **Dispatch** (one conversation; it splits the work into separate Cowork or Code sessions and does not do it itself) and OpenAI's **dots** (an always-on agent whose background agents report to it, and it decides what reaches you). Claude Duet and ChatGPT's scheduled tasks add the proactive half: periodic checks, a morning brief, and "message me only when it matters".

## Products

### Claude Duet (Anthropic, research preview since 2026-10-21)

- Runs on "its own computer in the cloud, so it can keep working after you close the app" ([support: get started](https://support.claude.com/en/articles/17631495-get-started-with-claude-duet), primary). "Describe a task in a short message, and it works out which apps to use and what steps to take in the background" ([Introducing Claude Duet](https://claude.com/resources/articles/claude-duet), primary).
- **One thread.** "You can message it through typing or voice in one ongoing thread" (announcement). "Your main thread stays in one place, so Claude Duet picks up where you left off. You can also start separate channels for different topics, and they share the same memory" (support). It "carries over your memory, skills, and connected apps from your Claude account" (announcement).
- **Several things at once.** The product page's sample message reads "5 things in progress and 2 that need you" ([product page](https://claude.com/product/claude-duet), primary). No page says whether it uses sub-agents.
- **Reporting back.** "You'll receive a notification when the task is complete or needs your review" (announcement). It "posts there when a task is done or needs a decision from you" (support). "Bigger results come back as files you can review and edit"; finished work "appears in the Make tab" (support). The product page: "returns finished work like a doc, sheet, or deck for your review". Its sample replies end with an offer: "Want me to dig in?"
- **Proactive.** "Checks your connected accounts every few hours" and "messages you only when something needs your attention"; "each morning, it sends you a short brief on the most important items across your connected apps" (announcement). The support page warns that "something urgent can arrive between checks".
- **Approvals.** "It shows you exactly what it's about to do and waits for your approval" before sending, paying, signing or deleting (announcement).
- **Cost.** It uses the plan's usage limits, "including any five-hour session limit"; "its background checks count toward those limits" (support, product page).
- Unlike AdmiralBot, Duet does the work itself, in its own browser.

### Claude Dispatch, in Cowork (Anthropic)

The closest match to AdmiralBot. All from [Run tasks in the background with Dispatch](https://claude.com/docs/cowork/guide/dispatch), primary, unless marked.

- "A long-running agent in Cowork that takes high-level instructions and carries them out in the background. You describe an outcome in a single conversation; the Dispatch agent breaks it into tasks, runs each one as a separate Cowork or Code session, and surfaces the results in the sidebar when they finish."
- "You converse with one Dispatch agent, but it can run many child tasks beneath that conversation. Child tasks don't spawn further children of their own."
- **Routing.** Coding work runs in Code "against a workspace you've already set up"; knowledge work runs in Cowork in a named project. "If you don't [say which], it lists what's available and chooses."
- **Child statuses**, each a row in the sidebar: Running, Awaiting input, Awaiting answer, Completed, Error, Archived. "Select any task to open its full transcript, the steps Claude took, and any files it produced."
- **Approvals are forwarded, with a timeout.** "When a child task needs permission … the prompt is forwarded to you. If you don't respond within ten minutes, the request is automatically denied and the task continues without that action."
- **Follow-up.** Open a child "to read the transcript, send follow-up messages, or ask the Dispatch agent to start a new task that builds on the result."
- **Phone.** The desktop is the host; the mobile app starts and reviews Dispatch work. Claude sends a push "when a task is done or needs your go-ahead" ([support: assign tasks from anywhere](https://support.claude.com/en/articles/13947068-assign-tasks-from-anywhere-in-claude-cowork), primary (snippet)).

### Claude Code: background subagents and agent view (Anthropic)

- Subagents started from the main conversation run in the background by default; their "results reach Claude as a completion notification in a later turn. Claude waits for that notification before reporting the subagent's results", and if asked about progress says it is still running ([sub-agents](https://code.claude.com/docs/en/sub-agents), primary (snippet)). The coordinator does not guess at unfinished work.
- **Agent view** (`claude agents`, research preview) is a table of background sessions ([agent view](https://code.claude.com/docs/en/agent-view), primary). "Every prompt you enter here starts its own new session"; also `claude --bg "<task>"`. States: Working, Needs input, Idle, Completed, Failed, Stopped.
  - "The one-line summary in each row is generated by a Haiku-class model so the row can tell you what the session is doing, what it needs, or what it produced without opening the transcript", rewritten at the end of each turn.
  - A **peek panel** shows "the session's most recent output, or the question it's waiting on, rather than the full transcript"; you reply from it, or attach for the whole conversation. "Detaching never stops a background session."
  - Notifications fire "when a local background session starts needing your input, finishes, or fails"; scheduled sessions "notify only when they need your input". They fire the `Notification` hook with `agent_needs_input` or `agent_completed`.
  - "Each session uses your subscription quota independently."

### Claude Research, and how Anthropic built it

[How we built our multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system), primary:

- "An orchestrator-worker pattern, where a lead agent coordinates the process." "The lead agent spins up 3-5 subagents in parallel rather than serially."
- **Scale to the ask.** "Simple fact-finding requires just 1 agent with 3-10 tool calls"; "complex research might use more than 10 subagents with clearly divided responsibilities."
- **The brief.** "Each subagent needs an objective, an output format, guidance on the tools and sources to use, and clear task boundaries." "Without detailed task descriptions, agents duplicate work, leave gaps, or fail to find necessary information."
- **References, not relayed text.** "Subagents call tools to store their work in external systems, then pass lightweight references back to the coordinator", which avoids a game of telephone through the lead.
- **Plan outside the context.** The lead saves "its plan to Memory to persist the context".
- **Known limit.** "Our lead agents execute subagents synchronously, waiting for each set of subagents to complete before proceeding", so one slow worker stalls the batch.
- Most Research reports finish in 5 to 15 minutes, up to 45 for complex ones ([claude.com/blog/research](https://claude.com/blog/research), primary (snippet)). No page found says whether Research notifies on completion.

### ChatGPT

OpenAI's help center and site blocked the fetcher, so this section rests on search excerpts of OpenAI's own pages.

- **ChatGPT agent** (2025) ran on a virtual computer; "if you have the ChatGPT app on your phone, it will send you a notification when it's done with your task". It "requests permission before taking actions of consequence"; you can interrupt, take over the browser, ask for a progress summary, or stop it and keep partial results ([Introducing ChatGPT agent](https://openai.com/index/introducing-chatgpt-agent/), [help: ChatGPT agent](https://help.openai.com/en/articles/11752874-chatgpt-agent), primary (snippet)). The help center now says agent mode is no longer available and points to **ChatGPT Work** for long multi-step tasks (primary (snippet)).
- **Deep research.** Before a run, "ChatGPT may ask clarifying questions to confirm your goals. You can review and edit its proposed research plan"; you can "view progress in real time, interrupt the research to adjust focus, and update which sources the research can access". In Chat it proposes a plan; "in Work or Codex, ChatGPT will ask you clarifying questions once you provide the initial prompt". Runs take 5 to 30 minutes, and "you'll get a notification when your report is ready" ([help: deep research](https://help.openai.com/en/articles/10500283-deep-research-in-chatgpt), primary (snippet)). Community threads report stalled runs with no result (secondary).
- **Scheduled tasks** (the Scheduled page, 2026-06-17) replaced Pulse: one page lists active tasks and their next run, with pause, resume, edit and delete. Monitoring tasks "check connected apps for changes and notify users only when there is something worth reporting". No more than once an hour; unattended tasks may pause after inactivity; Free users get three. Since 2026-08-25, tasks in ChatGPT Work can fire on webhooks: new Gmail, Slack messages, GitHub PR activity ([release notes](https://help.openai.com/en/articles/6825453-chatgpt-release-notes), [help: scheduled tasks](https://help.openai.com/en/articles/10291617-tasks-in-chatgpt), primary (snippet)). Delivery is push, email or both, set in Settings → Notifications (primary (snippet)).
- **Subagents in ChatGPT Work and Codex** ([subagents](https://learn.chatgpt.com/docs/agent-configuration/subagents), primary): "ChatGPT or Codex handles orchestration across agents, including spawning new subagents." "Codex waits until all requested results are available, then returns a consolidated response." "Return summaries from subagents instead of raw intermediate output." A cap, `agents.max_concurrent_threads_per_session`. Steering goes through the main agent: "Ask Codex directly to steer a running subagent, stop it, or close completed subagent threads"; the web sidebar "doesn't provide controls to stop or steer an individual subagent".

### OpenAI dots (2026)

OpenAI's answer to Duet, and the closest OpenAI analogue of AdmiralBot. From [Meet dots](https://learn.chatgpt.com/docs/dots) and [Tasks and memory](https://learn.chatgpt.com/docs/dots/tasks-and-memory), primary.

- "An always-on agent that keeps work moving and asks for your input when a decision needs you." It "lives in the cloud and has its own computer and browser".
- **Fan-out.** It "can divide work among background agents that run in parallel and report back to it. You can keep talking to your dot while they work." It "can also create separate, visible threads and send follow-up instructions." "Cloud threads appear as separate conversations in the desktop, web, and mobile apps."
- **The dot filters what reaches you.** Background agents report to the dot, which "brings back results and turns to you for decisions that need your judgment". You tell it "which changes deserve a notification", for example "Message me in ChatGPT only if a deadline is at risk or you need a decision."
- **Proactive research is read-only.** It "doesn't send messages, change connected apps, or control a browser or computer"; "any follow-up action is subject to the permissions and approvals that apply to that work." Before an action affects accounts or shares information, an auto-review decides whether it may proceed.
- **Schedules are separate objects.** "Work that repeats at fixed times needs a saved schedule", naming what to check, when, notification rules and where results go. Stopping active work and cancelling a schedule are separate actions.
- **Its own notes**, "separate from ChatGPT's saved memory", covering preferences, decisions and ongoing work.
- **Done is not success.** "A completed run doesn't by itself confirm that the requested result was achieved or delivered."

### Gemini (Google)

- **Deep Research**: you can leave the chat; when the report is ready, "Gemini will notify you in the web app, next to the chat thread, and in the mobile app as a device notification". The help page caps how many research requests run at once ([help: Deep Research](https://support.google.com/gemini/answer/15719111), primary (snippet)).
- **Tasks** (formerly Spark): "A schedule lets you hand over tasks to Gemini so they run automatically in the background", by time or "in response to an event". "Up to 15 tasks running at a given time"; "a schedule will not run if there are already 15 tasks running". It shows "what it's planning to do after you submit a prompt, its progress as it works", in a work panel in the task's thread, and asks before certain actions ([help: Tasks](https://support.google.com/gemini/answer/16596215), primary).

Grok Bot is covered in [`redesign-similar-products.md`](redesign-similar-products.md).

## Patterns that fit a coordinator that does no work itself

Each line names the products that show it, then what it would mean for AdmiralBot in Admiral's words.

1. **One conversation, many child runs under it.** Dispatch, dots, Duet, agent view. AdmiralBot's chat stays the one place the engineer talks; each launch is a ship with its own row, conversation and status. Dispatch's "child tasks don't spawn further children" is a clean rule; Admiral's version is that a ship may bring crew, but AdmiralBot sees only the ship.
2. **Brief each worker properly.** Anthropic's research system: objective, output format, tools and sources, boundaries. Vague briefs cause duplicate work and gaps. AdmiralBot's launch text should carry what "done" looks like (a PR, a diff, a written answer) and what is out of scope, not just the engineer's sentence.
3. **Clarify or show the plan before launching.** ChatGPT deep research (clarifying questions, editable plan), Gemini Tasks (shows its plan). For AdmiralBot, an unclear request becomes a chat question with the proposed launches before any ship sails; a clear one launches at once, as ADR 0023 already says.
4. **Scale the fan-out to the ask.** One worker for a lookup, 3 to 5 in parallel for broad work (Anthropic). AdmiralBot chooses between a scout ship, a quick ship and a ship with crew the same way, and splits independent parts into separate ships.
5. **Results arrive as an event in the coordinator's next turn; it never guesses.** Claude Code subagents, Codex, dots. When a ship sails off, Admiral wakes AdmiralBot with the outcome; until then AdmiralBot says the ship is still out.
6. **Return a reference, not relayed text.** Anthropic ("lightweight references"), Duet (files in the Make tab), Dispatch (transcript and files), dots (separate threads). AdmiralBot's report is a short line plus links to the PR, diff or the ship's conversation. It does not paste the ship's output into its own context.
7. **A one-line summary per child, made by a small model.** Agent view rewrites each row's line with a Haiku-class model at the end of every turn. Admiral could do the same for ship rows and for AdmiralBot's report cards, outside AdmiralBot's context.
8. **The coordinator filters what reaches the person, by rules the person sets.** dots ("message me only if a deadline is at risk or you need a decision"), Duet ("only when something needs your attention"), ChatGPT monitoring tasks. Admiral's version: per-fleet notification rules for AdmiralBot's reports, so a finished scout ship lands in the chat but only a PR or a question pushes to the phone.
9. **Approvals and questions go straight from the worker to the person.** Dispatch forwards a child's permission prompt to you (denied after ten minutes); agent view's peek panel answers a session in place. This matches ADR 0023: AdmiralBot never answers harness approvals. The ship's stop goes to the inbox; AdmiralBot may mention it, with a link.
10. **The person can reach any child directly, and steer through the coordinator.** Dispatch (open the child and follow up), agent view (attach, detach without stopping), Codex (steer by asking the main agent). Both routes fit: the engineer boards the ship, or tells AdmiralBot to give a finished turn its next prompt.
11. **"Completed" is not "succeeded".** dots says so in its docs. AdmiralBot should check the outcome against the brief (a PR exists, checks pass) before it reports success.
12. **Proactive work is read-only and scheduled; acting needs the usual approvals.** Duet checks every few hours and sends a morning brief; dots' proactive research cannot act; ChatGPT's scheduled tasks and Gemini Tasks run on time or on events. In Admiral these are commitments and routines, and a digest of what a fleet did is a scheduled read whose output is a chat message.
13. **Caps are visible.** Gemini allows 15 running tasks and skips a schedule over the cap; Codex has `max_concurrent_threads_per_session`; ChatGPT allows tasks at most hourly; Duet and agent view charge each run to the plan. Admiral already has the global cap and the plan-window Limit; AdmiralBot should say when a launch is waiting on them.
14. **Planning state lives outside the coordinator's context.** Anthropic saves the lead's plan to memory; dots keep their own notes. This is AdmiralBot's ledger.

## Where the products differ from AdmiralBot

- Duet, dots and ChatGPT's agent do the work themselves, in a cloud browser. AdmiralBot does none; every piece of work is a ship. Dispatch is the only one that, like AdmiralBot, only splits and routes.
- They run in the vendor's cloud and keep going with every device off. Admiral runs on the Mac (ADR 0001), like Dispatch, whose desktop "must stay awake".
- Duet keeps one main thread plus channels that share memory. Admiral keeps one AdmiralBot per fleet with separate ledgers, because fleets never mix.

## Sources not opened

- `help.openai.com` and `openai.com` pages: 403 to the fetcher; used from search excerpts as marked.
- Secondary coverage of Duet seen in search only: MacRumors (2026-10-21), Engadget, tbreak, cellcog. One (cellcog) claims Ultra access in conflict with others; a search excerpt of Anthropic's pages lists Pro, Max and Ultra.
