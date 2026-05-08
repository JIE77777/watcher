# Watcher

[中文](README.md)

Watcher is a self-hosted personal tool terminal.

It runs a small backend on your own machine, exposes a relay as the only
external entry point, and provides an Android client for everyday operation:
server status, file download, information surfaces, app updates, and opencode
sessions.

## Design Rule

Watcher is built for a single owner, not for multi-tenant SaaS.

```text
Android / browser / external network
        |
        v
watcher-relay      public boundary, device auth, install/update, TLS, rate limit
        |
        v
watcher-service    local source of truth, modules, opencode bridge, host tools
```

`watcher-service` should normally stay on `127.0.0.1`. Expose
`watcher-relay`, not the service port.

## Status

Watcher is in public preview.

The public mainline is intentionally small:

- `relay`: external boundary for Android, install/update, device auth, and module forwarding
- `service`: local runtime, SQLite state, module APIs, diagnostics
- `android`: mobile shell for signals, modules, settings, updates, and opencode sessions
- `opencode`: main agent module, backed by opencode native server/session APIs
- `host`: server overview and allowlisted file browsing/download
- `box`: config-driven information surfaces; public example is an LLM leaderboard fixture

`codex`, `pilot`, and `cc` are archived reference modules. They remain in the
tree as historical context, but they are not the recommended extension path.

## What You Can Do

- Install the Android APK from your own relay.
- Register Android with a relay owner token.
- Check shell/component diagnostics from Android.
- Browse Host status and download files from configured roots.
- Use Box as a hot-updated information source module.
- List, create, read, and continue opencode native sessions from Android.
- Keep `service` private while exposing only `relay` over LAN, Tailscale, or public IP.

## Quick Start: Prebuilt

Use the prebuilt release if you want to deploy without installing Go, Java,
Gradle, Android SDK, or Android build tools on the target server.

Download release assets from:

```text
https://github.com/JIE77777/watcher/releases
```

Extract the prebuilt package:

```bash
mkdir -p ~/watcher
tar -xzf watcher-v0.3.1-linux-amd64-prebuilt.tar.gz -C ~/watcher --strip-components=1
cd ~/watcher
./deploy/prebuilt/install.sh
```

Start manually:

```bash
bin/watcher-service --config config/service.json
bin/watcher-relay --config config/relay.json
```

Or generate user systemd services:

```bash
./deploy/prebuilt/install.sh --systemd --start
```

For Android access from another device, bind the relay to a reachable address:

```bash
WATCHER_RELAY_BIND=0.0.0.0:8780 \
WATCHER_ALLOWED_HOSTS=127.0.0.1,localhost,<server-ip-or-domain> \
./deploy/prebuilt/install.sh --force --systemd --start
```

Generated local secrets live under:

```text
config/tokens.env
config/service.json
config/relay.json
```

Keep them private. Android usually needs the relay URL and the relay owner
token.

## Install Android

After the relay is running, open:

```text
https://<relay-host>:8780/install
```

If TLS is disabled, use `http://` instead.

The install page can be loaded without auth, but APK download is locked. Enter
the relay owner token once to unlock a short-lived install session.

The APK in public preview is suitable for personal deployment. If you rebuild
the Android app yourself, keep using the same signing key; Android will not
update an installed app with an APK signed by a different key.

## From Source

Requirements:

- Go with CGO support
- SQLite build dependencies
- Android toolchain only if you build the APK yourself
- `opencode` only if you use the opencode module

Start the backend from a checkout:

```bash
go run ./service/cmd/watcher-service --config ./service/config.example.json
go run ./relay/cmd/watcher-relay --config ./relay/config.example.json
```

Health checks:

```bash
curl http://127.0.0.1:8765/api/v1/health
curl http://127.0.0.1:8780/api/v1/health
```

Local dashboard:

```text
http://127.0.0.1:8765/
```

The dashboard belongs to `watcher-service` and is intended for local use. For
external access, prefer Android or relay-backed surfaces instead of forwarding
the service port directly.

## Configuration

