# Opencode Agent Component

> Status: Implemented Prototype
> Version: 0.6
> Scope: `opencode` component design, not shell redesign
> Product line: Watcher main coding-agent component, replacing the `cc` advanced lane after validation
> Landing: Gate 1 backend prototype and Android foreground surface are implemented behind owner-auth API routes

---

## 1. Positioning

`opencode` is a Watcher component.

It is the main coding-agent component for cross-device coding turns. It replaces
the `cc` Claude Code + MiMo lane as the default advanced code-execution path
once it passes the gates in this document.

It is not:

- the Watcher product identity
- a shell transport
- a generic Agent platform
- an Android home chat surface
- a replacement for `codex` native thread workflows
- a reason to move component business logic into shell

Watcher remains:

```text
watcher = shell + components
```

`opencode` must stay inside that model.

## 2. Product Gate

This design must follow [Product Tone](../foundation/PRODUCT_TONE.md).

The product sentence for this component is:

> `opencode` is Watcher's main coding-agent component for cross-device coding turns.

The component must preserve these constraints:

- Android entry is under `Tools`.
- ShellHome may show only short `Signals`, such as running turn, pending permission, failed turn, or pending worktree.
- It must not make the home screen a chat interface.
- It must not expose destructive or full-access actions as default shell actions.
- It must report typed state through resources, operations, and events, not through natural-language parsing.
- It must use shell owner auth, `component_operations`, typed event bus, relay sync, and component manifest.

## 3. Module Boundary

### 3.1 Shell Owns

The shell owns shared platform concerns:

- owner auth
- component registry and manifest validation
- `component_operations`
- typed `EventEnvelope`
- relay sync and push delivery
- Android shell home shape
- shell diagnostics
- component runtime visibility

The shell must not know opencode-specific business rules such as native session
ids, permission request payloads, worktree retention policy, or driver protocol.

### 3.2 Opencode Component Owns

The component owns opencode-specific domain state:

- session
- turn
- event log
- permission request
- worktree
- patch/worktree disposition
- driver diagnostics
- opencode-specific failure model

Implementation may live in `service/cmd/watcher-service` and
`internal/opencode`, but the product boundary is the `opencode` component.

Native opencode database access belongs in `internal/opencode`. Service handlers
may call a typed adapter, but should not embed opencode SQLite queries directly.

### 3.3 Driver Layer Is Internal

`AgentDriver` is an internal anti-corruption layer inside `internal/opencode`.

It is not a shell extension point and not a public plugin system.

Allowed driver implementations:

- `server_adapter`: primary path. Watcher starts or attaches to `opencode serve`,
  uses opencode's HTTP API for session/message/command/cancel, and consumes SSE
  events as the live runtime stream.
- `cli_adapter`: fallback path. Watcher invokes `opencode run --format json`
  for diagnostics, compatibility, or emergency use.
- library bridge: optional future path only after the server adapter proves too
  limited for a required mobile workflow.

The driver interface exists so the component can survive opencode version
changes. It should not be advertised as "Watcher supports any coding agent".
If a future second coding-agent component becomes real, it should get its own
design and component boundary.

### 3.4 Runtime Model Catalog

Mobile runtime options must show canonical models, not every compatibility name
that the local gateway accepts.

The local LiteLLM/opencode gateway may keep old aliases for agent compatibility,
for example `wecode-gpt-5.5` or `openai-gpt-5.4`. Those aliases are runtime
inputs only. They must not become the mobile model picker, Langfuse reporting
standard, or product vocabulary.

Watcher reads `opencode.model_catalog_path` and applies it to runtime
capabilities:

- `canonical=true` models are the mobile-facing choices.
- `display=false` aliases remain callable by old agents but are hidden from the
  picker.
- `include_unlisted=false` means the picker is intentionally curated instead of
  mirroring every provider discovered by opencode.

This keeps the compatibility boundary in the gateway while preserving a small,
readable mobile model list.

### 3.5 Private Runtime Channel

UDS/TCP/stdin/stdout between `watcher-service` and the driver is private runtime
plumbing.

It does not violate the shell "no private transport" rule because it is not a
device, relay, Android, or cross-component protocol. The public product
contract remains:

```text
HTTP v2 API -> component_operations -> typed events -> relay -> Android
```

## 4. Core Scenario

The core scenario is cross-device coding turn handoff.

```text
PC or Android
  -> start opencode session / turn
  -> watcher-service creates operation
  -> driver runs opencode in the allowed project workspace
  -> native opencode session is bound as ses_...
  -> watcher-service persists events
  -> Android/PC can read events after seq
  -> permission requests become typed pending resources
  -> turn completes
  -> direct workspace changes remain in the project repo
```

