---
name: align-siblings
description: Audit or fix acp-go-* contract drift and compare shared feature implementations for consistency. Use for sibling alignment, architecture comparison, or convergence; audit requests remain read-only.
---

# Align Siblings

Align only the repositories and surfaces the user requested. An audit produces
findings; a fix request authorizes in-scope edits. Skill selection never
authorizes fixes, commits, live tests, or contract changes. Commit only when
requested, on the current branch.

## Baseline

Use [align-check](../align-check/SKILL.md#snapshot-preflight) for source
identities and dirty or missing repo handling. Take its scoped findings as
iteration zero. Read the applicable
[contract](../../../README.md#documentation) and
[registry](../../../docs/registry.md) and record what remains unverified.

## Review and resolve

For shared-feature work, follow
[implementation comparison](references/implementation-comparison.md). Logic
that is identical across siblings belongs in the core module; a sibling copy
of it is a finding.

Review contract compliance, exported behavior hosts use, and the affected
implementation and tests. Challenge both the sibling and the rule; a stale
rule is not evidence that correct code should change. Findings need a current
rule link, source location, observed and expected behavior, and an owning
repo.

For authorized fixes:

1. Deduplicate and prioritize defects. Apply changes at the owning layer,
   preserving hard cutover: only the current shape, no shims or dual paths.
2. Follow the affected repo's instructions and run its required checks from
   its own checkout. Live and credential tiers require their own authorization.
3. Reconcile a changed public surface with the registry.
   If a contract, pin, or family change is not authorized, report the concrete
   proposal and continue independent work.
4. Re-run relevant checks and `make drift-check`. Re-baseline your own edits
   before the next review.

## Stop and report

Use at most four review-and-fix passes. Stop when in-scope findings are
resolved and checks pass, when a pass yields no new evidence, or at the cap.
Report source identities, coverage, resolved and remaining findings, checks
run, and any authorization needed for a remaining change.
