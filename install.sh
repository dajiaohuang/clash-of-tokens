#!/usr/bin/env bash
# Standalone installer: no Go, Python, jq, sudo or shell-profile edits required.
set -euo pipefail
version=latest
repository=dajiaohuang/clash-of-tokens
install_dir="${HOME:?}/.local/bin"
binary=
expected=
from_source=0
usage() {
  echo 'Usage: bash install.sh [--version TAG|latest] [--repository OWNER/REPO] [--install-dir DIR]'
  echo '       bash install.sh --from-source [--install-dir DIR]'
  echo '       bash install.sh --binary FILE --sha256 HASH [--install-dir DIR]'
}
while (($#)); do
  case "$1" in
    --version|--repository|--install-dir|--binary|--sha256)
      (($# >= 2)) || { usage >&2; exit 2; }
      case "$1" in
        --version) version=$2;; --repository) repository=$2;; --install-dir) install_dir=$2;;
        --binary) binary=$2;; --sha256) expected=$2;;
      esac; shift 2;;
    --from-source) from_source=1; shift;;
    -h|--help) usage; exit 0;;
    *) echo "Unknown option: $1" >&2; exit 2;;
  esac
done
if ((from_source)); then
  [[ -z "$binary" && "$version" == latest ]] || { echo 'Source mode cannot be combined with a release version or binary.' >&2; exit 2; }
  root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
  [[ -f "$root/build.sh" ]] || { echo 'Source mode requires a repository checkout containing build.sh.' >&2; exit 1; }
  exec bash "$root/build.sh" --install --install-dir "$install_dir"
fi
case "$(uname -s)" in Linux) platform=linux;; Darwin) platform=darwin;; *) echo 'Use install.ps1 on Windows.' >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Supported architectures: amd64, arm64.' >&2; exit 1;; esac
[[ -n "$install_dir" ]] || { echo 'Install directory cannot be empty.' >&2; exit 2; }
mkdir -p -- "$install_dir"
install_dir=$(cd -- "$install_dir" && pwd)
stage=$(mktemp -d "$install_dir/.cot-install.XXXXXXXX")
# Only individually named files in our freshly created staging directory are removed.
trap 'rm -f -- "$stage/binary" "$stage/checksum"; rmdir -- "$stage" 2>/dev/null || true' EXIT
hash_file() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}';
  elif command -v shasum >/dev/null; then shasum -a 256 "$1" | awk '{print $1}';
  else echo 'Install sha256sum or shasum.' >&2; return 1; fi
}
if [[ -n "$binary" ]]; then
  [[ -f "$binary" ]] || { echo 'Binary file not found.' >&2; exit 1; }
  cp -- "$binary" "$stage/binary"
else
  [[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ && "$version" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo 'Invalid repository or version.' >&2; exit 2; }
  command -v curl >/dev/null || { echo 'Install curl first.' >&2; exit 1; }
  asset="clash-tokens-$platform-$arch"
  base="https://github.com/$repository/releases/download/$version"
  if [[ "$version" == latest ]]; then
    # Resolve latest once so a release published between downloads cannot mix assets.
    effective=$(curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 120 --output /dev/null --write-out '%{url_effective}' "https://github.com/$repository/releases/latest") || { echo 'No stable release could be downloaded. Use --from-source in a checkout.' >&2; exit 1; }
    prefix="https://github.com/$repository/releases/tag/"
    [[ "$effective" == "$prefix"* ]] || { echo 'No stable release is available. Use --from-source in a checkout.' >&2; exit 1; }
    version=${effective#"$prefix"}
    [[ "$version" =~ ^[A-Za-z0-9_.-]+$ ]] || { echo 'Unsupported release tag.' >&2; exit 1; }
    base="https://github.com/$repository/releases/download/$version"
  fi
  echo "Downloading $repository $version ($platform/$arch)"
  fetch() { curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --connect-timeout 20 --max-time 600 --retry 2 "$1" --output "$2"; }
  fetch "$base/$asset.sha256" "$stage/checksum" || { echo 'Release/checksum unavailable. No installed binary was changed.' >&2; exit 1; }
  expected=$(awk -v name="$asset" 'NF==2 && $2==name {print $1}' "$stage/checksum")
  fetch "$base/$asset" "$stage/binary"
fi
[[ "$expected" =~ ^[a-fA-F0-9]{64}$ ]] || { echo 'A single SHA-256 checksum is required.' >&2; exit 1; }
actual=$(hash_file "$stage/binary")
[[ "$actual" == "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" ]] || { echo 'Checksum mismatch. Installed binary was not changed.' >&2; exit 1; }
chmod 755 "$stage/binary"
"$stage/binary" version
[[ ! -d "$install_dir/clash-tokens" ]] || { echo 'Target is a directory.' >&2; exit 1; }
# Same-filesystem rename preserves the old installation until validation succeeds.
mv -f -- "$stage/binary" "$install_dir/clash-tokens"
echo "Installed: $install_dir/clash-tokens"
case ":$PATH:" in *":$install_dir:"*) ;; *) printf 'For this shell: export PATH=%q:"$PATH"\n' "$install_dir";; esac
printf 'Next: %q init -config config.json\n' "$install_dir/clash-tokens"
