# AGENTS.md

## Purpose

`acp-go-core` is the shared module of the `acp-go-*` family. It holds
behavior that is identical across siblings: the session store contract, the
lifecycle extension, process launch, wire error shapes, raw events, and the
image gates. Vendor-specific code never lives here.

## Domain map

| Path | Owns |
|---|---|
| `store.go` | `SessionStore` types and `InMemorySessionStore` |
| `storetest/` | The exported store contract battery |
| `lifecycle/` | Capability, envelope, decoder, reducer, emitter; `testdata/fixtures/` is the canonical battery |
| `process/` | Environment merge, executable resolution, launch, shutdown, epoch fence |
| `wire/` | Error constructors, raw-event framing, reserved literals |
| `image/` | Limits, media envelope, input gates, handoff, output, artifact store |

## Commands

```sh
make test            # race, shuffled
make lint            # pinned golangci-lint
make audit           # fmt-check lint build coverage-check tidy vuln modernize-check
```

## Rules

- Current shape only. No compatibility code, migrations, or alternate wire
  shapes. Remove superseded code with its tests.
- Nothing here isolates a process, selects an identity, or verifies
  containment. The harness is a plain child of whoever runs the sibling.
- The lifecycle fixture battery is the contract. A reducer change that breaks a
  vector is wrong until the contract says otherwise; never edit a vector to
  make a test pass.
- Comments state what code does or why a constraint exists. No history, plans,
  or external references.
- Tests protect observable behavior. Coverage is reported, not targeted.

## Boundaries

- This module names no sibling, no consumer, and no coordination repository.
- Public API changes move every sibling together.
- Live harness runs do not belong here; nothing in this module needs a
  harness binary.
