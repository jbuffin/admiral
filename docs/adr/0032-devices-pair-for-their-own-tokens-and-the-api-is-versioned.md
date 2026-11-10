# 32. Devices pair for their own tokens; the API is versioned

Status: accepted

## Context

Under [ADR 0001](0001-local-web-app-served-from-the-mac.md) every remote browser shares one startup token, and rotating it signs out every device at once. [ADR 0031](0031-remote-access-goes-through-tailscale-serve.md) keeps that token as the second factor beside a Tailscale login, and shows a sign-in QR in Settings → Remote access. Native apps and other clients that aren't browsers are out of scope for now, but the research in [#217](https://github.com/jbuffin/admiral/issues/217) ([note](../research/windows-and-native-apps.md)) found they need a versioned API and pairing first. [#235](https://github.com/jbuffin/admiral/issues/235) decided how devices pair and how the API is versioned. [#243](https://github.com/jbuffin/admiral/issues/243) added a typed code, because on iOS an installed web app keeps its own cookies apart from Safari's and no link or QR scan opens it.

## Decision

**Each device pairs for its own revocable token through a one-time code, and the shared token is retired. Clients that aren't browsers authenticate with `Authorization: Bearer`. The API moves to `/api/v1` with a version probe.**

- **Device tokens.** Each paired device gets its own token, stored hashed under `$ADMIRAL_HOME`, revocable alone. The shared `$ADMIRAL_HOME/token` is retired, with no fallback. A token lasts until it is revoked; a browser's cookie is re-issued on use, so the 400-day cookie cap never bites. A device unseen for 90 days is dropped.
- **The first device.** It gets in through the Mac's own browser (loopback, so local and signed in) or through `admiral pair`. The startup line prints the plain URL and a pairing hint, never a tokened URL.
- **Pairing code.** The QR in Settings → Remote access and `admiral pair` carry `<base>/pair?code=…`. The code is single-use, valid for 5 minutes, and minted each time it is shown. `<base>` is the ts.net URL in Tailscale mode and `http://<LAN IP>:7420` otherwise, so both modes share one flow. The Notifications page's "Pair a device · Show code" opens this same QR; there is no second code.
- **Redeeming it.** A browser that opens the URL gets its device token as the HttpOnly cookie and is redirected to `/`, so no token lands in a URL or in history. A native client POSTs the code and gets the token in JSON.
- **Typed code** ([#243](https://github.com/jbuffin/admiral/issues/243)). The same code also shows as short text, about 8 characters, under the QR and under the URL in `admiral pair`: one code, two ways to enter it. An unpaired installed app opens on **Pair this app**, where the code is typed, and `/pair` with no code shows the same entry in any browser. When the token stops working (revoked, dropped after 90 days, or its login removed from the allowlist), a 401 sends the app back to **Pair this app** with "This device was signed out"; a network failure shows Can't reach instead. On Android, pairing through the QR in Chrome carries over to the installed app, and the typed code is a fallback.
- **Bound to the Tailscale login.** Over the tailnet both factors stay: an allowed `Tailscale-User-Login` and the device token. A device that pairs over the tailnet has its login recorded, and its token then works only with that login, so a token copied to another tailnet device fails even when that login is allowed. Removing a login from `remote.allowedLogins` disables every device paired under it. A device paired over the LAN has no login recorded, and its token works only on the LAN path.
- **Managing devices.** Settings → Remote access gets a **Devices** list below the QR: a name guessed from the User-Agent and editable, the Tailscale login, paired on and last seen, and **Revoke**, which cuts that device's sockets at once. **Rotate token** becomes **Sign out all devices**. The CLI gains `admiral pair`, `admiral devices` and `admiral devices revoke <name|--all>`; `admiral token rotate` goes away.
- **Bearer and Origin.** `Authorization: Bearer <device token>` is accepted on every route, WebSocket upgrades included. A bearer request skips the Origin rule: a page can't attach that header across origins without a CORS preflight, and Admiral answers none. A cookie request keeps today's Origin rule. `?token=` on WebSockets is retired. Loopback with no proxy headers stays local and needs nothing, so the CLI and AdmiralBot are unchanged.
- **API versioning.** Every route moves to `/api/v1/…`, and the bridge to `/ws/v1/…`. `GET /api/version` (unversioned) returns the server version, the supported API versions (`[1]`) and a capability list. The bridge `join` carries the client's API version. Within v1, changes are additive only; a breaking change makes v2, and v1 keeps being served for at least one release after that.
- **The unversioned paths.** The new shell uses only `/api/v1`. The old UI stays on the unversioned `/api` until it is deleted, in the release after the new shell becomes the default ([#247](https://github.com/jbuffin/admiral/issues/247)).

## Considered options

- **Per-device tokens with one-time pairing** (chosen). Each device can be revoked alone, and a token is bound to the login that paired it.
- **Keep the shared token** ([ADR 0001](0001-local-web-app-served-from-the-mac.md), [ADR 0031](0031-remote-access-goes-through-tailscale-serve.md)). Nothing to build, but one leak or one lost phone means signing out everything, and native clients would carry the one secret.
- **Scan the QR inside the installed app.** Rejected in #243: it needs camera permission and a decoder library, where a typed code needs neither.

## Consequences

- This amends [ADR 0001](0001-local-web-app-served-from-the-mac.md): there is a pairing flow, and the one startup token is gone. It amends [ADR 0031](0031-remote-access-goes-through-tailscale-serve.md): the second factor is the device token, bound to the login that paired it, and the sign-in QR carries a pairing code instead of a token.
- A push subscription belongs to a paired device and goes with it ([ADR 0033](0033-pushes-are-web-push-from-the-mac-with-no-hosted-relay.md)).
- An installed app whose cached shell is newer or older than the server reloads when the `/api/version` probe shows a mismatch (#243).
