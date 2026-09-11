# Behavior

## Config Options

Model, mode, reasoning, and model parameters use ACP session config options
with `type: "select"`. No boolean config options.

| Option kind | Config id examples | Category |
|---|---|---|
| Model selector | `model` | `model` |
| Mode or agent selector | `mode`, `agent` | `mode` |
| Effort or reasoning selector | `effort`, `reasoning`, `thought_level` | `thought_level` |
| Other model parameters | `serviceTier`, `personality` | `model_config` |

- The model selector id is exactly `model` with category `model`.
- Native modes are config options with category `mode`, never ACP session
  modes; response `modes` stays empty.
- `session/set_config_option` accepts only the value-id variant. Boolean
  payloads, unknown `configId`, and unknown `value` return invalid params
  naming the offending member.
- Config options appear in `session/new`, `session/load`, and `session/resume`
  responses and in `config_option_update`.

## Model Metadata

When authoritative model metadata exists, attach it under each model select
value's `_meta.<vendor>`:

| Key | Type | Meaning |
|---|---|---|
| `modelId` | string | Invokable model id as shown in the value. |
| `contextWindow` | number | Context window in tokens. |
| `maxOutputTokens` | number | Maximum output tokens. |
| `supportedEffortLevels` | string array | Supported reasoning or effort values. |

