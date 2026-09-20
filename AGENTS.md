# AGENTS.md

Instructions for agents working in the `acp-go-*` family's shared module and
contract repository.

## Purpose and Sources

`acp-go-core` holds the behavior identical across siblings and the contract
that binds them. Native harness code lives in its sibling; provider clients
shared across harnesses live here.

| Source | Authority |
|---|---|
| [README.md](README.md) | Family membership and shared version pins |
| `docs/*.md` other than the registry | Current family contract |
| [docs/registry.md](docs/registry.md) | Per-sibling facts that differ between siblings |
| [lifecycle/testdata/fixtures/manifest.json](lifecycle/testdata/fixtures/manifest.json) | Canonical lifecycle reducer battery |
| [.agents/skills](.agents/skills) | Audit and alignment workflows |
| [tracking/upstream-acp.md](tracking/upstream-acp.md) | Outstanding upstream watch items |

When code and contract disagree, identify which is wrong and reconcile them
within the task's scope. Never weaken a rule merely to match an implementation.

## Domain Map

| Path | Owns |
|---|---|
| `store.go` | `SessionStore` types, `InMemorySessionStore`, and the store call bound |
| `sessionlog/` | Atomic mirror generations, record decoding, native-log reconciliation |
| `observer/` | Shared OpenTelemetry instrumentation; `exporters/` is the command-binary bootstrap |
| `storetest/` | The exported store contract battery |
| `lifecycle/` | Capability, envelope, decoder, reducer, emitter, publisher; `testdata/fixtures/` is the canonical battery |
| `process/` | Environment merge, executable resolution, launch, stderr tail, shutdown, seed files, file lock |
| `wire/` | Error constructors, raw-event framing, request builders, session metadata and gates, text rules, publication ordering, reserved literals, account-usage decoder and response shape |
| `usage/` | Shared provider usage readers; credentials and native route selection stay in siblings |
| `image/` | Limits, media envelope, input gates, handoff, output gates |
| `docs/` | The contract pages and the registry |
| `scripts/` | `check.py` for this repo, `drift-check.sh` for the siblings |

## Commands

```sh
make test            # race, shuffled
make lint            # pinned golangci-lint
make audit           # fmt-check lint build coverage-check tidy vuln modernize-check
make check           # links, skill metadata, fixtures, script syntax
make drift-check     # family structural contract across sibling checkouts
```

## Principles

- **If it is useless, cut it.** A rule, surface, test tier, or page survives
  only if a host uses it or a sibling cannot function without it.
- **Hard cutover.** No compatibility code, migrations, shims, retired-name
  catalogs, or migration notes. Remove superseded statements and code with
  their tests in the same change. History belongs in Git.
- **Interface, not isolation.** Nothing here isolates a process, selects an
  identity, or verifies containment. The harness is a plain child of whoever
  runs the sibling.
- **The fixture battery is the contract.** A reducer change that breaks a
  vector is wrong until the contract says otherwise; never edit a vector to
  make a test pass.
- **Comments do their job.** A comment states what the code does or why a
  constraint exists; never history, a plan, or an external reference.
- Tests protect observable behavior. Coverage is reported, not targeted.

## Research and Evidence

- Read the relevant rule and implementation before making a claim. Use `rg`
  for targeted discovery; follow call paths and tests beyond symbol presence.
- Use web search to verify mutable ACP, SDK, and native-harness claims. Prefer
  the published protocol, official documentation, upstream source, schemas,
  and release notes. Read in this order: the
  [ACP v1 protocol](https://agentclientprotocol.com/protocol/v1/overview) and
  [schema](https://agentclientprotocol.com/protocol/v1/schema), the README's
  protocol posture and the upstream watchlist, the pinned
  [Go SDK](https://github.com/coder/acp-go-sdk), then this module and the
  sibling's registry entry.
- Protocol truth comes from the published spec and schema. The pinned Go SDK
  establishes its own types and transport behavior, not the protocol.
- Distinguish contract, observed behavior, and inference. Record a source,
  version, and date for evidence that can age.
- Keep only actionable upstream watch items; replace stale status when
  rechecking.

## Writing and Maintenance

- State the current rule directly. Use MUST and MUST NOT for obligations.
- Give each rule one location and link to it elsewhere. Cut rationale and
  commentary that adds no requirement.
- A rule enters the contract when a sibling proves it. The registry records
  only facts that differ between siblings; uniform rules are not restated
  there.
- Keep the README family table, shared pins, and registry consistent. Pin
  values live only in the README.
- Preserve useful heading anchors and repair callers when a heading changes.
- Keep secrets, credential values, and user-specific paths out of docs.

## Working Across Repositories

- Before reading or changing a sibling, read its `AGENTS.md` and `CLAUDE.md`
  if present. Its instructions govern work there.
- Locate checkouts by repository name beside this repo or under
  `ACP_GO_FAMILY_ROOT`. Ask before cloning a missing checkout.
- Record branch, commit, and working-tree status for audits. Never move
  branches to make an audit pass.
- Keep changes in the owning repo. After a sibling public-surface change,
  update the registry in the same effort.
- Sibling code and public docs are self-contained: they name this module as a
  dependency and never this repository's contract, another sibling, or a
  host.
- A public API change here moves every sibling together.

## Verification

- Run `make test` for Go changes and `make audit` once they settle.
- Run `make check` for links, skill metadata, fixtures, and script syntax. It
  needs no sibling checkout.
- Run `make drift-check` after structural rule changes. A pass proves the
  enumerated structure, not behavior.
- A new or changed structural rule in `docs/06` has an assertion in
  `scripts/drift-check.sh` in the same change.
- Run sibling checks from that sibling's checkout using its own Makefile.
  Live model-token runs require explicit operator intent; nothing in this
  module needs a harness binary.
- Re-read affected pages and review the complete diff for contradictions,
  duplicated rules, and unsupported claims.

## Ask Before

Unless already authorized, ask before changing family policy in `docs/01`
through `docs/07`, shared pins, family membership, the numbering of `docs/`,
or an exported Go symbol. A correction supported by the current contract and
code does not require a new decision. Do independent preparation before
asking about an unresolved decision.
