# Public Go API

Use package name `<vendor>acp`, import path
`github.com/savid/acp-go-<vendor>`, vendor key `<vendor>`, and extension prefix
`_<vendor>/`.

## This Module

`github.com/savid/acp-go-core` holds every behavior that is identical across
siblings. A sibling imports it; it never copies it. Hosts import it for the
types that cross the sibling boundary. The module owns:

| Package | Contents |
|---|---|
| `acpcore` | `SessionStore` and its types, `InMemorySessionStore`, and `SessionStoreTimeout`, the bound on one store call ([04-sessions-and-store.md](04-sessions-and-store.md#store-api)). |
| `acpcore/sessionlog` | Bounded atomic native-log and session-configuration commits, strict record decoding, and native-log reconciliation. |
| `acpcore/observer` | OpenTelemetry spans and metrics with sibling identity supplied at construction, and prompt response usage mapping. |
| `acpcore/observer/exporters` | The `OTEL_*` exporter, propagator, and log-bridge wiring a sibling's command binary hands to its Agent; library code never imports it. |
| `acpcore/storetest` | The store contract battery a host store runs against itself. |
| `acpcore/lifecycle` | The `acp-go.dev/lifecycle` capability, envelope, event types, the reducer, the session publisher, and the embedded [fixture battery](08-testing.md#lifecycle-fixtures). |
| `acpcore/process` | Environment merge ([Process Environment](#process-environment)), executable resolution, child launch with its own process group, dedicated stdio pipes (or a caller-owned stdout file for a short-lived native command), and a bounded stderr tail, signal-and-wait shutdown, seed-file writes and the `-seed-file` flag value, and the exclusive native-home file lock. |
| `acpcore/wire` | Uniform error constructors, raw-event framing and sequencing, the [request builders](#request-builders), session metadata cloning and validation, native binding metadata through `NativeSessionMeta`, the session admission gate, restore reservation and list pagination, the slash-command sanitizer, the session title and context-resource text rules, ACP transport publication ordering, the reserved literals with the collision check for host-supplied `_meta`, and the [account-usage](03-wire-contract.md#account-usage) request decoder, response shape, and read bound. |
| `acpcore/usage` and provider subpackages | Bounded provider account readers returning `wire.AccountUsageResponse`, verification that native credentials and routes remain stable during a read, and the bounded model-list read of a forwarding gateway (`usage/gateway.Models`); callers supply credentials and an optional HTTP transport. Readers never acquire credentials or launch a harness. |
| `acpcore/image` | Decoded-byte limits, the media envelope, handoff validation, the image input gate, and output decoding ([03-wire-contract.md](03-wire-contract.md#image-content)). |

The exported API is the module's own Go documentation. This contract fixes
what belongs there and what the siblings do with it. A sibling MUST NOT
re-export a core type under its own name.

## Agent Surface

Every sibling exports this agent surface, plus the
[extension constants](03-wire-contract.md#extension-constants) its reads
require. `Agent` has unexported fields only.

```go
package vendoracp

import (
    "context"
    "encoding/json"
    "io"

    "github.com/coder/acp-go-sdk"
)

const RawEventMethod = "_<vendor>/rawEvent"

type Agent struct { /* unexported fields only */ }

func NewAgent(opts ...Option) *Agent
func Serve(ctx context.Context, input io.Reader, output io.Writer, opts ...Option) error

func (a *Agent) Close() error
func (a *Agent) Initialize(ctx context.Context, params acp.InitializeRequest) (acp.InitializeResponse, error)
func (a *Agent) Authenticate(ctx context.Context, params acp.AuthenticateRequest) (acp.AuthenticateResponse, error)
func (a *Agent) Logout(ctx context.Context, params acp.LogoutRequest) (acp.LogoutResponse, error)
func (a *Agent) NewSession(ctx context.Context, params acp.NewSessionRequest) (acp.NewSessionResponse, error)
func (a *Agent) LoadSession(ctx context.Context, params acp.LoadSessionRequest) (acp.LoadSessionResponse, error)
func (a *Agent) ResumeSession(ctx context.Context, params acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error)
func (a *Agent) ListSessions(ctx context.Context, params acp.ListSessionsRequest) (acp.ListSessionsResponse, error)
func (a *Agent) Prompt(ctx context.Context, params acp.PromptRequest) (acp.PromptResponse, error)
func (a *Agent) Cancel(ctx context.Context, params acp.CancelNotification) error
func (a *Agent) CloseSession(ctx context.Context, params acp.CloseSessionRequest) (acp.CloseSessionResponse, error)
func (a *Agent) UnstableDeleteSession(ctx context.Context, params acp.UnstableDeleteSessionRequest) (acp.UnstableDeleteSessionResponse, error)
func (a *Agent) SetSessionConfigOption(ctx context.Context, params acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error)
func (a *Agent) SetSessionMode(ctx context.Context, params acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error)
func (a *Agent) HandleExtensionMethod(ctx context.Context, method string, params json.RawMessage) (any, error)
```

Rules:

- `SetSessionMode` exists only because the SDK interface requires it. It
  returns method-not-found and is not advertised.
- `Authenticate` and `Logout` exist only because the SDK interface requires
  them. `authMethods` is always empty, `authenticate` answers `-32602`
  `{"methodId": <the id sent>}`, and `logout` returns method-not-found. The
  harness authenticates itself in its own home, outside ACP.
- No exported `Session` type and no session methods outside `Agent`.
- No fork method of any kind. Stable `session/fork` returns method-not-found.
- `HandleExtensionMethod` answers only the inbound extension methods the
  sibling advertises under [`_meta.<vendor>`](03-wire-contract.md#capability-_metavendor)
  and returns method-not-found for every other method. The only outbound
  extension surface is the `RawEventMethod` notification.
- Unsupported methods and option fields use the
  [uniform error shapes](00-overview.md#uniform-error-shapes).

## Process Options

Every sibling exports these option types and constructors.

```go
import (
    "log/slog"
    "time"

    "github.com/savid/acp-go-core"
    "go.opentelemetry.io/otel/metric"
    "go.opentelemetry.io/otel/propagation"
    "go.opentelemetry.io/otel/trace"
)

type Option func(*Options)

type ConcurrencyLimits struct {
    MaxActiveSessions        int
    MaxConcurrentClientCalls int
}

type ImageLimits struct {
    MaxInputBytesPerImage     int64
    MaxInputBytesPerPrompt    int64
    MaxOutputBytesPerImage    int64
    MaxOutputBytesPerToolCall int64
}

func WithLogger(logger *slog.Logger) Option
func WithAgentName(name string) Option
func WithAgentTitle(title string) Option
func WithAgentVersion(version string) Option
func WithExecutablePath(path string) Option
func WithHome(path string) Option
func WithScratchDir(path string) Option
func WithInputHandoffRoot(dir string) Option
func WithDefaultModel(model string) Option
func WithConfiguredModels(ids []string) Option
func WithEnv(env map[string]string) Option
func WithTracerProvider(provider trace.TracerProvider) Option
func WithMeterProvider(provider metric.MeterProvider) Option
func WithTextMapPropagator(propagator propagation.TextMapPropagator) Option
func WithSessionStore(store acpcore.SessionStore) Option
func WithConcurrencyLimits(limits ConcurrencyLimits) Option
func WithImageLimits(limits ImageLimits) Option
func WithSeedFiles(files map[string]string) Option
```

Rules:

- `WithExecutablePath` is the only executable selector. The adapter resolves
  it through `process.ResolveExecutable` against the
  [base environment](#process-environment) before session path directories
  apply, so a session directory can never shadow the harness. A bare name is
  searched on that `PATH`; a path containing a separator is used as given.
- `WithHome` names the harness's native config, auth, and runtime root and is
  mapped to that harness's own home variable, recorded in the
  [registry](registry.md#vendor-process-options). Unset, the harness resolves
  its home from the inherited environment exactly as it would from a shell.
  Set, it MUST be absolute. `Home` never acts as a scratch parent.
- `WithScratchDir` is the parent for all ephemeral state, such as extension
  staging. Empty means `os.TempDir()`; otherwise it MUST be absolute, and a
  relative path is a construction failure. A missing directory is created
  `0700`. Every ephemeral path is created through the sibling's
  single scratch accessor with a stable `acp-go-<vendor>-<purpose>-*` prefix
  so a host can sweep orphans. A sibling that allocates no ephemeral state
  accepts the option and allocates nothing; the
  [registry](registry.md#ephemeral-scratch) records which siblings allocate
  and what they put there.
- `WithInputHandoffRoot` is the only host-supplied read root and opts in to
  [handoff image input](05-behavior.md#image-input). It MUST be absolute; a
  relative path is a construction failure. The adapter never writes, moves, or
  deletes files under it and never exposes its paths to the harness.
- `WithConfiguredModels` names the models the host lists explicitly. Each id
  is a configured entry under [catalog membership](05-behavior.md#catalog-membership).
  An empty, whitespace, or duplicate id fails construction.
- `WithImageLimits` counts **decoded** bytes. Omitted, every field is 6 MiB
  (6,291,456). A field set to zero disables that policy limit and never
  bypasses the frame clamp or a native ceiling. A negative field is a
  construction failure. Every gate and advertisement uses the
  [effective limits](#effective-image-limits), never the raw fields.
- `WithSeedFiles` writes relative files into the resolved native config root
  before launch. Absolute paths, `..` escapes, and empty keys fail at session
  start with the unsupported error naming `seedFiles`. Each seed root keeps
  `.seed-manifest.json` listing the files the adapter manages; an existing file
  not in the manifest is never overwritten and fails with the same error.
  Secrets go in `env` and are referenced from seeded files by variable
  indirection. Native config injection is preferred where the harness offers
  it; the [registry](registry.md#vendor-process-options) records where.
- Construction failures use `<vendor>_invalid_options`
  ([00-overview.md](00-overview.md#uniform-error-shapes)): `NewAgent` returns
  no error, and `Initialize` and every session-establishing entry point deliver
  the verdict before native launch.
- Adapter-owned caches hold only material rebuilt from the adapter binary,
  never credentials, session state, or transcripts. They live under scratch,
  are content-addressed and immutable, and are recorded in the registry.
- Vendor-specific process options are prefixed `With<Vendor>...`. No sibling
  exposes a native-version option.
- Common options mean the same thing in every sibling. `Options` may add
  vendor fields, but common fields are named and typed exactly as above.

### Process Environment

The harness environment is one merge through `process.Environment`, later
wins:

1. the sibling's own process environment, read once at construction;
2. `WithEnv`, the static Agent-scoped overlay;
3. the session `env` from `_meta.<vendor>.options.env`;
4. the keys the sibling owns because of how it launches the harness: the home
   variable when `WithHome` is set, and any process marker it needs.

The only names removed are the sibling's own `ACP_GO_<VENDOR>_INTERNAL_*`
markers, dropped from the inherited, agent, and session layers before the
owned keys apply.
Nothing else is scrubbed, allowlisted, or refused by name. A name MUST be
non-empty and contain neither `=` nor NUL; a value MUST contain no NUL. An
invalid entry in `WithEnv` fails construction; one in session `env` fails the
request with `{"error":"unsupported","field":"_meta.<vendor>.options.env.<key>"}`.
Empty values are forwarded as `KEY=`. Native file paths that are relative
MUST be resolved against the native process cwd for adapter file I/O.

`PATH` is composed last: the session's ordered `extraPathDirs`, then the
`PATH` the merge produced, joined with `os.PathListSeparator` and omitting
empty components. Executable resolution uses steps 1 and 2 only.

On a multiplexed runtime the shared process receives steps 1, 2, and 4; each
logical session's `env` and `extraPathDirs` travel on that session's own
native start or resume request and never enter the shared process
environment.

### Effective Image Limits

Every gate and advertisement uses the same resolved limits.

| Field | Effective value |
|---|---|
| `MaxOutputBytesPerImage` | `min(policy-or-unbounded, 7,864,155)` |
| `MaxOutputBytesPerToolCall` | `min(policy-or-unbounded, 7,864,155)` |
| `MaxInputBytesPerImage` | `min(policy-or-unbounded, 7,864,155, native per-image ceiling if any)` |
| `MaxInputBytesPerPrompt` | Policy limit, or `0` when disabled |

The [frame clamp](03-wire-contract.md#output-frame-clamp) fixes 7,864,155.
Per-image input is always finite; handoff declarations are checked against it
before reading.

## Per-Session Vendor Options

Every sibling has a per-session options struct named `<Vendor>Options`, the
only typed builder for `_meta.<vendor>.options`.

```go
type VendorOptions struct {
    Model         string            `json:"model,omitempty"`
    Env           map[string]string `json:"env,omitempty"`
    ExtraPathDirs []string          `json:"extraPathDirs,omitempty"`

    // Vendor-specific fields follow. Add only proven native settings.
}

type VendorOption func(*VendorOptions)

func NewVendorOptions(opts ...VendorOption) VendorOptions
func (options VendorOptions) Meta() map[string]any
func WithVendorModel(model string) VendorOption
func WithVendorEnv(env map[string]string) VendorOption
func WithVendorExtraPathDirs(dirs ...string) VendorOption
func WithSessionVendorOptions(options VendorOptions) SessionRequestOption
```

`Meta` returns exactly `{"<vendor>": {"options": {...}}}` with only the
non-zero supported fields; a boolean the caller set explicitly travels even
when `false`, and the [registry](registry.md#vendor-session-options) records
which fields carry explicit presence. Maps and slices are cloned before storing
or returning. Unknown own-namespace keys fail closed with the unsupported error.
Only siblings with native structured output expose `OutputSchema map[string]any`
and `WithVendorOutputSchema(schema map[string]any) VendorOption`. The
[registry](registry.md#vendor-session-options) records native support.

### Session Environment and PATH

`Env` and `ExtraPathDirs` are per-session inputs and part of resume identity
([06-lifecycle.md](06-lifecycle.md#session-environment-and-path-scoping)).

- `ExtraPathDirs` accepts `[]string` and `[]any` with string elements. Entries
  MUST be non-empty, absolute, and free of `os.PathListSeparator`; a rejection
  names `_meta.<vendor>.options.extraPathDirs[i]`. Order and duplicates are
  preserved. An omitted key is an empty list on a new session; recovery
  reconstructs it from the session record.
- `Env` follows the [process environment](#process-environment) rule. `PATH`
  is an ordinary key: a session that sets it replaces the inherited `PATH`
  before `extraPathDirs` are prepended.
- The same validation, `process.ValidateNames` and
  `process.ValidateExtraPathDirs`, runs on new, load, and resume. Values are
  cloned at every boundary so caller mutation never changes a live session.
- `Validate<Vendor>SessionMeta(meta map[string]any) error` runs the namespace
  parsing without an Agent and returns the same refusal or nil.

## Request Builders

The vendor-free request builders live once in `wire`; a sibling exports only
the option constructors that carry its own namespace, and
`WithSessionOutputSchema` only where its
[registry](registry.md#vendor-session-options) row records structured output.

```go
// package wire
type SessionRequestOption func(*SessionRequestConfig)

func NewSessionRequest(cwd string, opts ...SessionRequestOption) acp.NewSessionRequest
func LoadSessionRequest(sessionID acp.SessionId, cwd string, opts ...SessionRequestOption) acp.LoadSessionRequest
func ResumeSessionRequest(sessionID acp.SessionId, cwd string, opts ...SessionRequestOption) acp.ResumeSessionRequest
func DeleteSessionRequest(sessionID acp.SessionId) acp.UnstableDeleteSessionRequest

func WithSessionAdditionalDirectories(paths ...string) SessionRequestOption
func WithSessionMeta(meta map[string]any) SessionRequestOption
func WithSessionMetaValue(meta map[string]any) SessionRequestOption

func PromptRequest(sessionID acp.SessionId, blocks ...acp.ContentBlock) acp.PromptRequest
func TextPromptRequest(sessionID acp.SessionId, text string) acp.PromptRequest
func CancelRequest(sessionID acp.SessionId) acp.CancelNotification
func SetConfigOptionRequest(sessionID acp.SessionId, configID acp.SessionConfigId, value acp.SessionConfigValueId) acp.SetSessionConfigOptionRequest

type ListSessionsRequestOption func(*acp.ListSessionsRequest)

func ListSessionsRequest(opts ...ListSessionsRequestOption) acp.ListSessionsRequest
func WithListSessionsCwd(cwd string) ListSessionsRequestOption
func WithListSessionsCursor(cursor string) ListSessionsRequestOption
func WithListSessionsMeta(meta map[string]any) ListSessionsRequestOption

// package <vendor>acp
func WithSession<Vendor>Options(options <Vendor>Options) wire.SessionRequestOption
func WithSessionOutputSchema(schema map[string]any) wire.SessionRequestOption
func WithSessionRawEvents(enabled bool) wire.SessionRequestOption
func SetModelRequest(sessionID acp.SessionId, model string) acp.SetSessionConfigOptionRequest
func Validate<Vendor>SessionMeta(meta map[string]any) error
```

Rules:

- Session-establishing builders always emit an empty `mcpServers` array. There
  is no MCP option ([03-wire-contract.md](03-wire-contract.md#uniform-rejections)).
- `WithSessionMeta` and `WithListSessionsMeta` reject a caller key matching any
  `acp-go.dev/*` reserved literal, through `wire.CheckReservedMeta`, rather
  than merge it. `WithSessionMetaValue` merges a vendor namespace the sibling
  built itself and is what the vendor option constructors use.
- Prompt correlation for the lifecycle extension is stamped by the host, not
  by these builders ([03-wire-contract.md](03-wire-contract.md#prompt-correlation)).
