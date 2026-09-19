package lifecycle

import "embed"

// Fixtures is the canonical family reducer battery. Every vector is a wire-level
// expectation: reducing those bytes must reach the stated verdict, whether the
// reducer is a sibling validating its own emitted stream or a host consuming
// one.
//
//go:embed testdata/fixtures/*.json
var Fixtures embed.FS

// FixtureDir is the directory inside Fixtures that holds the battery.
const FixtureDir = "testdata/fixtures"
