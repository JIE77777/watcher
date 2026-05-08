#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK_ROOT="${WATCHER_ANDROID_SDK_ROOT:-${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}}"
LOCAL_PROPERTIES="${ROOT_DIR}/local.properties"

if [[ -z "${SDK_ROOT}" && -f "${LOCAL_PROPERTIES}" ]]; then
  SDK_ROOT="$(sed -n 's/^sdk\.dir=//p' "${LOCAL_PROPERTIES}" | tail -n 1)"
fi

echo "Watcher Android doctor"
echo "Project: ${ROOT_DIR}"
echo

if command -v java >/dev/null 2>&1; then
  echo "[ok] java: $(command -v java)"
else
  echo "[missing] java"
fi

if command -v gradle >/dev/null 2>&1; then
  echo "[ok] gradle: $(command -v gradle)"
elif [[ -x "${ROOT_DIR}/gradlew" ]]; then
  echo "[ok] project gradlew: ${ROOT_DIR}/gradlew"
else
  echo "[info] gradle not on PATH, project gradlew will bootstrap a local copy if needed"
fi

if [[ -n "${SDK_ROOT}" ]]; then
  echo "[ok] android sdk root: ${SDK_ROOT}"
else
  echo "[missing] android sdk root"
fi

if [[ -n "${SDK_ROOT}" && -x "${SDK_ROOT}/platform-tools/adb" ]]; then
  echo "[ok] adb: ${SDK_ROOT}/platform-tools/adb"
else
  echo "[missing] adb in sdk root"
fi

if [[ -f "${ROOT_DIR}/local.properties" ]]; then
  echo "[ok] local.properties present"
else
  echo "[info] local.properties missing"
fi
