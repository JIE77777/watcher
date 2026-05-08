#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK_ROOT="${WATCHER_ANDROID_SDK_ROOT:-${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}}"

if [[ -z "${SDK_ROOT}" ]]; then
  echo "ANDROID_SDK_ROOT is not set. Export ANDROID_SDK_ROOT or WATCHER_ANDROID_SDK_ROOT first." >&2
  exit 1
fi

cat > "${ROOT_DIR}/local.properties" <<EOF
sdk.dir=${SDK_ROOT}
EOF

echo "Wrote ${ROOT_DIR}/local.properties"
