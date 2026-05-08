# Host Component

`host` is Watcher's small self-hosted server utility module.

It replaces the old `probe` component as the public non-agent example. The
module is intentionally practical: it shows the current server status and lets a
registered owner download files from configured allowlisted roots.

## Scope

- runtime: in-process service handlers
- surfaces: overview, files
- resources: host snapshot, file roots, files
- Android: single Host screen with status tiles, root selector, custom root entry,
  breadcrumb, and a file browser

## Safety

Host never exposes arbitrary filesystem access. File browsing and downloads are
limited to configured roots plus owner-created custom roots saved by the service.
The default roots are downloadable because the file manager's job is to retrieve
visible service files. Downloads still require an allowlisted root, a regular
file, and the configured size limit.

If a directory inside one root is also configured as a separate root, Android
switches to that root before listing files. This keeps the root boundary explicit
when a deployment chooses stricter per-root download settings.

MVP non-goals:

- uploads
- deletes
- rename/move
- remote shell commands
- public share links
