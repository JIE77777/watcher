# Pilot Module

> Status: archived reference
> Public mainline: no

`pilot` is archived as an advisory shell-assistant experiment. It is not part of
the public mainline, is not started by the base runtime, and should not be used
as the template for future non-agent modules.

The useful reference material is limited to shell-state capsules, deterministic
fallback behavior, and worker-lane operation/event plumbing. Future assistant or
recommendation modules should be designed against the current module contract
instead of extending this prototype directly.

## Historical Context

`pilot` is Watcher's shell-level semantic assistant component.

## Identity

- `component`: `pilot`
- `goal`: turn shell/component state into concise explanations, suggestions,
  and operator-facing artifacts
- `stage`: archived

## Responsibility

The component owns lightweight semantic work around Watcher itself:

- summarize current shell/component state
- explain likely stuck states
- produce next-action suggestions
- prepare artifacts that can later be handed to Codex or another coding agent

It does not own:

- Codex runtime execution
- automatic file edits
- approval resolution
- relay transport
- Android sync protocol

## Current Landing

The first operation is:

- `brief.create`

It accepts a Watcher state capsule from `watcher-service` and produces a
structured `pilot.suggestion` event plus a component operation result.

Provider behavior:

- `deterministic`: local summary with no LLM call
- `mimo`: call MiMo through the local Token Plan Anthropic-compatible config
- `auto`: use MiMo when a local token is configured, otherwise deterministic
- Brief calls use `CLAUDE_MIMO_BRIEF_MODEL` / `MIMO_BRIEF_MODEL`, defaulting
  to `mimo-v2.5`.
- Interactive Pilot chat turns use `CLAUDE_MIMO_MODEL`, defaulting to
  `mimo-v2.5-pro`, and stay inside Pilot sessions rather than Codex threads.

Runtime behavior:

- The worker process is long-lived while `watcher-service` is running, so normal
  brief requests do not respawn Python.
- Each brief is a stateless MiMo API call, not a persistent LLM conversation.
- Pilot chat sessions persist their own history under service state and each
  `chat.turn` call sends recent session history plus a fresh shell capsule.
- Brief and chat operations are serialized in-process to avoid overlapping MiMo
  calls from mobile double-taps or retries.
- Continuous free-form shell scheduling belongs in a Pilot chat session.
  Codex threads remain for Codex runtime/code-agent work.

## Event Surface

- stream: `pilot.suggestion`
- kind: `brief.ready`

## API Surface

- `POST /api/v2/modules/pilot/briefs/start`
- `GET /api/v2/modules/pilot/operations/{operationID}`
- `POST /api/v2/modules/pilot/chat/sessions/start`
- `GET /api/v2/modules/pilot/chat/sessions/{sessionID}`
- `POST /api/v2/modules/pilot/chat/sessions/{sessionID}/turns/start`

## Guardrails

- Pilot reads shell-provided capsules rather than crawling the system on its own.
- Pilot suggestions are advisory artifacts, not direct execution.
- Missing LLM credentials must degrade to deterministic output.
- Brief output should be Simplified Chinese for operator-facing summaries.
- Current component state has priority over historical diagnostics; manual
  restarts and worker-started logs are treated as recovery/operator context.
- Secrets must not be emitted in logs, events, or operation results.
