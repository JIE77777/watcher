# CC MiMo Module

> Status: archived reference
> Public mainline: no

`cc` / CC MiMo is archived as the managed Claude Code + MiMo advanced lane. It
is no longer part of the public mainline, is not started by the base runtime,
and should not appear as a default Android tool entry.

Keep it only as reference for worker-lane orchestration, patch artifacts,
timeout handling, and mobile patch review. `opencodev2` is the current coding
agent reference module; future advanced lanes should reuse its typed
conversation projection and operation model instead of extending this module.

## Historical Context

## Identity

- **Component**: `cc`
- **Goal**: Managed Claude Code + MiMo session lane for deliberate system evolution via mobile
- **Stage**: archived

`cc` is Watcher's managed Claude Code + MiMo session lane.

Historically, it was separate from:

- `pilot`: lightweight shell briefs and advisory MiMo chat
- `codex`: Watcher's Codex app-server runtime, also now archived

## Responsibility

### Does

- Manage Claude Code sessions via `claude-mimo` wrapper against MiMo endpoint
- Run one non-interactive `stream-json` coding turn per operation
- Isolate each turn in a detached git worktree; produce patch artifacts
- Stream progress events (tool use, assistant text, completion) to the event bus
- Rotate Claude session IDs on timeout/already-in-use errors
- Provide session CRUD and patch apply/discard for Android

### Does Not

- Replace the default `codex` module runtime
- Store MiMo API keys or manage provider secrets
- Perform unattended file mutations (patches require explicit apply)
- Run as the default coding agent for the shell

## Shell Dependencies

- `owner_auth` — all API endpoints require owner token
- `event_bus` — publishes `cc.session` stream events
- `operation_contract` — uses `ComponentOperation` lifecycle
- `diagnostics` — records shell diagnostic events
- `worker_lane` — runs as a heavy worker via `workers.Manager`

Does not bypass: relay sync, device registration, app release, shared storage primitives.

## API Surface

| Method | Path | Async | Description |
|--------|------|-------|-------------|
| GET | `/api/v2/modules/cc/sessions` | no | List sessions (default limit 40) |
| POST | `/api/v2/modules/cc/sessions/start` | no | Create session |
| GET | `/api/v2/modules/cc/sessions/{sessionID}` | no | Get session |
| POST | `/api/v2/modules/cc/sessions/{sessionID}/turns/start` | yes | Start coding turn |
| POST | `/api/v2/modules/cc/sessions/{sessionID}/cancel` | no | Cancel active operation |
| POST | `/api/v2/modules/cc/sessions/{sessionID}/clear` | no | Reset session messages |
| DELETE | `/api/v2/modules/cc/sessions/{sessionID}` | no | Delete session |
| PATCH | `/api/v2/modules/cc/sessions/{sessionID}` | no | Update title/model/cwd |
| GET | `/api/v2/modules/cc/operations/{operationID}` | no | Get operation status |
| GET | `/api/v2/modules/cc/operations/{operationID}/patch` | no | Get patch artifact + preview |
| POST | `/api/v2/modules/cc/operations/{operationID}/patch/apply` | no | Apply patch via git apply |
| POST | `/api/v2/modules/cc/operations/{operationID}/patch/discard` | no | Discard patch + cleanup worktree |

Write path (`turns/start`) is async: returns `accepted` with operation ID, progress via events.

## Event Surface

**Stream**: `cc.session`

| Kind | When | Key Payload Fields |
|------|------|--------------------|
| `turn.started` | Claude Code process init | `claude_session_id`, `model`, `tools` |
| `tool.started` | Claude Code tool use begins | `tool.name`, `tool.input_summary` |
| `tool.finished` | Claude Code tool result returns | `tool_result.is_error`, `tool_result.content_summary` |
| `assistant.text` | Claude Code generates text | `text` |
| `worktree.ready` | Worktree created and baseline committed | `worktree_root`, `baseline_commit` |
| `patch.created` | Turn completed with code changes | `patch.changed_files`, `patch.diff_stat` |
| `patch.empty` | Turn completed with no changes | `patch.status` |
| `patch.applied` | User applied patch | `patch.applied_at` |
| `patch.discarded` | User discarded patch | `patch.discarded_at` |
| `turn.completed` | Turn finished successfully | `message`, `git`, `patch` |
| `turn.failed` | Turn finished with error | `error` |
| `turn.timeout` | Turn exceeded timeout | `timeout_seconds` |
| `turn.interrupted` | Turn canceled or worker crashed | `error` |

App relies on: `turn.started` for status display, `tool.started`/`tool.finished` for progress, `patch.created`/`patch.empty` for patch panel, `turn.completed`/`turn.failed` for final state.

## State Ownership

### Component-owned (long-term)

