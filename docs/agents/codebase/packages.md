# Package map

The Go packages and what each owns, and the top-level directories. Read it to find the package for a change.

- `cmd/admiral/`, `internal/`: the Go server and CLI.
  - `internal/auth` is access control (per-device tokens in `$ADMIRAL_HOME/devices.json`, pairing codes, the device API; see [devices](devices.md)).
  - `internal/server` is HTTP and WebSocket.
  - `internal/home` finds `$ADMIRAL_HOME`.
  - `internal/crew` resolves crew members and their notes from layer directories it is handed (read-only; it reads no environment variable).
  - `internal/launcher` resolves a launch's pinned members over the built-in (the installed Commodore's `crew/`, found by `skillindex.CommodoreDir`), fleet and repo layers.
  - `internal/launchorders` lists the members a launch could pin.
  - `internal/crewapi` serves a fleet's Crew page (`/api/fleets/<key>/crew`), drafts a new member from a description with one judge call (`draft.go`, prompt `crewwriter.md`; the call is the `Service.Judge` seam, `judge.Run` when nil).
  - `internal/crewapi` also holds the write side of the crew directory: fleet member files and notes, written atomically through `Lstat`-checked paths that refuse a link (`ApplyNotes`, `KeepAdHoc` and `DropAdHoc` are what `internal/inbox` calls to write kept crew notes and ad hoc members, all under one `writeMu` hold and all-or-nothing: they plan every write in memory, refuse a misfit before writing, and put back what they wrote if a later write fails; `AppendNote` adds one note; they make the crew directories themselves).
  - `internal/config` loads, validates, watches and writes `$ADMIRAL_HOME/config.json`; it also reads the top-level `max_ships` (see [launching ships](launch.md)).
  - `internal/sessionhost` is the only non-test package that runs tmux or builds its commands (`TestNoTmuxOutsideSessionhost` guards it: callers use its `Server` methods and `*Call` builders, and `admiral hook` its current-server runners) and holds the one tmux control-mode connection on `$ADMIRAL_HOME/tmux.sock` (it re-reads the sessions about every half second, and a global `alert-bell` hook stamps bells).
  - `internal/launcher` creates ships (names, hook bundle in `$ADMIRAL_HOME/harness/`, command lines).
  - `internal/chat` is the AdmiralBot chat session (see [chat](chat.md)).
  - `internal/usage` parses the exit figures a harness leaves (tokens by disjoint class, money with its basis, Codex quota windows) from a Claude, Codex or Copilot transcript, and subtracts cumulative readings, and builds a `usage` outcome record's fields (`ExitFields`: a ship's totals are its own figures plus its crew boats', money only within one basis).
  - `internal/ships` writes the `usage` outcome record beside `ended` at each exit, once per exit and off the pass goroutine (`UsageRead`, filled by `TranscriptUsage`, reads the ship's and its crew boats' transcripts; `LastLive` is `usage.Store.LastLive`, see `internal/usage` above).
  - `internal/chat` writes a `usage` outcome record at each reset.
- `internal/stackmerge` answers whether a stack's top PR merges into its base, in an Admiral-owned bare clone under `$ADMIRAL_HOME/stackcheck/` (the moored poll's `Service.StackChecker`; see the `stack_ready` paragraph).
- If the tmux host never becomes readable (`sessionhost.Host.Known` stays false), no routine fires, and a ship that ended in the meantime is not marked failed.
- `internal/shiptitle` titles a ship launched without an order (`titled` unset): `shipconv.Tails` keeps each followed transcript's first prompt and Claude Code's `ai-title` (`Tails.Seed`), and once a first prompt is read the titler writes the `title`, `link` and `titled` session options once, from the GitHub title of the main link (`github.Client.ItemTitle`), Claude's title, the titler judge call (`titler.md`, Lookout model) or the first line. A launch from an order or a later round sets `titled` itself. `Service.Titling` is the ship's `titling` field while it works.
- `packaging/`: the Homebrew formula, the curl install script and the notices generator. `make release` builds the tarball.
- `e2e/`: server tests that run the real binary.
