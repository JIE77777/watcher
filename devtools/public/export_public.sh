#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
allowlist="$script_dir/public-files.txt"
target="${WATCHER_PUBLIC_DIR:-$repo_root/../watcher-public}"
force=0
dry_run=0

usage() {
  cat <<EOF
Usage: devtools/public/export_public.sh [--target DIR] [--force] [--dry-run]

Exports an allowlisted public staging tree from the private Watcher workspace.

Defaults:
  target: ${target}

Options:
  --target DIR   export to DIR instead of WATCHER_PUBLIC_DIR/default
  --force        allow replacing a non-git target or a dirty git target
  --dry-run      print what would be exported without writing files
  -h, --help     show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      [[ $# -ge 2 ]] || { echo "--target requires a directory" >&2; exit 2; }
      target="$2"
      shift 2
      ;;
    --force)
      force=1
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 2
  }
}

require_command rsync
require_command git

if [[ ! -f "$allowlist" ]]; then
  echo "allowlist not found: $allowlist" >&2
  exit 2
fi

target_parent="$(dirname "$target")"
mkdir -p "$target_parent"
target_abs="$(cd "$target_parent" && pwd)/$(basename "$target")"

if [[ "$target_abs" == "$repo_root" ]]; then
  echo "target must not be the private repository root" >&2
  exit 2
fi

if [[ "$target_abs" == "$repo_root"/* ]]; then
  echo "target must be outside the private repository tree" >&2
  exit 2
fi

mapfile -t entries < <(
  sed 's/[[:space:]]*$//' "$allowlist" |
    sed '/^[[:space:]]*$/d' |
    sed '/^[[:space:]]*#/d'
)

echo "source: $repo_root"
echo "target: $target_abs"
echo "allowlist: $allowlist"
echo "entries: ${#entries[@]}"

if [[ "$dry_run" -eq 1 ]]; then
  printf '%s\n' "${entries[@]}"
  exit 0
fi

if [[ -d "$target_abs/.git" ]]; then
  dirty="$(git -C "$target_abs" status --porcelain)"
  if [[ -n "$dirty" && "$force" -ne 1 ]]; then
    echo "target git tree has uncommitted changes; pass --force to replace exported files" >&2
    git -C "$target_abs" status --short >&2
    exit 3
  fi
elif [[ -d "$target_abs" && -n "$(find "$target_abs" -mindepth 1 -maxdepth 1 -print -quit)" && "$force" -ne 1 ]]; then
  echo "target exists and is not empty; pass --force to replace it" >&2
  exit 3
fi

tmp_dir="$(mktemp -d "${target_parent}/watcher-public.export.XXXXXX")"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

rsync_excludes=(
  --exclude '.git/'
  --exclude '.claude/'
  --exclude '.gradle/'
  --exclude 'build/'
  --exclude '__pycache__/'
  --exclude '*.pyc'
  --exclude '*.pyo'
  --exclude '*.db'
  --exclude '*.db-shm'
  --exclude '*.db-wal'
  --exclude '*.sqlite'
  --exclude '*.sqlite3'
  --exclude '*.log'
  --exclude '*.apk'
  --exclude '*.keystore'
  --exclude '*.jks'
  --exclude '.watch_state.json'
  --exclude 'local.properties'
  --exclude 'config.local.json'
  --exclude 'config.*.local.json'
  --exclude 'state/'
  --exclude 'releases/'
  --exclude 'lab/'
  --exclude 'consulting/'
  --exclude 'ops/'
  --exclude 'modules/box/private/'
  --exclude 'docs/modules/BOX_MODULE.md'
  --exclude 'internal/box/CLAUDE.md'
  --exclude 'internal/box/adapters/'
  --exclude 'service/cmd/watcher-service/box_private_adapters.go'
  --exclude 'tools/adapters/'
  --exclude 'tools/scrapers/'
  --exclude 'watch_kunpeng_scores.py'
  --exclude 'references/openai-codex/'
  --exclude 'devtools/android/workspace/*'
  --include 'devtools/android/workspace/README.md'
)

missing=0
for entry in "${entries[@]}"; do
  if [[ ! -e "$repo_root/$entry" ]]; then
    echo "warning: allowlisted path missing: $entry" >&2
    missing=1
    continue
  fi
  (
    cd "$repo_root"
    rsync -aR "${rsync_excludes[@]}" "$entry" "$tmp_dir/"
  )
done

mkdir -p "$tmp_dir/service/cmd/watcher-service"
cat > "$tmp_dir/service/cmd/watcher-service/box_public_adapters.go" <<'EOF'
package main

import (
	"path/filepath"

	"watcher/internal/box"
)

func (a *App) registerPrivateBoxAdapters(cfg Config) {
	boxRoot := filepath.Join(cfg.Shell.ComponentsRoot, "box")
	a.boxRegistry.RegisterProvider(box.NewCatalogProvider([]string{
		filepath.Join(boxRoot, "examples"),
	}, nil))
}
EOF

cat > "$tmp_dir/PUBLIC_SOURCE.txt" <<EOF
source_workspace: redacted-private-workspace
source_revision: redacted-private-source
exported_at: $(date -u +%Y-%m-%dT%H:%M:%SZ)
export_profile: public-mainline
allowlist: devtools/public/public-files.txt
EOF

mkdir -p "$target_abs"
find "$target_abs" -mindepth 1 -maxdepth 1 ! -name '.git' -exec rm -rf {} +
rsync -a "$tmp_dir"/ "$target_abs"/

if [[ "$missing" -ne 0 ]]; then
  echo "export completed with missing allowlist warnings" >&2
else
  echo "export completed"
fi
