# 36. The classic view stays as a dashboard

Status: proposed

## Context

[ADR 0032](0032-devices-pair-for-their-own-tokens-and-the-api-is-versioned.md) kept the old UI on the unversioned `/api` until it was deleted, in the release after the new shell became the default. The flip ([#275](https://github.com/jbuffin/admiral/issues/275)) opened the new shell at `/` and moved the old UI to `/classic/`. [#276](https://github.com/jbuffin/admiral/issues/276) would have deleted the old UI, the unversioned `/api` and `/ws` routes and the marina prototype. It was closed as won't fix: the engineer wants to keep the classic view as something to watch, with ships launching and running their missions in the marina.

## Decision

**The old UI stays at `/classic/` as a dashboard to watch ships. It isn't deleted, and the unversioned `/api` and `/ws` routes it reads stay while it does.**

- **Where it lives.** `/classic/` keeps serving the old UI's `index.html`, with its assets at `/assets/`. `/` stays on the new shell.
- **What it's for.** Watching the fleet: ships launching, running and mooring. The new shell is where work is done. A feature lands in the new shell first, and the classic view gets it only if watching needs it.
- **Its API.** It stays on the unversioned `/api` and `/ws`. These aren't part of the versioned API: no client but the classic view may use them, and their shapes follow what the classic view needs.
- **What stays with it.** The marina placement in `state.json`, the old design tokens in `designs/tokens.json`, and the marina prototype in `designs/prototype/`.

## Considered options

- **Keep the classic view as a dashboard** (chosen). The engineer keeps the view they like to watch, and the cost is a second UI and the unversioned routes.
- **Delete it as planned** ([#276](https://github.com/jbuffin/admiral/issues/276)). Less code, one set of tokens, and only `/api/v1` left. But the marina view is gone.
- **Keep it and move it onto `/api/v1` now.** Then the unversioned routes could go, but it means porting the old UI's reads before anyone needs them. It stays open as a follow-up.

## Consequences

- This amends [ADR 0032](0032-devices-pair-for-their-own-tokens-and-the-api-is-versioned.md): the old UI isn't deleted, and the unversioned `/api` and `/ws` aren't retired with it.
- A server change that touches a route the classic view reads must keep the classic view working, and the tests that cover it stay.
- AGENTS.md and `designs/README.md` describe the old UI as frozen until the new shell replaces it. They need to say it is kept as the classic dashboard.
