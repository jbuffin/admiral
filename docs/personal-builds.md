# Personal builds

A build from your own checkout can carry the official harness logos: put them in `web/logos/official/` (see "[Logos](#logos)" below) and run `make build`. `make release` refuses to run while that folder holds files, so a shared tarball only ever has the lettered placeholders. Admiral is not affiliated with or endorsed by Anthropic, OpenAI or GitHub. Claude, Codex, Copilot and their logos are trademarks of their owners.

## Logos

The flags draw a logo `<symbol>` by id (`logo-claude`, `logo-copilot`, `logo-codex`). Without official files the build uses lettered placeholders (Cl, Co, Cx). For a personal build, put the official logo files in `web/logos/official/` as `claude.svg`, `copilot.svg` and `codex.svg` (the folder is gitignored) and rebuild; they replace the placeholders under the same ids. Each file holds the mark only, on a transparent background, with a `viewBox`. Admiral draws each harness's field from the vendors' usage rules (Claude ivory, Copilot dark, Codex white) and centres the mark with clear space. A harness Admiral doesn't know draws the `logo-unknown` placeholder, a "?" on a plain field. Never redraw a vendor logo or commit these files.

The new shell at `/app/` reads its logos at runtime instead. Put `claude.svg`, `copilot.svg` and `codex.svg` in `$ADMIRAL_HOME/logos/` (`~/.admiral/logos/` by default) and Admiral serves them from `/api/v1/logos/<harness>`. Without a file the route answers 404 and the shell draws the lettered placeholder. These files are read from your own machine when asked and never shipped in a build.
