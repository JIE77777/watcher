# Host Module

`host` is the default base utility module for a self-hosted Watcher deployment.
It replaces `probe` in the public mainline.

## Responsibilities

- expose lightweight server health and resource status
- list files from configured roots
- allow the owner to add/remove local file roots without rebuilding Android
- download allowed files through owner/device authenticated APIs
- surface low disk or memory conditions as Shell Signals

## API

```text
GET /api/v2/modules/host/overview
GET /api/v2/modules/host/files?root=<root_id>&path=<relative_path>
GET /api/v2/modules/host/files/download?root=<root_id>&path=<relative_path>
POST /api/v2/modules/host/file-roots
DELETE /api/v2/modules/host/file-roots/{root_id}
```

## Configuration

```json
{
  "host": {
    "max_download_bytes": 524288000,
    "show_hidden": false,
    "file_roots": [
      {
        "id": "workspace",
        "label": "Workspace",
        "path": ".",
        "download": true
      },
      {
        "id": "releases",
        "label": "Releases",
        "path": "./releases",
        "download": true
      }
    ]
  }
}
```

Configured `file_roots` and saved custom roots are the security boundary. A
requested path is cleaned, symlinks are resolved where possible, and the resolved
path must remain inside the selected root. The file manager is intended to
download visible service files, so the default roots use `download=true`.
Download still requires an allowlisted root, a regular file, and a size within
`max_download_bytes`.

When a listed directory is also configured as another file root, the service
returns `target_root_id` metadata. Android should switch to that root before
opening it. This keeps nested roots explicit when a deployment chooses stricter
per-root download settings.

Custom roots are stored in the Watcher service database. They are useful for
personal deployments where the owner wants to browse a release folder, a log
folder, or another project directory from Android without rebuilding the APK.

## Replacement For Probe

`probe` remains an archived worker-lane reference, but it is no longer a public
product component or default Android entry. Host is the practical public sample
for a non-agent module: it has a manifest, service APIs, relay forwarding,
Shell Home integration, Android rendering, and documentation.
