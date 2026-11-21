# Order sources

Source events, the built-in GitHub source and views. Read it before changing `internal/sources` or `internal/github`.

- Source events: `internal/sources/events.go` keeps a runner's `events.catalog`, validates and bounds each `event` notification (60 a minute) and hands it to `Supervisor.OnEvent`, which `cmd/admiral/routines_wire.go` routes to `Scheduler.HandleEvent`.
  - The built-in GitHub source raises its own verified events (`review_requested`, `assigned`, `mentioned`) by diffing each poll against the last one; the first poll is a baseline and emits nothing, and every cycle runs its three event searches whether or not a routine uses them.
  - A review request or assignment names its actor from the timeline; a mention names none, and an actor it cannot name is `(unknown)`, which no allowlist holds, so the event is T3.
  - `ships.RefHolder` (wired as `Scheduler.Holder` over the ship listing, pending launches included) names the listed ship that holds an event's PR or issue: a PR in its `PRs` or its order URL, or an issue it claims.
  - `HandleEvent` asks it after the routines in scope are found and before the Lookout, and writes one `skipped` record per matching routine with reason `ref_has_ship` and `held_by` (the holder's session, never `Trigger.Ship`, which the launch readers take as an event ship).
- The built-in GitHub source (ADR 0006).
  - `plugin: "github"` runs inside the server, with no plugin folder and no subprocess: a `plugins/github` folder is ignored and `ListPlugins` always lists the built-in entry.
  - `internal/sources/github.go` has `githubConn`, which implements the `conn` interface (`call`, `stop`, `kill`, `exited`, `exitReason`) that `proc` also implements, so the runner's state, restart and error handling are the same for both and `parseInitialize`, `parsePanels` and `parseOrders` read its JSON unchanged.
  - It answers `initialize` with the `refresh` capability only and a settings schema with `views`; `orders.list` answers an error for a view that failed, as a plugin source does.
  - It refreshes at start, on `orders.refresh` and every `refreshMinutes` (default 5), through `internal/github.Client` with the token from `gh auth token` passed as an env variable. A cancel error is dropped.
  - Every refresh rebuilds `github.RouterFromFleets` from the live config and keeps only the items `Route` gives to the source's fleet; the router's warnings go on the runner and show as `SourceView.warnings`.
  - A change to fleet owners or checkouts refreshes every runner (`routingOf` in `supervisor.go`).
  - `serveAdd` and `serveSave` write `views: []` when the settings have none.
  - The startup migration (`config.Migrate`) turns an old plugin source, one whose settings have no `views`, into a built-in one (`panels` becomes `views`) and adds one to each fleet that has none, with no autopilot; `sources.RemoveRetired` deletes an untouched `plugins/github` folder.
  - Admiral ships no plugin, so nothing installs plugin folders.
- Views. `/api/sources` and the WebSocket `sources` message call a source's lists `views` (`SourceView.Views`); the old key was `panels`. Unchanged: the route paths (`/panels/<id>/autopilot`), the `PUT .../panel-order` body key `panels`, `panel_order`, the autopilot keys and the plugin wire method `panels.list`.
