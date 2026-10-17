# 1. Admiral is a local web app served from the Mac

Status: accepted

## Context

Admiral must be private, need no hosting, and be usable from the Mac it runs on and from a tablet on the home network or over VPN. Dragging must work with touch.

## Decision

Admiral is a web app served by a small local process on the Mac. The engineer opens it in a browser on the Mac, or on another device that reaches the Mac over the LAN or VPN. Nothing is hosted elsewhere.

## Considered options

- **Native macOS app.** Smooth, but needs signing and notarization, and does not reach a tablet.
- **Hosted web app.** Rejected: the work is private, and a hosted terminal bridge would expose the machine.

## Consequences

The server bridges terminals to the browser, which is remote code execution by design. It listens on the LAN by default, and every non-local browser must present a token: the server prints its URL with the token at startup, and opening it once sets a cookie. There is no pairing flow. The UI must be responsive and touch-first.

Revised by [LAN access with a startup token](https://github.com/jbuffin/admiral/issues/23): this ADR first made LAN access an opt-in, with pairing or a token.
