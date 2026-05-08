# Prebuilt Installer

This directory contains the no-build deployment helper for released Watcher
packages.

Run it from an extracted prebuilt package:

```bash
cd ~/watcher
./deploy/prebuilt/install.sh
```

The script generates machine-local files under `config/`:

- `config/service.json`
- `config/relay.json`
- `config/tokens.env`

These files are not part of the public source or release templates. They contain
local paths and random owner tokens for that deployment.

Common public relay example:

```bash
WATCHER_RELAY_BIND=0.0.0.0:8780 \
WATCHER_ALLOWED_HOSTS=127.0.0.1,localhost,<server-ip-or-domain> \
./deploy/prebuilt/install.sh --systemd --start
```

Enable built-in self-signed HTTPS:

```bash
WATCHER_TLS_ENABLED=true ./deploy/prebuilt/install.sh --force
```
