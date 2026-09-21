# Overview

## What an `acp-go-*` Package Is

An `acp-go-*` package gives one local harness binary an ACP interface. It
translates ACP requests into the harness protocol, translates harness events
back into ACP updates, and mirrors session state to a host-provided store. It
MUST implement ACP directly around the harness and MUST NOT proxy another ACP
server or expose a general harness SDK.

The host embeds the package by calling:

```go
err := vendoracp.Serve(ctx, input, output,
    vendoracp.WithExecutablePath("/path/to/harness"),
    vendoracp.WithScratchDir("/path/to/scratch"),
    vendoracp.WithSessionStore(store),
)
```

`Serve` runs ACP JSON-RPC over caller-supplied streams. In command-binary mode
those streams are `os.Stdin` and `os.Stdout`; embedded hosts use pipes. ACP
stdout is reserved for JSON-RPC.

## Ownership Boundaries

The package owns:

- the ACP `Agent` implementation
- the harness argument list, environment overlay, protocol, and lifetime
- mapping ACP requests to the harness protocol
- mapping harness events back to ACP `session/update`, permission,
  elicitation, usage, config, and extension surfaces
- durable session mirroring and hydration through `SessionStore`
- conformance tests and public docs

The package does not own:

- isolation of any kind: identity, containment, namespaces, descendant
  reaping, filesystem scope, or quarantine
- the host's store backend or workspace scoping
- global OpenTelemetry setup
- harness authentication beyond running the harness in the home it was given

## Family Principles

### Interface, Not Isolation

