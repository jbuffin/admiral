# Releasing

## Cutting a release

Tag `v<x.y.z>`; the version is `<x.y.z>`. `make release` drops the leading `v` once, so the tarball name, `admiral --version`, `install.sh`'s URL and the formula's `url` all use `<x.y.z>`.

Run the **Release** workflow on `main` (Actions → Release → Run workflow, or `gh workflow run release.yml -f bump=patch`). It refuses any other branch, and only one release runs at a time.

- `bump` is `patch`, `minor` or `major`. The workflow raises the highest `v<x.y.z>` tag (with none, the first release is `0.1.0`). `version` takes an exact `x.y.z` instead and must not be tagged yet. `packaging/next-version.sh` does this; run it locally to see what a bump would give.
- It runs `make build lint test` and `make release`, renders the formula with `packaging/homebrew/render.sh`, and checks it with `brew style` and an offline `brew audit --strict`. A failure up to here tags and publishes nothing.
- It then tags the commit, creates the GitHub release with generated notes and the tarball, its `.sha256` and `install.sh`, and commits the formula to `Formula/admiral.rb` in the tap `jbuffin/homebrew-admiral`. It runs `brew audit --strict --online` before pushing to the tap's `main`.
- Last, it installs the release with `brew install jbuffin/admiral/admiral` and `brew test admiral`, and again with the published `install.sh`, and checks that both print the version.
- `dry_run` stops after the formula check, so it builds and audits but tags, publishes and pushes nothing.

One-time setup: the public tap repo `jbuffin/homebrew-admiral`, a deploy key with write access on it, and that key's private half in this repo's secret `HOMEBREW_TAP_DEPLOY_KEY`. To replace the key:

```sh
ssh-keygen -t ed25519 -N '' -C "admiral release workflow" -f tap-key
gh repo deploy-key add tap-key.pub -R jbuffin/homebrew-admiral --allow-write --title "admiral Release workflow"
gh secret set HOMEBREW_TAP_DEPLOY_KEY -R jbuffin/admiral < tap-key
rm tap-key tap-key.pub
```

Then delete the old key with `gh repo deploy-key delete <id> -R jbuffin/homebrew-admiral`.

By hand, if the workflow can't run:

1. Tag the commit, `git tag -a v<x.y.z> -m "Admiral <x.y.z>"`, with no files in `web/logos/official/` and a clean tree, and push the tag.
2. Run `npm --prefix web ci` and `make release`. It rebuilds the UI from clean, builds `bin/admiral` for darwin/arm64 with `-trimpath`, stamped with the version (`VERSION=x.y.z` on the command line overrides `git describe`) and the commit, checks `codesign -v`, and writes `dist/admiral-<x.y.z>-darwin-arm64.tar.gz`, its `.sha256` and a copy of `install.sh`. The tarball holds `admiral`, `LICENSE`, `THIRD_PARTY_NOTICES` and reference copies of `plugins/freeform/` and `plugins/github/`. Never `strip` the binary: that breaks its signature.
3. Create the GitHub release for the tag `v<x.y.z>` and upload everything in `dist/`: the tarball, the `.sha256` file and `install.sh`.
4. In the tap repo, write `sh packaging/homebrew/render.sh <x.y.z> <sha256>` to `Formula/admiral.rb`, then run `brew style` and `brew audit --strict --online jbuffin/admiral/admiral`. The formula has no `version` line because Homebrew reads it from the `url`; if the audit disagrees, the audit decides.
