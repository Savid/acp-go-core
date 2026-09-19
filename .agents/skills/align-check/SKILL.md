---
name: align-check
description: Audit selected or all acp-go-* checkouts against the family contract, with reproducible source identities and findings linked to exact rules. Use for alignment, drift, or conformance checks; this skill is read-only.
---

# Alignment Check

Report contract violations within the user's scope. Never fix, commit, or
launch native or live tests as a side effect of selecting this skill. Use the
[family table](../../../README.md#the-family) to discover siblings and the
[contract index](../../../README.md#documentation) to find rules; do not
restate the contract from memory. Read each checkout's `AGENTS.md` and
`CLAUDE.md` before inspecting it.

## Snapshot preflight

Reusable by the other alignment skills.

- Locate requested checkouts beside this repo or under `ACP_GO_FAMILY_ROOT`.
  Missing repos are unverified; continue on those present and ask before
  cloning only when the missing source is needed.
- Record the audit date, branch, exact `HEAD` SHA, and `git status --short`
  for each source, including this repo. Never fetch, pull, switch, or move
  branches for an audit.
- A dirty checkout is not a clean-SHA source. Report the coverage gap or
  inspect a user-authorized working-tree snapshot identified by its base SHA
  and diff hashes. Never stash, reset, or discard unrelated work.
- Recheck identities before reporting. In an authorized edit loop, record your
  own changes as a new working-tree baseline after each pass.

## Checks and coverage

For a family audit, run `make drift-check` from this repo and retain its
output. For a targeted audit, inspect the requested rules directly. Every
in-scope `FAIL` needs a finding or an explanation of missing evidence. Every
`SKIP` is unverified, never passing. Missing tools are infrastructure faults,
not sibling defects.

Add judgment checks where the script cannot prove the requirement: README
truthfulness against the code, environment inheritance and merge order,
session environment scoping across recovery, native resume outside ACP, and
lifecycle emitter validation through the reducer. Follow the
[repository standards](../../../docs/07-repository-standards.md),
[lifecycle rules](../../../docs/06-lifecycle.md), and
[registry](../../../docs/registry.md). State what was sampled.

## Failure mapping

| Failure family | Contract rule |
|---|---|
| family table, pins, checkout not found | `README.md#the-family`, `README.md#shared-pins` |
| module, package, binary, prefix, or store format identity | `docs/00-overview.md#identity-constants` |
| required file, layout, or Go file structure | `docs/07-repository-standards.md#layout`, `#go-file-structure` |
| surface presence symbol or forbidden literal | `docs/07-repository-standards.md#surface-presence` |
| sibling docs or instructions name another repo or a host | `docs/00-overview.md#one-shape-one-core`, `docs/07-repository-standards.md#docs` |
| shared file or workflow differs | `docs/07-repository-standards.md#dot-files`, `#continuous-integration` |
| Makefile target, audit composition, or recipe | `docs/07-repository-standards.md#makefile-targets` |
| command flag | `docs/07-repository-standards.md#command-binary` |
| sibling carries a lifecycle fixture copy | `docs/08-testing.md#lifecycle-fixtures` |
| registry row missing | `docs/07-repository-standards.md#surface-presence` |

## Report

Give scope, date, source identities, coverage, missing or dirty checkouts,
then findings ordered by impact. For each finding include `mechanical` or
`judgment`, the exact contract link, source `file:line`, observed behavior,
and required behavior. Separate contract or checker defects from sibling
defects. Claim alignment only for verified scope.
