# Implementation Comparison

Use this when a feature exists in more than one sibling or belongs in the
core module.

## Decide the owner

- **Identical semantics** across siblings: the logic belongs in `acp-go-core`.
  A copy in a sibling is a finding; the fix moves it to the core and deletes
  the copies.
- **Same shape, native differences**: the public shape stays uniform and the
  difference lives under `internal/<harness>`. Record the difference in the
  registry.
- **Vendor-only**: keep it in the sibling with the `With<Vendor>` prefix.

## Compare

For each sibling in scope, trace the feature end to end: public option or
request field, validation, the native call, the event mapping back, the store
carrier, and the tests. Note where one sibling has a stronger implementation
than another and whether the difference is native or accidental.

## Report

For each feature: the owner decision, the siblings that deviate, the concrete
change per repo, and whether the contract or registry needs an edit.
