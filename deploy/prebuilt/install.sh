#!/usr/bin/env bash
set -euo pipefail

DEFAULT_APP_VERSION_CODE="${WATCHER_APP_VERSION_CODE:-83}"
DEFAULT_APP_VERSION_NAME="${WATCHER_APP_VERSION_NAME:-1.17.50}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
package_root="$(cd "$script_dir/../.." && pwd)"

home="${WATCHER_HOME:-$package_root}"
service_bind="${WATCHER_SERVICE_BIND:-127.0.0.1:8765}"
relay_bind="${WATCHER_RELAY_BIND:-127.0.0.1:8780}"
allowed_hosts="${WATCHER_ALLOWED_HOSTS:-127.0.0.1,localhost,10.0.2.2}"
tls_enabled="${WATCHER_TLS_ENABLED:-false}"
force="${WATCHER_FORCE_CONFIG:-0}"
write_systemd=0
start_services=0

usage() {
  cat <<EOF
Usage: deploy/prebuilt/install.sh [options]

Generate local Watcher config for a prebuilt package. This does not compile
backend binaries or the Android APK.

Options:
  --home DIR              installation root (default: package root)
  --service-bind ADDR     watcher-service bind address (default: 127.0.0.1:8765)
  --relay-bind ADDR       watcher-relay bind address (default: 127.0.0.1:8780)
  --allowed-hosts CSV     allowed hostnames/IPs for relay
  --tls                   enable relay built-in self-signed HTTPS
  --force                 overwrite generated config files
  --systemd               write user systemd unit files
  --start                 enable and start user systemd services
  -h, --help              show this help

Environment overrides:
  WATCHER_SERVICE_OWNER_TOKEN
  WATCHER_RELAY_OWNER_TOKEN
  WATCHER_SESSION_SECRET
  WATCHER_APK_PATH
  WATCHER_APP_VERSION_CODE
  WATCHER_APP_VERSION_NAME
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --home)
      [[ $# -ge 2 ]] || { echo "--home requires a directory" >&2; exit 2; }
      home="$2"
      shift 2
      ;;
    --service-bind)
      [[ $# -ge 2 ]] || { echo "--service-bind requires an address" >&2; exit 2; }
      service_bind="$2"
      shift 2
      ;;
    --relay-bind)
      [[ $# -ge 2 ]] || { echo "--relay-bind requires an address" >&2; exit 2; }
      relay_bind="$2"
      shift 2
      ;;
    --allowed-hosts)
      [[ $# -ge 2 ]] || { echo "--allowed-hosts requires a CSV value" >&2; exit 2; }
      allowed_hosts="$2"
      shift 2
      ;;
    --tls)
      tls_enabled=true
      shift
      ;;
    --force)
      force=1
      shift
      ;;
    --systemd)
      write_systemd=1
      shift
      ;;
    --start)
      write_systemd=1
      start_services=1
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

home="$(mkdir -p "$home" && cd "$home" && pwd)"
config_dir="$home/config"
state_dir="$home/state"
release_dir="$home/releases"
bin_dir="$home/bin"

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

json_array_csv() {
  local csv="$1"
  local out="["
  local first=1
  local part value
  IFS=',' read -r -a parts <<< "$csv"
  for part in "${parts[@]}"; do
    value="${part#"${part%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    [[ -n "$value" ]] || continue
    if [[ "$first" -eq 0 ]]; then
      out+=", "
    fi
    out+="\"$(json_escape "$value")\""
    first=0
  done
  out+="]"
  printf '%s' "$out"
}

json_bool() {
  case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
    1|true|yes|on) printf 'true' ;;
    *) printf 'false' ;;
  esac
}

http_base_from_bind() {
  local bind="$1"
  local host port
  if [[ "$bind" == :* ]]; then
    host="127.0.0.1"
    port="${bind#:}"
  else
    host="${bind%:*}"
    port="${bind##*:}"
  fi
  case "$host" in
    ""|"0.0.0.0"|"::"|"[::]")
      host="127.0.0.1"
      ;;
  esac
  printf 'http://%s:%s' "$host" "$port"
}

generate_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    od -An -N32 -tx1 /dev/urandom | tr -d ' \n'
    printf '\n'
  fi
}

require_path() {
  local path="$1"
  if [[ ! -e "$path" ]]; then
    echo "required package path missing: $path" >&2
    exit 2
  fi
}

write_once() {
  local path="$1"
  if [[ -e "$path" && "$force" != "1" ]]; then
    echo "kept existing $path"
    return 1
  fi
  return 0
}

