#!/bin/sh
# Prints THIRD_PARTY_NOTICES: the licence text of every Go module the binary
# links, every npm runtime dependency and the bundled fonts. Run from the repo
# root after `npm --prefix web ci`; `make release` calls it.
set -eu

echo "Admiral bundles the following third-party software."
echo

echo "== Go modules =="
go list -m -f '{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}' all | while read -r path version dir; do
	echo
	echo "-- $path $version"
	for f in LICENSE LICENSE.md LICENSE.txt COPYING; do
		if [ -f "$dir/$f" ]; then cat "$dir/$f"; break; fi
	done
done

echo
echo "== npm runtime dependencies =="
sed -n '/"dependencies"/,/}/p' web/package.json | sed -n 's/^ *"\([^"]*\)":.*/\1/p' | grep -v '^dependencies$' | while read -r name; do
	dir=web/node_modules/$name
	version=$(sed -n 's/^ *"version": "\(.*\)",*/\1/p' "$dir/package.json" | head -1)
	echo
	echo "-- $name $version"
	found=
	for f in LICENSE LICENSE.md LICENSE.txt OFL.txt; do
		if [ -f "$dir/$f" ]; then cat "$dir/$f"; found=1; break; fi
	done
	[ -n "$found" ] || echo "(licence: $(sed -n 's/^ *"license": "\(.*\)",*/\1/p' "$dir/package.json" | head -1); text in web/public/licenses/)"
done

echo
echo "== Fonts (SIL Open Font License 1.1) =="
for f in web/public/licenses/OFL-*.txt; do
	echo
	echo "-- $f"
	cat "$f"
done
