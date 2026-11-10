# Reaching Admiral from outside the LAN with Tailscale

> Research for the Admiral redesign map, 2026-11-09.

## How Admiral works today (repo facts)

- `admiral` listens on `0.0.0.0:7420` (`--listen`, `cmd/admiral/main.go:148`) over plain HTTP and prints `http://<LAN IPv4>:7420/?token=…` for other devices (ADR 0001, revised by #23).
- `internal/auth`: **a loopback peer is trusted with no token** once its Host header passes `hostAllowed`, which accepts localhost, the Mac's own interface IPs and `os.Hostname()`/`.local`. Any other peer must show the 256-bit token, either as a `?token=` that gets swapped for a cookie (SameSite=Strict, HttpOnly) or as `?token=` on the WebSocket. Origin must equal `http(s)://<Host>` on WebSocket and non-GET requests. `admiral token rotate` cuts off every remote socket.
- The bridge (`/ws/bridge/<session>`) goes through the same middleware, so remote access means typing into agent terminals, which is remote code execution by design.
- `web/src/Bridge.tsx` already checks `isSecureContext` before using `navigator.clipboard`. On `http://<LAN IP>` the iPad has no async clipboard, no service worker and no Web Push.
- `go.mod` has 4 direct dependencies, and the binary is about 14 MB.

## Options

**A. External: do nothing.** The user runs the Tailscale app on the Mac and the iPad and opens `http://<mac 100.x or MagicDNS name>:7420/?token=…`. This works today: the peer is not loopback, so the token check applies, and WireGuard encrypts the traffic. The gaps: the printed URL shows the LAN IP rather than the tailnet one, the page is still HTTP so it is not a secure context (no clipboard, PWA push or service worker), and Admiral also listens on every other interface.

**B. Optional built-in on an existing tailscaled (`tailscale serve`).** Serve runs a Go `httputil.ReverseProxy` that **keeps the incoming Host** (`r.Out.Host = r.In.Host`). It adds `X-Forwarded-*` and `Tailscale-User-Login/Name/Profile-Pic`, and strips copies of those headers sent by the client. It leaves the identity headers out for tagged devices and for Funnel traffic. It needs HTTPS and MagicDNS turned on for the tailnet, and the certificate renews automatically. WebSockets pass through.
- Today this **fails closed**. The request comes from 127.0.0.1 with Host `mac.tailnet.ts.net`, `hostAllowed` rejects that name, and Admiral answers 403.
- The danger is in fixing it naively. If you add the ts.net name to `hostAllowed`, every tailnet peer, and with Funnel the whole internet, becomes "local" and skips the token. Proper support needs Admiral to treat a loopback request that carries `X-Forwarded-For` or a ts.net Host as remote. It would then require the token, or a `Tailscale-User-Login` on an allowlist, and refuse any request that has no identity header (Funnel or a tagged device). It should also offer `--listen 127.0.0.1:7420` so the LAN path is closed.
- Cost: a small amount of Go and no new dependencies. Tailscale's own Serve doc says to bind to localhost when you trust these headers.

**C. First-class with tsnet embedded.** Admiral becomes its own tailnet node, for example `admiral.<tailnet>.ts.net`.
- `tsnet.Server{Dir, Hostname, AuthKey, Ephemeral, AdvertiseTags, ClientSecret}`. With no key it prints a login URL, which suits `admiral setup`. State would live under `$ADMIRAL_HOME/tsnet`.
- `ListenTLS` gives HTTPS with a Let's Encrypt certificate. `LocalClient().WhoIs(RemoteAddr)` returns the caller's login, which is a real identity and could replace the token for tailnet peers.
- `ListenFunnel` exists, so not exposing it is a code decision.
- The module is BSD-3-Clause (v1.104.x, Nov 2026).
- Costs:
  - Binary size: one report saw about +19 MB (28 MB to 47 MB). Admiral would roughly double from 14 MB.
  - A large transitive dependency tree, against 4 direct dependencies today, with Tailscale's release pace driving go.mod churn.
  - A userspace netstack inside the server process.
  - A second device in the tailnet next to the Mac's own client.
  - Node key expiry, 180 days by default, which breaks access unless disabled or tagged.
  - Every bug in that stack becomes Admiral's to support.

**Alternatives, for comparison only.**
- Cloudflare Tunnel: public hostname, so it needs Cloudflare Access in front. cloudflared connects from loopback with the public Host, so it hits the same 403 and the same "local" trap. The code already mentions it.
- ngrok: a public URL with traffic through a third party. Not acceptable for an RCE surface without its OAuth gate.
- Plain WireGuard: you run it yourself, it needs port forwarding or a relay, and it gives no identity or certificates. Admiral sees it the same as option A.

## Feasibility

All three are feasible. A works now. B is about a day: the auth middleware change, the listen and printed-URL changes, docs, and e2e coverage of the auth flow. C is a few days, plus ongoing maintenance and dependency weight.

## Security invariants (any option)

1. Never public. Admiral must not call `ListenFunnel`, and must refuse a proxied request that has no tailnet identity, which is what Funnel traffic looks like.
2. A request through a proxy is remote even when it arrives on loopback. The "loopback = local" rule has to account for `X-Forwarded-For` and the tailnet Host.
3. Identity: an allowlist of `Tailscale-User-Login` values (the owner by default). Tailscale ACLs alone are not enough, because shared-in users also get identity headers. Tagged devices are refused unless configured.
4. The token stays a second factor, or at least a fallback. Rotation must still cut off remote sockets.
5. Fleets: one Admiral serves every fleet, a work fleet included. If work repos are reachable from a personal tailnet, that may break employer policy. The ADR should say so and perhaps let the user opt out per fleet.
6. Certificate Transparency publishes the machine name, so the hostname must not be sensitive.

## iPad and PWA

Service workers, Web Push (Home Screen apps, iOS 16.4+) and `navigator.clipboard` all need a secure context, which means HTTPS. Plain-HTTP option A cannot provide them. Options B and C get a real certificate for `*.ts.net`, so they can. That is the strongest argument for doing more than A.

## Recommendation: optional built-in, using option B rather than tsnet

- Keep Tailscale outside the binary, and make Admiral **proxy-aware and Tailscale-aware** with the work in B: correct remote detection, an identity allowlist, loopback-only listen, and a printed tailnet HTTPS URL when `tailscale serve` is set up. This also fixes the latent proxy hole for Cloudflare and any other proxy.
- Document the external option A as the zero-setup path.
- Reasons: B gets HTTPS and identity, which covers the PWA and the clipboard, without the 19 MB, the dependency tree, key expiry or a second node. The Mac already needs the Tailscale app so it can reach the iPad.
- Revisit tsnet only if users want Admiral reachable without installing Tailscale on the Mac, or want a per-service tailnet identity.

## What an ADR must decide

- Whether loopback still means "local" when proxy headers are present (it supersedes part of ADR 0001's consequences).
- Whether a Tailscale identity replaces the token or comes on top of it, and where the login allowlist lives (`config.json`?).
- The default `--listen` when Tailscale is in use, and whether LAN access stays on.
- How Admiral rejects Funnel and tagged devices, and whether it checks the serve config itself (`tailscale serve status --json`).
- The fleet-level policy for work fleets on a personal tailnet.
- What `admiral setup` and the startup line print (the HTTPS URL).
- PWA scope: whether to add a manifest and a service worker now that HTTPS is possible.

## Sources

- https://pkg.go.dev/tailscale.com/tsnet
- https://tailscale.com/kb/1244/tsnet
- https://tailscale.com/kb/1312/serve
- https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go (Host preserved, identity headers, tagged devices skipped)
- https://tailscale.com/kb/1153/enabling-https
- https://tailscale.com/kb/1028/key-expiry
- https://tailscale.com/kb/1207/small-tailscale
- https://olivierforget.net/blog/2024/dropserver-progress-june-2024/ (tsnet +19 MB anecdote)
- https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/
- https://developer.mozilla.org/en-US/docs/Web/Security/Secure_Contexts
- Repo: `internal/auth/auth.go`, `cmd/admiral/main.go` (serve, about lines 146-420), `internal/server/lan.go`, `docs/adr/0001-local-web-app-served-from-the-mac.md`, `web/src/Bridge.tsx`
