#!/usr/bin/env bash
set -euo pipefail

VERSION="${WATCHER_GRADLE_VERSION:-8.7}"
INSTALL_ROOT="${WATCHER_GRADLE_HOME:-$HOME/.local/watcher/gradle}"
TARGET_DIR="${INSTALL_ROOT}/gradle-${VERSION}"
ZIP_PATH="${INSTALL_ROOT}/gradle-${VERSION}-bin.zip"

URLS=(
  "${WATCHER_GRADLE_URL:-https://downloads.gradle.org/distributions/gradle-${VERSION}-bin.zip}"
  "https://services.gradle.org/distributions/gradle-${VERSION}-bin.zip"
)

mkdir -p "${INSTALL_ROOT}"

if [[ -x "${TARGET_DIR}/bin/gradle" ]]; then
  echo "${TARGET_DIR}/bin/gradle"
  exit 0
fi

download_with_curl() {
  local url="$1"
  curl \
    --fail \
    --location \
    --retry 5 \
    --retry-all-errors \
    --retry-delay 2 \
    --connect-timeout 20 \
    --continue-at - \
    --output "${ZIP_PATH}" \
    "${url}"
}

download_with_wget() {
  local url="$1"
  wget \
    --tries=5 \
    --timeout=30 \
    --continue \
    --output-document="${ZIP_PATH}" \
    "${url}"
}

echo "Downloading Gradle ${VERSION}..."
rm -f "${ZIP_PATH}.tmp"
for url in "${URLS[@]}"; do
  [[ -n "${url}" ]] || continue
  echo "Trying ${url}"
  if command -v curl >/dev/null 2>&1 && download_with_curl "${url}"; then
    unzip -q -o "${ZIP_PATH}" -d "${INSTALL_ROOT}"
    echo "${TARGET_DIR}/bin/gradle"
    exit 0
  fi
  if command -v wget >/dev/null 2>&1 && download_with_wget "${url}"; then
    unzip -q -o "${ZIP_PATH}" -d "${INSTALL_ROOT}"
    echo "${TARGET_DIR}/bin/gradle"
    exit 0
  fi
done

echo "Failed to download Gradle ${VERSION}. Set WATCHER_GRADLE_URL to a reachable mirror if needed." >&2
exit 1