Important constraints:

- A `session` is a repository context.
- A `turn` is one prompt execution cycle.
- A session has at most one active turn.
- Follow-up while a turn is active is not live chat in Phase 1; it is either rejected or queued as the next turn.
- Live steering is a separate future capability and must not be implied by `turns/start`.
- Work happens in the allowed project workspace.
- PC handoff sessions are imported only when their resolved opencode directory
  is inside `opencode.allowed_repo_roots`. This allowlist is the product
  workspace boundary for both reading native history and starting mobile turns.
- The default dirty policy is `head_only`: preserve current dirty state, track preexisting files, and report paths newly changed by the turn.
- `clean` is an opt-in policy for runs that require a clean working tree.
- Old isolated worktrees are legacy artifacts only; the product should not create new isolated opencode worktrees.

## 5. Component Contract

### 5.1 Manifest

The component must have:

```text
modules/opencode/component.json
```

Recommended manifest shape:

```json
{
  "id": "opencode",
  "name": "Opencode",
  "stage": "prototype",
  "release_line": "component-opencode",
  "release_channel": "public_preview",
  "shell_contract": "v2",
  "component_class": "light",
  "runtime_shape": "in_process",
  "runtime_owner": "watcher-service",
  "streams": [
    "opencode.session",
    "opencode.turn",
    "opencode.permission"
  ],
  "resources": [
    "session",
    "turn",
    "event",
    "permission_request",
    "worktree",
    "operation"
  ],
  "operations": [
    "session.start",
    "turn.start",
    "turn.cancel",
    "question.reply",
    "question.reject",
    "permission.resolve",
    "mirror.message",
    "mirror.abort",
    "worktree.discard"
  ],
  "android_surfaces": [
    "opencode_sessions",
    "opencode_session"
  ],
  "shell_dependencies": [
    "owner_auth",
    "event_bus",
    "operation_contract",
    "app_sync",
    "diagnostics"
  ],
  "docs": [
    "modules/opencode/README.md",
    "docs/modules/OPENCODE_AGENT.md"
  ],
  "non_goals": [
    "shell_transport",
    "generic_agent_platform",
    "android_home_chat",
    "unattended_file_mutation"
  ]
}
```

### 5.2 Streams

Recommended streams:

- `opencode.session`
  - `session.created`
  - `session.updated`
  - `session.closed`
- `opencode.turn`
  - `turn.accepted`
  - `turn.started`
  - `turn.event`
  - `turn.completed`
  - `turn.failed`
  - `turn.interrupted`
  - `workspace.ready`
  - `worktree.discarded`
- `opencode.permission`
  - `permission.requested`
  - `permission.granted`
  - `permission.denied`
  - `permission.expired`

App state must depend on typed fields, not text summaries.

### 5.3 Operation Truth

Operation lifecycle truth is `component_operations`.

`opencode_turns.status` is component-local detail and must not become a parallel
operation state machine.

Rules:

- Every write path creates or updates one `component_operations` row.
- `operation_id` is stored on `opencode_turns`.
- terminal operation states are `completed`, `failed`, or `interrupted`.
- finalize must be idempotent.
- reconcile must never leave stale `running` operations after service restart.

## 6. State Model

### 6.1 Tables

Component-owned tables:

```sql
CREATE TABLE opencode_sessions (
    session_id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    repo_root TEXT NOT NULL,
    native_session_id TEXT,
    status TEXT NOT NULL,
    active_turn_id TEXT,
    driver TEXT NOT NULL,
    config_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE opencode_turns (
    turn_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES opencode_sessions(session_id),
    operation_id TEXT NOT NULL,
    prompt TEXT NOT NULL,
    status TEXT NOT NULL,
    worktree_root TEXT,
    base_commit TEXT,
    dirty_policy TEXT NOT NULL,
    driver TEXT NOT NULL,
    driver_run_id TEXT,
    started_at TEXT,
    completed_at TEXT,
    error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE opencode_events (
    event_id INTEGER PRIMARY KEY AUTOINCREMENT,
    turn_id TEXT NOT NULL REFERENCES opencode_turns(turn_id),
    seq INTEGER NOT NULL,
    kind TEXT NOT NULL,
    source TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    UNIQUE(turn_id, seq)
);

CREATE TABLE opencode_permission_requests (
    request_id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL REFERENCES opencode_turns(turn_id),
    operation_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    resource_json TEXT NOT NULL,
    status TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    responded_at TEXT,
    response_json TEXT
);

CREATE INDEX idx_opencode_turns_session_created
    ON opencode_turns(session_id, created_at DESC);

CREATE INDEX idx_opencode_events_turn_seq
    ON opencode_events(turn_id, seq ASC);

CREATE INDEX idx_opencode_permissions_turn_status
    ON opencode_permission_requests(turn_id, status);

CREATE UNIQUE INDEX idx_opencode_sessions_native_unique
    ON opencode_sessions(native_session_id)
    WHERE native_session_id IS NOT NULL;
```

