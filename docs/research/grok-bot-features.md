# What Grok Bot can do today

> Research for the map "about adding more useful power to AdmiralBot", 2026-11-15. It adds to `redesign-similar-products.md` §1a, which was written from the Sep 11 guide and secondary reviews before xAI published its Grok Bot docs. Where the two disagree, this note wins.

Question: what can xAI's Grok Bot do as of November 2026? The coordinator, background and parallel work, routines, connectors, memory, what it does on its own, permissions, and above all how results come back to the user through the main agent.

Sources are xAI's docs at `docs.x.ai/grok-bot/*`, xAI's design post and guides. A claim from anywhere else is marked **(secondary)**. The x.ai product page and changelog return 403 to the fetcher, so changelog items come from search snippets of those pages and are marked as such.

## Short answer

Grok Bot is a roster of persistent named Bots (about 50 per account) on one shared cloud computer. One of them is the **primary Bot**: the user's main point of contact, which "checks in on its own and coordinates work with your other Bots". "Chief of Staff" is not a product term. It is the name users gave to this pattern before the primary Bot shipped (around v0.66.0, Oct 2, "Main Bot"). Work runs in the cloud and keeps going when the laptop closes. The Bot reports in its own chat thread: step-by-step activity, result cards, approval cards, and a push notification when it starts sending a message. Coding is not done by the Bot. It writes a brief, starts a Cursor Cloud Agent, watches the agent's transcript, steers it, and posts the agent's card (files, then **View PR**) in its reply. Safety comes from a model reviewer (Auto Review) plus up to 20 "Ask first" and 20 "Allow automatically" prose rules. Unrequested background work is more restricted than work the user asked for in chat: its email waits as a draft, and its approval cards expire after 30 minutes.

## The coordinator: primary Bot

- "Your primary Bot is your main point of contact. It checks in on its own and coordinates work with your other Bots", and "it can pass work to them" (bots, how-grok-bot-works).
- If the user has none, the app prompts "Introducing your primary Bot", and the user creates one or picks an existing Bot. It can be replaced, but not with a Team Bot (bots).
- The product page says the user's "primary Bot can report on what every Bot has done", and "Bots work together, then bring you in" (overview).
- A primary Bot can set up a whole team of Bots on request, each owning one part of a goal, and the user reviews the final result (use-cases). Existing Bots "can suggest or create a focused Bot for a long-lived job" (bots).
- xAI's design post (Sep 3) still calls Chief of Staff a user pattern: "coordinating Bots handle routine routing and bring the user in when a decision requires judgment" (designing-grok-bot).
- Changelog v0.66.0 (Oct 2) is titled "Main Bot and more reliable updates" (x.ai changelog, via search snippet; the page itself was not opened).
- No routing rules are documented. The docs say only that the primary Bot coordinates, and they advise making one Bot "responsible for each step" (bots, how-grok-bot-works).

## Delegation between Bots and parallel work

- A Bot sends another Bot an asynchronous message, and the receiver "wakes, handles the request, and can reply later" (chat-and-collaboration).
- The docs warn that "parallel handoffs create duplicate work", so the user should ask for a single owner at each stage. Every message between Bots counts toward usage. To stop handoffs, the user tells the Bots not to message each other (chat-and-collaboration).
- Group chats hold 2 to 6 Bots. With no `@`, the Bots decide among themselves who answers. Approval requests, secret requests and email or Slack drafts never appear in a group; they go to the one-to-one chat (chat-and-collaboration).
- Parallelism: "Each Bot gets its own screen, so several Bots can work at once." A Bot runs only one computer-use task at a time on its screen. Screens are "separate work surfaces, not separate security boundaries" (computer-and-apps, faq).
- Coding goes to Cursor Cloud Agents. The engineering guide says the Bot will "kick off a cloud agent with my skills invoked" from a detailed prompt that states the expected proof. It "monitors each agent's transcript and artifacts", can "steer the run by queueing a message or interrupting it", tries to unblock stalled agents itself, and escalates "only when it lacks the security permissions". One fleet "manages more than 200 simultaneously" (grok-bot-for-engineering, Sep 10).
- Cloud Agents can run on the user's own machines when registered as Cursor private workers (grok-bot-for-engineering).
- Team admins have a Cloud Agents switch, on by default, to allow or block delegation (cursor.com/docs/grok-bot/teams, via search snippet).
- Gap: Bugbot review does not run on Cloud Agents launched from Grok Bot (Cursor forum, **secondary**).

