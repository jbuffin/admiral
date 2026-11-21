# Codebase

Where things are. Read the area file for the part you are changing; `packages.md` maps every package.

## Pointers

- `designs/`: the clickable prototype, screenshots, design tokens, SVG assets and source design files. Start with `designs/README.md`.
- [Admiral MVP spec](https://github.com/jbuffin/admiral/issues/45): what the MVP does, as user stories, implementation decisions and testing decisions.
- `docs/architecture.md`: an early proposal for the build, superseded by the spec and the ADRs.
- `docs/adr/`: decisions.
- `GLOSSARY.md`: the words Admiral uses.

## Areas

- [Package map](codebase/packages.md): every Go package and top-level directory and what it owns. Read it to find where a change goes.
- [Ships, routes and mooring](codebase/ships-and-moor.md): ship routes, statuses, mooring, the moored poll, merge, update branch, ready for review, chained PRs. Read before changing `internal/ships` or `internal/moor`.
- [Hooks and the autopilot guard](codebase/hooks-and-guard.md): `admiral hook`, `admiral guard pretooluse` and the env every ship gets. Read before changing `internal/cli/hook.go`, `guard*.go` or the launcher bundle.
- [Launching ships](codebase/launch.md): clone on launch, crew-member launches, the ship cap, locked resources, worktrees, restricted temp dirs. Read before changing `internal/launcher`.
- [Crew boats and subagents](codebase/crew-boats.md): teammates, the crew bridge and the crew wire shape. Read before changing crew handling.
- [Codex ships](codebase/codex.md): the Codex wrapper and live conversation. Read before changing Codex launches.
- [Outcome log and usage](codebase/outcomes-and-usage.md): outcomes, usage at exit and live usage. Read before changing `internal/outcomes` or usage.
- [Permission log and allow rules](codebase/permissions.md): the permission log and the allow rules built from it.
- [Routines and the Lookout](codebase/routines-and-events.md): routines, event routines and the Lookout.
- [Commitments and campaigns](codebase/commitments-and-campaigns.md): AdmiralBot's commitments runner and the campaign model.
- [Config, fleets and updates](codebase/config-and-fleets.md): fleet-owned sources, fleet fields, fleet env, chat settings, the update check.
- [Order sources](codebase/sources.md): source events and the built-in GitHub source.
- [Capabilities, probes and the autopilot gate](codebase/capabilities-and-probes.md): what each harness claims and how it is proven.
- [The Needs You inbox](codebase/inbox.md): items, review submit and discard, kept inbox, resume text.
- [The wake decision](codebase/wake.md): how the poll decides to wake a moored ship, hand rounds, tiers, the wake state.
- [The chat session](codebase/chat.md): AdmiralBot's per-fleet chat. Related: [harnesses](codebase/chat-harnesses.md) and [questions, cards and drafts](codebase/chat-cards.md).
- [Devices and pairing](codebase/devices.md): access control, tokens, pairing, push subscriptions.
- [API versions](codebase/api-versions.md): the `/api/v1` and `/ws/v1` aliases. Read before adding a route.
- [The new shell at `/app/`](codebase/new-shell.md): modules, pages and routes of the new web shell.
- [The old web UI](codebase/old-ui.md): the frozen Preact UI outside `web/src/app/`.
- [Idle memory and the setup terminal](codebase/idle-memory-and-setup.md): memory figures and `admiral setup`.
