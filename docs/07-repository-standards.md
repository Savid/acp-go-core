# Repository Standards

## Layout

```text
.
|-- .github/workflows/check.yml
|-- .gitignore
|-- .golangci.yml
|-- AGENTS.md
|-- CLAUDE.md
|-- LICENSE
|-- Makefile
|-- README.md
|-- doc.go
|-- example_test.go
|-- agent.go
|-- options.go
|-- request_builders.go
|-- session.go
|-- session_meta.go
|-- session_prompt.go
|-- cmd/acp-go-<vendor>/
|   |-- main.go
|   |-- otel.go
|   |-- signals_unix.go
|   `-- version.go
|-- integration/
|   |-- binary_test.go
|   |-- doc.go
|   `-- helpers_test.go
`-- internal/<harness>/
```

`LICENSE`, `.gitignore`, and `.golangci.yml` are byte-identical
across siblings. The public ACP surface lives in the root package. Native
protocol and process details live under `internal/<harness>`. Shared behavior
is imported from this module, never copied into `internal/` or into a root
file; the layout above fixes where a sibling's own code lives and never
requires a sibling to hold a copy of shared behavior.

`doc.go` explains embedding through `Serve`; `example_test.go` proves
initialize behavior.

## Go File Structure

The root files above are the shared core. Additional root files are
size-justified domain splits: `agent_<topic>.go` for ACP method handling,
`session_<topic>.go` for session orchestration, `image_<topic>.go` for image
behavior, and `<vendor>_<topic>.go` for thin public glue.

A sibling that allocates ephemeral state uses `scratch.go` as its sole
scratch accessor. A sibling with no such allocation omits it. No other
non-test source may create an empty-parent temp file or directory.

The Go file-structure rules in this section bind every sibling; this module
organizes its packages by package. Tests mirror production files:
`<stem>_test.go` mirrors `<stem>.go`. The standing extras are
`example_test.go`, `contract_test.go`, `helpers_test.go`, and the scripted
fake native binary `fake<vendor>_test.go`. Contract pins
assert public and wire shape; mirror tests assert construction and internal
branches. Never name a test file after development history or a topic no
production file carries.

A comment states what the code does or why a non-obvious constraint exists. It
never records history, a plan, or an external reference.

## Surface Presence

The structural gate checks these symbols and literals in every sibling:

- `Options.InputHandoffRoot` and `func WithInputHandoffRoot(dir string) Option`
  in root `options.go`.
- `Options.ConfiguredModels` and `func WithConfiguredModels(ids []string) Option`
  in root `options.go`.
- The reserved literals `acp-go.dev/mediaEnvelope`, `acp-go.dev/handoff`,
  and `acp-go.dev/lifecycle` used in non-test Go, through the `wire`
  constants.
- `cmd/acp-go-<vendor>/otel.go` obtains its providers from
  `observer/exporters.Configure`.
- The executable is resolved through `process.ResolveExecutable` against the
  base environment.
- `request_builders.go` declares only the vendor option constructors, each
  returning `wire.SessionRequestOption`.
- Native process death reports its stderr tail through
  `process.(*Process).StderrTail`.
- A sibling that exports `AccountUsageMethod` in non-test Go spells it
  `"_<vendor>/accountUsage"`, decodes through
  `wire.DecodeAccountUsageRequest`, advertises
  `wire.AccountUsageCapabilityKey`, calls `Validate` in every non-test file
  that assembles a `wire.AccountUsageResponse` or calls a shared provider
  reader (a read through `usage.ReadVerified` or `gateway.ReadRoutes`
  validates within core), and has a row in
  the registry's Account Usage table recording its scope; a sibling without
  the export uses none of those and its row reads `none`.

## Dot Files

The tracked dot files are `.github/`, `.gitignore`, and `.golangci.yml`.
Agent scratch directories are never committed. `.golangci.yml` uses the
strictest configuration in the family.

## Command Binary

Binary name `acp-go-<vendor>`. Go `flag` single-dash syntax. Common flags:

| Flag | Meaning |
|---|---|
| `-path` | Native executable path. |
| `-home` | Native config root. |
| `-scratch-dir` | Parent directory for ephemeral scratch; empty means system temp. |
| `-model` | Default model for new sessions. |
| `-seed-file` | Repeatable `<relpath>=<hostpath>` pair seeded into the native config root before launch. |
| `-debug` | Enable adapter debug logging to stderr. |
| `-version` | Print adapter version and exit. |

`version.go` carries `var buildVersion = "dev"`, overridden by
`-X main.buildVersion=$(VERSION)`. Vendor flags use `-<vendor>-...`. Native
mode and agent selection are session config options, never flags.

## Makefile Targets

| Target | Required behavior |
|---|---|
| `build` | Build package and command binary. |
| `test` | `go test -race -shuffle=on -timeout=$(GO_TEST_TIMEOUT) ./...`. |
| `coverage-check` | The same run with `-coverprofile=coverage.out -covermode=atomic`, then report the total percentage without a threshold. |
| `test-integration-smoke` | Fast integration smoke against the installed native binary where available. |
| `test-integration-live` | Full live integration that may spend tokens. |
| `lint` | `$(GOLANGCI_LINT) run --timeout=10m --allow-parallel-runners ./...` at a version pinned once in the Makefile. |
| `fmt`, `fmt-check` | Apply or verify formatting with pinned tooling. |
| `tidy` | Verify `go mod tidy`. |
| `vuln` | Run the pinned vulnerability scanner. |
| `modernize-check` | `go fix -diff ./...`. |
| `audit` | Exactly `fmt-check lint build coverage-check tidy vuln modernize-check`, in that order even under parallel make, then `go mod verify`. |
| `clean`, `help` | Remove artifacts; list targets. |

`GO_TEST_TIMEOUT ?= 40m` is declared once. Identical-class recipes are
byte-identical across siblings and this module; integration recipes may vary
in timeouts, package lists, and selectors. Integration recipes build with
`-tags=integration` and set `ACP_GO_<VENDOR>_RUN_INTEGRATION=1`; only
`test-integration-live` sets `ACP_GO_<VENDOR>_RUN_LIVE_TOKENS=1`, and every
recipe clears the gate it does not select. `@latest` is forbidden in build
tooling; pinned tool versions live in the Makefile and are byte-identical
across siblings and this module. Every sibling uses the family Go directive
and never commits a `toolchain` line.

## Continuous Integration

Every sibling carries one workflow, `.github/workflows/check.yml`, that runs
`make audit` on push and pull request with `contents: read`, concurrency
grouped by ref with `cancel-in-progress: true`, and every action pinned to a
full commit SHA. The family targets Linux and macOS; the workflow runs
`make audit` on both.

## Docs

Required docs are `README.md`, `doc.go`, `AGENTS.md`, and `CLAUDE.md`. There
is no docs site.

`README.md` states what the sibling wraps, how to install and run the binary,
how to embed `Serve`, every process option and command flag, the session
options and config options the sibling supports, and its store format. It
describes only the current shape and names no other sibling, this
repository's contract, or any host; this module is an ordinary dependency and
may be named.
`doc.go` mirrors the embedding section.

`AGENTS.md` carries, in order: purpose, project map, working commands, coding
rules, verification, and boundaries. `CLAUDE.md` is only the heading and the
`@AGENTS.md` import. Sibling instructions are self-contained and mention no
other repository.

## Code Standards

gofmt-clean, golangci-lint-clean, table-driven tests, ethPandaOps house style.
Keep it simple.

## Drift Check

`make drift-check` runs `scripts/drift-check.sh` against every sibling the
[family table](../README.md#the-family) lists, located beside this repository
or under `ACP_GO_FAMILY_ROOT`. It verifies:

- required files, identity constants, the module path, and the exported
  `SessionStoreFormat`, `RawEventMethod`, and `AccountUsageMethod`;
- the [account-usage](#surface-presence) structural rule, including the
  sibling's registry Account Usage row;
- executable resolution through `process.ResolveExecutable` on the base
  environment;
- the Go directive, ACP SDK pin, and this module's pin against the README, in
  every sibling and in this module's own `go.mod`, and one version per module
  across every family `go.mod`, indirect requirements included;
- the [surface presence](#surface-presence) symbols;
- that README, AGENTS.md, and doc.go name no other sibling or this
  repository;
- byte-identical `LICENSE`, `.gitignore`, and `.golangci.yml`
  across siblings;
- the Makefile audit composition, identical-class recipes, and integration gates;
- CLAUDE.md import shape, AGENTS.md section order, permitted root file
  stems, matching test stems, and scratch allocation ownership;
- README process-option and flag coverage, tracked dot-file names, and CI
  matrix, triggers, permissions, and action pins;
- that no sibling carries a copy of the lifecycle fixture battery.

A missing checkout is reported and skipped. A pass proves the enumerated
structure, not behavior. Every rule the list above names has an assertion in
the script, and a rule change and its assertion land together; the remaining
rules on this page are proved by each sibling's own `make audit` or by review.
