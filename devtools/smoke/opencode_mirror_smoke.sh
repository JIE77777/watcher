#!/usr/bin/env bash
set -euo pipefail

base_url="${WATCHER_BASE_URL:-http://127.0.0.1:8780}"
token="${WATCHER_OWNER_TOKEN:-}"

if [[ -z "$token" && -f relay/config.local.json ]] && command -v jq >/dev/null 2>&1; then
  token="$(jq -r '.owner_token // empty' relay/config.local.json)"
fi

if [[ -z "$token" ]]; then
  echo "WATCHER_OWNER_TOKEN is required when relay/config.local.json is unavailable." >&2
  exit 2
fi

tmp_dir="${TMPDIR:-/tmp}"
sessions_json="$tmp_dir/watcher_opencode_mirror_sessions.json"
snapshot_json="$tmp_dir/watcher_opencode_mirror_snapshot.json"
pulse_json="$tmp_dir/watcher_opencode_mirror_pulse.json"

curl_json() {
  local path="$1"
  local out="$2"
  curl -fsS \
    -H "Authorization: Bearer $token" \
    -H "Accept: application/json" \
    "$base_url$path" > "$out"
}

curl_json "/api/v2/modules/opencode-mirror/sessions?limit=20" "$sessions_json"

if command -v jq >/dev/null 2>&1; then
  jq '{count:(.items|length), sync:.sync}' "$sessions_json"
  native_session_id="$(jq -r '.items[0].native_session_id // empty' "$sessions_json")"
else
  echo "Session list fetched: $sessions_json"
  native_session_id=""
fi

if [[ -z "$native_session_id" ]]; then
  echo "No opencode mirror sessions found; list path is healthy." >&2
  exit 0
fi

encoded_session_id="$(python3 - <<PY
from urllib.parse import quote
print(quote("$native_session_id", safe=""))
PY
)"

curl_json "/api/v2/modules/opencode-mirror/sessions/$encoded_session_id/snapshot?message_limit=80&sync=0" "$snapshot_json"
curl_json "/api/v2/modules/opencode-mirror/sessions/$encoded_session_id/pulse?after_seq=0&limit=120&sync=0" "$pulse_json"

if command -v jq >/dev/null 2>&1; then
  jq '{session:.snapshot.session.native_session_id, messages:(.snapshot.messages|length), events:(.snapshot.events|length), conversation:(.snapshot.conversation|length), sync:.snapshot.sync}' "$snapshot_json"
  jq '{events:(.pulse.events|length), changed_messages:(.pulse.changed_messages|length), conversation:(.pulse.conversation|length), last_event_seq:.pulse.last_event_seq, focus:.pulse.presentation.focus_reason}' "$pulse_json"
else
  echo "Snapshot fetched: $snapshot_json"
  echo "Pulse fetched: $pulse_json"
fi
