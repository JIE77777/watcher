# Public Workspace Tools

These tools manage the generated public staging tree.

Private source:

```text
<watcher-private>
```

Default public staging tree:

```text
../watcher-public
```

Use:

```bash
devtools/public/export_public.sh --force
devtools/public/audit_public.sh
```

Do not develop business changes in the public staging tree. Fix issues in the
private source and export again.

Public documentation is allowlisted. Keep public-facing docs safe in the
private source, and keep internal notes outside `devtools/public/public-files.txt`.