## How results come back

This is the part that matters most for AdmiralBot.

- **Into the owning Bot's own chat.** "Routine results and test outcomes are delivered in the Bot's chat. Routines can also post results to a conversation, as specified in their instructions" (skills-routines-and-automations). The design post: "The transcript shows what ran and gives the user a place to review the result or handle an exception" (designing-grok-bot).
- **Live steps.** "The chat shows each step as the Bot works." The transcript shows tool activity, computer use, created files, questions, approval requests and voice memos (how-grok-bot-works, chat-and-collaboration).
- **Done means a notification.** "If you close your laptop, the Bot keeps going and notifies you when it's done" (how-grok-bot-works). From v0.63.0 (Sep 29), desktop and phone alerts fire as soon as a Bot starts sending a message, not only when it finishes. From v0.66.0, the phone skips a Bot's alerts while that Bot is open on the computer, until the computer locks or goes idle (changelog, via search snippets). Notifications can be switched off per Bot, but not per group (chat-and-collaboration).
- **Result cards.** Files, pages, images and videos arrive as cards in the conversation. An updated page's earlier card says "Updated below." A Library lists everything the Bot made, newest first (files-and-results).
- **Cloud Agent results.** The Bot copies images, videos and files cited in the agent's final report to its computer and attaches them to its reply. On desktop, the agent's card shows the files. On the phone, they appear once the run is "Done", "Error" or "Expired", and **View PR** shows if a PR was opened (files-and-results).
- **Evidence on request.** For consequential work, the docs suggest asking for facts found, assumptions, completed actions, pending approvals and unresolved questions as separate lists, plus source links, screenshots, timestamps and an action log (files-and-results).
- **Status without opening a chat.** The avatar moves to show the Bot's state, and hovering shows its current action (designing-grok-bot).
- **Interrupting.** A direct message "takes priority over background work and can redirect the current turn" (chat-and-collaboration).
- **Off-app results.** The engineering guide's Bots also update a shared Notion database "so you can scan progress without reading long chats", and each morning there are tasks marked ready to merge (grok-bot-for-engineering).
- **Webhooks.** A `200` "means the run started, not that it finished. Check the Bot's chat for the result" (skills-routines-and-automations).

The pattern: each Bot reports in its own thread, and the primary Bot is where the user asks "what did everyone do". The primary Bot does not seem to relay every result automatically. No digest of other Bots' results is documented except one built as a routine (morning briefing).

## Routines (schedules and events)

- A routine is owned by one Bot. The user creates it by asking that Bot, or on the phone with **Add routine**, which starts the message "Set up a routine to". "Ask a Bot to repeat a task, and it becomes a routine" (skills-routines-and-automations, overview).
- Schedules must be at least 5 minutes apart. A new routine waits for its first scheduled time; **Test** runs it now and does real work (skills-routines-and-automations).
- Events: a Slack message (a mention, a phrase, a reaction, or any message, in one channel or all of them), GitHub, Linear, Sentry or PagerDuty events, email to the Bot's own `mail.grokbot.com` address, or a webhook (`Authorization: Bearer <key>`). Only new activity triggers a run (skills-routines-and-automations).
- Management: pause or resume, Test, Edit, Delete. The phone shows Active, Schedule, Next run, Instruction and Run history (Running, Succeeded, or Failed with a reason). Limits: 50 routines per Bot, the 20 most recent run records kept, 300 runs per hour across all routines and 200 per routine. A run that fails from a shared limit is not retried (skills-routines-and-automations).
- "Grok Bot may ask whether to keep routines running after a long absence and pause them if you don't respond" (skills-routines-and-automations).
- The docs ask the user to confirm, for each routine: the owning Bot, the schedule and time zone, the input source, the expected result, the approval boundary, and what to do when a source is missing (skills-routines-and-automations).

## Skills and Teach a task

