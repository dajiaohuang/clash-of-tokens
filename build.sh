#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
install=0
test=0
install_dir="${HOME:?}/.local/bin"
output=
version=0.1.0-dev
while (($#)); do
  case "$1" in
    --install) install=1; shift;; --test) test=1; shift;;
    --install-dir|--output|--version)
      (($#>=2)) || { echo "Missing value: $1" >&2; exit 2; }
      case "$1" in --install-dir) install_dir=$2;; --output) output=$2;; --version) version=$2;; esac; shift 2;;
    --help|-h) echo 'Usage: bash build.sh [--install] [--test] [--output FILE] [--install-dir DIR] [--version VERSION]'; exit 0;;
    *) echo "Unknown option: $1" >&2; exit 2;;
  esac
done
[[ "$version" =~ ^[A-Za-z0-9_.+-]+$ ]] || { echo 'Invalid build version.' >&2; exit 2; }
case "$(uname -s)" in Linux) platform=linux;; Darwin) platform=darwin;; *) echo 'Use build.ps1 on Windows.' >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Supported architectures: amd64, arm64.' >&2; exit 1;; esac
cd -- "$root"
required=$(awk '$1=="toolchain" {print $2}' go.mod)
[[ "$required" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'go.mod must declare a pinned toolchain.' >&2; exit 1; }
hash_file() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}
go_cmd=$(command -v go || true)
if [[ -z "$go_cmd" ]] || [[ "$(GOTOOLCHAIN=local "$go_cmd" version)" != "go version $required "* ]]; then
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/clash-of-tokens/toolchains/$required-$platform-$arch"
  go_cmd="$cache/go/bin/go"
  if [[ ! -x "$go_cmd" ]]; then
    archive="$required.$platform-$arch.tar.gz"
    expected=$(awk -v name="$archive" '$2==name {print $1}' scripts/go-toolchains.txt)
    [[ "$expected" =~ ^[a-f0-9]{64}$ ]] || { echo 'Pinned toolchain checksum missing. Update scripts/go-toolchains.txt.' >&2; exit 1; }
    command -v curl >/dev/null || { echo 'Install curl first.' >&2; exit 1; }
    mkdir -p -- "$(dirname -- "$cache")"
    staging=$(mktemp -d "$(dirname -- "$cache")/.download.XXXXXXXX")
    echo "Downloading verified $required to $cache"
    # Failed downloads remain in the private cache for diagnosis; no Go caches are deleted.
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 900 --retry 2 "https://go.dev/dl/$archive" -o "$staging/archive"
    [[ "$(hash_file "$staging/archive")" == "$expected" ]] || { echo 'Go checksum mismatch.' >&2; exit 1; }
    tar -xzf "$staging/archive" -C "$staging"
    if [[ -e "$cache" ]]; then echo "Toolchain directory already exists but is incomplete: $cache" >&2; exit 1; fi
    mv -- "$staging" "$cache"
  fi
fi
export GOTOOLCHAIN=local GOOS="$platform" GOARCH="$arch" CGO_ENABLED=0
unset GOFLAGS GOROOT
if [[ "$platform" == darwin ]]; then
  xcrun --find clang >/dev/null 2>&1 || { echo 'macOS Keychain requires Xcode Command Line Tools. Run: xcode-select --install, then rerun.' >&2; exit 1; }
  export CGO_ENABLED=1
fi
"$go_cmd" version
"$go_cmd" mod download
"$go_cmd" mod verify
if ((test)); then "$go_cmd" test -tags=cot_test_keyring ./... -count=1 -timeout 180s; fi
output=${output:-"$root/dist/clash-tokens"}
[[ ! -d "$output" ]] || { echo 'Output path must be a file, not a directory.' >&2; exit 2; }
mkdir -p -- "$(dirname -- "$output")"
temporary=$(mktemp "$(dirname -- "$output")/.cot-build.XXXXXXXX")
trap 'rm -f -- "$temporary"' EXIT
"$go_cmd" build -trimpath -ldflags="-s -w -X main.version=$version" -o "$temporary" ./cmd/clash-tokens
"$temporary" version
hash=$(hash_file "$temporary")
if ((install)); then bash "$root/install.sh" --binary "$temporary" --sha256 "$hash" --install-dir "$install_dir"; fi
mv -f -- "$temporary" "$output"
printf '%s  %s\n' "$hash" "$(basename -- "$output")" > "$output.sha256"
echo "Built: $output"
