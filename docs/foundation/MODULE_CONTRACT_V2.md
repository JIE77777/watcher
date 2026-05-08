# Module Contract V2

> Status: implementation baseline
> Scope: Watcher shell/module boundary, Android decoupling, open-source module authoring

Watcher 不是只为 agent 服务的项目。长期结构是：

```text
Watcher = shell + modules + clients
```

`module` 是一块产品能力，不是一个固定类型。Agent 会话、任务监控、诊断、数据集、工具面板都可以是 module。Shell 只承载模块，不替模块做业务判断。

## Design Goal

这一版契约解决四个问题：

- Android 不再靠硬编码页面猜测模块能力
- Service 不把某个 agent runtime 的内部状态冒充平台模型
- Relay 只转发 owner-auth API 和 typed events，不承接业务判断
- 开源用户可以通过 manifest 理解一个模块能做什么、入口在哪、动作风险是什么

## Ownership

### Shell Owns

- owner auth
- device auth
- relay forwarding
- typed event bus
- async operation lifecycle
- module discovery
- runtime diagnostics
- shared Android shell navigation

### Module Owns

- domain resources
- operations
- event vocabulary
- runtime adapter
- presentation state
- failure semantics
- module-specific screens

### Android Owns

- rendering
- local cache
- optimistic UI only where the module contract allows it
- navigation by `ShellTarget`

Android may understand common presentation contracts such as `feed`, `collection`, `conversation`, `operation_list`, `artifact`, and `pending_input`. Android should not understand service internals such as opencode server adapter structs, Codex app-server request shapes, worker process bookkeeping, or SQLite overlay rows.

Android has a generic module detail fallback for module descriptors. A module can ship without a bespoke Android screen and still expose status, capabilities, surfaces, actions, resources, operations, and streams through `GET /api/v2/modules/{component_id}`.

The Android Tools page consumes `GET /api/v2/modules` for its live module list. `GET /api/v2/shell/home` remains the source for Signals and shell status summary.

## Base Open-Source Boundary

The open-source line includes the Watcher base, not only agent modules. Base code should stay useful without any single module enabled.

Base responsibilities:

- shell status, diagnostics, component registry, module descriptors, and operation lifecycle
- relay forwarding and device entry for shell/module APIs
- Android shell navigation, settings/system surfaces, diagnostics, update flow, and local caches
- component manifests, module templates, smoke commands, and authoring docs

Module responsibilities stay inside `modules/<module>` docs, service handlers, store projections, and Android bespoke screens when needed. Security posture is documented at the base level, while HTTPS/public-port hardening can evolve independently from business modules.

## Manifest Fields

`modules/<module>/component.json` remains the registration file. Shell Contract v2 adds these fields:

```json
{
  "capabilities": ["interactive_session", "pending_input", "operation"],
  "surfaces": [
    {
      "id": "sessions",
      "title": "Sessions",
      "kind": "collection",
      "primary": true,
      "target": {
        "component_id": "example",
        "surface": "sessions"
      }
    }
  ],
  "default_target": {
    "component_id": "example",
    "surface": "sessions"
  },
  "actions": [
    {
      "action_id": "turn.start",
      "label": "Start turn",
      "kind": "submit",
      "operation_name": "turn.start",
      "async": true,
      "target": {
        "component_id": "example",
        "surface": "session"
      }
    }
  ]
}
```

### Capabilities

Capabilities are composable strings. They are not a closed enum yet.

Current examples:

- `feed`
- `dataset`
- `interactive_session`
- `conversation_snapshot`
- `pending_input`
- `permission_flow`
- `artifact`
- `operation`
- `worker_runtime`
- `diagnostics`
- `health`

A module can expose multiple capabilities. Do not create module types like `agent_module` or `dataset_module` until at least two independent modules need the same stronger abstraction.

### Surfaces

Surface IDs are stable navigation targets. `surface.kind` tells generic clients how far they can render without module-specific code.

Recommended kinds:

- `feed`
- `collection`
- `conversation`
- `operation`
- `operation_list`
- `resource`
- `artifact`
- `pending_input`
- `diagnostics`
- `settings`

