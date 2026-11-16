# Install, upgrade and uninstall

The [README](../README.md) covers the usual path: install with Homebrew, then `admiral setup` and `admiral service install`. This page has the rest.

## Install script

The script installs to `~/.local/bin` and needs no `sudo`:

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://github.com/jbuffin/admiral/releases/latest/download/install.sh | sh
```

Options go between the pipe and `sh`, as in `curl … | PREFIX=<dir> ADMIRAL_VERSION=<x.y.z> sh`:

- `PREFIX=<dir>` puts the binary at `<dir>/bin/admiral` (default `~/.local`).
- `ADMIRAL_VERSION=<x.y.z>` picks a release (default the latest).

The script checks the tarball against its SHA-256 file before unpacking it. It warns when `tmux` is missing (it doesn't install it) and when the install folder isn't on your `PATH`; add it in `~/.zshrc`, for example `export PATH="$HOME/.local/bin:$PATH"`.

To upgrade, run the script again with the same `PREFIX`, then restart the service:

```sh
launchctl kickstart -k gui/$UID/dev.jasonbuffin.admiral
```

## Gatekeeper

The binary is ad-hoc signed, not notarized. Homebrew and the install script leave no quarantine attribute, so Gatekeeper doesn't stop it. A tarball downloaded in a browser needs `xattr -d com.apple.quarantine bin/admiral` first.

## Switch from the script to Homebrew

```sh
brew install jbuffin/admiral/admiral
$(brew --prefix)/bin/admiral service install   # points the service at the Homebrew binary; set ADMIRAL_HOME again here if you use one
rm ~/.local/bin/admiral                        # or <PREFIX>/bin/admiral
```

## Where Admiral keeps its data

Everything lives in `~/.admiral/`, or in `$ADMIRAL_HOME` if you set it: your config, the access token, state, `logs/`, `plugins/` (the bundled plugins and any you added) and `data/` (each order source's data).

## Uninstall

```sh
admiral service uninstall
tmux -S ~/.admiral/tmux.sock kill-server   # ends every running ship; use $ADMIRAL_HOME/tmux.sock if you set it
brew uninstall admiral                     # or, for a script install: rm ~/.local/bin/admiral (or <PREFIX>/bin/admiral)
```

Admiral's tmux server and its ships outlive the service, so end them with `kill-server` before you delete the data folder. Uninstalling leaves `~/.admiral/`; delete it to remove everything.