Omit absent metadata; never zero-fill. Metadata comes only from the harness's
programmatic surface. Static catalogs, model-name heuristics, and output
scraping never supply it. No modality fields, `capabilities` list, or
generator flags appear per model; native modality data feeds only the
internal [selected-model gate](#image-capability-model).

### Catalog Membership

A **native entry** is one the harness enumerates for itself. A **configured
entry** is one the deployment named: the default model and a host-listed id
from `WithConfiguredModels`. Configured entries are always published, after
the native rows, as the id alone unless a native row carries it.

Membership never gates selection: a value absent from a menu still travels to
the harness. An adapter never reads a provider endpoint the harness does not
itself address. How each sibling builds its catalog is in the
[registry](registry.md#how-each-model-catalog-is-built).

## Images

Images use standard ACP image content or resource links. Custom image types,
`outputModalities`, and client `canReceiveImages` negotiation are forbidden.
Wire shapes and errors are in [03-wire-contract.md](03-wire-contract.md#image-content);
limits in [02-public-api.md](02-public-api.md#effective-image-limits). The
`image` package runs every gate; a sibling supplies only its native input
shape and output surfaces.

### Image Capability Model

- `promptCapabilities.image: true` means exactly: this adapter accepts a
  standard image block and maps it to its harness. It never means every model
  sees images or that generation exists.
- Each adapter keeps an internal selected-model image-input state of
  `supported`, `unsupported`, or `unknown`, sourced only from an authoritative
  field of the harness. Model-name inference and static catalogs yield
  `unknown`. The gate runs at prompt time against the selected model and is
  never cached across a switch. `unsupported` rejects pre-turn with
  `unsupported_by_model`; `supported` and `unknown` forward.
- Generator availability is never advertised or inferred from tool names.

### Image Input

- Handoff reads hold an `os.Root` on the configured root and open paths
  relative to it, so confinement is atomic with the open. On Unix the open carries
  `O_NONBLOCK`. Stat the descriptor, never the path, and require a regular
  file. Verdicts: regular file and relative in-root symlink read; absolute,
  escaping, or dangling-absolute symlinks and non-regular files are
  `path_not_allowed`; absent names and dangling relative symlinks are
  `missing_file`. A hardlink to an outside file reads, and the digest still
  binds.
- Validate size before I/O, read at most declared `sizeBytes` plus one, and
  verify exact size and SHA-256 before forwarding. Thread the caller's context
  through validation. Handoff paths never appear in the native request.
- The input allowlist is exactly `image/png`, `image/jpeg`, `image/gif`,
  `image/webp`. MIME routing lowercases and strips parameters; allowlist
  membership compares the declared string, so `IMAGE/PNG` fails
  `invalid_media_type`.
- Every embedded `resource.blob` is either refused before decode with
  `{"error":"unsupported","field":"prompt.resource"}` or strictly decoded,
  bounded by the per-image gate, and counted toward the aggregate. Blobs with
  an `image/` MIME run the full chain; the sibling's disposition for other
  MIMEs is recorded in the registry.
- Animation is rejected by structural inspection. Dimension and animation
  checks never decode a raster and allocate nothing proportional to declared
  size. No decode-allocation gate: large declared rasters are the harness's
  responsibility.
- Preserve relative text and image order; multiple images never collapse.

### Typed Image Output

The consumer receives standard ACP image content with embedded base64 and a
trustworthy MIME whatever the native artifact shape:

| Native result | Mapping |
|---|---|
| Base64 plus trustworthy MIME | Decode, validate, enforce limits, emit one image |
| Data URL | Parse, validate, enforce limits, emit one image |
| Remote URI only | Emit one `resource_link`; never fetch |

- Never emit both an image and a resource link for one artifact. Signed URLs
  are redacted from diagnostics.
- Output is not format-allowlisted: any sniffable raster is emitted with its
  sniffed MIME. `not_a_raster` applies only to an artifact the harness
  presented as an image whose bytes are not one. SVG and PDF are never emitted
  as image blocks.
- **Agent images append.** Each agent image travels alone in its own
  `agent_message_chunk`, bounded by `MaxOutputBytesPerImage`.
- **Tool content replaces.** A content-bearing `tool_call_update` carries the
  tool call's complete current content array; a status-only update omits
  `content`. The frame unit is the whole array, bounded by
  `MaxOutputBytesPerToolCall`.
- Provenance follows the native origin. An artifact appears once per content
  array and once across a turn's agent chunks, keyed by native identity plus
  fingerprint.

### Replay

`session/load` replay delivers the same image content as live output, decoded
through the same output gate from the sibling's durable source: the mirrored
native rows when they carry the bytes, otherwise a wrapper-owned artifact
store keyed by native identity plus fingerprint. Replay of an artifact the
source no longer holds fails the whole load with the closed non-prompt error
vocabulary, never a silent hole. Raw events and logs carry safe metadata only,
never a second copy of the base64.

### Image Failure Fatality

- **Native failures follow the native outcome.** A native tool failure the
  harness continues past is emitted as the failed tool state; a provider
  rejection after turn start is the native turn failure.
- **An adapter refusal is reported, not fatal.** `invalid_base64`,
  `not_a_raster`, `media_type_mismatch`, `missing_file`, `path_not_allowed`,
  and `too_large` are reported as the refused artifact's own result content in
  the position the image would have occupied, and the turn continues. An
  adapter that owns the tool call's status also reports it `failed`.
- **The refusal message is a compile-time constant** per verdict token, the
  `image` package's guidance strings, with nothing about the input
  interpolated.
- **A genuine internal failure stays fatal.** `storage_failed` fails the prompt
  with the `stage:"image_output"` envelope, after a status-only failed tool
  update on tool provenance.
- Never emit an image block until its bytes, MIME, limits, and durable replay
  representation have passed validation.

## Usage Updates

Context-window and cost usage is authoritative only through ACP
`usage_update`. Vendor token breakdowns MAY appear under `_meta.<vendor>` for
debugging; hosts must not depend on them. Optional `_meta.<vendor>.messageId`
correlates usage to a streamed message.

`size` is the model's true context window in tokens, never fabricated. An
adapter that cannot determine it sets `size: 0`.

## Assistant Text Streaming

`agent_message_chunk` and `agent_thought_chunk` are append-only deltas:

- A sibling never emits text it already streamed in the same turn. When the
  harness delivers a terminal full-message frame after deltas, the adapter
  emits only the suffix the deltas did not carry.
- A harness that delivers only a terminal frame produces exactly one chunk.
- Several native assistant messages in one turn each produce their text once,
  in native order, deduplicated on identity.

## Native Turn Failure

A native turn failure is any way a turn ends other than a clean stop reason.

- **Failure is a JSON-RPC error, never a stop reason.** The valid terminal stop
  reasons are `end_turn`, `max_tokens`, `max_turn_requests`, `refusal`, and
  `cancelled`.
- Failures use the [uniform shape](00-overview.md#uniform-error-shapes)
  through `wire.TurnFailed`. `statusCode` and `providerCode` come from the
  harness.
- **Recover the cause before surfacing.** Parse the result frame or read the
  exit status and stderr tail so `message` names the real cause.
- **The session stays addressable and retriable.** A failure neither removes,
  tombstones, nor poisons the session. The next prompt re-drives the turn,
  relaunching the native process lazily where the lifecycle requires. Only a
  wrapper-invariant break poisons.
- **Malformed native lines are not fatal.** Skip-and-count or surface a
  structured `cause:"transport"` failure; never hang.
- **Cancellation stays distinct.** A native error observed while the turn is
  cancelled maps to stop reason `cancelled`; the cancel guard runs before
  failure mapping.
- **Turn deadline.** `WithTurnTimeout` expiry aborts the native turn and
  returns `cause:"timeout"`.

## Lifecycle State Machines

The scopes nest:

```text
Session                  native conversation, one incarnation, one ordered stream
├── Submission           one accepted client prompt or command
├── Turn                 one foreground running → idle cycle
└── Activity tree        task | subagent
    └── Action           one permission or elicitation awaiting an answer
```

A sibling emits an event only from a structured native signal, through the
`lifecycle` package's emitter, which validates every envelope before delivery.

### The Foreground Cycle

- Validation failure before native dispatch creates neither submission nor
  turn.
- A prompt-origin turn is created at acceptance, enters `running`, passes
  through any blocking `requires_action`, and ends at `idle`. Accepted failure
  before `running` terminalizes directly at `idle`.
- An agent-origin turn opens only from an `activity`-caused `idle → running`
  transition. A `session`-caused transition never creates a turn.
- A terminal turn never reopens.
- Completion requires the first ending `idle` and, for a prompt-origin turn,
  the matching prompt response or error. The terminal event follows the
  [foreground commit](04-sessions-and-store.md#lifecycle-commit-points) and
  precedes the response.
- If the foreground commit fails, fail the prompt without terminal `idle`, end
  the incarnation, and let the next incarnation's snapshot state the truth.

### Activities

An activity is one owned unit of native work with a stable id and kind `task`
or `subagent`. Identity and parentage are immutable from first sight. Terminal
is final. Children terminalize before parents. An activity is created only
from independent structured native evidence, never from a command invocation,
transcript text, or a `plan_update`, which is presentation state. Visible
progress remains ordinary tool-call updates.

### Actions

Actions follow the closed states and blocking order in
[03-wire-contract.md](03-wire-contract.md#the-closed-event-set). The sibling
registers the wire request before announcing the action and resolves it
exactly once. Cancellation, connection loss, and the
[shutdown ladder](06-lifecycle.md#shutdown-ladder) terminalize it; an
unanswerable action never remains `pending`.

### Violation Behavior

A sibling that observes a native sequence it cannot express truthfully fails
the affected prompt or session rather than emit an event it cannot support. It
never resequences, drops, coalesces, or synthesizes an event.

## Permissions and Elicitation

| Native event | ACP surface | Cancellation value |
|---|---|---|
| Tool permission prompt | `session/request_permission` | `{"outcome":"cancelled"}` |
| Non-permission user question | `elicitation/create` | `{"action":"cancel"}` or JSON-RPC `-32800` |

Record `clientCapabilities` at initialize. An omitted or `null` `elicitation`
advertises nothing; `elicitation: {}` advertises zero modes; a non-null `form`
or `url` advertises that mode. Send form elicitation only when `form` is
non-null and URL elicitation only when `url` is non-null, always with an
explicit `mode`. Form mode never requests credentials; use URL mode or fail.
If the required mode is absent, decline or cancel the native question
deterministically and complete the prompt with exactly one terminal result.

| Client capability | Form | URL |
|---|---:|---:|
| omitted or `null` | no | no |
| `{}` | no | no |
| `{"form":null,"url":null}` | no | no |
| `{"url":{}}` | no | yes |
| `{"form":{}}` | yes | no |
| `{"form":{},"url":{}}` | yes | yes |

Permission requests are turn-scoped and in-memory. On cancel, shutdown, or
native stream failure, answer pending native permissions as cancelled before
aborting the native turn where the surface allows. While the lifecycle
capability is enabled, both surfaces carry their action correlation and are
announced as ordered actions.

## Slash Commands

Slash commands are ACP's generic command surface and the only cross-provider
command contract. Advertise only what the harness natively discovers through a
documented per-session surface. Never fabricate commands or ship static
catalogs.

- Commands appear only in `available_commands_update` with native `name`,
  `description`, and an optional input hint. Each update is a full
  replacement. A sibling with a native discovery surface emits an initial
  snapshot for every established session, explicitly empty when the catalog is
  empty, after the establishing response has been written. A sibling with no
  native surface emits nothing. A relaunch re-fetches and re-emits.
- One shared sanitizer rejects empty names, names containing `/`, invalid
  UTF-8, and Unicode whitespace, control, or format runes. Names are emitted
  without a leading `/`.
- Invocation enters as `/name args` prompt text and reaches the harness as
  that text. The adapter does only the routing a documented native command
  API requires. Unrecognized `/text` is plain prompt text.
- A native invariant-breaking event poisons the session and clears advertised
  commands with an empty update.

## Session Delete

`session/delete` is idempotent and silently succeeds for missing sessions.

1. Write a durable tombstone first.
2. Close and cancel any active session with the same id.
3. Delete store entries. Native state is left in place
   ([Hydrate In](04-sessions-and-store.md#hydrate-in)).
4. Hide tombstoned sessions from `session/list`, `session/load`, and
   `session/resume`.
5. Retry partial cleanup on future list, load, resume, and delete paths.
6. Return partial cleanup errors only after the tombstone is durable.

A `session/load` or `session/resume` that passed its entry check re-checks the
tombstone under the lock that installs the session, tears down a prepared
replacement when a delete completed meanwhile, and never clears a deletion
marker. A deleted session is wire-indistinguishable from one that never
existed: session-scoped requests answer the unknown-session error, and
`session/cancel` on such a session is a wire-silent no-op.
