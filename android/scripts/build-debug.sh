#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "${ROOT_DIR}"
"${ROOT_DIR}/gradlew" --no-daemon assembleDebug

echo
echo "APK path:"
echo "${ROOT_DIR}/app/build/outputs/apk/debug/app-debug.apk"
