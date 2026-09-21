# acp-go-core

The shared module and contract of the `acp-go-*` family: Go packages that give
local coding-agent harnesses an [ACP](https://agentclientprotocol.com/)
interface. This repository holds the code every sibling has in common, the
family contract, the per-sibling registry, the lifecycle fixture battery, and
the checks that keep the siblings aligned.

## The Family

| Repo | Package | Vendor key | Native surface | Store format |
|---|---|---|---|---|
| [acp-go-claude](https://github.com/savid/acp-go-claude) | `claudeacp` | `claude` | Claude Code stream-json and control protocol | `claude-transcript-jsonl-v1` |
| [acp-go-codex](https://github.com/savid/acp-go-codex) | `codexacp` | `codex` | `codex app-server` stdio protocol | `codex-rollout-jsonl-v1` |
| [acp-go-hermes](https://github.com/savid/acp-go-hermes) | `hermesacp` | `hermes` | `hermes serve` WebSocket JSON-RPC and HTTP persistence | `hermes-session-json-v1` |
| [acp-go-opencode](https://github.com/savid/acp-go-opencode) | `opencodeacp` | `opencode` | `opencode serve` HTTP and SSE | `opencode-sync-events-v1` |
| [acp-go-pi](https://github.com/savid/acp-go-pi) | `piacp` | `pi` | `pi --mode rpc` JSONL protocol | `pi-session-jsonl-v1` |
| [acp-go-amp](https://github.com/savid/acp-go-amp) | `ampacp` | `amp` | `amp threads continue` stream-json with a native lifecycle plugin | `amp-thread-json-v1` |

[The registry](docs/registry.md) records each sibling's capabilities, options,
and deviations, with native verification where a run has been recorded.

## Packages

The package list and what each owns is in
[docs/01](docs/01-public-api.md#this-module).

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
| Core module | `github.com/savid/acp-go-core@v0.0.0-20260921035237-f2ee79a0f2bc` |
| Go directive | `go 1.26.6` |

Every sibling MUST require the core version in this table without a `replace`
directive for that module. Local development may use an untracked Go workspace.

A pin change covers every sibling and reruns each conformance suite. Shared
direct dependencies such as OpenTelemetry and testify also move together;
their versions live in `go.mod` files and are compared by
`make drift-check`.

### Protocol Posture

- The family implements published ACP v1. Adopting v2 is one coordinated
  cutover across the family and its hosts.
- Protocol claims come from the published spec and schema. The pinned Go SDK is
  evidence for its own generated types and transport bounds only.
- The three reserved family literals are defined in
  [docs/00](docs/00-overview.md#family-global-reserved-literals).
- Current upstream status and adoption triggers live in the
  [watchlist](tracking/upstream-acp.md).

## Checks

```sh
make test          # race, shuffled
make audit         # fmt-check lint build coverage-check tidy vuln modernize-check, then go mod verify
make check         # links, skill metadata, fixture structure, script syntax
make drift-check   # family structural contract across sibling checkouts
```

`make check` validates this repo's local links and anchors, skill metadata and
symlinks, lifecycle fixture structure and violation coverage, and script
syntax. It runs without sibling checkouts and needs Bash, Make, and Python 3
with PyYAML.

`make drift-check` verifies the enumerable structural rules in
[docs/06](docs/06-repository-standards.md#drift-check) against the sibling
checkouts beside this repo. It needs Bash, Git, Python 3, and `rg`. Missing
checkouts are reported and skipped. A pass proves the enumerated structure,
not behavior.

## Core Principles

The family principles are stated once, in
[docs/00](docs/00-overview.md#family-principles).


## Documentation

| Page | Scope |
|---|---|
| [00 · Overview](docs/00-overview.md) | Ownership, identity, reserved literals, error vocabulary |
| [01 · Public API](docs/01-public-api.md) | This module, agent surface, options, builders |
| [02 · Wire contract](docs/02-wire-contract.md) | Capabilities, methods, metadata, envelopes |
| [03 · Sessions and store](docs/03-sessions-and-store.md) | Persistence, commit ordering, restore, identity |
| [04 · Behavior](docs/04-behavior.md) | Config, models, images, usage, permissions, commands, delete |
| [05 · Lifecycle](docs/05-lifecycle.md) | Process model, cancellation, shutdown, concurrency |
| [06 · Repository standards](docs/06-repository-standards.md) | Layout, tooling, CI, docs |
| [07 · Testing](docs/07-testing.md) | Unit, conformance, fixtures, integration |
| [Registry](docs/registry.md) | Current per-sibling facts and deviations |

The [lifecycle manifest](lifecycle/testdata/fixtures/manifest.json) is the
canonical reducer battery, embedded as `lifecycle.Fixtures` and run by
`go test ./lifecycle`. Siblings validate their emitters through the reducer.
