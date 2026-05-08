# Prebuilt Deployment

> Scope: deploy Watcher without compiling on the target host.

This path is for a personal owner who wants to run released binaries and an APK
instead of installing Go, JDK, Gradle, and Android SDK on the server.

## Artifacts

A prebuilt release should provide:

- `watcher-service` binary for the target OS/arch
- `watcher-relay` binary for the target OS/arch
- runtime assets:
  - `watcher.shell.json`
  - `VERSION`
  - `modules/`
  - `tools/`
- `service/config.example.json`
- `relay/config.example.json` or `relay/config.lan.example.json`
- Android APK
- optional SHA-256 checksums

Because the backend uses SQLite through `go-sqlite3`, the binary must match the
target platform. If a prebuilt Linux binary does not start because of libc/CGO
compatibility, use the source-build path in [Environment Setup](ENVIRONMENT_SETUP.md).

The target host still needs a normal Linux userland:

- `glibc` compatible with the published binary
- `openssl` or another local way to generate high-entropy tokens
- `curl` for health checks
- optional `python3` only if you run Python-based tools from `tools/`
- optional `opencode` only if you use the `opencode` component

It does not need Go, Java, Gradle, Android SDK, or Android build tools when using
the prebuilt backend binaries and APK.

## No Private Config In Artifacts

Release artifacts must not contain machine-local config:

- no `service/config.local.json`
- no `relay/config.local.json`
- no generated `config/*.json`
- no real owner tokens
- no databases, logs, keystores, APK signing keys, or host-specific paths

The release package contains examples and an installer only. First-run config is
generated on the target host.

## Server Layout

Recommended single-user layout:

```bash
mkdir -p ~/watcher/bin ~/watcher/config ~/watcher/state ~/watcher/releases
```

Put binaries in `~/watcher/bin`:

```text
~/watcher/bin/watcher-service
~/watcher/bin/watcher-relay
```

Put editable configs in `~/watcher/config`:

```text
~/watcher/config/service.json
~/watcher/config/relay.json
```

Put runtime assets in `~/watcher`:

```text
~/watcher/watcher.shell.json
~/watcher/VERSION
~/watcher/modules/
~/watcher/tools/
```

Use high-entropy tokens:

```bash
openssl rand -hex 32
```

The prebuilt installer can generate this layout and tokens automatically:

```bash
cd ~/watcher
./deploy/prebuilt/install.sh
```

For a relay that Android can reach over LAN or public IP:

```bash
cd ~/watcher
WATCHER_RELAY_BIND=0.0.0.0:8780 \
WATCHER_ALLOWED_HOSTS=127.0.0.1,localhost,<server-ip-or-domain> \
./deploy/prebuilt/install.sh --systemd --start
```

To enable built-in self-signed HTTPS:

```bash
WATCHER_TLS_ENABLED=true ./deploy/prebuilt/install.sh --force
```

Generated local files:

```text
~/watcher/config/service.json
~/watcher/config/relay.json
~/watcher/config/tokens.env
```

`tokens.env` is only for the owner to retrieve the generated tokens. Keep it
private and do not publish it.

Minimum config rules:

- `service.owner_token` is the owner token used by Dashboard and relay service forwarding.
- `relay.owner_token` is the owner token used by Android registration and relay owner APIs.
- `relay.service.owner_token` must equal `service.owner_token`.
- `service.relay.owner_token` and `service.relay_push.owner_token` must equal `relay.owner_token` when service publishes events or push wakeups through relay.
- Keep `watcher-service` bound to `127.0.0.1` unless you intentionally expose it.
- Expose `watcher-relay`, not `watcher-service`, to Android.

## First Run

Start service:

```bash
~/watcher/bin/watcher-service --config ~/watcher/config/service.json
```

Start relay:

```bash
~/watcher/bin/watcher-relay --config ~/watcher/config/relay.json
```

Health checks:

```bash
curl http://127.0.0.1:8765/api/v1/health
curl http://127.0.0.1:8780/api/v1/health
```

If the relay is public and you do not have a domain, enable the built-in
self-signed HTTPS config under `relay.security.tls`. Android can trust the relay
certificate fingerprint once from Settings. The port does not need to be `443`.

## APK Install And Updates

For first install, either:

- open the relay install page in a browser: `https://<relay-host>:8780/install`
- install the APK directly on Android

The browser install page is public to load, but APK download is locked. Enter
the existing relay owner token once; relay sets a short-lived install cookie and
then allows `/install/apk`. This avoids adding another long-lived token while
keeping anonymous clients from downloading the APK.

For in-app updates, configure `relay.app_release`:

```json
{
  "app_release": {
    "version_code": 83,
    "version_name": "1.17.50",
    "notes": "Release notes",
    "apk_path": "/home/you/watcher/releases/watcher-1.17.50.apk",
    "published_at": "2026-05-08T00:00:00Z"
  }
}
```

Android only offers an update when `version_code` is higher than the installed
app.

## Android Signing

Android update trust is based on the APK signing certificate.

For personal/private deployment:

- debug signing is acceptable for testing and private use if you keep using the
  same debug keystore
- debug signing is not a public release identity
- an APK signed by a different key cannot update the installed app

For public release:

- use a dedicated release keystore
- never commit the keystore, key password, or signing config with secrets
- keep the same release key for future updates

Switching from debug-signed APK to release-signed APK usually requires
uninstalling the old app first, because Android treats them as different signing
identities.

## Build Labels

APK signing certificate is the real update identity. For private debug APKs, the
debug signing certificate itself is the practical "watermark": devices can only
update to APKs signed by the same key.

`WATCHER_BUILD_WATERMARK` is only an optional visible build label. It does not
replace APK signing and does not prove authenticity.

For private builds, set:

```properties
WATCHER_BUILD_WATERMARK=your-name-or-device-line
```

The Android Settings page and debug report show this value when it is present.
Leave it blank if the signing certificate is enough. Changing the label requires
rebuilding and signing the APK. If you use a prebuilt APK, use release notes or
the relay install page for deployment notes instead.

## systemd Sketch

`watcher-service.service`:

```ini
[Unit]
Description=Watcher Service
After=network-online.target
StartLimitIntervalSec=5min
StartLimitBurst=4

[Service]
WorkingDirectory=%h/watcher
ExecStart=%h/watcher/bin/watcher-service --config %h/watcher/config/service.json
Restart=always
RestartSec=5
MemoryAccounting=yes
MemoryHigh=1500M
MemoryMax=2200M
OOMPolicy=stop
TasksMax=256

[Install]
WantedBy=default.target
```

`watcher-relay.service`:

```ini
[Unit]
Description=Watcher Relay
After=network-online.target watcher-service.service

[Service]
WorkingDirectory=%h/watcher
ExecStart=%h/watcher/bin/watcher-relay --config %h/watcher/config/relay.json
Restart=on-failure

[Install]
WantedBy=default.target
```

Keep public exposure thin: relay is the Android entry, service remains local.