If a surface needs a bespoke screen, it can still declare a known kind for fallback rendering.

### Actions

Actions describe owner-triggered commands. They do not replace module APIs.

Rules:

- write actions should map to async operations unless the result is trivial and immediate
- destructive actions must declare `destructive=true`
- confirmation-worthy actions must declare `requires_confirmation=true`
- action IDs should match the operation name when there is a one-to-one operation

## Module Registry API

`GET /api/v2/modules` returns module descriptors derived from component manifests and runtime status.

`GET /api/v2/modules/{component_id}` returns one descriptor with the same shape.

The response is for clients. It is not a raw component registry dump.

```json
{
  "shell_contract": "v2",
  "modules": [
    {
      "component_id": "opencode",
      "name": "Opencode",
      "status": "ready",
      "capabilities": ["interactive_session"],
      "surfaces": [],
      "default_target": {
        "component_id": "opencode",
        "surface": "sessions"
      },
      "actions": []
    }
  ]
}
```

Relay forwards the same route so Android can consume it through the existing device-auth path.

## Shell Home

`GET /api/v2/shell/home` is a curated mobile home summary, not the authoritative module registry.

Current behavior:

- component cells are generated from discovered manifests
- `default_target` decides the entry target
- `android_surfaces` is the temporary visibility gate for current Android pages
- shell-local tools such as the bundled block game may remain outside the module registry

The long-term module list and capability discovery live in `GET /api/v2/modules`.

## Opencodev2 Positioning

`opencodev2` is the first serious reference module for this contract.

Its intended shape:

- upstream opencode native `ses_*` sessions remain the continuity source
- Watcher supplies owner auth, relay forwarding, operation lifecycle, Android presentation, cache, and diagnostics
- module state is projected into session list, snapshot, pulse, pending input, actions, artifacts, and operations
- Android renders typed presentation state rather than parsing assistant prose
- opencode first implementation is archive/reference only

The module should use opencode's open-source server/API behavior where possible. Watcher should adapt that behavior into a healthy shell contract, not fork opencode semantics into hidden Android-only assumptions.

The first concrete pattern from opencodev2 is cache-first conversation IO:

- module service owns upstream runtime sync and event reduction
- clients receive cached snapshots for first render
- clients poll typed pulses with stable cursors
- background repair sync is visible through `sync` metadata, not represented as
  a blocking loading state
- Android may keep local cache for offline/slow-link recovery, but the service
  remains the authoritative projection boundary

## Archived Reference Modules

Codex-v2 is intentionally not implemented in this phase.

The existing `codex`, `pilot`, and `cc` modules are archived reference modules.
They stay in the repository for lessons, tests, and migration context, but they
are not public-mainline modules.

Archived modules:

- use `stage=archived` and `release_channel=archived`
- keep docs and historical API descriptions
- do not expose default Android surfaces
- do not expose manifest actions as recommended owner commands
- do not start worker runtimes by default

If Codex-v2 or another advanced agent lane is rewritten later, it should reuse
the current module registry, action model, operation lifecycle, and service-owned
conversation presentation contract instead of copying old Codex/Pilot/CC
Android/service coupling.

## Construction Plan

### P0: Contract Baseline

- document module contract
- add manifest fields for capabilities, surfaces, default targets, and actions
- add service `GET /api/v2/modules`
- forward the route through relay
- keep Codex-v2 out of implementation

### P1: Opencodev2 As Reference

- keep upstream opencode as source of truth
- make snapshot/pulse/message/abort/pending-input flows fully typed
- keep Android cache keyed by native session and turn identity
- reduce Android knowledge to common conversation presentation plus opencode-specific affordances

### P2: Generic Presentation

- extract common `conversation` rendering from opencodev2 only after behavior is stable
- allow future modules to opt into feed, collection, operation list, artifact, and conversation renderers
- keep bespoke screens possible for high-value module workflows

### P3: Open-Source Line

- remove private defaults from examples
- label archived modules and reference implementations clearly
- document setup, smoke tests, security posture, and release gates
- avoid assuming private model aliases, hostnames, local paths, or tokens
