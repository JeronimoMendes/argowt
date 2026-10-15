#!/bin/sh
# Install the latest argowt release binary.
#
#   curl -fsSL https://raw.githubusercontent.com/JeronimoMendes/argowt/main/install.sh | sh
#
# Env:
#   ARGOWT_VERSION  release tag to install, e.g. v1.2.0 (default: latest)
#   BINDIR          install directory (default: ~/.local/bin)
set -eu

RELEASES_URL="https://github.com/JeronimoMendes/argowt/releases"
VERSION="${ARGOWT_VERSION:-latest}"
BINDIR="${BINDIR:-$HOME/.local/bin}"

fail() {
	echo "argowt install: $*" >&2
	exit 1
}

# Map the platform to a release asset name, e.g. argowt-darwin-arm64.
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
darwin | linux) ;;
*) fail "unsupported OS: $os" ;;
esac

arch="$(uname -m)"
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "unsupported architecture: $arch" ;;
esac

asset="argowt-$os-$arch"

if [ "$VERSION" = latest ]; then
	base="$RELEASES_URL/latest/download"
else
	base="$RELEASES_URL/download/$VERSION"
fi

# Download in a temp dir, removed on exit.
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
trap 'exit 1' INT TERM

echo "Downloading $asset ($VERSION)"
curl -fsSL -o "$workdir/$asset" "$base/$asset"
curl -fsSL -o "$workdir/checksums.txt" "$base/checksums.txt"

# Verify the binary against the published checksum.
expected="$(awk -v name="$asset" '$2 == name { print $1 }' "$workdir/checksums.txt")"
[ -n "$expected" ] || fail "no checksum for $asset"

if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$workdir/$asset" | awk '{ print $1 }')"
else
	actual="$(shasum -a 256 "$workdir/$asset" | awk '{ print $1 }')"
fi
[ "$actual" = "$expected" ] || fail "checksum mismatch for $asset"

mkdir -p "$BINDIR"
install -m 0755 "$workdir/$asset" "$BINDIR/argowt"
echo "Installed $("$BINDIR/argowt" --version) to $BINDIR"

# Warn when the install dir is not on PATH.
case ":$PATH:" in
*":$BINDIR:"*) ;;
*) echo "Add $BINDIR to PATH to run argowt" ;;
esac
