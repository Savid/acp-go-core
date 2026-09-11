# Lifecycle

## Serve

`Serve` creates an `Agent`, attaches it to the supplied JSON-RPC streams,
blocks until context cancellation or connection close, calls `Agent.Close`,
and returns the close error in preference to the loop's result, else
`ctx.Err()` if the context cancelled first, else nil on normal peer close. It
never owns stdin or stdout beyond reads and writes on the supplied streams.

## Native Process Model

- The harness is launched through `process.Start`: resolved executable, merged
  [environment](02-public-api.md#process-environment), session cwd, its own
  process group, and three dedicated pipes. Native stdout and stderr never
  inherit ACP stdout and are routed to logs only after JSON-RPC separation is
  guaranteed.
- One session owns one native process. Its cwd, model, permission state,
  callbacks, cancellation, and persistence belong to that session alone.
- Tests inject native stdout and stderr noise and prove ACP stdout stays valid.

Every native event is fenced by the native-process epoch, session, and turn
nonce. A native process exit fails the in-flight turn exactly once and
suppresses late events from the dead epoch. The session stays addressable:
the next prompt relaunches the process against the same native state.

### Session Environment and PATH Scoping

Session `env` and ordered `extraPathDirs` are per-session configuration.

- **The addressed session carries them, never the Agent.** They apply to that
  session's own process.
- Concurrent sessions carry their own values and never inherit a peer's.
- **Both are part of resume identity.** The session record stores the ordered
  list in order; reordering is a distinct configuration.
- **A change is a resume boundary, not a mutation.** Load and resume carry the
  values the request supplies; live native state is never mutated in place.
- **Recovery reconstructs them** from the session record before launching the
  native process.
- Executable resolution and version probing use the base environment before
  session path directories apply.

## Shutdown Ladder

For every native session, in order:

1. Mark the session closed and stop accepting prompts.
2. Resolve pending permission requests as ACP `cancelled`.
3. Resolve pending elicitation requests as action `cancel` or `-32800`.
4. Send the provider-native interrupt under a bounded background context.
5. Signal the process group and wait for the root process through
   `process.Process.Shutdown`.
6. Commit the durable state the boundary owes, the same rung a wire
   `session/close` runs; only diagnostics flushing is best-effort.
7. Release scratch directories, stream readers, and goroutines. Native state
   in the home is left in place.

The ladder binds `session/close`, `session/delete`, and `Agent.Close`. A
session close never terminates peers.

## JSON-RPC Request Cancellation

`$/cancel_request` is stable ACP v1 and distinct from `session/cancel`. The
pinned SDK cancels the addressed handler context and sends a best-effort
outbound `$/cancel_request` when an in-flight peer-request context is
cancelled. Handler work derives from the SDK context, and permission,
elicitation, and client requests receive their owning turn context rather than
a detached background context. The original request still completes exactly
once with a valid result or `-32800`.

## Cancel Determinism

1. `session/cancel` cancels local turn state immediately. It is
   session-scoped: it applies to the addressed session's current turn, if any,
   and is a wire-silent no-op otherwise.
2. Native interrupt uses a bounded background context, not the caller context.
3. Pending permission requests receive outcome `cancelled`; pending
   elicitations receive action `cancel` or `-32800`.
4. Each prompt produces exactly one terminal result.
5. Repeated cancels for the same turn are idempotent.

## Lifecycle Stream Fencing

A lifecycle stream belongs to exactly one session incarnation, and ending the
incarnation ends the stream.

- `streamId` does not rotate on a reconnect to the same surviving source and
  does not survive the incarnation.
- A validated cancel terminalizes every pending action as `cancelled`, and an
  owned activity only where a structured native event reports it terminal. The
  sibling emits the cancelled cycle's terminal `idle` with outcome `cancelled`
  and admits no further prompt until it has.
- **`session/close` ends the addressed session.** After the ladder's native
  cleanup succeeds, close terminalizes every nonterminal owned activity and
  action as `cancelled`, emits those updates and any open turn's terminal
  `idle`, commits owed state, fences the stream, and returns. A failed commit
  fails the close with the stream fenced.
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
| Concurrent prompts per session | 1 (fixed) | `acp.NewInvalidRequest({"error":"backpressure","limit":<registered token>})` |
| Concurrent server-to-client calls per agent | 16 (default) | `acp.NewInvalidRequest({"error":"backpressure","limit":"client_calls"})` |

Prompt turns are serialized per session; independent sessions run
concurrently. `WithConcurrencyLimits` may change the two configurable values;
zero means default; negative fails construction. `wire.Backpressure` builds
the error.

## Startup Version and Capability Gating

- Resolve the executable from the base environment before session path
  directories apply.
- Probe the binary version before launching sessions.
- Fail fast with an actionable error if required methods, events, or
  permission surfaces are missing. Never silently downgrade: if a dependent
  surface is unavailable, do not advertise the capability and fail its use
  closed.
