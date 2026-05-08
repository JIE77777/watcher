# Open Source Readiness

> Status: Project requirement
> Scope: public release preparation for the Watcher base and opencodev2 reference module

Watcher is preparing to become open source for users with the same class of
need: a single owner who wants a private, diagnosable bridge between a personal
server, local coding agents, Android, and an optional relay.

Open source readiness does not change the product tone. Watcher remains:

```text
watcher = shell + components
```

It is still owner-first, event and operation driven, and usable without assuming
a managed cloud service.

## Public Scope

The public release scope is the base project plus the first reference module.

Base Watcher includes:

- `service`: owner-auth APIs, shell status, module registry, operations, typed events, diagnostics, and local runtime ownership
- `relay`: device entry, app release forwarding, durable event forwarding, and shell/module API forwarding
- `android`: shell home, tools/modules navigation, settings/system surface, local caches, diagnostics, update flow, and module screens
- `internal/components`, `modules/*/component.json`, and docs that define the shell/component contract
- `devtools` smoke and scaffolding utilities needed to reproduce builds and verify main paths

`opencodev2` is included as the reference interactive conversation module. It demonstrates how a module adapts an upstream runtime into Watcher snapshots, pulses, operations, pending input, Android presentation, and relay forwarding.

`box` is included as the public non-agent module example. The public export
contains the configurable catalog/view framework and a public-safe LLM
leaderboard fixture. Private scraper-backed sources remain under
`modules/box/private/` and are not exported.

Archived reference modules are included only as historical material:

- `codex`: Codex app-server/mobile bridge research and legacy Android thread UI
- `pilot`: shell semantic assistant prototype and worker-lane capsule experiment
- `cc`: Claude Code + MiMo managed patch lane

Archived modules must not start workers by default, must not appear as primary
Android tools, and must not be presented as recommended extension points for
new open-source users.

Security hardening remains a separate line. Public docs must state the expected posture, token requirements, trusted deployment shape, and HTTPS/reverse-proxy responsibility, but this pass should not inflate the base with a managed security product.

## Target User

The public target user is not a generic consumer.

They are expected to:

- run their own Linux host, development machine, or small server
- understand token-based owner auth, reverse proxy basics, and local logs
- want Android as a terminal for status, operations, and recovery
- want opencode handoff across PC and Android without turning Watcher into a chat SaaS

The project should be understandable to a new owner, but it should not add
marketing copy, onboarding flows, or social/product-growth surfaces.

## Release Requirement

Before calling a state publicly usable, the repo must satisfy these gates:

- Fresh setup path: a new machine can run base `service`, `relay`, Android debug build, module discovery, and opencodev2 mirror smoke by following tracked docs and example configs.
- No private defaults: tracked config examples must not require machine-specific home paths,
  private domains, private model aliases, real tokens, or local-only secrets.
- Explicit security posture: owner token, session secret, host allowlist,
  trusted proxies, HTTPS, and public relay exposure are documented without treating hardening as part of this mainline pass.
- Stable public contract: shell/component boundaries, operation lifecycle,
  typed event streams, module descriptors, and Android surfaces are documented
  before promotion.
- Reproducible smoke tests: Go test commands and Android build commands are
  documented and expected to pass before release notes.
- License and contribution boundary: the repo has an explicit license and a
  short contribution policy before public announcement.
- Public export metadata: generated metadata must not expose private absolute
  paths, private revision state, hostnames, usernames, or local workspace names.
- Archive policy: retired implementation paths are labeled as reference, not
  advertised as the recommended path.
- Runtime hygiene: archived worker modules are disabled by the base registry and
  kept out of the Android Tools page.

## Opencode Public Story

`opencodev2` is the public story for the opencode component.

The public positioning is:

- opencode native `ses_*` sessions are the continuity source
- Watcher provides the shell contract, owner-auth API, async operations, typed
  status, relay forwarding, and Android surface
- Android uses mirror session list, snapshot, pulse, message submit, and abort
- Watcher does not claim ownership transfer between devices
- Watcher does not parse assistant prose to decide UI state

The first Watcher-managed opencode implementation is archived as reference. It
may remain useful for tests, redaction, operation lifecycle, native database
inspection, and failure handling, but it must not be the default open-source
workflow.

## Public Defaults

Open-source defaults should be conservative:

- bind local services to loopback unless a guide explicitly says otherwise
- require owner-provided high-entropy tokens
- keep relay optional
- keep Android as a terminal under `Signals / Tools / System`
- keep component APIs under `/api/v2/modules/<component>`
- expose module discovery through `GET /api/v2/modules`
- prefer examples with placeholders over machine-specific paths

## Non-Goals

Open source does not mean:

- multi-user accounts
- hosted SaaS mode
- plugin marketplace
- community feed
- onboarding wizard
- public AI agent marketplace
- default full-access coding actions from the Android home screen

## Checklist For Future Changes

For every feature added after this requirement, ask:

- Does this still serve a single self-hosted owner?
- Can another owner reproduce it from docs without private local context?
- Are all secrets, hostnames, paths, and model aliases configurable?
- Does the feature stay inside shell/component boundaries?
- Does Android consume typed state rather than natural language?
- Is the failure mode visible through operations, events, or diagnostics?

If any answer is unclear, the feature is not ready for the open-source line.