A sibling inherits the environment it was started in: every variable, the
`PATH`, the home, the cwd. Its only power over the harness environment is to
overlay variables when it launches the harness, in the merge order fixed in
[01-public-api.md](01-public-api.md#process-environment). It MUST NOT scrub,
allowlist, or refuse inherited or caller-supplied names, and it MUST NOT make
or verify any claim about process or filesystem containment. Isolation is the
responsibility of whoever executes the sibling.

The harness launches as a plain child process: its own process group,
dedicated stdin, stdout, and stderr pipes, the merged environment, and the
session cwd. Close signals the group and waits for the root process.

The harness's own security features are the harness's: sandbox and approval
policies pass through as session options and are never reimplemented.

### One Shape, One Core

Shared behavior lives once in this module
([01-public-api.md](01-public-api.md#this-module)). A sibling contains only
vendor-specific code: its public options, its native protocol, its event
mapping, and its store format. Common concepts keep identical names and
signatures across siblings. Sibling public docs never reference this
repository, another sibling, or a host.

### Current Shape Only

Every sibling implements only the API, wire, command, and storage shapes this
contract declares. Code, tests, and documentation MUST NOT recognize,
translate, repair, backfill, preserve, or describe an alternate shape. Database
creation defines the current schema directly from an empty database. Code that
exists only to serve a prior shape is removed with its tests.

### Native Runtime Strategies

Every sibling selects exactly one strategy and records it in the
[registry](registry.md#native-surfaces-and-process-models):

- **session runtime** — one native process serves one loaded ACP session, is
  started when the session is established, is relaunched lazily against the
  same native state when it has exited, and is stopped by `session/close`.
- **multiplexed runtime** — one Agent-owned native process serves many
  logical ACP sessions, is started by the first operation that needs it, and
  is replaced by the next explicit operation after it exits.
- **prompt runtime** — one native process serves one prompt: it attaches to
  the remote conversation, submits the prompt, and is reaped before terminal
  publication. Restore and observation attach without input. Nothing runs
  between prompts, so `updatesOutsidePrompt` is `false`.

`session/close` releases only the addressed logical session. `Agent.Close`
closes the Agent-owned runtime and every remaining session. A shared native
runtime MUST never make callbacks, cancellation, cwd, permissions, model
state, or store data process-global when the corresponding ACP fact is scoped
to a logical session.

### Family-Global Reserved Literals

Exactly three `acp-go.dev/*` literals are reserved. They live only in `_meta`,
carry only their defined fields, and are never placed under `_meta.<vendor>`.
Adding a literal requires a family contract amendment.

| Literal | Direction | Advertised by |
|---|---|---|
| [`acp-go.dev/mediaEnvelope`](02-wire-contract.md#media-envelope) | agent → host at initialize | every sibling, unconditionally |
| [`acp-go.dev/handoff`](02-wire-contract.md#handoff-envelope) | host → agent per image block | every sibling, exactly while `WithInputHandoffRoot` is set |
| [`acp-go.dev/lifecycle`](02-wire-contract.md#lifecycle-envelope) | bilateral at initialize; agent → host per session update and permission/elicitation request; host → agent per prompt | every sibling |

The first two advertisements use `agentCapabilities._meta`; lifecycle uses
`InitializeResponse._meta`. The literals are the `wire` package constants; a
sibling never spells them.

### Identity Constants

Every sibling claims exactly one of each:

| Constant | Pattern | Example |
|---|---|---|
| Vendor key | `<vendor>` | `pi` |
| Package name | `<vendor>acp` | `piacp` |
| Import path | `github.com/savid/acp-go-<vendor>` | `github.com/savid/acp-go-pi` |
| Binary name | `acp-go-<vendor>` | `acp-go-pi` |
| Extension prefix | `_<vendor>/` | `_pi/` |
| Store format | `<vendor>-<native-state-kind>-v1` | `pi-session-jsonl-v1` |

### Shared Pins

The ACP SDK and the Go directive are pinned family-wide in the
[README](../README.md#shared-pins), and every sibling requires one shared
version of this module. Wire behavior follows protocol version and
capabilities; `Unstable` Go symbols may back stable methods only where this
contract permits it. A pin move covers every sibling and reruns every
conformance suite.

## Uniform Error Shapes

The `wire` package constructs every shape below; a sibling never assembles one
by hand.

Unsupported or absent methods return `acp.NewMethodNotFound(method)`:

```json
{"code": -32601, "message": "Method not found", "data": {"method": "<method>"}}
```

Unsupported option fields and malformed values return:

```go
acp.NewInvalidParams(map[string]any{"error": "unsupported", "field": "<json path>"})
```

A required key the host omitted, such as the lifecycle prompt correlation on an
enabled connection, returns:

```go
acp.NewInvalidParams(map[string]any{"error": "missing", "field": "<json path>"})
```

`unsupported` names a value that is present and refused; `missing` names a
value the contract requires and the caller left out. They are never collapsed.

When a method's session lookup finds no eligible session:

```go
acp.NewInvalidParams(map[string]any{"error": "unknown session", "field": "sessionId"})
```

After `Agent.Close`, every request returns:

```go
acp.NewInvalidRequest(map[string]any{"error": "agent closed"})
```

The agent accepts no further request on that connection; the token is closed
and carries no other member.

A native turn that fails — the harness dies mid-turn, the transport breaks, or
the provider rejects the turn — terminates `session/prompt` with `-32603` and
no stop reason:

```json
{
  "code": -32603,
  "message": "Internal error",
  "data": {
    "error": "<vendor>_turn_failed",
    "cause": "process_exit",
    "message": "<real native cause text>",
    "statusCode": 429,
    "providerCode": "<provider error code>"
  }
}
```

`cause` is one of `process_exit`, `transport`, `provider`, or a vendor cause
the [registry](registry.md#turn-failure) enumerates. `message` carries the
real native cause and is never a placeholder or a bare `EOF`; it is valid
UTF-8 bounded by `wire.TurnFailed`: at most 18 KiB for `process_exit`,
including the complete retained 16 KiB stderr tail, and at most 2048 bytes
for other causes. `statusCode` and `providerCode` appear only when the harness
supplies them. Semantics are in [04-behavior.md](04-behavior.md#native-turn-failure).

Every other `-32603` a sibling emits carries a closed `data.error` token and
the constant `message`:

| Token | Condition | Additional members |
|---|---|---|
| `<vendor>_invalid_options` | The agent was constructed with options it will not serve under. `NewAgent` returns no error; the verdict is delivered at `initialize` and every session-establishing entry point. | `field` naming the refused option. |
| `<vendor>_restore_failed` | A stored session could not be restored: on `session/load`, `session/resume`, or a lazy relaunch that re-hydrates native state before a prompt or config-option change. The entry is neither deleted nor tombstoned. | none |
| `<vendor>_runtime_unavailable` | A shared native runtime the operation needs is gone and the sibling could not start a replacement. A runtime that merely exited is not this token; the next explicit operation starts one replacement ([05-lifecycle.md](05-lifecycle.md#shared-runtime-loss)). | none |
| `<vendor>_session_poisoned` | The addressed session is poisoned ([03-sessions-and-store.md](03-sessions-and-store.md#store-formats)) and refuses every operation but `session/close` and `session/delete`. | `cause`, a closed token the sibling documents |
| `<vendor>_internal_failure` | Every failure the sibling cannot classify above: a native process that fails to start carries `class: "native_start"`; a native [account-usage](02-wire-contract.md#account-usage) read that fails carries `class: "account_usage"`; a failed commit on session establishment, close, delete, list, or a config-option change carries the bare token. | Optional `class`; account-usage HTTP failures also carry `statusCode` and optional `retryAt` as defined in [02](02-wire-contract.md#account-usage) |

The data MUST NOT carry a bare unprefixed token, joined Go error text, native
text, or a `message` member. [registry.md](registry.md#known-deviations)
records which tokens each sibling emits.
