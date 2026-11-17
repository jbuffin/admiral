<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/images/hero-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="docs/images/hero-light.png">
  <img alt="Admiral: launch coding agents by dragging a task onto a dock, then watch every session sail as a ship you can board" src="docs/images/hero-light.png" width="1280">
</picture>

# Admiral

A dashboard for running coding agents (Claude Code, Codex CLI, GitHub Copilot CLI) in the background. Drag a task onto a harness's dock to launch a session, watch every session sail as a ship, and tap one to attach to its terminal. It works from your Mac, iPad or phone.

## Requirements

- An Apple silicon Mac with `tmux` (`brew install tmux`).
- At least one harness, installed and signed in: Claude Code, Codex CLI or GitHub Copilot CLI.
- Optional: the [Commodore](https://github.com/jbuffin/skills) skill, the default skill for a launch. Admiral offers to install it in `admiral setup` and in the launch dialog. To do it yourself, run `npx skills add jbuffin/skills`, or in Claude Code install the `jasonbuffin@jbuffinskills` plugin. Without it, you launch with another skill or none.

## Install

```sh
brew install jbuffin/admiral/admiral
```

No Homebrew? Use the [install script](docs/install.md#install-script).

## Get started

```sh
admiral setup            # find your harnesses and add your first fleet
admiral service install  # run Admiral at login and keep it running
```

Open <http://127.0.0.1:7420> on your Mac. To add an iPad or phone, run `admiral pair` and open the pairing URL it prints on that device; the code lasts five minutes.

`admiral service status` says whether the service is running. Logs go to `~/.admiral/logs/admiral.log`.

## Upgrade

```sh
brew upgrade admiral
launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral   # restart the service on the new version
```

To uninstall, see [Install, upgrade and uninstall](docs/install.md#uninstall).

## Contributing

Bugs and ideas go in [GitHub Issues](https://github.com/jbuffin/admiral/issues). To build from source you need Go (version in `go.mod`), Node (version in `.nvmrc`) and tmux:

```sh
npm --prefix web ci                               # once per checkout
npm --prefix web exec playwright install webkit   # once, for the browser tests
make build                                        # the UI, then bin/admiral
bin/admiral --listen 127.0.0.1:7420
```

For UI work, keep that server running and start Vite with `npm --prefix web run dev`.

Before you open a pull request, run `make check` (build, lint and unit tests; what PR CI runs) and `make e2e` (the server and browser tests, which CI doesn't run), and paste the `make e2e` result into the PR's `## E2E evidence` section. [`AGENTS.md`](AGENTS.md) covers the rest: the glossary, the code map, which tests to write and how to run them.

## More

- [Command line reference](docs/cli.md): every `admiral` command.
- [Plugin guide](docs/plugins.md): write an order source plugin.
- [Personal builds](docs/personal-builds.md): build with the official harness logos.

Admiral is released under the MIT License ([`LICENSE`](LICENSE)); its bundled third-party code keeps its own licences, listed in `THIRD_PARTY_NOTICES`. Admiral is not affiliated with or endorsed by Anthropic, OpenAI or GitHub. Claude, Codex, Copilot and their logos are trademarks of their owners.
