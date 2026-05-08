# Opencode Component

`opencode` is Watcher's main coding-agent component for cross-device coding
turns.

## Identity

- `component`: `opencode`
- `goal`: run opencode-backed coding turns through Watcher's operation and event model
- `stage`: prototype

## Responsibility

Does:

- manage opencode sessions, turns, events, permission requests, native session binding, and legacy worktree inspection
- run one active coding turn per session
- run in an allowlisted project workspace
- sync native opencode sessions from opencode metadata for PC/Android handoff
  when the native session directory resolves inside `opencode.allowed_repo_roots`
- expose typed progress through `opencode.*` event streams
- use `component_operations` for operation lifecycle truth

Does not:

- replace the Watcher shell
- define relay, auth, or mobile sync
- provide a generic agent marketplace
- make Android home a chat surface
- create isolated worktrees for new turns
- claim ownership transfer between PC and Android

## Shell Dependencies

- `owner_auth`
- `event_bus`
- `operation_contract`
- `app_sync`
- `diagnostics`

The component must not bypass shell auth, relay sync, typed events, or the
component manifest registry.

Native opencode SQLite access lives in `internal/opencode`; service handlers
should use typed adapter functions rather than owning database query details.
Session discovery must stay metadata-only and must not call repository search
endpoints with an empty pattern.

## API Surface

- `GET /api/v2/modules/opencode/sessions`
- `POST /api/v2/modules/opencode/sessions/start`
- `POST /api/v2/modules/opencode/sessions/sync-native`
- `GET /api/v2/modules/opencode/sessions/{session_id}`
- `GET /api/v2/modules/opencode/sessions/{session_id}/snapshot`
- `GET /api/v2/modules/opencode/sessions/{session_id}/native-history`
- `GET /api/v2/modules/opencode/sessions/{session_id}/turns`
- `POST /api/v2/modules/opencode/sessions/{session_id}/turns/start`
- `GET /api/v2/modules/opencode/sessions/{session_id}/turns/{turn_id}`
- `GET /api/v2/modules/opencode/sessions/{session_id}/turns/{turn_id}/events`
- `GET /api/v2/modules/opencode/sessions/{session_id}/turns/{turn_id}/timeline`
- `GET /api/v2/modules/opencode/turns/{turn_id}/pulse`
- `GET /api/v2/modules/opencode/turns/{turn_id}/permissions`
- `GET /api/v2/modules/opencode/turns/{turn_id}/questions`
- `POST /api/v2/modules/opencode/turns/{turn_id}/cancel`
- `POST /api/v2/modules/opencode/permissions/{request_id}/resolve`
- `POST /api/v2/modules/opencode/questions/{request_id}/reply`
- `POST /api/v2/modules/opencode/questions/{request_id}/reject`
- `GET /api/v2/modules/opencode/turns/{turn_id}/worktree`
- `POST /api/v2/modules/opencode/turns/{turn_id}/worktree/discard`
- `GET /api/v2/modules/opencode/operations/{operation_id}`

Native mirror API:

- `GET /api/v2/modules/opencode-mirror/sessions`
- `POST /api/v2/modules/opencode-mirror/sessions`
- `GET /api/v2/modules/opencode-mirror/sessions/{native_session_id}/snapshot`
- `GET /api/v2/modules/opencode-mirror/sessions/{native_session_id}/runtime-capabilities`
- `GET /api/v2/modules/opencode-mirror/sessions/{native_session_id}/pulse`
- `POST /api/v2/modules/opencode-mirror/sessions/{native_session_id}/messages`
- `POST /api/v2/modules/opencode-mirror/sessions/{native_session_id}/abort`

Write paths are async and anchored by `component_operations`. Direct native
mirror writes use `mirror.message` and `mirror.abort` operations so Android can
diagnose submission and cancellation without relying on free-text responses.

Android should use `snapshot` for initial session hydration and `pulse` for
running-turn polling. `native-history` is cache-aware and may return a
`not_modified` cache response when the client cache key is current.

Android may use `opencode-mirror` for direct native `ses_*` sessions. The mirror
surface syncs session status, message history, incremental server events, and
mobile-submitted prompts without creating Watcher turn records.

Mirror read paths are cache-first:

- `sessions` returns the local session list immediately and starts native sync
  in the background unless `sync=0` is passed.
- `snapshot` returns cached session/messages/events immediately, starts the
  event stream, and schedules a background native sync unless `sync=0` is
  passed.
- `pulse` returns events after `after_seq` and only the messages touched by
  those events. With `after_seq=0`, it returns the current message window for
  initial hydration. It also starts the event stream and can schedule background
  repair sync without blocking the client.

Watcher follows opencode's upstream event reducer shape for mirror projection:
`message.updated`, `message.part.updated`, `message.part.delta`,
`message.part.removed`, `session.status`, `question.*`, and `permission.*`
events are normalized into local cache and typed presentation state.

`snapshot` and `pulse` also expose a `conversation` projection. Each row contains
an `OpencodeTurn`, timeline items, pending input lists, and `latest` / `active`
flags. Android should prefer this service-owned projection and keep its local
message/event projector only as a cache or compatibility fallback.

Mirror runtime options are discovered through opencode server endpoints
`/config/providers`, `/agent`, and `/command`. Normal mirror prompts call
opencode's `/session/{id}/prompt_async`; slash-command submissions call
`/session/{id}/command`. This matches the opencode 1.14 server API shape kept in
the project lab reference.

## Event Surface

- `opencode.session`
- `opencode.turn`
- `opencode.permission`

Android must depend on typed fields and operation state, not natural-language
agent output.

## Runtime Ownership

Runtime owner is `watcher-service`.

The driver channel to opencode is component-private runtime plumbing, not a
shell transport or public extension point.

## Android Surface

Entry: `Tools -> Opencode`

Screens:

- session list
- session detail
- turn event log
- permission request panel
- legacy worktree summary

ShellHome only shows short signals for active turns, pending permissions, failed
turns, and legacy retained worktrees.

## Failure Model

- driver unavailable
- turn failed
- turn interrupted
- permission expired
- native session import failed
- opencode compatibility failure

Failures must be diagnosable through typed operation state, events, and shell
diagnostics.

## Smoke Verification

Use the documented local Go path when the shell cannot find `go`:

```bash
export PATH=/path/to/go/bin:$PATH
go test ./service/cmd/watcher-service ./internal/store ./internal/opencode
```

Android build smoke:

```bash
cd <watcher-workspace>/android
./gradlew :app:assembleDebug --no-daemon
```

Mirror API smoke:

```bash
devtools/smoke/opencode_mirror_smoke.sh
```

End-to-end checks before promoting beyond prototype:

- relay forwards `opencode-mirror` read and write paths with device initiator headers
- Android lists native `ses_*` sessions through `opencode-mirror/sessions`
- Android can read mirror runtime capabilities from opencode providers, agents, and commands
- Android submit creates a `mirror.message` operation and server receives `/prompt_async` or `/command`
- Android busy session shows `停止`; abort creates a `mirror.abort` operation
- pulse updates message history and typed status without parsing assistant text

## Docs

- [Opencode Agent Component](../../docs/modules/OPENCODE_AGENT.md)
- [Product Tone](../../docs/foundation/PRODUCT_TONE.md)
- [Component Standard](../../docs/foundation/COMPONENT_STANDARD.md)

## Non-Goals

- generic coding-agent marketplace
- shell transport
- Android home chat
- unattended file mutation
- ownership transfer between devices
