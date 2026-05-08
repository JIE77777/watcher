# Contributing

Watcher uses a generated public repository.

The private development workspace is the source of truth. The public repository
is a release mirror produced by `devtools/public/export_public.sh`.

## Workflow

1. Open an issue or pull request against the public repository.
2. Keep changes scoped to the base, public modules, docs, or examples.
3. Avoid adding local paths, private hosts, tokens, generated state, APKs,
   databases, logs, or machine-specific defaults.
4. Accepted changes are applied to the source workspace first, then exported
   back into the public repository.

## Local Checks

Before proposing a change, run:

```bash
go test ./...
cd android
./gradlew :app:assembleDebug --no-daemon
```

Public export maintainers should also run:

```bash
devtools/public/export_public.sh --force
devtools/public/audit_public.sh
```

## Project Direction

Watcher is a self-hosted, owner-first tool shell. The first public line is:

- base service / relay / Android shell
- `opencode` conversation bridge
- `box` configurable information-source example
- `probe` as a minimal worker-runtime example

`codex`, `pilot`, and `cc` are archived references. New public work should not
extend those paths unless they are intentionally rewritten against the current
module contract.
