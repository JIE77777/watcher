#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK_ROOT="${WATCHER_ANDROID_SDK_ROOT:-${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}}"
APK_PATH="${ROOT_DIR}/app/build/outputs/apk/debug/app-debug.apk"
ADB_BIN=""

if [[ -z "${SDK_ROOT}" && -f "${ROOT_DIR}/local.properties" ]]; then
  SDK_ROOT="$(sed -n 's/^sdk\.dir=//p' "${ROOT_DIR}/local.properties" | tail -n 1)"
fi

if [[ -n "${SDK_ROOT}" && -x "${SDK_ROOT}/platform-tools/adb" ]]; then
  ADB_BIN="${SDK_ROOT}/platform-tools/adb"
elif command -v adb >/dev/null 2>&1; then
  ADB_BIN="$(command -v adb)"
fi

if [[ -z "${ADB_BIN}" ]]; then
  echo "adb not found. Set ANDROID_SDK_ROOT or install platform-tools." >&2
  exit 1
fi

if [[ ! -f "${APK_PATH}" ]]; then
  echo "Debug APK not found, building first..."
  "${ROOT_DIR}/gradlew" --no-daemon assembleDebug
fi

"${ADB_BIN}" start-server >/dev/null
"${ADB_BIN}" install -r "${APK_PATH}"

echo
echo "Installed ${APK_PATH}"
