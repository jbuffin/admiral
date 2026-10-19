#!/bin/sh
# Prints THIRD_PARTY_NOTICES: the licence text of the Go toolchain, every Go
# module the binary links, every production npm package in the UI (the whole
# tree, from the lockfile), the bundled fonts and the GitHub plugin icon. Run
# from the repo root after `npm --prefix web ci`; `make release` calls it. It
# fails when an entry has no licence text.
set -eu

# licence_text <dir> prints the first licence file in dir, or fails.
licence_text() {
	for f in "$1"/LICENSE "$1"/LICENSE.md "$1"/LICENSE.txt "$1"/LICENSE-MIT "$1"/LICENCE "$1"/COPYING; do
		if [ -s "$f" ]; then cat "$f"; return 0; fi
	done
	echo "notices.sh: no licence text in '$1'" >&2
	return 1
}

echo "Admiral bundles the following third-party software."

echo
echo "== Go standard library and runtime =="
echo
echo "-- go $(go env GOVERSION)"
licence_text "$(go env GOROOT)"

echo
echo "== Go modules =="
mods=$(go list -m -f '{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}' all)
while read -r path version dir; do
	echo
	echo "-- $path $version"
	licence_text "$dir"
done <<EOM
$mods
EOM

echo
echo "== npm runtime dependencies =="
pkgs=$(node -e '
const lock = require("./web/package-lock.json");
for (const [k, p] of Object.entries(lock.packages)) {
  if (k.startsWith("node_modules/") && !p.dev && !p.devOptional) console.log(k.slice("node_modules/".length) + " " + p.version);
}')
[ -n "$pkgs" ] || { echo "notices.sh: no production packages in web/package-lock.json" >&2; exit 1; }
while read -r name version; do
	echo
	echo "-- $name $version"
	licence_text "web/node_modules/$name"
done <<EOM
$pkgs
EOM

echo
echo "== Fonts (SIL Open Font License 1.1) =="
for f in web/public/licenses/OFL-*.txt; do
	echo
	echo "-- $f"
	cat "$f"
done

echo
echo "== Primer Octicons (the GitHub plugin's icon) =="
echo
echo "-- plugins/github/LICENSE-octicons"
cat plugins/github/LICENSE-octicons
