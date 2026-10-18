#!/bin/sh
# Installs Admiral from a release tarball. Needs no admin rights and writes only under PREFIX.
#
#   curl -fsSL https://github.com/jbuffin/admiral/releases/latest/download/install.sh | sh
#
# PREFIX           where bin/admiral goes (default ~/.local)
# ADMIRAL_VERSION  the release to download (default: the latest)
# ADMIRAL_TARBALL  a local tarball to install instead; its checksum is the
#                  file next to it with ".sha256" appended
set -eu

repo=jbuffin/admiral
prefix=${PREFIX:-$HOME/.local}

fail() { echo "install.sh: $*" >&2; exit 1; }
warn() { echo "warning: $*" >&2; }

[ "$(uname -s)" = Darwin ] || fail "Admiral runs on macOS only"
# uname -m says x86_64 for a shell running under Rosetta; sysctl knows the hardware.
sysctl=$(command -v sysctl || echo /usr/sbin/sysctl)
[ "$("$sysctl" -n hw.optional.arm64 2>/dev/null || echo 0)" = 1 ] || fail "Admiral needs an arm64 (Apple silicon) Mac"

work=$(mktemp -d "${TMPDIR:-/tmp}/admiral-install.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

if [ -n "${ADMIRAL_TARBALL:-}" ]; then
	tarball=$ADMIRAL_TARBALL
	sumfile=$ADMIRAL_TARBALL.sha256
	[ -f "$tarball" ] || fail "$tarball not found"
	[ -f "$sumfile" ] || fail "$sumfile not found"
else
	if [ -n "${ADMIRAL_VERSION:-}" ]; then
		version=${ADMIRAL_VERSION#v}
		base=https://github.com/$repo/releases/download/v$version
	else
		base=https://github.com/$repo/releases/latest/download
		version=$(curl -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed 's|.*/tag/v||')
		[ -n "$version" ] || fail "could not find the latest release"
	fi
	name=admiral-$version-darwin-arm64.tar.gz
	# curl leaves no quarantine attribute, so Gatekeeper doesn't block the unsigned binary.
	curl -fsSL -o "$work/$name" "$base/$name" || fail "could not download $base/$name"
	curl -fsSL -o "$work/$name.sha256" "$base/$name.sha256" || fail "could not download $base/$name.sha256"
	tarball=$work/$name
	sumfile=$work/$name.sha256
fi

want=$(awk '{print $1}' "$sumfile")
got=$(shasum -a 256 "$tarball" | awk '{print $1}')
[ -n "$want" ] && [ "$want" = "$got" ] || fail "checksum mismatch for $tarball: expected '$want', got '$got'"

tar -xzf "$tarball" -C "$work"
bin=$(ls "$work"/admiral-*-darwin-arm64/admiral 2>/dev/null | head -n 1)
[ -n "$bin" ] || fail "the tarball has no admiral binary"

mkdir -p "$prefix/bin"
cp "$bin" "$prefix/bin/admiral.new"
chmod 755 "$prefix/bin/admiral.new"
mv -f "$prefix/bin/admiral.new" "$prefix/bin/admiral"
echo "Installed $prefix/bin/admiral"

case ":$PATH:" in
*":$prefix/bin:"*) ;;
*) warn "$prefix/bin is not on your PATH. Add it, for example: export PATH=\"$prefix/bin:\$PATH\"" ;;
esac
missing=
command -v tmux >/dev/null 2>&1 || missing="$missing tmux"
command -v python3 >/dev/null 2>&1 || missing="$missing python3"
[ -z "$missing" ] || warn "missing:$missing. Admiral needs them; run: brew install tmux python@3.15"

echo "Next: admiral setup, then admiral service install to start it at login."
