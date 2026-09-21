---
name: align-agents
description: Audit or align sibling AGENTS.md and CLAUDE.md with actual repository structure, commands, verification gates, and boundaries. Use for agent-instruction cleanup or consistency; preserve vendor-specific rules and self-contained siblings.
---

# Align Agent Instructions

Align only the requested repositories and instruction files. Audits are
read-only; edit when requested or already authorized. Instruction review does
not authorize native or live execution or changes to product behavior.

## Establish the current workflow

Use [snapshot preflight](../align-check/SKILL.md#snapshot-preflight) for
source identity. Read the [docs standards](../../../docs/06-repository-standards.md#docs).

Verify the instructions against the repository itself: its file layout,
Makefile recipes, test gates, public API, and native ownership. Inspect
claimed files and commands instead of copying another sibling's map.

## Align what agents need

- Keep the required sections and their order: purpose, domain map, working
  commands, coding rules, verification, and boundaries.
- Describe coverage as a review report; never instruct agents to add
  scaffolding to reach a percentage.
- Link detailed local rules rather than repeating schemas or test inventories.
- Remove obsolete paths, commands, and claims.
- Keep every sibling self-contained: no mention of this repo, other siblings,
  or hosts. This module is an ordinary dependency and may be named.
- Make `CLAUDE.md` only the heading and the `@AGENTS.md` import.

## Verify and report

Run `make drift-check` from this repo. Trace representative tasks through the
resulting instructions: an ordinary fix, a lifecycle change, and a request
involving live tests. Report coverage, corrections, retained vendor
differences, and any remaining code-versus-instruction disagreement.