- A skill is a reusable instruction set, shared by all of the account's Bots, saved by asking in chat and called by name. `@` mentions Bots, groups, routines and apps. Team skills can be published, synced and unpublished (skills-routines-and-automations).
- **Teach a task** records up to 10 minutes of the user doing a browser workflow and drafts a skill from it. "The generated skill is only a draft": the user adds decision rules, failure handling and approval boundaries (skills-routines-and-automations).

## Connectors

- "Grok Bot supports the same MCP servers, plugins, and skills as Cursor" (grok-bot-101). Apps are installed from **Connect apps**. "Added" means installed, not signed in. A Bot can install catalog apps or add an MCP server by URL or by command on request; a private-network server needs approval (computer-and-apps).
- Apps belong to the account: "Every Bot on your account can use every connected account." Some apps take several accounts, each with a label. Gmail takes one mailbox (computer-and-apps).
- OAuth tokens stay on Cursor's connector backend, and Bots call tools without seeing them. A Bot can run a command on the user's own computer only if allowed, and by default it asks each time (computer-and-apps, faq).
- Bots are also reachable in Slack (Team Bots), in X Chat, and by tagging @bot on an X post (faq).

## Memory

- Each Bot keeps "stable preferences, rules you set, key facts, and a dated log of its work". It decides on its own what to save. The user asks in chat to see, correct or forget a memory; personal Bots have no memory screen (bots).
- A few facts (name, time zone) are shared by all of a user's Bots. Resetting a conversation keeps memory; deleting the Bot deletes it (bots).
- "Memory isn't a source of truth. Back safety boundaries with approvals and Auto-review rules, not memory alone." Changing figures should be looked up again (bots, how-grok-bot-works).

## Acting alone or on request

- Work starts from a user message, a schedule, an event, or another Bot: "A prompt can start a session, but so can a schedule, an event, or another Bot" (designing-grok-bot). The primary Bot "checks in on its own" (bots).
- "Only your own messages count as instructions" (how-grok-bot-works).
- Email the user asked for in chat goes out once Auto Review allows it. Email from routines, incoming messages or other unrequested background work "waits as a draft until you press **Send**". Forwards, and attachments Auto Review can't fully read, always need the user's OK (approvals-security-and-privacy).
- The documented examples draft everything that leaves the account (Gmail, Slack, Notion) and wait for approval: "Every sending, publishing, purchasing, or production action waits for your approval" (overview, use-cases).
- The engineering guide's Bots auto-merge a PR when the review is "highly confident and the blast radius is low". Otherwise the user decides (grok-bot-for-engineering).

## Permissions