- Session metadata: `state/cc_mimo_sessions/{sessionID}.json` — session ID, title, cwd, model, status, messages, claude session ID
- Patch artifacts: `state/cc_mimo_patches/{operationID}/artifact.json` — patch path, changed files, diff stat, apply/discard timestamps
- Worktrees: `state/cc_mimo_worktrees/{operationID}/` — isolated git worktree per operation

### Shell-owned (overlay)

- Operation state: `component_operations` table in SQLite — status, result, timing
- Event envelopes: relay event bus — `cc.session` stream

### External truth

- Claude Code's own session persistence (separate from Watcher's parallel session file)
- Git repository state (worktree is a detached copy; main workspace unchanged until patch applied)

## Runtime Ownership

- **Runtime holder**: `workers.Manager` in `watcher-service`
- **Scheduler**: Worker loop with 2s backoff on crash; health ping every 15s
- **Broker**: stdin/stdout JSON line protocol between shell and worker process
- **Retry**: Worker auto-respawns on crash; in-flight operations marked `interrupted`
- **Cancellation**: Shell sends `operation.cancel` → worker adds to cancelled set → process group killed on next poll
- **Timeout**: Turn timeout enforced worker-side (900–1800s); process killed on expiry

## Android Surface

- **Entry**: `MainActivity` → Shell Home → CC MiMo cell → `CcMimoSessionsActivity`
- **Screens**:
  - `CcMimoSessionsActivity` — session list with status indicators
  - `CcMimoSessionActivity` — message thread, prompt composer, patch panel
- **Interaction style**:
  - Text input + Send for turn prompts
  - Patch panel with Apply/Discard buttons (visible only when patch exists)
  - Manual refresh button (no auto-polling during idle)
  - 1s poll loop during active turn (up to 1200 iterations = 20 min)
- **Data flow**: HTTP via relay proxy → service; events via cursor-based polling

## Failure Model

| Failure Type | Cause | App Rendering |
|-------------|-------|---------------|
| Session not found | Invalid session ID | Error toast, finish activity |
| Active operation conflict | Turn already running | Status text: "session already has active operation" |
| Worker unavailable | Worker process not started | HTTP 503, status text: "worker manager is not available" |
| Turn timeout | Claude Code exceeded 900–1800s | Status: "turn timed out"; patch may still exist |
| Turn interrupted | User canceled or worker crashed | Status: "turn interrupted" |
| Claude session rotation | Timeout or already-in-use | Transparent; next turn uses fresh UUID |
| Patch apply failed | git apply conflict | Error text in patch panel; artifact saved with last_error |
| Empty response | Claude Code returned no text | Status: "empty Claude Code MiMo response" |
| Worktree creation failed | Not a git repo / git error | Turn fails with descriptive error |
| MiMo API key missing | claude-mimo.env not configured | Claude Code process exits with error; turn fails |

## Default Guardrails

- Default `permission_mode` is `bypassPermissions`.
- Default tools are `default`, which lets Claude Code expose its normal coding
  tool set while Watcher keeps operation state, diagnostics, and worker
  lifecycle ownership.
- The worker appends Watcher-specific guidance with
  `--append-system-prompt`; it does not replace Claude Code's native coding
  agent prompt.
- Default turn timeout is 15 minutes. Phone-originated 5 minute defaults are
  upgraded service-side because real code editing turns can legitimately exceed
  300 seconds.
- If a turn times out or leaves Claude Code reporting `session id already in
  use`, the next turn rotates to a fresh Claude session and carries only
  Watcher's recent clean mobile history forward. This prevents one half-finished
  tool chain from poisoning the next message.
- Failed or timed-out Claude Code session IDs are treated as unhealthy. The
  next turn automatically rotates to a new Claude Code session ID while keeping
  recent Watcher mobile history in the prompt for continuity.
- Mobile turns are serialized per session.
- Each turn records a conservative git status audit before and after execution.
- The worker reads MiMo credentials through `<watcher-config-dir>/claude-mimo.env`
  via the existing `claude-mimo` wrapper and must not log secrets.
- CC MiMo is a full-access advanced lane for deliberate system evolution, not
  the default Codex runtime.

## Non-Goals

- Default Codex runtime
- Unattended file mutation
- Provider secret storage

## Runtime Notes

Claude Code itself persists its own conversation under its normal local state.
Watcher persists a parallel mobile-friendly session file under service state so
Android can render the conversation, active operation, cwd, permissions,
workflow, patch state, and last error without parsing Claude Code internals.

## Direction

Directly attaching Watcher to Claude Code's interactive terminal would be
fragile. The durable path is a protocol bridge:

- Claude Code remains the native coding worker.
- Watcher owns session metadata, operation state, relay events, Android UI, and
  diagnostics.
- `stream-json` is the bridge boundary for progress, tool calls, tool results,
  final result, timeout, and cancellation.
- The default workflow is `worktree_patch`: each turn runs in a dedicated
  detached git worktree that mirrors the current workspace, commits that mirror
  as a private baseline, captures only the new turn diff as a patch artifact,
  and waits for an explicit apply/discard action.
