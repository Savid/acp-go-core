# Sessions and the Session Store

Persistence is store-backed. Native local state is a cache; the host-provided
`SessionStore` is the durability boundary. Store-backed restore MUST work after
deleting native state.

Every sibling installs a fresh `InMemorySessionStore` when the caller provides
none. Omitting the option changes durability, not authority: `session/list`,
`session/load`, `session/resume`, and delete consult the default store and
never discover or adopt a native-only session with no live store entry.

## Store API

The store types live in this module's root package and are used directly by
hosts and siblings.

```go
package acpcore

import (
    "context"
    "encoding/json"
)

const SessionStoreMainSubpath = ""

type SessionStoreEntry = json.RawMessage

type SessionKey struct {
    SessionID string
    Subpath   string
}

type SessionSummary struct {
    SessionID          string
    UpdatedAtUnixMilli int64
}

type SessionStoreReplacement struct {
    Key     SessionKey
    Entries []SessionStoreEntry
}

type SessionStore interface {
    Load(ctx context.Context, sessionID string) (map[string][]SessionStoreEntry, error)
    Replace(ctx context.Context, main SessionKey, replacements []SessionStoreReplacement) error
    Delete(ctx context.Context, key SessionKey) error
    ListSessions(ctx context.Context) ([]SessionSummary, error)
}

type InMemorySessionStore struct { /* unexported fields only */ }

func NewInMemorySessionStore() *InMemorySessionStore
```

Each sibling exports its own `const SessionStoreFormat = "<vendor>-<kind>-v1"`.
Store keys are exactly `{SessionID, Subpath}`. Workspace, worker, and format
scoping belong to the host's store adapter, which records the sibling's format
beside its own scoped key, never inside `SessionKey`.

## Store Semantics

| Method | Required behavior |
|---|---|
| `Load` | Atomically returns the latest complete session generation as subpath → entries, preserving row order; nil when missing or tombstoned. A live empty main record is present at key `""`. Returned maps, slices, and bytes belong to the caller. |
| `Replace` | Atomically publishes one session's complete generation and is durable before return. It copies the entries it receives; the caller keeps ownership and may reuse them. Uncommitted generations are invisible; failure preserves the previous generation. |
| `Delete` | Durable tombstone first. Deleting main cascades to all subpaths; deleting a subpath deletes only it. Missing keys succeed. |
| `ListSessions` | Committed, non-tombstoned main keys, newest `UpdatedAtUnixMilli` first, then `SessionID`. |

Every store call MUST be bounded by `SessionStoreTimeout`. `sessionlog.Load`
and `sessionlog.Commit` apply that bound to the calls they own.

All timestamps are Unix milliseconds. An empty `SessionID` makes delete a
no-op and load return nil; every replace fails `ErrSessionIDRequired`.

### Replacement Shape

A `Replace` addresses exactly one session:

- `main.Subpath` equals `SessionStoreMainSubpath`, and every replacement key
  names `main.SessionID`, with the main key exactly once. Duplicates fail with
  an error naming the duplicate, before any write.
- Exactly the listed keys survive; unlisted subpaths are tombstoned
  atomically. A listed key with empty `Entries` remains live. A listed subpath
  has its tombstone cleared.
- A main key tombstoned by `Delete` never revives: replacement writes nothing
  and returns success. The store enforces this itself.
- External stores stage generation entries and publish a final commit marker;
  reads pin one committed generation for every returned subpath and ignore uncommitted generations.

`storetest.Run` proves these rules against any implementation.

## Ephemeral Sessions

A `session/new` whose `_meta["acp-go.dev/session"].ephemeral` is `true` opens
a session the host deletes without ever needing it back, such as a discovery
or usage probe. For such a session the sibling MUST NOT call `Replace` or
`Delete` on the store and MUST NOT list it; the native runtime holds it alone
until `session/delete`. Every other rule of a session applies unchanged.

## Store Formats