require_path "$home/watcher.shell.json"
require_path "$home/VERSION"
require_path "$home/modules"
require_path "$home/tools"
require_path "$bin_dir/watcher-service"
require_path "$bin_dir/watcher-relay"

mkdir -p "$config_dir" "$state_dir" "$release_dir"
chmod 700 "$config_dir" "$state_dir"
chmod +x "$bin_dir/watcher-service" "$bin_dir/watcher-relay"

tokens_file="$config_dir/tokens.env"
if [[ -f "$tokens_file" && "$force" != "1" ]]; then
  # shellcheck disable=SC1090
  source "$tokens_file"
else
  WATCHER_SERVICE_OWNER_TOKEN="${WATCHER_SERVICE_OWNER_TOKEN:-$(generate_token)}"
  WATCHER_RELAY_OWNER_TOKEN="${WATCHER_RELAY_OWNER_TOKEN:-$(generate_token)}"
  WATCHER_SESSION_SECRET="${WATCHER_SESSION_SECRET:-$(generate_token)}"
  cat > "$tokens_file" <<EOF
WATCHER_SERVICE_OWNER_TOKEN=$WATCHER_SERVICE_OWNER_TOKEN
WATCHER_RELAY_OWNER_TOKEN=$WATCHER_RELAY_OWNER_TOKEN
WATCHER_SESSION_SECRET=$WATCHER_SESSION_SECRET
EOF
  chmod 600 "$tokens_file"
fi

apk_path="${WATCHER_APK_PATH:-}"
if [[ -z "$apk_path" ]]; then
  apk_path="$(find "$release_dir" -maxdepth 1 -type f -name 'watcher-*.apk' | sort -V | tail -n 1 || true)"
fi

app_version_code=0
app_version_name=""
app_notes=""
published_at=""
if [[ -n "$apk_path" && -f "$apk_path" ]]; then
  apk_path="$(cd "$(dirname "$apk_path")" && pwd)/$(basename "$apk_path")"
  app_version_code="$DEFAULT_APP_VERSION_CODE"
  app_version_name="$DEFAULT_APP_VERSION_NAME"
  app_notes="Prebuilt APK"
  published_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi

allowed_hosts_json="$(json_array_csv "$allowed_hosts")"
tls_json="$(json_bool "$tls_enabled")"
service_base_url="${WATCHER_SERVICE_BASE_URL:-$(http_base_from_bind "$service_bind")}"
relay_base_url="${WATCHER_RELAY_BASE_URL:-$(http_base_from_bind "$relay_bind")}"

service_config="$config_dir/service.json"
if write_once "$service_config"; then
  cat > "$service_config" <<EOF
{
  "bind_addr": "$(json_escape "$service_bind")",
  "database_path": "$(json_escape "$state_dir/service.db")",
  "tools_root": "$(json_escape "$home/tools")",
  "owner_token": "$(json_escape "$WATCHER_SERVICE_OWNER_TOKEN")",
  "scheduler_interval_seconds": 10,
  "relay": {
    "base_url": "$(json_escape "$relay_base_url")",
    "owner_token": "$(json_escape "$WATCHER_RELAY_OWNER_TOKEN")"
  },
  "relay_push": {
    "base_url": "$(json_escape "$relay_base_url")",
    "owner_token": "$(json_escape "$WATCHER_RELAY_OWNER_TOKEN")"
  },
  "push": {
    "xiaomi": {"app_id": "", "app_key": "", "app_secret": "", "use_sandbox": false, "channel_id": ""},
    "fcm": {"project_id": "", "service_account_json_path": ""},
    "apns": {"team_id": "", "key_id": "", "bundle_id": "", "key_file": "", "use_sandbox": false},
    "huawei": {"app_id": "", "app_secret": ""}
  },
  "codex": {
    "executable": "codex",
    "sessions_root": "",
    "prompt_timeout_seconds": 300
  },
  "opencode": {
    "executable": "opencode",
    "driver": "cli_adapter",
    "server_executable": "opencode",
    "server_url": "",
    "server_hostname": "127.0.0.1",
    "server_port": 4096,
    "server_password": "",
    "gateway_env_path": "",
    "model_catalog_path": "",
    "agent_home": "$(json_escape "$state_dir/opencode_home")",
    "worktree_root": "$(json_escape "$state_dir/opencode_worktrees")",
    "native_database_path": "",
    "default_timeout_seconds": 900,
    "allowed_repo_roots": ["$(json_escape "$home")"]
  },
  "host": {
    "max_download_bytes": 524288000,
    "show_hidden": false,
    "file_roots": [
      {"id": "workspace", "label": "Workspace", "path": "$(json_escape "$home")", "download": true},
      {"id": "releases", "label": "Releases", "path": "$(json_escape "$release_dir")", "download": true}
    ]
  },
  "shell": {
    "manifest_path": "$(json_escape "$home/watcher.shell.json")",
    "version_file": "$(json_escape "$home/VERSION")",
    "components_root": "$(json_escape "$home/modules")"
  },
  "display": {
    "default_language": "zh",
    "timezone": "Asia/Shanghai"
  },
  "security": {
    "allowed_hosts": ["127.0.0.1", "localhost"],
    "trusted_proxies": [],
    "secure_cookies": false,
    "session_secret": "$(json_escape "$WATCHER_SESSION_SECRET")",
    "session_ttl_seconds": 86400,
    "max_body_bytes": 1048576,
    "global_rate_limit_per_minute": 240,
    "login_rate_limit_per_minute": 20,
    "enable_hsts": false
  }
}
EOF
  chmod 600 "$service_config"
