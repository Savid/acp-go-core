# Testing

Test tiers use the Makefile targets in
[07-repository-standards.md](07-repository-standards.md#makefile-targets).

## Unit Tests

Tests protect observable behavior or a concrete failure boundary. Prefer a
small table of distinct cases. Do not add production seams, unreachable
branches, or tests whose only purpose is raising coverage. Coverage is
reported across the complete package graph with no threshold and reviewed by
risk.

Concurrent tests establish ordering with explicit barriers or observable
state, and use deadlines to bound failure rather than sleeps. Prefer
`testing/synctest` for in-process timers; subprocess work needs explicit
synchronization and bounded cleanup. Parallel tests own their state and never
mutate process-global environment or working directory.

Unit tests are deterministic, race-clean, and independent of installed native
CLIs, credentials, or network. The native process is replaced by a
deterministic scripted fake, the test binary re-executing itself, that rejects
unknown protocol operations. `make test` and `coverage-check` cover the
complete package graph; no test turns a missing native CLI or credential into
a passing skip.

## Conformance Tests

Every sibling proves, against the current ACP v1 schema:

- Initialize shape: `authMethods` empty; no fork, MCP, NES, providers, modes,
  goals, import, or document capabilities advertised.
- Position-encoding selection.
- Stable `session/fork` returns method-not-found; `session/set_mode` returns
  method-not-found; `authenticate` echoes the method id; `logout` returns
  method-not-found; every extension method returns method-not-found.
- `_meta.<vendor>` strictness: unknown own-namespace keys rejected, foreign
  namespaces ignored, trace keys preserved, `acp-go.dev/lifecycle` refused on
  session lifecycle requests.
- A non-empty `mcpServers` is refused naming `mcpServers`.
- Config options are select-only with the contract categories.
- `session/delete` tombstones and hides list, load, and resume, including the
  race where a load past its entry check installs nothing and tears down its
  prepared replacement.
- Default-store parity: omitting the store uses a fresh in-memory store as the
  sole authority; residual native state with no store entry is neither listed
  nor adopted.
- A session-scoped request against an unknown, unloaded, or tombstoned id
  returns the uniform `-32602`; `session/cancel` on such a session is silent.
- `$/cancel_request` cancels the addressed handler context only, and the
  original request settles exactly once.
- A native turn failure surfaces as `<vendor>_turn_failed` with a registered
  cause and never a stop reason. Every other `-32603` carries a token from
  the closed vocabulary with no `message` and no native text.
- Assistant text is append-only: a fixture whose terminal frame repeats the
  streamed text yields chunks whose concatenation equals the final text once;
  a deltas-free fixture yields one chunk; a multi-message turn yields each
  message once.
- Elicitation capability gating for all six client-capability cases, with the
  both-null case exercised through JSON decoding.
- Native stdout and stderr noise cannot corrupt ACP stdout.
- Command advertisement: native-list-only, shared sanitizer, full replacement,
  initial snapshot including the explicit empty one, re-emission after a
  relaunch, and post-response ordering; or command silence where the harness
  has no surface.
- **Lifecycle capability and strictness.** The offer is read from
  `InitializeRequest._meta` and answered on `InitializeResponse._meta`, never
  in `agentCapabilities._meta`. Strict decoding rejects a missing or duplicate
  `version`, any value other than integer `1`, fractional, string, or boolean
  values, unknown members, and trailing input. `activityKinds` encodes an empty
  set as `[]`. Prompt correlation is required when enabled and rejected
  otherwise.
- **Emitted envelopes are well formed and legally carried**: exactly four
  members, one per notification, on an identity-only `session_info_update`.
  The emitter's self-validation renders the notification, decodes its bytes,
  and reduces the decoded value through the `lifecycle` reducer.
  `session/request_permission` and `elicitation/create` carry their action
  correlation while enabled, and their responses' `_meta` cannot change an
  outcome.
- **Close on a fenced or never-opened incarnation** succeeds with zero
  emissions on the dead stream, the owed commits still land, and an entity
  terminalized as `failed` is never rewritten to `cancelled`.
- **Session environment and PATH.** `With<Vendor>ExtraPathDirs` produces the
  ordered list, deep-copied at every boundary. Both decoded list forms are
  accepted; a non-string, empty, relative, or separator-bearing entry fails at
  the exact indexed field. A test captures the environment the native process
  actually receives and asserts the directories appear first, in order, ahead
  of the merged `PATH`, joined with the platform separator and carrying no
  empty component. Executable resolution and version probing ignore session
  directories, so a planted fixture binary never shadows the harness. An
  empty-valued key reaches the native process as `KEY=`. Two sessions with
  distinct values both succeed and never cross.
- **Environment inheritance.** A variable set in the adapter's own process
  environment reaches the harness unchanged, `WithEnv` overrides it, session
  `env` overrides that, and only the sibling's `ACP_GO_<VENDOR>_INTERNAL_*`
  markers are absent.
- Two concurrent logical sessions remain independently addressable without
  crossing cwd, callbacks, permissions, turn results, or cancel. On a
  multiplexed sibling, a native crash fails both in-flight turns exactly once
  and the next operation rebinds both through one replacement.
- **Native resume outside ACP.** A session created over ACP leaves native
  state the harness can resume natively after the adapter closes; a later
  `session/load` of that session through the adapter adopts rows the harness
  appended meanwhile and materializes nothing over them.

### Catalog Membership

A sibling covers: a host-listed id absent from the native list published
after the native rows, one the native list already carries published once,
and construction refusing an empty, whitespace, or duplicate id. Coverage
never depends on the developer's own environment for a credential or a route.

### Image Gates

Every sibling proves these deterministic gates with fixtures and fake native
boundaries:

1. Initialize advertises the exact standard image capability and the exact
   media envelope with this sibling's effective values, including
   zero-disabled configuration; `acp-go.dev/handoff` is asserted present with
   `WithInputHandoffRoot` and absent without.
2. A real embedded raster reaches the intended native input shape.
3. Data wins when data and URI are both supplied.
4. Invalid base64, MIME mismatch, unsupported format, animated input, oversize
   bytes, and known text-only selection fail with the specified pre-turn
   error.
5. Unknown selected-model modality forwards.
6. The handoff form reaches the same native input as the embedded form over
   the same bytes, the handoff path appears nowhere in the native request or
   logs, and handoff bytes count toward the aggregate.
7. The handoff pre-gate verdicts fire in order and each claims only what it
   owns, including: stat-versus-read divergence is `handoff_digest_mismatch`;
   no-I/O verdicts open no file; real symlink and non-regular cases under a
   real root; the 64-block cap with the aggregate disabled; mid-read failure
   is `missing_file`; an under-declared file forwards nothing.
8. Inline bytes normalize to embedded images; URI-only artifacts become
   resource links; no artifact is emitted twice.
9. Provenance and stable identity survive live execution and replay.
10. Repeated native updates do not duplicate an artifact, and every
    content-bearing `tool_call_update` is a complete snapshot array.
11. Invalid or oversize output is refused in place with the constant guidance,
    the turn continues, and only `storage_failed` fails the turn.
12. Where a sibling reads output from a path, a file in `os.TempDir()` is
    readable and a file outside every root is refused.
13. Typed binary data is not duplicated into raw events or logs.

Gates 8 to 13 bind each native output surface a sibling implements. Byte
limits are exercised at the boundary and one byte over, per image and
aggregate. Animated GIF, animated WebP, two-frame APNG, and single-frame
`acTL` PNG fixtures fail pre-turn; a non-allowlist output raster is emitted
with its sniffed MIME. The `image` package's own tests prove the gates once;
a sibling proves that its native boundaries reach them.

### Lifecycle Fixtures

The canonical reducer battery lives in this module under
`lifecycle/testdata/fixtures/`, embedded as `lifecycle.Fixtures`, and
`go test ./lifecycle` runs every vector. Siblings do not copy it; they validate
their emitters through the reducer the battery proves.

- `manifest.json` lists every fixture with the invariant it pins.
- Each fixture carries `name`, `purpose`, the `negotiated` capability answer,
  an `input` array in delivery order, and `expect`. An input element with
  `method` and `params` is one notification; one with only `control` is an
  out-of-band event over a vocabulary closed at `session_closed`.
- `expect.verdict` is `accepted` or `fail_closed`. A `fail_closed` fixture
  carries `violation` and `atInput`, the zero-based index of the refused
  element, and MAY carry `postRefusal`, further inputs the runner MUST feed and
  assert are refused with the same token at the same identity.
- `expect.state` has the fixed member set the manifest's `stateShape` fixes
  and is compared for exact equality.
- Comparison is over decoded values; key order and whitespace are never
  differences.

The battery pins every token in the closed violation vocabulary with at least
one vector, each naming exactly one token, and proves the accepted projections
for acceptance before running, turn settlement with a live activity,
agent-origin turns, background and blocking actions, exact and reordered
retransmission, resumed snapshots, per-prompt incarnations, and close fencing.
A new violation condition without a vector is a gap to close.

**The evidence gate.** A sibling advertises `updatesOutsidePrompt` or an
`activityKinds` entry only when a deterministic fixture in its own repository
proves the native source that fact reads and the ordering it claims, against
captured native frames. The fixture and its provenance live under
`testdata/native/`.

## Integration Smoke

Runs against an installed native binary, skips cleanly when the binary is
absent, spends no tokens, and proves version probing, initialization, session
creation, and deterministic close and delete paths.

## Integration Live

Env-gated; may spend tokens; runs the harness in an isolated temporary home
with auth injected explicitly. Proves a full prompt turn, permissions,
elicitation where supported, raw-event opt-in, store-backed load and resume,
delete, and cancellation of a real long-running native process. Proves the
[native resume outside ACP](#conformance-tests) scenario against the real
harness. Plants a marker executable in a session-scoped directory passed
through `extraPathDirs` and asserts the native tool resolves it and receives
that directory first, except for native-owned prefixes explicitly recorded
in the [registry](registry.md). Then rotates the directory on a second turn
and proves the old value is gone. A prefix exception MUST identify the native
directory from the installed harness; it MUST NOT accept arbitrary earlier
`PATH` entries.

## Pin-Change Re-Verification

Version-pinned facts — native item names, store paths, line grammars, catalog
shapes — are re-verified by gated live tests whenever a harness pin moves. A
pin change with no such run is a release blocker. The pinned values live in
[registry.md](registry.md).

## Integration Practices

Integration env vars use `ACP_GO_<VENDOR>_` plus:

| Suffix | Meaning |
|---|---|
| `RUN_INTEGRATION` | Enables the integration tiers. |
| `RUN_LIVE_TOKENS` | Opts in to token-spending live tests. |
| `AGENT_BINARY` | Path to a prebuilt `acp-go-<vendor>` binary. |
| `HOME` | Source native home copied into the isolated temp home. |
| `MODEL` | Model override for live tests. |
| `HARNESS_PATH` | Native harness binary override. |

- **Double gate.** Integration tests live behind the `integration` build tag
  and the explicit env var. Values other than `1` never enable a tier.
- **Skip vs fail.** Smoke skips cleanly naming the missing prerequisite; live
  fails when explicitly requested with broken prerequisites.
- **Hermetic homes.** Every integration test runs the harness against an
  isolated temp home, never the developer's real config.
- **Cleanup.** Register teardown with `t.Cleanup` so it runs LIFO on failure.
- **Determinism.** Live prompts use exact sentinel replies; assert the stop
  reason and streamed updates, not fuzzy output.
- **Bounded and parallel.** Runs set an explicit `-timeout`; `t.Parallel` only
  where per-session isolation makes it safe.
