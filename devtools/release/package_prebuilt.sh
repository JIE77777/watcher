#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
public_dir="${WATCHER_PUBLIC_DIR:-$repo_root/../watcher-public}"
version="${WATCHER_RELEASE_VERSION:-$(tr -d '[:space:]' < "$repo_root/VERSION")}"
target="${WATCHER_RELEASE_TARGET:-linux-amd64}"
service_bin="${WATCHER_SERVICE_BIN:-$repo_root/state/bin/watcher-service}"
relay_bin="${WATCHER_RELAY_BIN:-$repo_root/state/bin/watcher-relay}"
apk_path="${WATCHER_APK_PATH:-}"
out_dir="${WATCHER_RELEASE_DIR:-$repo_root/releases}"

usage() {
  cat <<EOF
Usage: devtools/release/package_prebuilt.sh [--public-dir DIR] [--service-bin FILE] [--relay-bin FILE] [--apk FILE] [--out DIR]

Build a no-source-build release archive from public runtime assets plus prebuilt
backend binaries and APK.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --public-dir)
      public_dir="$2"; shift 2 ;;
    --service-bin)
      service_bin="$2"; shift 2 ;;
    --relay-bin)
      relay_bin="$2"; shift 2 ;;
    --apk)
      apk_path="$2"; shift 2 ;;
    --out)
      out_dir="$2"; shift 2 ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2 ;;
  esac
done

if [[ -z "$apk_path" ]]; then
  apk_path="$(find "$repo_root/releases" -maxdepth 1 -type f -name 'watcher-*.apk' | sort -V | tail -n 1 || true)"
fi

require_file() {
  [[ -f "$1" ]] || { echo "required file missing: $1" >&2; exit 2; }
}

require_dir() {
  [[ -d "$1" ]] || { echo "required directory missing: $1" >&2; exit 2; }
}

require_dir "$public_dir"
require_file "$service_bin"
require_file "$relay_bin"
require_file "$apk_path"
require_file "$public_dir/watcher.shell.json"
require_file "$public_dir/VERSION"
require_dir "$public_dir/assets/branding"
require_dir "$public_dir/modules"
require_dir "$public_dir/tools"
require_file "$public_dir/service/config.example.json"
require_file "$public_dir/relay/config.example.json"
require_file "$public_dir/deploy/prebuilt/install.sh"

mkdir -p "$out_dir"
tmp_dir="$(mktemp -d "$out_dir/watcher-prebuilt.XXXXXX")"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

package_name="watcher-v${version}-${target}-prebuilt"
root="$tmp_dir/$package_name"
mkdir -p "$root/bin" "$root/releases" "$root/service" "$root/relay" "$root/docs" "$root/deploy" "$root/assets" "$root/modules" "$root/tools"

install -m 0755 "$service_bin" "$root/bin/watcher-service"
install -m 0755 "$relay_bin" "$root/bin/watcher-relay"
install -m 0644 "$apk_path" "$root/releases/$(basename "$apk_path")"

rsync -a "$public_dir/modules/" "$root/modules/"
rsync -a "$public_dir/tools/" "$root/tools/"
rsync -a "$public_dir/deploy/prebuilt" "$root/deploy/"
rsync -a "$public_dir/assets/branding" "$root/assets/"
install -m 0644 "$public_dir/watcher.shell.json" "$root/watcher.shell.json"
install -m 0644 "$public_dir/VERSION" "$root/VERSION"
install -m 0644 "$public_dir/README.md" "$root/README.md"
install -m 0644 "$public_dir/LICENSE" "$root/LICENSE"
install -m 0644 "$public_dir/service/config.example.json" "$root/service/config.example.json"
install -m 0644 "$public_dir/relay/config.example.json" "$root/relay/config.example.json"
install -m 0644 "$public_dir/docs/PREBUILT_DEPLOYMENT.md" "$root/docs/PREBUILT_DEPLOYMENT.md"
install -m 0644 "$public_dir/docs/SECURITY.md" "$root/docs/SECURITY.md"

(
  cd "$root"
  find . -type f -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
)

archive="$out_dir/$package_name.tar.gz"
tar -C "$tmp_dir" -czf "$archive" "$package_name"
sha256sum "$archive"
echo "$archive"
