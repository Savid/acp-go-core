# Wire Contract

## Initialize Response

Before reading ACP input, a sibling MUST install its connection, logger, and
metadata transport. Requests available at startup use the same strict decoding
as later requests.

All siblings return this shape; only vendor `_meta` details, the media bounds,
the handoff key, and the lifecycle answer vary. The top-level `_meta` block is
absent when the host omitted the lifecycle capability. `auth`,
`mcpCapabilities`, `promptCapabilities`, and `sessionCapabilities` are
struct-typed in the pinned SDK and are always present, empty where nothing is
advertised.

```json
{
  "protocolVersion": "<acp.ProtocolVersionNumber>",
  "agentInfo": {
    "name": "acp-go-<vendor>",
    "title": "acp-go-<vendor>",
    "version": "<configured version>"
  },
  "authMethods": [],
  "agentCapabilities": {
    "loadSession": true,
    "auth": {},
    "mcpCapabilities": {},
    "promptCapabilities": {
      "embeddedContext": true,
      "image": true
    },
    "sessionCapabilities": {
      "additionalDirectories": {},
      "close": {},
      "delete": {},
      "list": {},
      "resume": {}
    },
    "_meta": {
      "<vendor>": {},
      "acp-go.dev/mediaEnvelope": {
        "maxBytes": 6291456,
        "maxPromptBytes": 6291456,
        "maxDimension": 0,
        "imageFormats": ["image/png","image/jpeg","image/gif","image/webp"],
        "documentFormats": []
      },
      "acp-go.dev/handoff": {"version":1}
    }
  },
  "_meta": {
    "acp-go.dev/lifecycle": {
      "version": 1,
      "updatesOutsidePrompt": true,
      "activityKinds": []
    }
  }
}
```

Advertise only features you implement and test. Omitted means unsupported.

Position encoding: prefer `utf8`, else `utf16`, never `utf32`; default to
`utf16` when the client offers neither.