### 6.2 Event Seq

Watcher assigns `seq`.

The driver may send a driver-local sequence, timestamp, or event id, but those
fields stay inside `payload_json`. Mobile clients consume only Watcher's
`(turn_id, seq)` ordering.

### 6.3 Native Session

`native_session_id` is optional until Gate 0 proves the selected driver can
reliably create and resume opencode native sessions.

Without native session proof:

- session still groups turns by repository and UI context
- follow-up turns may include Watcher-managed recent context
- the component must not claim native opencode continuity

With native session proof:

- the driver returns `native_session_id`
- later turns may resume it
- same-session turns remain serialized
- native sessions imported from opencode's local database are mapped idempotently
- the native id is the continuity source for PC/Android handoff

## 7. Runtime Model

### 7.1 Workspace Policy

Default policy:

- run in a normalized, allowlisted `repo_root`
- use `opencode run --format json --dir <repo_root>`
- use `--session <native_session_id>` when the Watcher session is bound to a native opencode session
- record `workspace.ready`
- record `preexisting_changed_files` before the turn
- report `new_changed_files` after the turn

Dirty policy:

- `head_only` is the mobile default because it can run in an already dirty desktop workspace and still report what was newly touched.
- `clean` rejects a dirty repository before running.

Legacy policy:

- retained worktree endpoints remain for old turns only
- imported native sessions whose directory is under the old opencode worktree root are skipped
- new opencode turns must not create isolated worktrees

### 7.2 Driver Process

The driver process must:

- run in the project workspace
- belong to its own process group
- emit structured events to Watcher
- accept cancel and permission response commands
- terminate when Watcher cancels or when its private runtime channel is closed

Service restart policy:

- do not attempt to reconnect to old driver processes in the first product version
- mark active operations `interrupted`
- collect best-effort diagnostics

Long-running driver survival across service restart requires a separate
supervisor design. It is out of scope for this component entry.

### 7.3 Runtime Channel

Preferred channel for the selected driver:

- localhost TCP to `opencode serve` for `server_adapter`
- stdin/stdout process capture for `cli_adapter`
- Unix domain socket only if Watcher later owns a custom opencode bridge

The channel carries:

- driver event
- permission request
- permission response
- cancel
- heartbeat
- final result

The channel must be framed and versioned. JSON Lines is acceptable for the first
implementation if the schema is fixed in Gate 0.

### 7.4 Driver Options

Gate 0 chooses one:

1. `OpencodeLibDriver`
   - use opencode as a library or internal API if stable enough
   - preferred only if API boundaries are real
2. `OpencodePatchDriver`
   - small patch to opencode I/O and permission boundary
   - acceptable if patch is narrow and verifiable
3. `OpencodeCLIAdapter`
   - use `opencode run --format json --dir ...`
   - fallback and diagnostic path, not the preferred product path

No option is considered selected until the Gate 0 proof passes.

## 8. API Surface

All endpoints use owner auth and the existing `/api/v2/modules` shape.

Gate 1 implements:

```text
GET    /api/v2/modules/opencode/sessions
POST   /api/v2/modules/opencode/sessions/start
GET    /api/v2/modules/opencode/sessions/{session_id}
GET    /api/v2/modules/opencode/sessions/{session_id}/native-history

GET    /api/v2/modules/opencode/sessions/{session_id}/turns
POST   /api/v2/modules/opencode/sessions/{session_id}/turns/start
GET    /api/v2/modules/opencode/sessions/{session_id}/turns/{turn_id}
GET    /api/v2/modules/opencode/sessions/{session_id}/turns/{turn_id}/events?after_seq=&limit=

POST   /api/v2/modules/opencode/turns/{turn_id}/cancel

GET    /api/v2/modules/opencode/turns/{turn_id}/permissions
POST   /api/v2/modules/opencode/permissions/{request_id}/resolve

GET    /api/v2/modules/opencode/turns/{turn_id}/worktree
POST   /api/v2/modules/opencode/turns/{turn_id}/worktree/discard

POST   /api/v2/modules/opencode/sessions/sync-native

GET    /api/v2/modules/opencode/operations/{operation_id}
```

