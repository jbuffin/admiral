# 33. Pushes are Web Push from the Mac, with no hosted relay

Status: accepted

## Context

Today Admiral only chimes in an open browser. Away from the Mac, the engineer has to know when a ship or AdmiralBot is waiting on them. The research in [#217](https://github.com/jbuffin/admiral/issues/217) ([note](../research/windows-and-native-apps.md)) weighed Web Push from an installed web app against native push through an APNs relay, which would have to be hosted. [ADR 0031](0031-remote-access-goes-through-tailscale-serve.md) gives HTTPS through the tailnet URL, and [ADR 0032](0032-devices-pair-for-their-own-tokens-and-the-api-is-versioned.md) gives each device its own token. [#236](https://github.com/jbuffin/admiral/issues/236) decided how pushes travel and what pushes.

## Decision

**Pushes travel as standard Web Push (VAPID) from the Mac to each subscribed device, with no hosted relay. Admiral pushes only what the engineer must act on, skips the push while the engineer is watching, and shows an attention count everywhere else.**

- **Transport.** Web Push only; the APNs relay goes out with the native apps, so nothing is hosted outside the Mac and [ADR 0001](0001-local-web-app-served-from-the-mac.md) holds. The payload is encrypted end to end (RFC 8291) and carries the fleet, the ship or source name and one line of text, never code, diffs or secrets, since it may show on a lock screen. One VAPID key pair serves all of Admiral, made on first use and kept in `$ADMIRAL_HOME`.
- **Subscriptions.** A subscription is stored with its paired device, and its registration endpoint lives under `/api/v1` with bearer or cookie auth. A 404 or 410 from the push service drops it, and that device shows notifications off. Revoking or expiring a device drops its subscriptions.
- **What pushes.** AdmiralBot's waiting chat question, a ship's question or permission prompt, approvals (a T3 wake proposal, approval before code pushes), a feedback round's findings item, **Ready to merge**, an anchored ship that ended with an error, and a routine with **Notify me** on. Finished-turn stops, FYI items and quarantined triggers go to the Inbox and the attention count only.
- **Not while watching.** If any signed-in window is visible and was used in the last ~2 minutes, that window gets the chime and a toast, and no device gets a push. Otherwise every subscribed device gets it. A push is tagged with its item, so an update replaces it instead of stacking. Answering an item sends no push to clear it, because iOS revokes subscriptions that get pushes it doesn't show; the app closes stale notifications when it opens.
- **Controls.** Notifications are turned on per device, on that device, which subscribes its service worker. Each fleet has a **Notify** switch, on by default. There are no quiet hours; the OS's Focus modes cover that. A loopback browser on the Mac can turn notifications on without pairing; it registers as a device called This Mac, shows in the device list and can be revoked. A device on plain HTTP can't subscribe: its switch is disabled, says it needs Tailscale mode and links to Remote access. An iPhone or iPad not running Admiral from the Home Screen sees "Add to Home Screen first".
- **Tapping a push.** An Inbox item, AdmiralBot's waiting question included, opens the Inbox with Peek on that item, or the Inbox alone if the item is already answered. A Notify-me routine opens its result: AdmiralBot's chat at that message, or the launched ship. There are no action buttons on any platform, since iOS Web Push has none.
- **Attention count.** What waits on the engineer across every fleet: needs-you stops plus items that aren't FYI. It shows as "(3) Admiral" in the tab title, as a dot on the favicon, and as the app icon badge in an installed app (Badging API). It drops only when an item is answered or removed; viewing an item doesn't change it.
- **Chime and toasts.** The chime and its mute stay. Only the visible window plays it, and only for the events that push. Toasts follow the same events, at most two on screen, a third replacing the oldest; tapping one opens Peek.
- **Service worker.** It handles `push`, `notificationclick`, the app icon badge and closing stale notifications. It also caches the app shell for an installed app and nothing else, no API data ([#243](https://github.com/jbuffin/admiral/issues/243)).

## Considered options

- **Web Push from the Mac** (chosen). Nothing hosted, and the payload is encrypted to the device. On iOS it needs the Home Screen install, and it has no action buttons.
- **Native push through an APNs relay.** Needs native apps and a hosted relay, against ADR 0001. Out of scope with the native apps.

## Consequences

- Devices subscribe against Admiral's VAPID key, so the key can't change without every device subscribing again.
- A device on plain HTTP, with no Tailscale mode, never gets pushes. It still gets the tab-title badge, the chime and toasts.