The prebuilt installer generates usable local config. If you edit config by
hand, keep these rules:

- `service.owner_token` protects the local service dashboard and service APIs.
- `relay.owner_token` is used for Android registration and relay owner APIs.
- `relay.service.owner_token` must match `service.owner_token`.
- `service.relay.owner_token` and `service.relay_push.owner_token` must match `relay.owner_token`.
- `service.bind_addr` should usually stay `127.0.0.1:8765`.
- `relay.bind_addr` is the address Android should reach.
- `service.security.allowed_hosts` should list the actual hosts you use.

For opencode server mode, Watcher follows opencode's default Basic Auth
username:

```json
{
  "opencode": {
    "driver": "server_adapter",
    "server_username": "opencode",
    "server_password": "your-random-password"
  }
}
```

If your opencode setup overrides `OPENCODE_SERVER_USERNAME`, set
`server_username` to the same value.

## Modules

### Opencode

The opencode module is the main agent surface. Watcher starts or connects to an
opencode server, mirrors native `ses_*` sessions into local SQLite, and exposes
mobile-friendly `snapshot` and `pulse` APIs for Android.

Android should use the `opencode-mirror` surface for current work. The older
Watcher turn/session APIs are kept for compatibility and historical reference.

### Host

Host is a small server utility surface:

- CPU/memory/disk overview
- configured file roots
- breadcrumb browsing
- file downloads with root and size limits

It is read/download-oriented. Upload, delete, rename, move, and remote shell
are non-goals.

### Box

Box is the public example for non-agent modules. A `.box.json` file describes
sources, datasets, views, and signals. Android fetches the latest catalog and
view schema at runtime, so changing a Box definition does not require rebuilding
the app.

The public tree includes a safe LLM leaderboard fixture. Private scraper-backed
boxes should stay outside the public export.

## Security

Watcher keeps security thin but explicit:

- one external boundary: `watcher-relay`
- owner token for first trust and owner APIs
- device tokens for registered Android clients
- host allowlist
- request body limit
- per-IP rate limit
- secure headers
- optional relay self-signed HTTPS
- service dashboard same-origin checks

If you expose the relay publicly and do not have a domain, enable the built-in
self-signed HTTPS mode and trust the certificate fingerprint once from Android
Settings. The relay port does not need to be `443`.

## Repository Layout

```text
android/        Android client
service/        local runtime and module APIs
relay/          external relay, install/update, device auth
internal/       shared Go packages and SQLite store
pkg/            reusable public packages
modules/        component manifests and module docs
tools/          public tool/parser placeholders
deploy/         prebuilt deployment scripts
devtools/       export, release, smoke, scaffold helpers
docs/           architecture, security, module, deployment docs
```

## Development Checks

Backend:

```bash
go test ./pkg/serverguard ./service/cmd/watcher-service ./relay/cmd/watcher-relay
```

Android debug build:

```bash
cd android
./gradlew :app:assembleDebug --no-daemon
```

Public export audit:

```bash
devtools/public/audit_public.sh --target ../watcher-public
```

## Documentation

- [Prebuilt Deployment](docs/PREBUILT_DEPLOYMENT.md)
- [Environment Setup](docs/ENVIRONMENT_SETUP.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Security](docs/SECURITY.md)
- [Open Source Readiness](docs/foundation/OPEN_SOURCE_READINESS.md)
- [Public Export](docs/foundation/PUBLIC_EXPORT.md)
- [Module Contract V2](docs/foundation/MODULE_CONTRACT_V2.md)
- [Opencode Agent](docs/modules/OPENCODE_AGENT.md)
- [Host Module](docs/modules/HOST_MODULE.md)
- [Android Connection Troubleshooting](docs/ANDROID_CONNECTION_TROUBLESHOOTING.md)
- [Contributing](CONTRIBUTING.md)
- [License](LICENSE)

## Non-Goals

- multi-user SaaS hosting
- exposing `watcher-service` as a public API gateway
- generic agent marketplace
- arbitrary remote shell from Android
- public export of private scrapers or personal automation