Direct native mirror endpoints are part of the Android handoff surface, not a
second chat component:

```text
GET    /api/v2/modules/opencode-mirror/sessions
POST   /api/v2/modules/opencode-mirror/sessions
GET    /api/v2/modules/opencode-mirror/sessions/{native_session_id}/snapshot
GET    /api/v2/modules/opencode-mirror/sessions/{native_session_id}/runtime-capabilities
GET    /api/v2/modules/opencode-mirror/sessions/{native_session_id}/pulse
POST   /api/v2/modules/opencode-mirror/sessions/{native_session_id}/messages
POST   /api/v2/modules/opencode-mirror/sessions/{native_session_id}/abort
```

Mirror runtime capabilities are discovered from opencode server providers,
agents, and commands. Mirror normal-message submission calls opencode's
`/session/{id}/prompt_async`; slash-command submission calls
`/session/{id}/command`. Both are tracked as `mirror.message`. Mirror
cancellation is tracked as `mirror.abort`. Write paths return component
operations so Android can show typed progress and diagnostics.

Mirror read paths are intentionally cache-first. Session list and session
snapshot return local cache immediately, then trigger background native sync
unless clients pass `sync=0`. Pulse returns events after `after_seq` and the
cached messages touched by those events; the initial `after_seq=0` pulse returns
the current message window. This keeps Android responsive on public relay links
and makes opencode server/API latency a background repair concern instead of a
first-render blocker.

The mirror projection follows opencode's upstream event reducer behavior:

- `message.updated` upserts message info
- `message.part.updated` upserts a part
- `message.part.delta` appends streamed text to an existing part
- `message.part.removed` removes a part from the cached message
- `session.status` updates typed session status
- `question.*` and `permission.*` feed pending-input presentation

`snapshot` and `pulse` expose the resulting conversation projection as typed
rows: turn metadata, timeline items, pending input lists, and latest/active
flags. Android should consume that projection first and only fall back to local
message/event projection for stale cache or older service compatibility.

Android consumes this typed projection and should not infer state from assistant
prose.

Worktree endpoints are legacy-compatible inspection/discard endpoints. New direct-workspace turns return an empty worktree root.

Future session edit/delete endpoints require a separate retention and active-turn
policy. They are not part of the Gate 1 contract.

`turns/start` is async. It returns `202 Accepted` with:

```json
{
  "operation_id": "op_...",
  "turn_id": "octurn_...",
  "session_id": "ocsess_..."
}
```

## 9. Android Surface

Android entry:

```text
Tools -> Opencode
```

Screens:

- session list
- session detail
- turn event log
- permission request panel
- worktree summary / discard

ShellHome rules:

- show active turn as one short signal
- show pending permission as action signal
- show failed/interrupted turn as warning signal
- do not stream agent text on shell home
- do not expose full prompt composer on shell home

## 10. Security

### 10.1 Repo Root

`repo_root` must be normalized and constrained:

- absolute path
- `EvalSymlinks`
- inside shell repo root or explicit allowlist
- not inside `.git`
- not inside known secret directories

### 10.2 Permissions

Permission requests are typed resources.

They must include:

- request id
- turn id
- operation id
- requested action
- resource or command
- risk summary
- expiry
- allowed response choices

Resolution must support at least:

- grant once
- deny
- expire

Session-wide grants are future work and require separate policy.

### 10.3 Secret Redaction

Events, logs, operation results, and diagnostics must redact:

- owner token
- MiMo/LiteLLM/API keys
- common `*_TOKEN`
- common `*_KEY`
- auth headers

## 11. Implementation Gates

The gates are entry requirements, not estimates.

### Gate 0: Product And Driver Proof

Must pass before product implementation.

Deliverables:

- Product Gate answers from [Product Tone](../foundation/PRODUCT_TONE.md)
- driver option decision record
- minimal runnable driver prototype under `lab/opencode-driver/`
- fixed event schema
- fixed runtime channel schema
- fake repo test harness
- opencode version and source/API inspection notes

Pass criteria:

- selected driver runs a fake repo turn
- Watcher receives 1000 events with no loss
- Watcher assigns continuous seq values
- permission request pauses driver and resumes after response
- cancel terminates the driver process group
- service kill leads to driver exit or deterministic interruption within 5 seconds
- native session support is either proven or explicitly deferred

### Gate 1: Backend Component Closed Loop

Deliverables:

- `modules/opencode/component.json`
- `internal/opencode` store and runtime package
- SQLite migration
- HTTP handlers
- `component_operations` integration
- typed event publishing
- reconcile on service startup

Pass criteria:

- session and turn CRUD work
- `turns/start` creates component operation
- events persist and can be queried by `after_seq`
- one active turn per session is enforced
- cancel works
- service restart marks active turn interrupted and keeps worktree
- ShellHome shows short opencode signals

### Gate 2: Cross-Device Handoff

Deliverables:

- Android session list: implemented
- Android session detail: implemented
- event polling: implemented through module event API
- permission resolution UI: implemented for pending typed requests
- relay stream integration: ShellHome signals implemented; direct turn event view uses module polling
- native session import and continuation: implemented through local opencode SQLite sync

Pass criteria:

- PC starts turn, Android sees progress
- Android resolves permission, driver continues
- Android disconnect/reconnect resumes from `after_seq`
- Android can see allowed PC native sessions and continue one through the same `ses_...`
- PC can continue the same native session after Android writes a turn
- no Android screen depends on service internal structs

### Gate 3: Stabilization And CC Demotion

Deliverables:

- driver diagnostics
- compatibility notes
- smoke test script
- legacy worktree cleanup policy
- migration note from `cc`

Pass criteria:

- one week stable private use
- failed turns are diagnosable without reading raw logs
- `cc` is archived as reference and hidden from the default Android tool surface
- opencode component does not require shell contract changes

## 12. Dual-Endpoint Session Handoff

Watcher session management is only the product wrapper. The continuity source is the native opencode `ses_...` session.

Rules:

- Android session lists sync native opencode sessions from opencode's local
  metadata store. Session discovery must not call opencode repository search
  endpoints such as `/find`; broad allowlists can otherwise turn into expensive
  full-tree `rg` scans.
- New sessions must send an explicit `repo_root` selected on mobile or typed
  manually, and that path must resolve inside `opencode.allowed_repo_roots`.
- A PC native session under `<owner-home>` requires `<owner-home>` in
  `opencode.allowed_repo_roots`; a repo-only allowlist such as
  `<watcher-workspace>` intentionally hides broader home/global sessions.
- Native history is a read-only projection from opencode's local database:
  user/assistant text and lightweight stats are visible; reasoning/process parts
  remain hidden by default.
- Imported sessions are mapped idempotently by `native_session_id`; duplicate Watcher wrappers are not allowed.
- Android turns on an imported session must run `opencode run --session <native_session_id>`.
- PC can continue the same conversation by continuing that native opencode session.
- Legacy isolated worktree sessions are skipped during import.
- A Watcher turn must take both the Watcher session lock and the native session lock before starting.
- Starting a turn is rejected if another Watcher operation already owns the same native session.
- Starting a turn is also rejected when a local opencode process or unfinished assistant message indicates that the native session is already active.

This is not ownership transfer. Phone and PC are peers writing to one native session; locks only serialize writes.

## 13. Risks

### Opencode Internal Churn

Open source does not mean stable internal APIs.

Mitigation:

- keep driver boundary small
- avoid core AI logic patches
- keep CLI fallback for diagnostics
- verify compatibility before upgrading

### Component Scope Creep

`AgentDriver` could grow into a generic Agent platform.

Mitigation:

- keep it internal to `opencode`
- do not expose driver selection as product surface until there is a second real component
- keep docs and manifest named `opencode`

### Mobile Control Surface Creep

The Android UI could become a full control console.

Mitigation:

- `Tools` entry only
- ShellHome short signals only
- destructive actions behind component page and typed confirmations

### Permission Race Conditions

Async permissions can race with cancel, timeout, or service restart.

Mitigation:

- permission requests expire
- terminal turns close pending permissions
- resolve endpoint checks operation state
- driver treats missing response as denial on shutdown

## 14. Non-Goals

- generic coding-agent marketplace
- full shell terminal streaming
- unattended mutation of the original repo
- isolated opencode worktrees for new turns
- Android home chat
- relay-side opencode business logic
- cross-service-restart live driver reconnection
- session-wide permission grants in the first version

## 15. Acceptance Summary

The design is ready to implement only when these are true:

1. `opencode` is registered as a component, not a shell feature.
2. `AgentDriver` remains an internal component boundary.
3. public state uses component resources, `component_operations`, and typed events.
4. service restart has deterministic interruption behavior.
5. Android remains a terminal surface: sessions, turns, permissions, and legacy worktree inspection.
6. `cc` has a clear demotion path once opencode is stable.

Until then, this document is a design entry, not an implementation mandate.
