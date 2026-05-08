# Versioning

`watcher` uses Git as the source of truth for project history and lightweight semantic versions for releases.

## Goals

- Make it easy to see what changed
- Keep runtime secrets and machine-local state out of version control
- Support future app and backend releases without rewriting history conventions

## What is versioned

Tracked in Git:

- Source code
- Docs
- Config examples
- Scripts
- Build definitions

Not tracked in Git:

- `state/` runtime databases and runtime configs
- generated APKs and build outputs
- machine-local Android SDK pointers such as `android/local.properties`
- transient logs and caches

## Version scheme

Project version lives in:

- [VERSION](../VERSION)

Current baseline:

- `0.2.0`

Meaning:

- `MAJOR`: breaking API or architecture changes
- `MINOR`: new features, new user-facing capabilities, or major workflow milestones
- `PATCH`: fixes, hardening, docs improvements, or UX polish

## Release process

Recommended lightweight flow:

1. Update code and docs
2. Bump [VERSION](../VERSION)
3. Add a new section to [CHANGELOG.md](../CHANGELOG.md)
4. Commit
5. Tag the commit, for example `v0.1.1`

Example:

```bash
cd <watcher-workspace>
git add .
git commit -m "Release v0.1.1"
git tag -a v0.1.1 -m "watcher v0.1.1"
```

## Suggested commit style

Keep commit messages short and functional:

- `init: bootstrap watcher repository`
- `feat: add relay-backed app update endpoint`
- `fix: handle Android system bar insets`
- `docs: add environment setup guide`

## App version vs project version

These can move independently:

- Project version: [VERSION](../VERSION)
- Android app version: [app/build.gradle.kts](../android/app/build.gradle.kts)

That means:

- backend/docs-only change may bump project version without changing Android app version
- app release can bump Android version while the project version advances on its own cadence
