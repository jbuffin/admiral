# What Admiral's API already serves a client that shows only AdmiralBot

> Research for [#936](https://github.com/jbuffin/admiral/issues/936), 2026-11-27, against `main` at `e2489142`. Sources are this repo's code and docs. Line numbers are at that commit.

## Short answer

A native client that shows only AdmiralBot can be built on today's `/api/v1` with no server change. It pairs by typed code and gets a bearer token. It reads each fleet's chat (`GET /api/v1/chat/<fleet>/session`), sends messages, answers chat questions and follows everything live on one `/ws/v1` socket, which pushes `chat`, `inbox` and `ships` snapshots for every fleet. Ship cards, launch lines and report-reply threads all arrive as chat entries.

A separate push companion can also pair as a device and subscribe to Admiral's existing Web Push. Admiral encrypts each push to whatever subscription keys it was given, so the companion can decrypt it with standard RFC 8291 code. The catch is the endpoint. Admiral only POSTs to an `https` endpoint that resolves to a public address. Loopback, LAN and tailnet (100.64.0.0/10) addresses are refused at dial time. So the companion needs a public HTTPS receiver or a real push service endpoint, which in practice means something hosted. ADR 0033 rules that out for Admiral itself. A native iOS app cannot get a Web Push endpoint at all, so native push still needs a relay (APNs) or a new subscription type.

The cheaper companion follows `/ws/v1` and works out events itself. The socket sends full snapshots, not the "this pushed" events: the `alert` message goes only to windows that report they are being watched, and a client that reports that suppresses every push to every device. "Without Admiral knowing" does not hold either way. A companion is a paired device: it shows in the Devices list and can be revoked, and nothing marks it as different from a browser.

## 1. Each fleet's chat

| Need | What serves it | Evidence |
|---|---|---|
| Session and history | `GET /api/v1/chat/<fleet>/session`: `fleet`, `running`, `state` (`idle`/`working`/`waiting`), `messages` (newest 200, oldest first), `questions`, `error`, `needsTerminal` | `internal/chat/service.go:264`, `:671`; `HistoryWindow = 200` at `internal/chat/service.go:56`; `docs/api.md` "A fleet's chat" |
| Entry kinds | `message` (user/assistant), `question`, `reset`, `commitment`, `ship_card`, `following_card`, `launch_line`, `answer`, `campaign_draft_card`; `thread` links a report turn and its reply to a ship card | `docs/api.md` "`GET /api/chat/<fleet>/session`", "Ship cards", "Launch lines" |
| Send | `POST /api/v1/chat/<fleet>/messages` `{"text"}` (16 KiB cap; 409 while not running or refreshing) | `internal/chat/service.go:266` |
| Start / New chat | `POST …/start`, `POST …/new` (deferred while a queued turn waits) | `internal/chat/service.go:265`, `:267` |
| Answer a chat question | `POST /api/v1/chat/<fleet>/questions/<id>/answer`, or the Inbox's `POST /api/v1/inbox/_chat-<fleet>/<id>/answer` | `internal/chat/service.go:268`; `internal/inbox/service.go:214` |
| Live updates | `/ws/v1` sends `{"type":"chat","fleet",…session fields}` on connect (one per fleet with chat on) and within about a second of a change, coalesced per fleet | `internal/chat/service.go:274`, `:353`; `docs/api.md` chat "WebSocket" |
| Which fleets have a chat | `GET /api/v1/config` → `fleets[].chat.on`, `off_reason`; also the `config` socket message | `internal/server/server.go:220`–`242` |
| Ledger and instructions | `GET …/notes`, `GET …/commitments`, `GET`/`PUT …/instructions`, delete and promote | `internal/chat/service.go:255`–`263` |

What a client that is not a browser needs:

- **Bearer auth.** `Authorization: Bearer <device token>` works on every route, WebSocket upgrades included, and skips the Origin check (`internal/auth/auth.go:212`–`220`, `:244`). A cookie request must pass Origin (`:221`–`226`). `?token=` is gone.
- **`/api/v1`.** The `/v1` prefix is stripped before routing, so every route has a v1 path (`internal/server/server.go:97`–`135`). Within v1, changes are additive only (ADR 0032).
- **Version probe.** `GET /api/version` (unversioned, `internal/server/server.go:106`, `:147`) answers `version`, `commit`, `api_versions: [1]` and `capabilities`: `ships, sources, inbox, launch-orders, bridge, setup, resources, chat, conversation, campaigns, ship-input, usage` (`cmd/admiral/main.go:725`). The socket's first message is `{"type":"hello","version","commit"}` (`internal/server/server.go:167`).
- **The terminal**, if wanted: `/ws/v1/bridge/_chat-<fleet>` (`docs/bridge-protocol.md`).

## 2. What waits on the engineer across fleets

- **Inbox.** `GET /api/v1/inbox` (`internal/inbox/service.go:213`, `:242`) lists items from every fleet, oldest first. Each item has `kind`, `id`, `title`, `created`, `session`, `name`, `ship_title`, `harness`, `fleet`, `fleet_name`, `fleet_color` and its kind's own object. A fleet's waiting chat question is an item with `session: "_chat-<fleet>"` and `name: "AdmiralBot"`, so a client can pick AdmiralBot's items out by session prefix. Campaign proposals (`campaign-…`) are chat-only and never Inbox items. The socket's `inbox` message carries the same list on connect and on every change (`internal/inbox/service.go:183`, `:204`).
- **Needs-you stops.** These come from `GET /api/v1/ships` and the `ships` socket message (`internal/ships/ships.go:393`–`403`): `status: "needs_you"` with `reason`, `seen`, `stop_since`, and `crew[]` with each boat's status and reason.
- **Attention count.** The server does not serve it. The web shell derives it in `web/src/app/inbox.ts:162`–`164` from a queue built out of the inbox and ships (`:75`–`154`). The queue holds every inbox item except `notice`, every ship with `needs_you` whose reason is not `crew` and that is not `seen`, every crew boat that needs you (unless its ship is sailing off), a moored `stack_ready` ship that is not a review, and a moored review whose author responded. A native client has to copy those rules.
- **Acting on items.** `POST /api/v1/inbox/<session>/<id>/<action>` (`internal/inbox/service.go:214`–`227`; `docs/api.md` "Acting on an item").

## 3. Pairing, listing and revoking

- **Mint a code.** `POST /api/v1/pairing-codes`, from any authenticated device or the Mac (`internal/auth/api.go:15`, `:105`). The answer is `{"code":"ABCD-EFGH","url","expires"}`: 5 minutes, single use, and a new code replaces the live one (`internal/auth/pair.go:28`).
- **Redeem it.** `POST /api/v1/pair` `{"code","name"?}` answers `{"token","device":{"id","name"}}` with no cookie (`internal/auth/pair.go:68`–`139`; routed before auth at `internal/auth/auth.go:175`). The code is case-insensitive, with or without the hyphen. A wrong code is 400 and ten failures in 5 minutes are 429. A request from the Mac itself is **403** (`internal/auth/pair.go:70`–`72`): a companion that runs on the Mac can't pair. It doesn't need to, because the Local route needs no token (`internal/auth/auth.go:34`–`49`, `:198`–`206`).
- **Token binding.** A device paired over the tailnet has its `Tailscale-User-Login` recorded, and its token works only with that login. A device paired over the LAN works only on the LAN (ADR 0032).
- **List, rename, revoke.** `GET /api/v1/devices` answers `id, name, login?, pairedAt, lastSeen, current, notifications, thisMac?`. Then `PATCH /api/v1/devices/<id>`, `DELETE /api/v1/devices/<id>` and `DELETE /api/v1/devices` (`internal/auth/api.go:10`–`14`). A revoke closes that device's sockets (ADR 0032). A device unseen for 90 days is dropped. The CLI has `admiral pair` and `admiral devices [revoke]`.

## 4. Push

### What each push carries

The plaintext is the JSON `Payload` (`internal/push/decide.go:155`–`173`): `name` and `line` always (the line is the first line, at most 120 runes, with control and bidi characters stripped), `tag` (the item key, so a repeat replaces the last one), and when set `fleet`, `fleet_name`, `target` (`peek` / `ship` / `chat` / `routine`), `session`, `item`, `crew`, `merge`, `routine`, `message`. It never carries code, diffs, prompts or secrets.

| Event kind | Source | `name` / `line` | Tap target |
|---|---|---|---|
| `chat_question` (AdmiralBot asks) | a new Inbox question whose session is `_chat-<fleet>` (`watch.go:236`–`239`) | `AdmiralBot` / the question title | `peek`, `session` `_chat-<fleet>`, `item` |
| `ship_question` | the ship or a crew boat goes `needs_you` with question, permission, bell or not_started (30 s grace) (`watch.go:117`–`201`) | ship name / "has a question", "needs your approval", "needs you to finish starting", "needs you" | `peek`, `session` (+`crew`) |
| `inbox_question` | a ship's Inbox question file | ship / title | `peek`, `item` |
| `wake_proposal`, `findings` | new Inbox items of those kinds (`watch.go:241`–`250`) | ship or fleet / title | `peek`, `item` |
| `ready_to_merge` | a moored ship's stack becomes ready (`watch.go:177`–`188`) | ship / "is ready to merge" | `peek`, `merge: true` |
| `anchored` | the ship ended with an error (`watch.go:173`–`176`) | ship / "stopped with an error" | `ship` |
| `report_reply` | AdmiralBot's reply to a ship's report turn (`decide.go:75`–`80`; produced at `cmd/admiral/main.go:911`) | `<ship> <result in words>` / reply's first line as plain text | `chat`, `fleet`, `message` |
| `digest_reply` | AdmiralBot's reply to a fleet digest routine (`decide.go:89`–`93`; `cmd/admiral/main.go:919`) | `Fleet digest` / first line | `chat`, `message` |
| `routine_notify` | a routine with Notify me on (`internal/push/routine_notify.go:16`; wired at `cmd/admiral/main.go:597`) | routine name / … | `routine` or the ship |
| `code_approval` | no producer yet (`decide.go:22`) | | |

The AdmiralBot-only set is `chat_question`, `report_reply` and `digest_reply`, plus a `routine_notify` whose run was a prompt. A client tells them apart by `session` starting `_chat-` or by `target: "chat"`. The payload has no `kind` field.

`Decide` (`decide.go:142`–`150`): a fleet with Notify off hears nothing. If **any** window is watching, the event becomes an `alert` for the watched windows, and **no device** gets a push. Otherwise every subscription gets it. The first snapshot after startup only seeds what was already waiting, so it never pushes (`watch.go:48`–`49`, `:148`–`162`).

### Can a companion be a Web Push subscriber?

Partly. Admiral posts to whatever endpoint it was given, but that endpoint must be public.

- **Subscribing.** `GET /api/v1/push/key` gives the VAPID public key. `PUT /api/v1/push/subscription` takes `{"endpoint","keys":{"p256dh","auth"}}` for the requesting device (`internal/auth/push.go:115`–`164`). The check at subscribe time only asks for an `https` URL, a P-256 point and a 16-byte auth secret (`internal/auth/push.go:212`–`225`). Nothing requires a browser, so a companion can make its own key pair and auth secret.
- **Decrypting.** Admiral seals each push as one `aes128gcm` record with RFC 8291 key derivation, to the subscriber's `p256dh` and `auth` (`internal/push/encrypt.go:29`–`97`). Whoever holds the private key and auth secret can decrypt it with any Web Push library. The headers are `TTL: 3600`, `Urgency: high`, a VAPID `Authorization` and no `Topic` (`internal/push/sender.go:184`–`188`). Fan-out goes to **every** subscription of every device (`sender.go:127`–`144`), so the companion gets everything the phone gets.
- **Where Admiral will POST.** The sender's client refuses to dial loopback, private, link-local, multicast, unspecified, reserved and CGNAT/tailnet addresses, including IPv4 wrapped in NAT64 or 6to4, after DNS resolution (`internal/push/endpoint.go:34`–`80`). Redirects are not followed. So the companion can't receive pushes on the Mac, the LAN or the tailnet. It needs one of these:
  - a public HTTPS receiver of its own, which is a hosted relay in all but name, or
  - an endpoint from a real push service (FCM, Mozilla autopush, Apple's `web.push.apple.com`), which a non-browser process can only get through unofficial client protocols. Apple's is for Safari and home-screen web apps only. A native iOS app's APNs device token is not a Web Push endpoint.
- **A 404 or 410** from the endpoint drops that subscription (`sender.go:197`–`199`).

### Can a companion follow an event stream instead?

Yes, though it gets state, not events.

- `/ws/v1` with a bearer token. On connect it sends `hello`, then `config`, `ships`, `inbox`, `chat` (per fleet) and `resources`, and the same messages again on every change (`internal/server/server.go:159`–`205`). A client that never sends `presence` is never counted as watching (`internal/server/hub.go:91`–`106`), so it does not suppress pushes. A companion can diff the snapshots the way `push.Watcher` does (`internal/push/watch.go:142`–`264`) to learn what is new.
- The ready-made `alert` message (same payload as a push, plus `type`) goes only to windows that count as watching (`internal/push/alert.go:16`–`21`, `internal/server/hub.go:108`–`116`). To get it, a companion would have to send `{"type":"presence","visible":true,"idle_ms":0}` every 30 s. That would turn off every push to every device while it stays connected, so it is not a usable path.
- `report_reply`, `digest_reply` and `routine_notify` are not Inbox items. On the socket they show up only as new chat entries: an assistant message carrying `thread` for a report reply, and routine runs on the Routines routes. A companion has to infer them, and has no record of what was pushed while it was offline.

## Gaps

1. **No event stream of what would push.** The socket gives snapshots, and `alert` only reaches watched windows. A companion must reimplement `push.Watcher`, including the not-started 30 s wait, the first-snapshot seeding and the ready-to-merge poll rule, or Admiral needs a "subscriber" socket mode that gets the `Decided` events without counting as watching.
2. **No server-side attention count.** It is derived in `web/src/app/inbox.ts`. A native client must duplicate the queue rules or the server must serve the count, per fleet and overall.
3. **Web Push can't reach a native app or an unhosted companion.** The endpoint must be public HTTPS, and APNs and FCM device tokens aren't accepted. Native push needs a relay or a new subscription type. Both go against ADR 0033's "nothing hosted" unless that ADR is revisited.
4. **Push fan-out is all-or-nothing.** A subscription can't say "AdmiralBot only" or name a kind, and the payload has no `kind`. A companion and a phone both subscribed show every push twice. Watching anywhere suppresses all of them.
5. **A device has no type.** Admiral can't tell a companion from a browser: one name, the same list, the same rights. Any paired device can mint pairing codes and revoke every device.
6. **Chat history is capped at the newest 200 entries**, with no `before` paging, unlike a ship's conversation.
7. **No catch-up for push-only events.** Report replies, digest replies and routine notifies aren't kept as items, and there's no "since" cursor on the socket or the REST routes.
8. **One Admiral per Mac.** Every fleet lives in one Admiral, so one connection covers all of a machine's fleets, but each fleet has its own AdmiralBot (ADR 0023) and there is no cross-fleet chat. A second Mac is a second server, a second pairing and a second token.
9. **Copilot fleets.** `POST …/start` is 501 for a Copilot chat harness, so those fleets have no AdmiralBot to show.
