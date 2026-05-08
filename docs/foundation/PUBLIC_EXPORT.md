# Public Export

> Status: implementation baseline
> Scope: managing the private development workspace and public staging workspace

Watcher is not opened by publishing the current private workspace directly.

The project uses two workspaces:

```text
<watcher-private>   private development source
<watcher-public>    public staging export
```

The private workspace is the only development source. The public workspace is a
generated staging tree used for audit, build checks, and public repository
commits.

## Rules

- Develop in the private workspace.
- Export to the public workspace with `devtools/public/export_public.sh`.
- Audit the public workspace with `devtools/public/audit_public.sh`.
- Do not hand-edit business code in the public workspace.
- If the public workspace exposes a problem, fix it in the private workspace and
  export again.
- Do not merge the public repository history back into the private repository.

The public repository should start from a clean exported tree, not from the
private repository history. This avoids publishing local configs, runtime state,
private paths, and historical mistakes.

## Export Shape

The export is allowlist based. The allowlist lives at:

```text
devtools/public/public-files.txt
```

Allowed paths are copied into the public staging tree. Runtime state and local
deployment files are excluded even when their parent directories are exported.

Documentation is also allowlisted. Do not export `docs/` as a whole directory:
the private tree may contain research notes, old design snapshots, and local
deployment records. Public documentation should be source-authored in the
private repository, but written so it is safe to copy directly into the public
tree.

The first public profile includes the `box` framework and public-safe LLM
leaderboard example. It omits private `box` sources and scraper/tool
experiments.

Default excluded classes:

- `.git`, `.claude`, local editor/runtime files
- `state`, `releases`, `lab`, `consulting`, `ops`, private references
- private `box` sources and scraper/tool experiments
- local config files such as `service/config.local.json`,
  `relay/config.local.json`, and `android/local.properties`
- databases, logs, APKs, keystores, build outputs, and caches
- real tokens, private hostnames, private IPs, and machine-specific paths

## Commands

Preview the export target:

```bash
devtools/public/export_public.sh --dry-run
```

Export to the default staging directory:

```bash
devtools/public/export_public.sh --force
```

Audit the exported tree:

```bash
devtools/public/audit_public.sh
```

Use a different target:

```bash
WATCHER_PUBLIC_DIR=/tmp/watcher-public devtools/public/export_public.sh --force
WATCHER_PUBLIC_DIR=/tmp/watcher-public devtools/public/audit_public.sh
```

## Documentation Maintenance

Public docs have one source of truth: the private workspace.

- Keep public-facing docs public-safe in place.
- Put internal notes under private-only paths such as `docs/private/`, `lab/`,
  `ops/`, or other paths omitted from `public-files.txt`.
- Keep archived module docs short in public. Detailed historical notes should
  stay private unless they are intentionally rewritten for public readers.
- Do not edit docs directly in the public workspace. Fix the private source,
  export again, audit again, then commit the public tree.
- Prefer placeholders in public docs and examples: `<watcher-workspace>`,
  `<server-host>`, `<owner-token>`, and `127.0.0.1`.

## Public Source Metadata

Each export writes `PUBLIC_SOURCE.txt` into the public staging tree. It records:

- redacted source workspace marker
- redacted source revision marker
- export time
- export profile
- allowlist path

This file is for public mirror traceability. It must not contain private
absolute paths, private revision state, hostnames, or local usernames.

## Pull Requests From Public Repo

Public contributions should still land in the private source first:

1. Review the public PR.
2. Apply the accepted patch to the private workspace.
3. Run private tests.
4. Export again to the public staging workspace.
5. Commit the regenerated public tree.

This keeps the private workspace as the source of truth and the public
repository as a clean release mirror.
