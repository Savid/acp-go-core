# Lifecycle

## Serve

`Serve` creates an `Agent`, attaches it to the supplied JSON-RPC streams,
blocks until context cancellation or connection close, calls `Agent.Close`,
and returns the close error in preference to the loop's result, else
`ctx.Err()` if the context cancelled first, else nil on normal peer close. It
never owns stdin or stdout beyond reads and writes on the supplied streams.

## Native Process Model

- The harness is launched through `process.Start`: resolved executable, merged
  [environment](01-public-api.md#process-environment), session cwd, its own
  process group, and dedicated pipes for stdin, stdout, and stderr. Native
  stdout and stderr never inherit ACP stdout and are routed to logs only after
  JSON-RPC separation is guaranteed. A short-lived native command that is not
  the harness may write its stdout to a caller-owned file instead of a pipe
  (`process.Request.Stdout`).
- Shared writable native state has exactly one writer. A sibling that owns
  shared writable native state holds an exclusive lock on it from before
  seeding until the process exits and is waited on; the
  [registry](registry.md#native-surfaces-and-process-models) records which.
  Logical-session cwd,
  model, permission state, callbacks, cancellation, and persistence remain
  independently routed on a multiplexed runtime.
- Tests inject native stdout and stderr noise and prove ACP stdout stays valid.

Every native event is fenced by the native-process epoch, logical session, and
native turn identity. A native process exit fails each in-flight turn exactly once,
suppresses late events from the dead epoch, and fences every incarnation that
generation carried. Every session stays addressable: the next explicit
operation binds it again on a fresh generation with a fresh incarnation.

### Shared Runtime Loss

A multiplexed sibling treats the loss of its shared runtime as an epoch fence:

- The dead epoch is fenced, every thread bound to it is unbound, and every
  in-flight turn fails exactly once.
- **The next explicit operation starts one replacement.** `session/new`,
  `session/load`, `session/resume`, a prompt on an unbound session, and an
  agent-scoped [account-usage read](02-wire-contract.md#account-usage) each
  admit a fresh runtime generation and rebind through it. A sibling never
  requires an adapter restart to recover from a runtime that exited, and never
  starts a replacement speculatively.
- A replacement that cannot start answers runtime-needing operations with
  `<vendor>_runtime_unavailable` until one can. `session/list` and
  `session/delete` keep working.

### Session Environment and PATH Scoping

Session `env` and ordered `extraPathDirs` are per-session configuration.

- **The addressed session carries them, never the Agent.** A multiplexed
  sibling applies them to that session's own native start or resume request;
  a session runtime applies them to its own process.
- Concurrent sessions carry their own values and never inherit a peer's.
- **Both are part of resume identity.** The session record stores the ordered
  list in order; reordering is a distinct configuration.
- **A change is a resume boundary, not a mutation.** Load and resume carry the
  values the request supplies; live native state is never mutated in place.
- **Recovery reconstructs them** from the session record before launching the
  native process.
- Executable resolution follows
  [01-public-api.md](01-public-api.md#process-options).

## Shutdown Ladder

For every native session, in order:

1. Mark the session closed and stop accepting prompts.
2. Resolve pending permission requests as ACP `cancelled`.
3. Resolve pending elicitation requests as action `cancel` or `-32800`.
4. Send the provider-native interrupt under a bounded background context.
5. Signal the process group and wait for the root process through
   `process.Process.Shutdown`, where the session owns one.
6. Commit the durable state the boundary owes, the same rung a wire
   `session/close` runs; only diagnostics flushing is best-effort.
7. Release scratch directories, stream readers, and goroutines. Native state
   in the home is left in place.

The ladder binds `session/close`, `session/delete`, and `Agent.Close`. For a
multiplexed Agent it runs per logical session, then closes the shared process
exactly once. A logical session close never terminates peers.

## JSON-RPC Request Cancellation

`$/cancel_request` is stable ACP v1 and distinct from `session/cancel`. The
pinned SDK cancels the addressed handler context and sends a best-effort
outbound `$/cancel_request` when an in-flight peer-request context is
cancelled. Handler work derives from the SDK context, and permission,
elicitation, and client requests receive their owning turn context rather than
a detached background context. The original request still completes exactly
once with a valid result or `-32800`.

A prompt's native turn is not cancelled by its own handler context. Only
`session/cancel`, `session/close`, `session/delete`, or incarnation loss
cancels a turn. The pinned SDK cancels the previous prompt's handler context
when a second `session/prompt` arrives for the same session
([watchlist](../tracking/upstream-acp.md)); a refused second prompt MUST NOT
end the first.

## Cancel Determinism

1. `session/cancel` cancels local turn state immediately. It is
   session-scoped: it applies to the addressed session's current turn, if any,
   and is a wire-silent no-op otherwise.
2. Native interrupt uses a bounded background context, not the caller context.
3. Pending permission requests receive outcome `cancelled`; pending
   elicitations receive action `cancel` or `-32800`.
4. Each prompt produces exactly one terminal result.
5. Repeated cancels for the same turn are idempotent.
6. A shared-runtime session cancel uses only its native protocol operation and
   never signals the shared process.

## Lifecycle Stream Fencing

A lifecycle stream belongs to exactly one session incarnation, and ending the
incarnation ends the stream.

- `streamId` does not rotate on a reconnect to the same surviving source and
  does not survive the incarnation.
- Envelope delivery blocks on the host reading the connection. A host that
  stops reading holds every publication behind it, the fence, the terminal
  `idle`, `session/close`, and `Close` included, until it reads or
  disconnects; no timeout stands in for a reading host.
- A validated cancel terminalizes every pending action as `cancelled`, and an
  owned activity only where a structured native event reports it terminal. The
  sibling emits the cancelled cycle's terminal `idle` with outcome `cancelled`
  and admits no further prompt until it has.
- **`session/close` ends the addressed session.** After the ladder's native
  cleanup succeeds, close commits owed state, then terminalizes every
  nonterminal owned activity and action as `cancelled`, emits those updates
  and any open turn's terminal `idle`, fences the stream, and returns. A
  failed commit fails the close with the stream fenced and no terminal `idle`
  ([commit points](03-sessions-and-store.md#lifecycle-commit-points)). A
  failed close still releases the session: its id is detached and no longer
  counts against `active_sessions`.
- **Incarnation loss terminalizes as `failed`, close and cancel as
  `cancelled`.**
- **A fenced stream is terminal.** Later conversation reuse resumes stored
  state into a new incarnation with a new `streamId` and a fresh snapshot. A
  closed session admits no further incarnation of itself.
- **Incarnation loss fences the stream.** A sibling never continues an old
  `streamId` across a new native process and never reconstructs undelivered
  events.
- Ladder resolutions stand on their own: an action resolved `cancelled` at
  steps 2 or 3 stays resolved even when a later rung fails.

## Concurrency Limits

| Limit | Value | Backpressure error |
|---|---:|---|
| Active sessions per agent | 32 (default) | `acp.NewInvalidRequest({"error":"backpressure","limit":"active_sessions"})` |
| Concurrent foreground operations per session: prompts, config changes, and session-scoped account-usage reads | 1 (fixed) | `acp.NewInvalidRequest({"error":"backpressure","limit":"session_prompt"})` |
| Concurrent server-to-client calls per agent | 16 (default) | `acp.NewInvalidRequest({"error":"backpressure","limit":"client_calls"})` |

A conflicting load or resume uses the `session_restore` limit token.
Prompt turns are serialized per session. Multiplexed runtimes admit concurrent
turns on independent sessions. `WithConcurrencyLimits` may change the two configurable values;
zero means default; negative fails construction. `wire.Backpressure` builds
the error.

## Startup Capability Gating

- Resolve the executable as
  [01-public-api.md](01-public-api.md#process-options) states.
- Fail fast with an actionable error if required methods, events, or
  permission surfaces are missing. Never silently downgrade: if a dependent
  surface is unavailable, do not advertise the capability and fail its use
  closed.
