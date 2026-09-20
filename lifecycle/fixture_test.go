package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// controlSessionClosed is the whole control vocabulary: an out-of-band wire event
// with no notification of its own, meaning session/close completed for the
// addressed session.
const controlSessionClosed = "session_closed"

const notificationMethod = "session/update"

type manifest struct {
	Version   int    `json:"version"`
	Extension string `json:"extension"`
	Fixtures  []struct {
		File      string `json:"file"`
		Invariant string `json:"invariant"`
	} `json:"fixtures"`
}

type fixture struct {
	Name        string            `json:"name"`
	Purpose     string            `json:"purpose"`
	Negotiated  fixtureNegotiated `json:"negotiated"`
	Input       []fixtureInput    `json:"input"`
	PostRefusal []fixtureInput    `json:"postRefusal"`
	Expect      fixtureExpected   `json:"expect"`
}

type fixtureNegotiated struct {
	Negotiated
}

func (n *fixtureNegotiated) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("{}")) {
		n.Negotiated = Negotiated{}

		return nil
	}

	return json.Unmarshal(data, &n.Negotiated)
}

type fixtureInput struct {
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Control string          `json:"control"`
}

type fixtureExpected struct {
	Verdict   string          `json:"verdict"`
	Violation string          `json:"violation"`
	AtInput   *int            `json:"atInput"`
	State     json.RawMessage `json:"state"`
}

func TestFixtureManifestListsEveryVector(t *testing.T) {
	t.Parallel()

	index := loadManifest(t)
	require.Equal(t, Version, index.Version)
	require.Equal(t, MetaKey, index.Extension)

	listed := make(map[string]struct{}, len(index.Fixtures))

	for _, entry := range index.Fixtures {
		listed[entry.File] = struct{}{}

		require.NotEmpty(t, entry.Invariant, entry.File)
	}

	entries, err := fs.ReadDir(Fixtures, fixtureDir)
	require.NoError(t, err)

	for _, entry := range entries {
		if entry.Name() == "manifest.json" {
			continue
		}

		require.Contains(t, listed, entry.Name(), "fixture is not listed in the manifest")
	}

	require.Len(t, listed, len(entries)-1)
}

// The battery discharges the whole closed violation vocabulary in both
// directions: every token has a vector and every vector's token is known.
func TestFixtureBatteryPinsEveryViolationToken(t *testing.T) {
	t.Parallel()

	pinned := make(map[ViolationKind]string)

	for _, entry := range loadManifest(t).Fixtures {
		vector := loadFixture(t, entry.File)
		if vector.Expect.Verdict == "fail_closed" {
			pinned[ViolationKind(vector.Expect.Violation)] = entry.File
		}
	}

	for _, token := range vocabulary {
		require.Contains(t, pinned, token, "no vector pins %s", token)
	}

	for token, file := range pinned {
		require.Contains(t, vocabulary, token, "%s names a token outside the vocabulary", file)
	}
}

func TestReducerFixtures(t *testing.T) {
	t.Parallel()

	for _, entry := range loadManifest(t).Fixtures {
		t.Run(strings.TrimSuffix(entry.File, ".json"), func(t *testing.T) {
			t.Parallel()

			vector := loadFixture(t, entry.File)
			reducer := NewReducer(Options{Negotiated: vector.Negotiated.Negotiated})
			refusal, refusedAt := driveFixture(t, reducer, vector)

			switch vector.Expect.Verdict {
			case "accepted":
				require.Nil(t, refusal, "the fixture expects every input to reduce")
				require.Empty(t, vector.PostRefusal, "an accepted fixture has nothing to latch")
			case "fail_closed":
				require.NotNil(t, refusal, "the fixture expects a refusal")
				require.Equal(t, ViolationKind(vector.Expect.Violation), refusal.Kind)
				require.NotNil(t, vector.Expect.AtInput)
				require.Equal(t, *vector.Expect.AtInput, refusedAt)
				requireLatched(t, reducer, vector, refusal)
			default:
				t.Fatalf("unknown verdict %q", vector.Expect.Verdict)
			}

			requireStateEquals(t, vector.Expect.State, reducer.State())
		})
	}
}

// driveFixture delivers every input in order, stopping at the first refusal so the
// projection is the one that stood at the moment it was refused.
func driveFixture(t *testing.T, reducer *Reducer, vector fixture) (*ViolationError, int) {
	t.Helper()

	for index, input := range vector.Input {
		if input.Control != "" {
			require.Equal(t, controlSessionClosed, input.Control, "unknown control event")
			reducer.Close()

			continue
		}

		require.Equal(t, notificationMethod, input.Method)

		err := reducer.ReduceSessionUpdate(input.Params)
		if err == nil {
			continue
		}

		var refusal *ViolationError

		require.True(t, errors.As(err, &refusal), "input %d: %v", index, err)

		return refusal, index
	}

	return nil, -1
}

// requireLatched feeds every post-refusal input and proves the latch holds.
func requireLatched(t *testing.T, reducer *Reducer, vector fixture, refusal *ViolationError) {
	t.Helper()

	for index, input := range vector.PostRefusal {
		if input.Control != "" {
			require.Equal(t, controlSessionClosed, input.Control, "unknown control event")
			reducer.Close()

			continue
		}

		require.Equal(t, notificationMethod, input.Method, "post-refusal input %d", index)

		var latched *ViolationError

		err := reducer.ReduceSessionUpdate(input.Params)
		require.True(t, errors.As(err, &latched), "post-refusal input %d: %v", index, err)
		require.Equal(t, refusal, latched, "post-refusal input %d", index)
	}

	requireStateEquals(t, vector.Expect.State, reducer.State())
}

func loadManifest(t *testing.T) manifest {
	t.Helper()

	data, err := Fixtures.ReadFile(path.Join(fixtureDir, "manifest.json"))
	require.NoError(t, err)

	var index manifest

	require.NoError(t, json.Unmarshal(data, &index))
	require.NotEmpty(t, index.Fixtures)

	return index
}

func loadFixture(t *testing.T, file string) fixture {
	t.Helper()

	data, err := Fixtures.ReadFile(path.Join(fixtureDir, file))
	require.NoError(t, err)

	var vector fixture

	require.NoError(t, json.Unmarshal(data, &vector))
	require.NotEmpty(t, vector.Purpose, "a fixture states the invariant it proves")

	return vector
}

// requireStateEquals compares the projection against the fixture's expected state for
// exact equality.
func requireStateEquals(t *testing.T, expected json.RawMessage, actual State) {
	t.Helper()

	encoded, err := json.Marshal(actual)
	require.NoError(t, err)

	var want, got any

	require.NoError(t, json.Unmarshal(expected, &want))
	require.NoError(t, json.Unmarshal(encoded, &got))
	require.Equal(t, want, got)
}
