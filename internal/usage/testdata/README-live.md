Live-format samples for the live store's parsers.

- codex-tokenusage-recorded.json: a real `thread/tokenUsage/updated` frame (copied from internal/conversation/testdata/codex-live-sample.jsonl).
- codex-ratelimits-unverified.json: UNVERIFIED. Built from the documented app-server schema with fake values; no real `account/rateLimits/updated` frame has been captured. A real capture is owed.
- claude-statusline-kept.json: the documented-shape status line as `admiral statusline` keeps it, with fake values; not a capture. The raw shape is internal/cli/testdata/claude-statusline.json.