`authMethods` is always `[]`: the harness authenticates itself in its home.
`activityKinds` is `[]` for every sibling. `updatesOutsidePrompt` is answered
under the [evidence gate](08-testing.md#lifecycle-fixtures); a permanent-channel
sibling answers `true` and delivers between-prompt native work as ordinary
updates under agent-origin turns. The routes behind every absent capability are
listed under [Banned SDK Routes](#banned-sdk-routes).

## Extension Constants

Every sibling exports:

```go
const RawEventMethod = "_<vendor>/rawEvent"
```

| Method | Direction | Params |
|---|---|---|
| `_<vendor>/rawEvent` | Agent notification to client | `{ "sessionId": string, "sequence": number, "source": string, "event": object }` |

Raw events are off by default, enabled per session through
`_meta.<vendor>.rawEvent.enabled`, size-capped to 64 KiB per notification, and
non-authoritative.

- **Oversize is marked, never omitted.** An event whose marshalled `params`
  exceeds 65536 bytes is sent once with `event` replaced by
  `{"truncated":true,"reason":"oversize","maxBytes":65536,"sizeBytes":<int>}`.
  An event that fails to marshal uses `reason:"unserializable"` and no
  `sizeBytes`.
- **`sequence` is per session, starts at 1, and is contiguous across
  successful deliveries.** A failed `NotifyExtension` does not consume its
  number.
- **An emit failure never fails the turn.**
- **Live turn only.** No replay of raw events on `session/load`.

## Banned SDK Routes

The pinned SDK dispatches these only if a sibling implements the optional
interface. No sibling does:

- `session/fork`: no sibling implements `UnstableForkSession`, so the route is
  never dispatched.
- `document/*`, `nes/*`, `providers/*`.
- `session/set_mode`: the required method returns method-not-found.
- `mcp/message`: never implemented. `mcpCapabilities` advertises every
  transport false: the pinned SDK types it as a struct, so the key rides as
  `{}`, which is the ACP default and advertises nothing
  ([watchlist](../tracking/upstream-acp.md)); no MCP server value is ever
  accepted.

The required stable routes are `initialize`, `authenticate`, `logout`,
`session/new`, `session/load`, `session/resume`, `session/list`,
`session/prompt`, `session/cancel`, `session/close`, `session/delete`, and
`session/set_config_option`.

## Uniform Rejections

These inbound shapes are rejected identically with `acp.NewInvalidParams`
(`-32602`):

- **Unsupported prompt content.** `{"error":"unsupported","field":"prompt"}`
  before native start. Image blocks use their [own gates](#canonical-input-shape).
  Unrepresentable text/image ordering follows the
  [image input rule](05-behavior.md#image-input).
- **Relative `cwd`** on `session/new`, `session/load`, or `session/resume`:
  `{"error":"unsupported","field":"cwd"}` before any native process or store
  entry exists.
- **Empty prompt**, or one whose blocks map to nothing forwardable:
  `{"error":"unsupported","field":"prompt"}`. An empty turn never reaches the
  harness.
- **Invalid image block.** The structured data in [Image Content](#image-content).
- **Non-empty `mcpServers`** on `session/new`, `session/load`, or
  `session/resume`: `{"error":"unsupported","field":"mcpServers"}`.
- **Lifecycle key on a non-carrier surface.** `unsupported` at
  `_meta["acp-go.dev/lifecycle"]` ([Surfaces That Carry the Key](#surfaces-that-carry-the-key)).

## Image Content

### Canonical Input Shape

An image prompt block arrives in one of two forms, both standard ACP image
blocks carrying one of the four canonical MIME strings.

**Embedded form**, accepted everywhere:

```json
{"type": "image", "data": "<base64 raster bytes>", "mimeType": "image/png"}
```

**Handoff form**, accepted only while `WithInputHandoffRoot` is set:

```json
{
  "type": "image",
  "mimeType": "image/png",
  "data": "",
  "uri": "file:///<handoff-root>/<subpath>/<name>.png",
  "_meta": {"acp-go.dev/handoff": {"version": 1, "digest": "<sha256-hex>", "sizeBytes": 123456}}
}
```

- Non-empty `data` selects embedded input even with a handoff envelope. Empty
  `data` plus a handoff key or `file` URI selects handoff. Empty `data` with
  neither fails `missing_data`.
- Embedded `data` is authoritative; a URI beside it is provenance only. Handoff
  reads only a `file://` path inside the configured root. Remote fetch is
  forbidden for every block type.
- The host owns handoff files. The adapter never writes, moves, or deletes
  them. Path-based native input copies verified bytes into adapter scratch.

### Input Error Taxonomy

Every pre-turn image rejection is `-32602`, built from `image.InputError`,
with:

```json
{"field": "prompt.image", "error": "too_large", "message": "decoded image exceeds the configured per-image limit", "index": 1, "sizeBytes": 12582912, "maxBytes": 6291456}
```

- `field` names the inbound block type, `prompt.image` or `prompt.resource`.
- `error` is a token from the table below.
- `message` is display-only. It is required for the four handoff verdicts and
  optional elsewhere. Handoff messages are compile-time constants with no path,
  filename, digest, size, or OS error text.
- `index` counts every image and blob block in request order; text resources
  spend the aggregate but consume no index.
- `sizeBytes` and `maxBytes` appear only for a byte-limit failure or the
  handoff block-count cap. An aggregate failure names the first crossing block
  and reports cumulative bytes.

| `error` | Meaning |
|---|---|
| `missing_data` | Embedded base64 absent or empty |
| `invalid_base64` | Data cannot be decoded |
| `invalid_media_type` | MIME absent, non-canonical, or outside the allowlist |
| `media_type_mismatch` | Bytes sniff as no allowlisted raster, or conflict with the declared MIME |
| `animated_not_supported` | Multi-descriptor GIF, WebP `VP8X` `ANIM`, or PNG `acTL` |
| `invalid_dimensions` | Recognized format with no valid dimensions |
| `too_large` | Per-image or aggregate bytes exceed the effective limit, or the handoff count cap |
| `unsupported_by_model` | Authoritative selected-model metadata says text-only |
| `invalid_handoff` | Handoff intent with a malformed block: unset root, absent or malformed envelope, extra field, or a URI that is not an absolute `file` path with empty or `localhost` host |
| `path_not_allowed` | The name escapes the root, is refused by `os.Root`, or is not a regular file |
| `missing_file` | The name is inside the root but does not exist, dangles, or cannot be read to completion |
| `handoff_digest_mismatch` | Bytes do not match the declared `digest` and `sizeBytes`, including a larger file |

Validation stops at the first failing block in request order.

Handoff pre-gate order, with every no-I/O check before the file is opened:

```text
invalid_handoff (unset root) → too_large (block count) → invalid_handoff (envelope, uri) → invalid_media_type → too_large (declared size) → path_not_allowed | missing_file → read ≤ declared+1 → handoff_digest_mismatch
```

- At most **64** handoff blocks per prompt; the crossing block reports
  `too_large` with the count as `sizeBytes` and `64` as `maxBytes`.
- The two filesystem verdicts come from one `os.Root` open: `fs.ErrNotExist`
  is `missing_file`; every other failure or a non-regular descriptor is
  `path_not_allowed`.
- The read stops at declared `sizeBytes` plus one. More bytes than declared is
  `handoff_digest_mismatch` with nothing forwarded. A partial read is
  `missing_file`. A `Stat` size never decides a verdict.

Embedded-form gate order:

```text
missing_data → invalid_media_type → invalid_base64 → media_type_mismatch (recognition) → invalid_dimensions → animated_not_supported → media_type_mismatch (declared vs sniffed) → too_large (per image) → too_large (per prompt)
```

Handoff bytes run the same content gates, skipping the ones the pre-gate
already decided, and count toward the prompt aggregate identically. Text
resources share the aggregate and charge their raw text before any envelope
expansion.

### Output Failure Envelope

An image output verdict is never invalid params. `invalid_base64`,
`not_a_raster`, `media_type_mismatch`, `missing_file`, `path_not_allowed`, and
`too_large` are refused in place and reported to the model as the refused
artifact's own result content; the turn continues
([05-behavior.md](05-behavior.md#image-failure-fatality)).

Only `storage_failed` reaches the wire, as the turn-failure error with
`cause:"transport"` plus:

```json
{"error": "<vendor>_turn_failed", "cause": "transport", "message": "image output is no longer available from the artifact store", "stage": "image_output", "reason": "storage_failed"}
```

`stage`, `reason`, `sizeBytes`, and `maxBytes` appear only on image output
failures. `reason` is one of `invalid_base64`, `not_a_raster`,
`media_type_mismatch`, `missing_file`, `path_not_allowed`, `too_large`,
`storage_failed`. On this channel `missing_file` and `path_not_allowed` name
a harness-returned path, not a host handoff path.

### Output Frame Clamp

Output limits clamp to 7,864,155 decoded bytes. Base64 of that is 10,485,540
characters; the pinned SDK's line scanner is bounded at 10,485,760 bytes, which
leaves 220 bytes for the JSON-RPC envelope. The scanner bound is an unexported
SDK constant, so a shared-pin move re-derives this clamp. An oversized inbound
frame disconnects before dispatch and produces no verdict.

## Vendor `_meta` Contract

Custom metadata lives only in `_meta`. `traceparent`, `tracestate`, and
`baggage` are reserved for propagation.

Session lifecycle requests (`session/new`, `session/load`, `session/resume`)
accept only this owned namespace:

```json
{"_meta": {"<vendor>": {"options": {}, "rawEvent": {"enabled": false}}}}
```

| Namespace | Unknown key behavior |
|---|---|
| `_meta.<vendor>.options` | Invalid params with the offending field path. |
| `_meta.<vendor>` outside `options` and `rawEvent` | Invalid params. |
| Foreign `_meta.*` namespaces | Ignored. The owned namespace is exactly `<vendor>`; anything else, including the module path, is foreign. |
| `acp-go.dev/*` literals | Never foreign. Each carries its own rule, and one on a surface where this contract requires it to be read is never ignored. `acp-go.dev/lifecycle` on a session lifecycle request is invalid params naming `_meta["acp-go.dev/lifecycle"]`. |
| Reserved trace keys | Pass through only for propagation. |

This governs inbound `_meta` only. Agent-emitted `_meta.<vendor>` on outbound
updates is update metadata governed by [05-behavior.md](05-behavior.md).

## Media Envelope

`acp-go.dev/mediaEnvelope` is advertised unconditionally by every sibling in
`agentCapabilities._meta`, built by `image.MediaEnvelope`, with exactly these
five fields:

| Field | Meaning |
|---|---|
| `maxBytes` | Effective decoded per-image input bound; finite and greater than zero. |
| `maxPromptBytes` | Effective decoded aggregate input bound; `0` means disabled. |
| `maxDimension` | Enforced native per-dimension pixel bound; `0` means none. |
| `imageFormats` | The inbound allowlist, in order: `image/png`, `image/jpeg`, `image/gif`, `image/webp`. |
| `documentFormats` | MIMEs mapped to a native document representation; `[]` when none. |

Numeric bounds come from the same [resolution](02-public-api.md#effective-image-limits)
the gates enforce. The envelope is instance-scoped and model-independent;
`unsupported_by_model` remains a prompt-time verdict.

## Handoff Envelope

`acp-go.dev/handoff` appears in two places:

- on `agentCapabilities._meta` at initialize as `{"version":1}`, **if and only
  if** `WithInputHandoffRoot` is set;
- on an image content block in `session/prompt` as
  `{"version":1,"digest":"<sha256-hex>","sizeBytes":<int>}`.

The block envelope carries exactly those three members. Any extra or missing
member, a non-object, a wrong version, a `digest` that is not 64 lowercase hex
characters, or a `sizeBytes` that is not a non-negative integer is
`invalid_handoff` before any file is touched. With the root unset every
handoff-form block is `invalid_handoff`; there is no fallback transport.

## Lifecycle Envelope

`acp-go.dev/lifecycle` is the family's ordered session-lifecycle extension.
Standard ACP messages, thoughts, plans, tool calls, permissions, and
elicitations keep their surfaces and meaning; this extension adds only what
ACP v1 does not carry: prompt-acceptance correlation, an ordered event stream
with a global sequence, and causal activity and action ownership.

It follows the shape of the ACP v2
[prompt lifecycle RFD](https://agentclientprotocol.com/rfds/v2/prompt), which
answers a prompt on acceptance and reports turns and background work through
session updates. Carrying that shape in `_meta` now, on top of v1, is what
lets a host move to native v2 with a small change when the protocol ships.

Every value in this section lives only in `_meta`, carries exact scalar
`version: 1`, and rejects an unknown member. Every opaque identifier is a
non-empty string of at most 4096 bytes; an empty one is `malformed_envelope`.

A notification carrying an envelope MUST fit the inbound frame bound. A
`lifecycle_snapshot` states the complete nonterminal sets and is never split; a
sibling whose sets cannot be stated inside the bound MUST NOT open the stream.

### Lifecycle Capability

The host requests it on `InitializeRequest._meta`:

```json
"acp-go.dev/lifecycle": {"version": 1}
```

The sibling answers on `InitializeResponse._meta` with proven facts for the
active configuration:

```json
"acp-go.dev/lifecycle": {"version": 1, "updatesOutsidePrompt": true, "activityKinds": ["task", "subagent"]}
```

| Field | Rule |
|---|---|
| `version` | Exact integer `1`. Any other value, a duplicate key, an unknown field, or trailing input is refused. |
| `updatesOutsidePrompt` | `true` only when the sibling delivers `session/update` notifications while no prompt is in flight, and drops none. |
| `activityKinds` | The kinds this sibling emits, a duplicate-free subset of `task`, `subagent`. `[]` when none, never `null`. |

- Every field is a proven structured fact for the active configuration, never
  derived from silence, timers, transcript text, or ACP `idle`.
- A non-object, missing field, wrong type, or unknown member is rejected with
  `acp.NewInvalidParams` naming the path, for example
  `{"error":"unsupported","field":"_meta[\"acp-go.dev/lifecycle\"].activityKinds"}`.
- Absence of the key in the request means the host asked for nothing: the
  sibling omits the key from its response and emits no envelope, reads no
  prompt correlation, and stamps no action correlation on that connection.

**Enabling version 1 obligates the foreground stream.** A sibling whose answer
is present emits, for every session on the connection, `lifecycle_snapshot`
opening the stream, `prompt_accepted` for every accepted prompt, and a
`state_update` for every foreground transition. `activityKinds` governs only
which `activity_update` kinds appear.

**An event MUST NOT assert a fact the answer did not claim.** An
`activity_update` outside the answer's kinds, an envelope while no prompt is in
flight on a connection answering `updatesOutsidePrompt: false`, and any
envelope on a connection where the key was omitted are each
`unnegotiated_fact`.

### The Session Update Envelope

Every lifecycle event rides the `session/update` notification's
`_meta["acp-go.dev/lifecycle"]`:

```json
{"version": 1, "streamId": "opaque-session-incarnation", "sequence": 42, "event": {"type": "activity_update"}}
```

Exactly four members. `sequence` is a positive integer; `event.type` is one of
the five below. One envelope per notification, on the notification's `_meta`
beside `sessionId` and `update`, never inside `update`.

**Carrier rule.** Every envelope rides its own identity-only
`session_info_update` that sets neither `title` nor `updatedAt`. Every other
`sessionUpdate` variant is `illegal_carrier`. Visible tool progress still flows
as ordinary tool-call updates with no envelope. A sibling never invents
content to carry an envelope and adds no custom `sessionUpdate` or stop-reason
variant.

A host reduces the envelope from the notification stream in arrival order,
never from a coalesced projection.

### The Closed Event Set

`event.type` is exactly one of `lifecycle_snapshot`, `prompt_accepted`,
`state_update`, `activity_update`, `action_update`. Three member objects recur:

```json
"foreground": {"state": "running", "cycleId": "opaque", "turnId": "opaque", "origin": "submission"}
```

```json
"activity": {"activityId": "opaque", "kind": "task", "state": "running", "parentId": "opaque", "toolCallId": "native-correlation", "cause": "submission", "originTurnId": "opaque", "runId": "opaque", "progress": {}}
```

```json
"action": {"actionId": "opaque", "kind": "permission", "state": "pending", "owner": {"type": "turn", "id": "opaque"}, "runId": "opaque", "blocksForeground": true}
```

`foreground` appears only in snapshots and carries `state` and `cycleId`.
`state` is `running`, `idle`, or `requires_action`. A live state carries
`turnId` and `origin` (`submission` or `activity`); `idle` omits both.

`activity.kind` is an advertised kind; `activity.state` is one of `pending`,
`running`, `requires_action`, `completed`, `failed`, `cancelled`;
`activity.cause` is `submission`, `activity`, or `session`. `activityId`,
`kind`, `state`, `cause`, and `originTurnId` are required; `parentId`,
`toolCallId`, `runId`, and `progress` are optional. `progress` is an opaque
object a host renders and never reduces, bounded at 4096 serialized bytes, and
still part of the event's content for duplicate comparison.

`action.kind` is `permission` or `elicitation`; `action.state` is `pending`,
`accepted`, `declined`, `cancelled`, or `failed`; `action.owner.type` is
`turn` or `activity`. `actionId`, `kind`, `state`, `owner`, and
`blocksForeground` are required on first sight; `runId` is optional. A later
`action_update` carries `actionId` and `state` and restates an immutable member
only with its first-sight value.

**`lifecycle_snapshot`**, the first event of every stream:

```json
{"type": "lifecycle_snapshot", "foreground": {"state": "idle", "cycleId": "opaque"}, "activities": [], "actions": []}
```

It carries the current foreground and the complete nonterminal activity and
action sets, always present as arrays. A set listing a terminal entity or a
duplicate id is `malformed_envelope`. Every parent and owner reference resolves
inside the snapshot or is `unknown_entity`. A `running` or `requires_action`
snapshot projects the named turn as open with its origin. A sibling that cannot
reconstruct a truthful snapshot MUST NOT open the stream
([04-sessions-and-store.md](04-sessions-and-store.md#lifecycle-stream-and-incarnation-identity)).

**`prompt_accepted`**:

```json
{"type": "prompt_accepted", "submissionId": "opaque", "clientNonce": "opaque", "turnId": "opaque", "runId": "opaque"}
```

`submissionId`, `clientNonce`, and `turnId` are required; `runId` only when
the prompt named one. `submissionId` and `clientNonce` echo the prompt's
correlation value. It is emitted after validation and after the native
dispatcher has accepted the frame, before any event caused by it. A failure
before that point emits nothing and creates neither submission nor turn.
Reusing a `turnId` the stream already introduced is
`immutable_identity_change`, or `post_terminal_mutation` when that turn is
terminal.

**`state_update`**, one foreground transition:

```json
{"type": "state_update", "state": "idle", "cycleId": "opaque", "turnId": "opaque", "cause": "submission", "stopReason": "end_turn", "outcome": "success"}
```

`state`, `cycleId`, and `cause` are required. `turnId` is required for
`submission` and `activity` causes and for every live state; it is optional
only on a `session`-caused `idle`. `stopReason` and `outcome` are absent on
`running` and `requires_action`. An `idle` ending an open turn carries one
`outcome` of `success`, `refused`, `cancelled`, `limit`, or `failed`, and a
standard ACP `stopReason` except when `outcome` is `failed`, when the stop
reason is absent. Presence violations are `malformed_envelope`.

Outside a snapshot exactly two events introduce a turn: `prompt_accepted`
(origin `submission`) and an `activity`-caused `state_update{state:"running"}`
with a new `turnId` (origin `activity`). Every other turn reference must
already resolve or is `unknown_entity`.

**`activity_update`**, first sight or a later patch:

```json
{"type": "activity_update", "activity": {"activityId": "opaque", "state": "completed"}}
```

The first update carries every immutable identity field. Later updates carry
`activityId`, `state`, and optionally `progress`, and may restate an immutable
field only with its first-sight value; a different value is
`immutable_identity_change`, as is a first sight missing one.

**`action_update`**:

```json
{"type": "action_update", "action": {"actionId": "opaque", "kind": "permission", "state": "pending", "owner": {"type": "turn", "id": "opaque"}, "blocksForeground": true}}
```

Only an action with `blocksForeground: true` bears on `requires_action`. A
blocking action's update does not itself move the foreground; the transition
is always its own event:

- A blocking action blocks the foreground cycle current at its first sight.
- The sibling emits `state_update{state:"requires_action"}` for the blocked
  cycle, and `state_update{state:"running"}` or the terminal `idle` when the
  last blocking action resolves.
- Every blocking action terminalizes before the transition that unblocks its
  cycle; a cancelled cycle terminalizes its blockers as `cancelled` before its
  terminal `idle`.
- A `requires_action` with no outstanding blocker, a snapshot whose
  `requires_action` has no blocker in its own set, a blocking action with no
  accompanying transition, and a transition that leaves `requires_action` or
  ends the cycle while a blocker is nonterminal are each
  `inconsistent_foreground`. A structurally invalid transition is
  `malformed_envelope` first.

### Prompt Correlation

While the lifecycle capability is enabled, the host stamps exactly this on
every `session/prompt`, and the sibling reads it:

```json
{"version":1,"submission":{"submissionId":"opaque","clientNonce":"opaque","runId":"optional"}}
```

A missing key is `{"error":"missing","field":"_meta[\"acp-go.dev/lifecycle\"]"}`.
A non-object, wrong version, empty or over-bound identifier, or extra field is
`{"error":"unsupported","field":"_meta[\"acp-go.dev/lifecycle\"].<member>"}`.
Both fire before dispatch. With the capability omitted the key is refused with
`unsupported` on the bare path. `clientNonce` is the host's own input
identity, distinct from every JSON-RPC id.

### Action Correlation

While the capability is enabled, the sibling stamps exactly this on every
`session/request_permission` and `elicitation/create`:

```json
{"version":1,"streamId":"opaque","action":{"actionId":"opaque","owner":{"type":"turn","id":"opaque"}}}
```

The sibling registers the inbound JSON-RPC request against `actionId` before
emitting the `action_update` that announces it, so a host never sees an id it
cannot answer. The held request resolves exactly once; a second resolution or
one naming a terminal or differently-owned action fails closed. Connection loss
terminalizes the pending action. `actionId` is lifecycle identity only, never
a routing or authorization token.

### `_meta` on `session/request_permission` and `elicitation/create`

Beyond the propagation keys, the agent sets only its own `<vendor>` namespace
annotations and, while enabled, `acp-go.dev/lifecycle`. It never sets the media
or handoff literal there. On the response, `_meta` is read by nobody: the
outcome comes only from the structural outcome union or elicitation action.

### Surfaces That Carry the Key

| Surface | Direction | Value | Strictness |
|---|---|---|---|
| `initialize` request | host → agent | capability request | MUST read; anything but exact `{"version":1}` is invalid params |
| `initialize` response | agent → host | capability answer | on `InitializeResponse._meta` when requested; omitted otherwise; never in `agentCapabilities._meta` |
| `session/prompt` | host → agent | [prompt correlation](#prompt-correlation) | MUST read while enabled; `missing` when absent, `unsupported` when malformed, both before dispatch |
| `session/update` | agent → host | [the envelope](#the-session-update-envelope) | only on the identity-only `session_info_update` carrier |
| `session/request_permission`, `elicitation/create` | agent → host | [action correlation](#action-correlation) | MUST emit while enabled |
| `session/cancel` | host → agent | none | fails the cancel closed before native interrupt; wire-silent |
| every other inbound surface | host → agent | none | invalid params naming `_meta["acp-go.dev/lifecycle"]` |
| every other outbound surface | agent → host | none | never emitted |

### Sequencing and Fail-Closed Rules

- **Identity is `(streamId, sequence)`.** Validate and deduplicate before
  reducing either the extension or its carrier. A rejected envelope never
  delivers its carrier's side effects. Carrier legality is checked before
  ordering.
- `streamId` names the native lifecycle source and incarnation. It does not
  rotate on a reconnect to the same source and does not survive the incarnation
  ([06-lifecycle.md](06-lifecycle.md#lifecycle-stream-fencing)).
- Sequences are positive and contiguous, reserved before delivery so drops
  leave gaps. The opening snapshot may use any positive sequence; consumers
  never assume `1`.
- **A stream begins with `lifecycle_snapshot`**, emitted after the establishing
  response is written and before any other envelope; a connection that is not
  the stdio transport publishes it inline. A configuration answering
  `updatesOutsidePrompt: false` opens one incarnation per prompt: the snapshot
  is the first notification inside the prompt, before `prompt_accepted`, and
  the stream ends when the prompt's process exits. A second snapshot on the
  same `streamId` is `stream_cycle`.
- A completed close fences the stream and every later incarnation of that
  session: every later event, an opening snapshot included, is `stale_stream`.
  A new load or resume opens a separate logical session.
- A valid fresh incarnation supersedes the prior projection even when it holds
  open work. The host owns settlement of abandoned work.
- Lifecycle value equality compares decoded values deeply, ignoring key order
  and whitespace, with numbers compared by exact mathematical value using
  non-expanding normalized decimal forms. Integer-typed members reject
  fractional or exponent spellings.
- **Exact wholesale retransmission is idempotent.** An identical notification
  at an already-reduced identity is suppressed. Any difference is
  `conflicting_duplicate`.
- **A terminal entity admits only no-op restatement.** An update naming a
  terminal entity is judged member-wise: every carried member must equal the
  reduced record. A no-op is suppressed and consumes its sequence; any
  difference is `post_terminal_mutation`, which wins over
  `immutable_identity_change`.
- Locally minted entity ids are provenance, not replay evidence.

The violation vocabulary is closed. Every token is a fail-closed verdict:

| Token | Condition |
|---|---|
| `unsupported_version` | `version` is not exact integer `1`. |
| `unknown_field` | An unknown member of the capability, envelope, event, or correlation value. |
| `malformed_envelope` | A missing, wrong-typed, or over-bound member; a non-positive or non-integer `sequence`; a `stopReason` or `outcome` present on a live transition or absent from an ending `idle` as required; a snapshot listing a terminal entity or a duplicate id within one set; an integer spelled with a fraction or exponent. Checked before ordering, entity resolution, and blocked-cycle consistency. |
| `unknown_event_type` | An `event.type` outside the closed five. |
| `unnegotiated_fact` | An event asserting a fact the capability answer did not claim, or any envelope where the key was omitted. |
| `illegal_carrier` | An envelope on any `sessionUpdate` other than the identity-only `session_info_update`, or a non-object at the key. Wins over every ordering token. |
| `delta_before_snapshot` | Any event other than `lifecycle_snapshot` as a stream's first event. |
| `conflicting_duplicate` | A repeated `(streamId, sequence)` whose content differs. |
| `sequence_gap` | A sequence that skips the expected next number. |
| `sequence_regression` | A sequence below the stream's reduced range that was never reduced. |
| `stream_cycle` | A second `lifecycle_snapshot` on a live stream. |
| `stale_stream` | An event from an incarnation already fenced or superseded, or any event for a session whose close completed. |
| `post_terminal_mutation` | An update carrying any difference from the reduced terminal record of an activity, action, or turn. |
| `immutable_identity_change` | A later update changing an immutable identity field, a first sight missing one, or a `prompt_accepted` reusing an introduced `turnId`. |
| `inconsistent_foreground` | A blocked-cycle rule in [`action_update`](#the-closed-event-set) violated on a structurally valid event. |
| `parent_terminal_before_child` | A parent activity terminalizing while an owned descendant is nonterminal. |
| `child_after_parent_terminal` | A new child activity naming a terminal parent. |
| `unknown_entity` | An event naming an entity the stream never introduced: a `parentId`, `originTurnId`, `owner`, or `state_update.turnId` with no prior introduction, including inside a snapshot's own sets. |

Fail closed has one meaning on each side. A consumer stops reducing that
stream and terminalizes what it holds. An emitter fails the affected prompt or
session rather than emit a stream it knows violates these rules. Neither side
repairs a violation by resequencing, reordering, dropping, or synthesizing an
event. The [fixture battery](08-testing.md#lifecycle-fixtures) pins every
token.

## Native Session Binding

Successful `session/new`, `session/load`, and `session/resume` responses and
each `session/list` entry carry:

```json
{"_meta": {"<vendor>": {"nativeSessionId": "native-conversation-id"}}}
```

`nativeSessionId` is the current native conversation id for direct native
continuation. It is output metadata; clients MUST address ACP methods with
the ACP `sessionId`. The [identity rules](04-sessions-and-store.md#lifecycle-stream-and-incarnation-identity)
govern persistence and replacement.

## Capability `_meta.<vendor>`

```json
{
  "agentCapabilities": {
    "_meta": {
      "<vendor>": {
        "elicitation": {"unstable": true, "scope": "session", "tracks": "ACP v1 elicitation"},
        "rawEvent": {"method": "_<vendor>/rawEvent", "enabledBy": "_meta.<vendor>.rawEvent.enabled", "maxBytes": 65536, "defaultEnabled": false},
        "sessionStore": {"format": "<SessionStoreFormat>", "key": ["sessionId", "subpath"]}
      }
    }
  }
}
```

`elicitation` advertises adapter support only, not that the client opted in.
`unstable` labels this private discovery object; ACP v1 `elicitation/create`
itself is stable.

Advertise structured output under `_meta.<vendor>.structuredOutput` only when
the harness has proven support:

```json
{"structuredOutput": {"config": "_meta.<vendor>.options.outputSchema", "result": "_meta.<vendor>.structuredOutput", "schema": "json_schema"}}
```

A sibling without it fails `outputSchema` at session start with
`{"error":"unsupported","field":"_meta.<vendor>.options.outputSchema"}`.
