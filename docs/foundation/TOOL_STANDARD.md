# Tool Standard

Watcher tools are small, replaceable probes for data-oriented components.

They are intentionally not components, plugins, transports, or Android features.
Their job is to observe an external source and emit one stable fact snapshot.

The current private `box` tools are not part of the first public export. This
standard stays as the contract for a later public example component.

## Elegance Bar

- Keep the interface small.
- Keep identities stable.
- Write diagnostics to `stderr`.
- Write only `SourceSnapshot` JSON to `stdout`.
- Let the owning component define domain meaning.
- Let `shell` own auth, sync, event bus, releases, and mobile delivery.

## Placement

Runtime tools live under one of:

- `tools/scrapers/<tool_id>/`
- `tools/connectors/<tool_id>/`
- `tools/parsers/<tool_id>/`

Engineering helpers, reverse-engineering scripts, APK tools, and local operator
commands live under `devtools/`, not `tools/`.

## Required Files

Every runtime tool must provide:

- `manifest.json`
- an executable entry point
- `config.example.json`
- a short `README.md`

When behavior is non-trivial, also add fixtures or a smoke note that explains how
to run the tool without changing product state.

## Manifest

`manifest.json` is deliberately small:

```json
{
  "id": "example_tool",
  "name": "Example Tool",
  "version": "v1",
  "kind": "scraper",
  "language": "python",
  "runtime": "python3",
  "entry_point": "run.py",
  "description": "Fetches an external source and emits a SourceSnapshot."
}
```

Rules:

- `id` uses lowercase ASCII letters, numbers, `_`, or `-`.
- `kind` is `scraper`, `connector`, or `parser`.
- `version` is the output contract version, not the package release version.
- `entry_point` is relative to the tool directory unless absolute.

## Runtime Contract

The service invokes a tool as:

```bash
<runtime> <entry_point> --config <path>
```

The config file is JSON. `watcher-service` injects `task_id`, `task_name`, and
`task_labels` into the tool config before execution.

Output rules:

- `stdout` must contain exactly one `SourceSnapshot` JSON document.
- `stderr` may contain human-readable diagnostics.
- exit code `0` means the snapshot was emitted successfully.
- non-zero exit means the run failed; `stderr` should explain why.

## SourceSnapshot Rules

The stable identity fields are the product contract:

- `source_id` must equal `manifest.id`.
- `task_id` must match the task being run.
- `version` must be stable for a given output shape.
- every item must have a stable `item_key`.
- every item should have a stable `thread_key`; if omitted, service fills a fallback from `task_id:item_key`.

Do not put volatile timestamps, ranks, counters, or fetched text into identity
fields. Put volatile facts in `data` or `raw_meta`.

## Failure Style

Tools should fail plainly:

- external HTTP/API failure: non-zero exit with a short `stderr` message.
- parse failure: non-zero exit and include the field or source section that failed.
- partial data: emit a valid snapshot and put missing source details in `raw_meta`.

The app should never need to understand tool internals. A failed tool run becomes
Box state, diagnostics, and `watcher.task` behavior through the service.

## Non-Goals

Tools must not:

- publish events directly.
- talk to relay directly.
- define Android UI.
- implement owner auth or device sync.
- write product state outside their output snapshot.
- depend on another component's private implementation.