fi

relay_config="$config_dir/relay.json"
if write_once "$relay_config"; then
  cat > "$relay_config" <<EOF
{
  "bind_addr": "$(json_escape "$relay_bind")",
  "database_path": "$(json_escape "$state_dir/relay.db")",
  "owner_token": "$(json_escape "$WATCHER_RELAY_OWNER_TOKEN")",
  "service": {
    "base_url": "$(json_escape "$service_base_url")",
    "owner_token": "$(json_escape "$WATCHER_SERVICE_OWNER_TOKEN")",
    "request_timeout_seconds": 300
  },
  "app_release": {
    "version_code": $app_version_code,
    "version_name": "$(json_escape "$app_version_name")",
    "notes": "$(json_escape "$app_notes")",
    "apk_path": "$(json_escape "$apk_path")",
    "published_at": "$(json_escape "$published_at")"
  },
  "push": {
    "xiaomi": {"app_id": "", "app_key": "", "app_secret": "", "use_sandbox": true, "channel_id": "watcher_push"}
  },
  "display": {
    "default_language": "zh",
    "timezone": "Asia/Shanghai"
  },
  "security": {
    "allowed_hosts": $allowed_hosts_json,
    "trusted_proxies": [],
    "max_body_bytes": 1048576,
    "global_rate_limit_per_minute": 240,
    "enable_hsts": false,
    "tls": {
      "enabled": $tls_json,
      "auto_self_signed": true,
      "cert_file": "",
      "key_file": "",
      "fingerprint_file": "$(json_escape "$config_dir/relay.tls.fingerprint")",
      "hosts": []
    }
  }
}
EOF
  chmod 600 "$relay_config"
fi

if [[ "$write_systemd" -eq 1 ]]; then
  systemd_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  mkdir -p "$systemd_dir"
  cat > "$systemd_dir/watcher-service.service" <<EOF
[Unit]
Description=Watcher Service
After=network-online.target
StartLimitIntervalSec=5min
StartLimitBurst=4

[Service]
WorkingDirectory=$home
ExecStart=$bin_dir/watcher-service --config $service_config
Restart=always
RestartSec=5
MemoryAccounting=yes
MemoryHigh=1500M
MemoryMax=2200M
OOMPolicy=stop
TasksMax=256

[Install]
WantedBy=default.target
EOF
  cat > "$systemd_dir/watcher-relay.service" <<EOF
[Unit]
Description=Watcher Relay
After=network-online.target watcher-service.service

[Service]
WorkingDirectory=$home
ExecStart=$bin_dir/watcher-relay --config $relay_config
Restart=on-failure

[Install]
WantedBy=default.target
EOF
  systemctl --user daemon-reload
fi

if [[ "$start_services" -eq 1 ]]; then
  systemctl --user enable --now watcher-service.service
  systemctl --user enable --now watcher-relay.service
fi

cat <<EOF
Watcher prebuilt config is ready.

Home:           $home
Service config: $service_config
Relay config:   $relay_config
Tokens:         $tokens_file
APK:            ${apk_path:-not configured}

Start manually:
  $bin_dir/watcher-service --config $service_config
  $bin_dir/watcher-relay --config $relay_config

Health checks:
  curl $service_base_url/api/v1/health
  curl $relay_base_url/api/v1/health
EOF
