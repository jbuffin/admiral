# 31. Remote access goes through `tailscale serve`, and a proxied request is remote

Status: accepted, amended by [ADR 0032](0032-devices-pair-for-their-own-tokens-and-the-api-is-versioned.md) (the second factor is a per-device token tied to the pairing login, and the QR carries a pairing code)

## Context

[ADR 0001](0001-local-web-app-served-from-the-mac.md) serves Admiral on the LAN with one startup token, and treats a loopback request as local. The engineer wants to reach Admiral from a phone or iPad away from home. The research in [#215](https://github.com/jbuffin/admiral/issues/215) ([note](../research/remote-access-tailscale.md)) compared opening the tailnet IP by hand (A), staying behind the Mac's own `tailscale serve` (B), and embedding tsnet (C). `tailscale serve` forwards to Admiral on loopback, so under [ADR 0001](0001-local-web-app-served-from-the-mac.md) every request through it would count as local. [#234](https://github.com/jbuffin/admiral/issues/234) decided the shape.

## Decision

**Admiral has optional built-in Tailscale support behind the Mac's own `tailscale serve`, with no embedded tsnet. A request that came through a proxy is remote even on loopback. A tailnet request needs an allowed Tailscale login and a token. Tailscale mode listens on loopback only, so one `https://<mac>.<tailnet>.ts.net` URL serves every device, at home and away.**

- **What counts as local.** A request is local only when its peer is loopback, it carries no proxy headers (`X-Forwarded-For`, `Forwarded`, `Tailscale-User-*`) and its Host passes `hostAllowed`. Everything else is remote. This closes the same hole for any other local proxy.
- **Two factors.** A request through `tailscale serve` needs a `Tailscale-User-Login` on the allowlist and a token ([ADR 0032](0032-devices-pair-for-their-own-tokens-and-the-api-is-versioned.md) makes it the device's own).
- **Allowlist.** `remote.allowedLogins` in `config.json`, Admiral-wide, not per fleet. `admiral setup` fills it with the Mac's own login from `tailscale status --json`. Shared-in users and tagged devices are refused unless their login is listed, and no setting admits tagged devices.
- **Never public.** A proxied request with no `Tailscale-User-Login` is refused. That covers Funnel traffic, tagged devices and other proxies, so Cloudflare Tunnel and the like are unsupported. At startup Admiral reads `tailscale serve status --json` and refuses to start if Funnel is on for its port.
- **Listen address.** `remote.tailscale: true` in `config.json` turns the mode on. With it on, `--listen` defaults to `127.0.0.1:7420`, so plain-HTTP LAN access is off; an iPad at home keeps the ts.net URL, since Tailscale connects directly over the LAN. `--listen 0.0.0.0:7420` opts back into LAN plus Tailscale. Without the mode, the LAN default is unchanged.
- **Setup.** `admiral setup` offers the mode. If the engineer agrees, it checks that HTTPS and MagicDNS are on, warns that Certificate Transparency publishes the machine name, runs `tailscale serve --bg --https=443 http://127.0.0.1:7420` and fills the allowlist.
- **Startup.** The startup line shows the ts.net URL instead of the LAN IP. If the mode is on but tailscaled is down or the serve config is missing, Admiral still starts on loopback, warns on the startup line and shows the failing check in Settings. Only Funnel stops startup.
- **Settings → Remote access** (Preferences, all fleets): status checks (Tailscale running and logged in, HTTPS and MagicDNS on, serve set up for Admiral's port, Funnel off), **Set up** (the same serve command), the tailnet URL with copy, the sign-in QR shown only to a browser already signed in, and the allowed logins with add and remove. Every change applies live: turning the mode on or off rebinds the listener without a restart and sends a browser on the old address to the new URL before the old listener closes, and allowlist edits take effect at once. Removing your own login from a remote device warns but isn't blocked.
- **Work fleets.** There is no per-fleet local-only flag; one Admiral serves every fleet. Whether a work fleet may be reached over a personal tailnet is the engineer's call.

## Considered options

- **Behind `tailscale serve`** (chosen, option B). HTTPS and a stable name with nothing new in the binary.
- **Open the tailnet IP by hand** (option A). It stays documented as the zero-setup path: plain HTTP to `http://<tailnet IP>:7420`, with no HTTPS.
- **Embed tsnet** (option C). Rejected: about 19 MB more, its dependency tree, node key expiry and a second tailnet node. Revisit only if Admiral must be reachable without the Tailscale app on the Mac, or needs its own tailnet identity.

## Consequences

- This amends [ADR 0001](0001-local-web-app-served-from-the-mac.md): loopback is local only without proxy headers, Tailscale mode listens on loopback, and the token stays as a second factor beside a Tailscale identity allowlist.
- Web Push and an installed app can rely on HTTPS through the tailnet URL ([ADR 0033](0033-pushes-are-web-push-from-the-mac-with-no-hosted-relay.md)).
