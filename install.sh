#!/bin/sh
# Downloads the right copilot-local program for this computer, checks it, and runs it.
# Nothing is installed: the program runs from a temporary folder that's deleted afterwards.
#   curl -fsSL https://raw.githubusercontent.com/artofthesmart/local-inference-setup/main/install.sh | sh
# Pass arguments after `sh -s --`, for example: ... | sh -s -- ollama --dry-run
set -eu

base="${COPILOT_LOCAL_BASE_URL:-https://github.com/artofthesmart/local-inference-setup/releases/latest/download}"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "Sorry, this launcher supports Linux and macOS. On Windows, use install.ps1." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "Sorry, $(uname -m) computers aren't supported yet." >&2; exit 1 ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q "$1" -O "$2"; }
else
  echo "Please install curl or wget, then try again." >&2; exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha() { sha256sum "$1" | cut -d' ' -f1; }
else
  sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
fi

name="copilot-local_${os}_${arch}.tar.gz"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading copilot-local for $os/$arch..."
fetch "$base/$name" "$tmp/$name"
fetch "$base/checksums.txt" "$tmp/checksums.txt"

want="$(awk -v n="$name" '$2 == n { print $1 }' "$tmp/checksums.txt")"
got="$(sha "$tmp/$name")"
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "The download didn't match its checksum, so it wasn't run. Please try again." >&2
  exit 1
fi

tar -xzf "$tmp/$name" -C "$tmp" copilot-local

# When piped into sh, stdin is the script itself, so give the menu the real keyboard.
if [ -t 1 ] && { : </dev/tty; } 2>/dev/null; then
  "$tmp/copilot-local" "$@" </dev/tty
else
  "$tmp/copilot-local" "$@"
fi