- **Auto Review**, a model-based reviewer, is on by default and checks every command, app call and computer action. Most actions pass without a prompt (approvals-security-and-privacy).
- **Approval cards** appear in the chat where the work is happening and show the operation and its inputs. The choices are Allow once, Always allow (which saves a rule) and Deny (the Bot won't retry until the next message). A card for work started from chat waits with no timer. A card for background work expires after 30 minutes and the action is skipped. A new message closes open cards. In a group or on a Team Bot only the requester can approve, and there is no Always allow (approvals-security-and-privacy).
- **Custom Rules**: Ask first and Allow automatically, 20 of each, up to 1,000 characters each. Ask first wins when both match. Team rules always apply, and personal rules can only make things stricter (approvals-security-and-privacy).
- **Secrets**: Vault, then 1Password, then a Secure Form. The model never sees the values. The user takes over the computer for SSO, passkeys and CAPTCHAs, then selects **I'm done** (approvals-security-and-privacy).
- **No isolation between Bots**: "Bots are not separated from each other." Files, browser sessions and CLI credentials are shared, and deleting a Bot leaves them behind (how-grok-bot-works, approvals-security-and-privacy).

## Limits that matter for comparison

- The user cannot pick a model; usage comes from a weekly pool and stops when the pool runs out unless on-demand usage is on (faq).
- No CLI or TUI integration, and the computer is cloud-only, with opt-in local commands (computer-and-apps; forum point in `redesign-similar-products.md`).
- One shared login set across all Bots. Admiral's fleet separation has no counterpart.

## Capabilities AdmiralBot could borrow

1. **A primary coordinator per fleet that checks in on its own.** AdmiralBot already is the per-fleet coordinator. The new part is the scheduled or idle check-in ("here's what sailed off, what's waiting on you"), as opt-in routines rather than unprompted chatter.
2. **Results come back in the coordinator's chat as cards**: live step lines while a ship works, then a result card with Done, Error or Expired, the files the ship cited, and **View PR**. An updated card says "Updated below" instead of repeating itself.
3. **Brief, watch, steer, escalate.** AdmiralBot writes the ship's brief with the expected proof, watches its transcript, sends a queued message or interrupt to steer it, unblocks stalls itself, and asks the engineer only when it lacks permission. That is the Cloud Agent loop, with ships in place of Cloud Agents.
4. **Notify on the first message, not only at the end**, and stop pushing to the phone while the same chat is open on the desktop.
5. **Evidence-shaped reports**: facts found, assumptions, actions done, pending approvals and open questions, plus links and screenshots, as a fixed result template for "add X to Admiral, then come back to a diff or PR".
6. **Background is stricter than requested.** Approval cards from unrequested work expire (30 minutes) and skip the action. Outbound actions from unrequested work stay drafts. A new message from the engineer closes open cards.
7. **Ask first / Allow automatically rules in prose**, a fixed number of each, with Ask first winning, and fleet-wide rules that personal rules can only tighten. A model reviewer in front of them.
8. **Routines that AdmiralBot creates from chat**, owned by one coordinator, with Test, Next run, and a short run history with failure reasons. Rate limits, and a pause prompt after a long absence.
9. **The engineering loop as a routine**: every N minutes, check open PRs for CI failures, review comments and conflicts; send fixes back to the ship; mark clean ones ready; offer to merge when confident and the blast radius is small. That would be the engineer's choice, never the default.
10. **Single owner per step.** Grok Bot's docs warn that parallel handoffs duplicate work. Admiral's resource locks and claims already enforce this, and a coordinator design should keep that.
11. **A memory the bot curates, with a dated work log**, viewed and corrected by asking. That matches the ledger's notes. "Memory isn't a source of truth" matches the ledger-over-context rule in `admiralbot-context.md`.
12. **A progress board outside the chat** (their Notion database), so the engineer can scan state without reading threads. Admiral's ship list already plays this part.

What not to copy: one login set shared by every Bot, no model choice, hidden usage, and Bots messaging each other freely (it costs usage and duplicates work).

## Sources

Primary, opened:

- xAI docs: <https://docs.x.ai/grok-bot/overview>, <https://docs.x.ai/grok-bot/how-grok-bot-works>, <https://docs.x.ai/grok-bot/bots>, <https://docs.x.ai/grok-bot/chat-and-collaboration>, <https://docs.x.ai/grok-bot/skills-routines-and-automations>, <https://docs.x.ai/grok-bot/files-and-results>, <https://docs.x.ai/grok-bot/approvals-security-and-privacy>, <https://docs.x.ai/grok-bot/computer-and-apps>, <https://docs.x.ai/grok-bot/use-cases>, <https://docs.x.ai/grok-bot/faq>
- xAI, "Designing Grok Bot for a world of persistent agents" (Sep 3, 2026): <https://x.ai/news/designing-grok-bot>
- xAI guides: <https://x.ai/bot/guides/grok-bot-101> (Sep 11), <https://x.ai/bot/guides/grok-bot-for-engineering> (Sep 10)

Primary, seen only as search snippets (403 to the fetcher or not opened):

- xAI changelog: <https://x.ai/changelog/bot>, <https://x.ai/bot/changelog> (v0.63.0 Sep 29, v0.66.0 Oct 2)
- Cursor, Grok Bot for Teams: <https://cursor.com/docs/grok-bot/teams>

Secondary:

- Cursor forum, Bugbot missing on Cloud Agents launched from Grok Bot: <https://forum.cursor.com/t/review-bugbot-is-missing-on-cloud-agents-launched-from-grok-bot/170096>
- The Daily Bot newsletter on changelog versions up to v0.75.2 (Oct 22): <https://buttondown.com/nyfe/archive/the-daily-bot-oct-23-grok-bot-can-use-calendar/>
