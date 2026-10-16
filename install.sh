#!/bin/sh
# Install the latest argowt release binary.
#
#   curl -fsSL https://raw.githubusercontent.com/JeronimoMendes/argowt/main/install.sh | sh
#
# Env:
#   ARGOWT_VERSION  release tag to install, e.g. v1.2.0 (default: latest)
#   BINDIR          install directory (default: ~/.local/bin)
#   NO_COLOR        disable colors and animations
set -eu

RELEASES_URL="https://github.com/JeronimoMendes/argowt/releases"
VERSION="${ARGOWT_VERSION:-latest}"
BINDIR="${BINDIR:-$HOME/.local/bin}"

# Animate only on a terminal; piped or logged output stays plain.
tty=
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ] && [ "${TERM:-dumb}" != dumb ]; then
	tty=1
fi

paint() {
	if [ -n "$tty" ]; then
		printf '\033[%sm%s\033[0m' "$1" "$2"
	else
		printf '%s' "$2"
	fi
}

step() {
	printf '  %s %s\n' "$(paint '1;32' ✓)" "$1"
}

fail() {
	[ -n "$tty" ] && printf '\r\033[K'
	printf '  %s %s\n' "$(paint '1;31' ✗)" "argowt install: $*" >&2
	exit 1
}

# human 1572864 prints "1.5 MB".
human() {
	awk -v bytes="$1" 'BEGIN {
		if (bytes >= 1048576) printf "%.1f MB", bytes / 1048576
		else printf "%d KB", bytes / 1024
	}'
}

# bar 42 prints a 24-cell progress bar 42% full.
bar() {
	awk -v percent="$1" 'BEGIN {
		filled = int(percent * 24 / 100)
		for (i = 0; i < 24; i++) printf (i < filled ? "█" : "░")
	}'
}

# download URL FILE LABEL fetches URL, drawing a spinner and progress bar
# while it runs.
download() {
	if [ -z "$tty" ]; then
		curl -fsSL -o "$2" "$1"
		return
	fi

	# Follow redirects to the final response for the size; it is optional.
	total="$(curl -fsSLI "$1" 2>/dev/null | awk 'tolower($1) == "content-length:" { size = $2 } END { print size + 0 }')"
	: >"$2"
	curl -fsSL -o "$2" "$1" &
	pid=$!
	printf '\033[?25l'
	while kill -0 "$pid" 2>/dev/null; do
		for frame in ⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏; do
			size="$(wc -c <"$2")"
			if [ "$total" -gt 0 ]; then
				percent=$((size * 100 / total))
				[ "$percent" -le 100 ] || percent=100
				progress="$(paint '38;5;81' "$(bar "$percent")") $(printf '%3d%%' "$percent")  $(paint 2 "$(human "$size") / $(human "$total")")"
			else
				progress="$(paint 2 "$(human "$size")")"
			fi
			printf '\r\033[K  %s %s  %s' "$(paint '38;5;141' "$frame")" "$3" "$progress"
			sleep 0.08
			kill -0 "$pid" 2>/dev/null || break
		done
	done
	status=0
	wait "$pid" || status=$?
	pid=
	printf '\r\033[K\033[?25h'
	return "$status"
}

banner() {
	echo
	[ -n "$tty" ] || return 0
	set -- 87 81 117 111 147 183
	while IFS= read -r line; do
		printf '  %s\n' "$(paint "1;38;5;$1" "$line")"
		shift
		sleep 0.05
	done <<'EOF'
 █████╗ ██████╗  ██████╗  ██████╗ ██╗    ██╗████████╗
██╔══██╗██╔══██╗██╔════╝ ██╔═══██╗██║    ██║╚══██╔══╝
███████║██████╔╝██║  ███╗██║   ██║██║ █╗ ██║   ██║
██╔══██║██╔══██╗██║   ██║██║   ██║██║███╗██║   ██║
██║  ██║██║  ██║╚██████╔╝╚██████╔╝╚███╔███╔╝   ██║
╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝  ╚═════╝  ╚══╝╚══╝    ╚═╝
EOF
	echo
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

# Download in a temp dir, removed on exit along with any running download.
pid=
workdir="$(mktemp -d)"
cleanup() {
	[ -n "$pid" ] && kill "$pid" 2>/dev/null
	[ -n "$tty" ] && printf '\033[?25h'
	rm -rf "$workdir"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

echo
printf '  %s %s\n\n' "$(paint '1;38;5;81' 'Installing argowt')" "$(paint 2 "($VERSION, $os/$arch)")"

download "$base/$asset" "$workdir/$asset" "Downloading $asset" || fail "download failed: $base/$asset"
step "Downloaded $asset $(paint 2 "($(human "$(wc -c <"$workdir/$asset")"))")"
download "$base/checksums.txt" "$workdir/checksums.txt" "Fetching checksums" || fail "download failed: $base/checksums.txt"

# Verify the binary against the published checksum.
expected="$(awk -v name="$asset" '$2 == name { print $1 }' "$workdir/checksums.txt")"
[ -n "$expected" ] || fail "no checksum for $asset"

if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "$workdir/$asset" | awk '{ print $1 }')"
else
	actual="$(shasum -a 256 "$workdir/$asset" | awk '{ print $1 }')"
fi
[ "$actual" = "$expected" ] || fail "checksum mismatch for $asset"
step "Verified checksum"

mkdir -p "$BINDIR"
install -m 0755 "$workdir/$asset" "$BINDIR/argowt"
step "Installed to $BINDIR/argowt"

banner
printf '  %s\n\n' "$(paint 1 "$("$BINDIR/argowt" --version)")"

# Warn when the install dir is not on PATH.
case ":$PATH:" in
*":$BINDIR:"*)
	printf '  Run %s to get started.\n\n' "$(paint '1;38;5;141' argowt)"
	;;
*)
	printf '  %s %s is not on your PATH. Add it with:\n\n' "$(paint '1;33' !)" "$BINDIR"
	printf '    %s\n\n' "$(paint '38;5;81' "export PATH=\"$BINDIR:\$PATH\"")"
	;;
esac