Each sibling exports exactly one `<vendor>-<native-state-kind>-v1` format. Raw
native rows or exports are mirrored after turns under the main subpath, plus a
configuration sidecar. The [registry](registry.md#session-stores) records each
sibling's kind and carrier record.

A multiplexed runtime persists one logical session at a time and never
snapshots or restores the shared native runtime root.

A session is **poisoned** when a wrapper invariant breaks: unexpected native
session-id drift, a conversation-reset frame, or a store generation the sibling cannot
trust. A poisoned session refuses every operation but `session/close` and
`session/delete` with `<vendor>_session_poisoned`.

## Mirror Out

- Read native rows after turns and publish them with the current session
  configuration through `sessionlog.Commit`, using one atomic store generation.
  Configuration changes commit even when no native rows were added. A successfully established empty conversation commits an empty main record with its configuration.
- Never commit while native input, a foreground-blocking permission or
  elicitation, or message generation is pending.
- Preserve raw native bytes.

### Commit Ordering

- For a turn that completes normally, the mirror commit is durable before the
  `session/prompt` response returns. The commit is awaited on the prompt path,
  never a goroutine or debounce that can outlive the turn.
- A failed commit fails the prompt. Never return success for a turn the store
  does not hold.
- Native tails cannot be fenced: state the harness writes after its own
  completion signal is captured in the next committed mirror where the surface
  allows. Each sibling's tail, cancelled-turn, and close behavior is in
  [registry.md](registry.md#mirror-commit-ordering).

### Lifecycle Commit Points

- **At terminal `idle`, commit the foreground prefix**: the largest prefix of
  that turn's native state the adapter can commit truthfully while background
  work may continue. It is durable before the terminal `idle` event, which is
  before the prompt response.
- An action with `blocksForeground: false` does not block that commit.
- A failed foreground commit fails the prompt.

## Hydrate In

- Restore missing native state from the store through the harness's native
  persistence surface. The restored conversation MUST remain usable natively.
- **Existing native state wins.** When the home already holds the native
  state for the session and it is at least as long as the store's copy, load
  from it and adopt its newer rows into the store. Materialize from the store
  when native state is absent. Replace shorter native state only when the native
  persistence surface supports it. If it refuses replacement of an existing id,
  shorter native state MUST fail restore. A disagreement at a shared
  position fails `<vendor>_restore_failed`.
- **Native state is never deleted by the adapter** on close, retire, or
  runtime replacement. The store is the durability boundary; the native copy
  is what lets the operator continue the same session outside ACP.
- Credentials are never restored from the store. Session env values are
  carrier state and may contain secrets; the host protects those rows.
- Require the current configuration record and validate its identity, paths,
  environment, and ordered path directories. Refuse unknown or duplicate
  record fields and malformed native rows with `<vendor>_restore_failed`.
- Validate restored files against path traversal and format rules.
- A native runtime MUST NOT bind the conversation until its initial state is
  hydrated. A server whose import API owns persistence may start before hydration.
  An observer MAY attach without submitting input to verify an imported conversation.

## Lifecycle Stream and Incarnation Identity

| Identity | Scope | Wire name |
|---|---|---|
| ACP session id | public, stable for the conversation's life | `sessionId` |
| Native conversation id | the currently bound native conversation, recorded with its backup | `_meta.<vendor>.nativeSessionId` |
| Native transport session id | internal connection-local identity, when the harness separates it from the conversation id | none |
| Session incarnation | one native lifecycle source's lifetime | `streamId` |

- The ACP session id MUST remain stable across load, resume, and native recovery.
  ACP requests, notifications, and store keys MUST use it. Native commands and
  native identity checks MUST use the recorded native conversation id.
- Every configuration record MUST contain `sessionId` and `nativeSessionId`.
  The latter MUST identify the native history in the same atomic generation.
  The values MAY be equal. An ACP session MUST have one current native binding.
- Recovery MAY replace a missing native conversation with a new native id.
  Before load or resume succeeds, the sibling MUST verify the restored history
  and atomically commit it with the new binding under the original ACP key.
  A failed commit MUST preserve the previous generation. Recovery MUST NOT
  submit a user prompt, replace conflicting history, or treat an authentication,
  authorization, or transport failure as proof that native state is missing.
- A binding change MUST fence the previous lifecycle incarnation and its native
  deliveries. An unexpected id change during normal operation MUST fail.
- New, load, and resume responses and each session-list entry MUST publish the
  current native id through the [session metadata](02-wire-contract.md#native-session-binding).
- Lifecycle state is keyed by ACP session id, incarnation, and entity id. An
  incarnation is never reused and never adopts a prior incarnation's entities.
- A stream's first event is a whole-state assertion. A sibling that cannot
  reconstruct the complete nonterminal sets MUST NOT open a stream for the
  session and resumes only from a boundary where it can, otherwise failing the
  resume.
- `session/load` replay is conversation history; the stream is lifecycle
  state. Neither substitutes for the other.

## Load vs Resume

| ACP method | Behavior |
|---|---|
| `session/load` | Restores native state and replays history to the client. |
| `session/resume` | Restores native state and returns without replaying. |

## Listing

A session's title is its first prompt's first non-empty text, whitespace
collapsed and bounded to 256 runes by `wire.NormalizeTitle`.
`session/list` orders live and stored sessions by descending `updatedAt`,
then ascending session id, and paginates through `wire.PaginateSessions`.
Cursors are raw URL-base64 offsets. An empty cwd filter includes every cwd.

## Proving the Format

Before writing production restore code, run a spike proving the candidate
format restores after deleting all native state:

| Check | Required proof |
|---|---|
| Messages | User and assistant messages, parts, tool parts, usage, and stop or error state restore well enough for load replay and resume. |
| Auxiliary state | Todos and permission history needed for display survive. |
| Model settings | Provider id, model id, mode, and relevant config restore. |
| Pending-input absence | Commit is blocked while a foreground-blocking action or message generation is pending. |
| Native state deletion | The proof deletes the native state before hydrating. |
| Native binding | Different ACP and native ids route load, resume, prompts, events, and native continuation correctly. Replacement recovery retains the ACP id and commits the verified history with its new native id. |
| Lifecycle snapshot | The restored state reconstructs a truthful `lifecycle_snapshot`, or the sibling resumes only at an idle boundary. |

A format that fails any required proof MUST NOT enter the package.
