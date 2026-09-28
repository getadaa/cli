#!/bin/sh
# Installs the adaa CLI from the latest GitHub release, after checking it
# against the release's checksums.txt.
#
#   curl -fsSL https://raw.githubusercontent.com/getadaa/cli/main/install.sh | sh
#
# ADAA_VERSION picks a release (e.g. 0.3.0) and ADAA_INSTALL_DIR where the
# binary goes (default ~/.local/bin, which needs no sudo). `adaa update` keeps
# an install made this way current.
set -eu

repo="getadaa/cli"
dir="${ADAA_INSTALL_DIR:-$HOME/.local/bin}"

fail() {
	echo "adaa install: $*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

need curl
need tar
need uname

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) fail "unsupported system $(uname -s); on Windows use install.ps1" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "unsupported architecture $(uname -m)" ;;
esac

version="${ADAA_VERSION:-}"
if [ -z "$version" ]; then
	# The latest-release page redirects to its tag, which saves an API call
	# and the rate limit that comes with one.
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
		fail "could not reach GitHub"
	version="${latest##*/}"
fi
version="${version#v}"
[ -n "$version" ] || fail "could not work out the latest version"

archive="adaa_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/v$version"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading adaa $version for $os/$arch..." >&2
curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "could not download $base/$archive"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
else
	fail "sha256sum or shasum is required to verify the download"
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $archive: refusing to install it"

tar -xzf "$tmp/$archive" -C "$tmp" adaa
mkdir -p "$dir"
# Replaced by rename rather than overwritten, so a running adaa is not
# corrupted mid-flight.
cp "$tmp/adaa" "$dir/.adaa.new"
chmod 0755 "$dir/.adaa.new"
mv -f "$dir/.adaa.new" "$dir/adaa"

echo "Installed $("$dir/adaa" --version) to $dir/adaa" >&2
case ":$PATH:" in
*":$dir:"*) ;;
*) echo "Note: $dir is not on your PATH. Add it, e.g. echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile" >&2 ;;
esac
echo "Next: adaa login" >&2
