#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/../.." && pwd)"
target="${WATCHER_PUBLIC_DIR:-$repo_root/../watcher-public}"

usage() {
  cat <<EOF
Usage: devtools/public/audit_public.sh [--target DIR]

Audits the generated public staging tree for forbidden files and obvious
private paths or secrets.

Defaults:
  target: ${target}
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target)
      [[ $# -ge 2 ]] || { echo "--target requires a directory" >&2; exit 2; }
      target="$2"
      shift 2
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

if [[ ! -d "$target" ]]; then
  echo "public staging tree not found: $target" >&2
  exit 2
fi

failures=0

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

check_absent_path() {
  local rel="$1"
  if [[ -e "$target/$rel" ]]; then
    fail "forbidden path exists: $rel"
  fi
}

for rel in \
  .claude \
  state \
  releases \
  lab \
  consulting \
  ops \
  modules/box/private \
  docs/modules/BOX_MODULE.md \
  internal/box/CLAUDE.md \
  internal/box/adapters \
  service/cmd/watcher-service/box_private_adapters.go \
  tools/adapters \
  tools/scrapers \
  watch_kunpeng_scores.py \
  relay/config.local.json \
  relay/config.public.local.json \
  service/config.local.json \
  android/local.properties
do
  check_absent_path "$rel"
done

while IFS= read -r path; do
  fail "forbidden generated file: ${path#$target/}"
done < <(
  find "$target" \
    -path "$target/.git" -prune -o \
    -type f \( \
      -name '*.apk' -o \
      -name '*.db' -o \
      -name '*.db-shm' -o \
      -name '*.db-wal' -o \
      -name '*.sqlite' -o \
      -name '*.sqlite3' -o \
      -name '*.log' -o \
      -name '*.keystore' -o \
      -name '*.jks' \
    \) -print
)

if command -v rg >/dev/null 2>&1; then
  declare -a patterns=(
    '/home''/steam'
    '49\.232\.4\.22'
    'kunpeng''_rank'
    'huawei''_contest'
    '"owner_token"[[:space:]]*:[[:space:]]*"[0-9a-fA-F]{24,}"'
    'Bearer[[:space:]]+[A-Za-z0-9._-]{32,}'
    'sk-[A-Za-z0-9]{20,}'
    'ghp_[A-Za-z0-9_]{20,}'
    'github_pat_[A-Za-z0-9_]{20,}'
    '-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----'
  )
  for pattern in "${patterns[@]}"; do
    if rg -n --hidden --glob '!.git/**' -- "$pattern" "$target" >/tmp/watcher_public_audit_rg.txt; then
      echo "pattern matched: $pattern" >&2
      sed -n '1,40p' /tmp/watcher_public_audit_rg.txt >&2
      fail "private pattern found: $pattern"
    fi
  done
  private_channel='private''_selected'
  private_channel_pattern='"release_channel"[[:space:]]*:[[:space:]]*"'$private_channel'"'
  if rg -n --hidden --glob '!.git/**' --glob 'watcher.shell.json' --glob 'modules/**/component.json' --glob 'modules/COMPONENT_MANIFEST_TEMPLATE.json' -- "$private_channel_pattern" "$target" >/tmp/watcher_public_audit_rg.txt; then
    echo "private release channel found in public manifest:" >&2
    sed -n '1,40p' /tmp/watcher_public_audit_rg.txt >&2
    fail "private release channel found in public manifests"
  fi
else
  echo "warning: rg not found; content audit skipped" >&2
fi

if [[ "$failures" -ne 0 ]]; then
  echo "public audit failed with $failures issue group(s)" >&2
  exit 1
fi

echo "public audit passed: $target"
