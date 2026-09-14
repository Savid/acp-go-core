# acp-go-core

The shared module and contract of the `acp-go-*` family: Go packages that give
local coding-agent harnesses an [ACP](https://agentclientprotocol.com/)
interface. This repository holds the code every sibling has in common, the
family contract, the per-sibling registry, the lifecycle fixture battery, and
the checks that keep the siblings aligned.

## The Family

| Repo | Package | Vendor key | Native surface | Store format |
|---|---|---|---|---|
| [acp-go-codex](https://github.com/savid/acp-go-codex) | `codexacp` | `codex` | `codex app-server` stdio protocol | `codex-rollout-jsonl-v1` |
| [acp-go-pi](https://github.com/savid/acp-go-pi) | `piacp` | `pi` | `pi --mode rpc` JSONL protocol | `pi-session-jsonl-v1` |

[The registry](docs/registry.md) records each sibling's capabilities, options,
native evidence, and deviations.

## Packages

| Package | Contents |
|---|---|
| `acpcore` | `SessionStore` and its types, `InMemorySessionStore` |
| `acpcore/sessionlog` | Atomic native-log and configuration mirroring, strict record decoding, and reconciliation with native continuation |
| `acpcore/observer` | ACP, prompt, dialog, store, and native-process OpenTelemetry instrumentation |
| `acpcore/storetest` | The store contract battery a host store runs against itself |
| `acpcore/lifecycle` | The `acp-go.dev/lifecycle` extension: capability, envelope, events, reducer, emitter, and the embedded fixture battery |
| `acpcore/process` | Environment merge, executable resolution, child launch with its own process group and pipes, shutdown, and seed-file writes |
| `acpcore/wire` | Uniform error shapes, raw-event framing and sequencing, publication ordering, and the reserved `_meta` literals |
| `acpcore/image` | Decoded-byte limits, the media envelope, prompt image validation in both forms, output normalization |

```go
import (
    acpcore "github.com/savid/acp-go-core"
    "github.com/savid/acp-go-core/lifecycle"
    "github.com/savid/acp-go-core/process"
)
```

A sibling imports what it needs and re-exports nothing. A host imports the
root package for the store types it implements and `lifecycle` for the
reducer.

## Shared Pins

Every sibling moves together on these pins. Values live only in this table.

| Pin | Value |
|---|---|
| ACP SDK | `github.com/coder/acp-go-sdk@v0.13.5` |
| Core module | `github.com/savid/acp-go-core` (unreleased; pinned here at first tag) |
| Go directive | `go 1.26.6` |

A pin change covers every sibling and reruns each conformance suite. Shared
direct dependencies such as OpenTelemetry and testify also move together;
their versions live in `go.mod` files and are compared by
`make drift-check`.

### Protocol Posture

- The family implements published ACP v1. Adopting v2 is one coordinated
  cutover across the family and its hosts.
- Protocol claims come from the published spec and schema. The pinned Go SDK is
  evidence for its own generated types and transport bounds only.
- Extension methods and enum values require the `_` prefix; `_meta` keys do
  not. The three reserved family literals are defined in
  [docs/00](docs/00-overview.md#family-global-reserved-literals).
- Current upstream status and adoption triggers live in the
  [watchlist](tracking/upstream-acp.md).

## Checks

```sh
make test          # race, shuffled
make audit         # fmt-check lint build coverage-check tidy vuln modernize-check
make check         # links, skill metadata, fixture structure, script syntax
make drift-check   # family structural contract across sibling checkouts
```

`make check` validates this repo's local links and anchors, skill metadata and
symlinks, lifecycle fixture structure and violation coverage, and script
syntax. It runs without sibling checkouts and needs Bash, Make, Git, Python 3
with PyYAML, and `rg`.

`make drift-check` verifies the enumerable structural rules in
[docs/07](docs/07-repository-standards.md#drift-check) against the sibling
checkouts beside this repo. Missing checkouts are reported and skipped. A pass
proves the enumerated structure, not behavior.

## Core Principles

- **Interface, not isolation.** A sibling translates ACP to a harness protocol
  and back. It inherits the environment it was started in and does no
  isolation; whoever executes the sibling owns that.
- **Harness security features stay.** Sandbox and approval policies are the
  harness's own and pass through as options.
- **One shape, one core.** Shared behavior lives once here. A sibling holds
  only what is vendor-specific.
- **Current shape only.** No compatibility code, migrations, or alternate wire
  shapes. Database creation defines the current schema from empty.
- **Store-backed durability.** `SessionStore` owns session recovery; native
  state is a cache.

## Documentation

| Page | Scope |
|---|---|
| [00 · Overview](docs/00-overview.md) | Ownership, identity, reserved literals, error vocabulary |
| [01 · Getting started](docs/01-getting-started.md) | Source order and native-surface research |
| [02 · Public API](docs/02-public-api.md) | This module, agent surface, options, builders |
| [03 · Wire contract](docs/03-wire-contract.md) | Capabilities, methods, metadata, envelopes |
| [04 · Sessions and store](docs/04-sessions-and-store.md) | Persistence, commit ordering, restore, identity |
| [05 · Behavior](docs/05-behavior.md) | Config, models, images, usage, permissions, commands, delete |
| [06 · Lifecycle](docs/06-lifecycle.md) | Process model, cancellation, shutdown, concurrency |
| [07 · Repository standards](docs/07-repository-standards.md) | Layout, tooling, CI, docs |
| [08 · Testing](docs/08-testing.md) | Unit, conformance, fixtures, integration |
| [Registry](docs/registry.md) | Current per-sibling facts and deviations |

The [lifecycle manifest](lifecycle/testdata/fixtures/manifest.json) is the
canonical reducer battery, embedded as `lifecycle.Fixtures` and run by
`go test ./lifecycle`. Siblings validate their emitters through the reducer.
