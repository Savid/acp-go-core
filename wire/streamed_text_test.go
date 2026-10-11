package wire

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStreamedTextEmitsOnlyUnstreamedSuffixes(t *testing.T) {
	t.Parallel()

	var text StreamedText

	require.Equal(t, "whole", text.Suffix("unstreamed", "whole"), "a terminal frame without deltas is emitted once")
	require.Empty(t, text.Suffix("unstreamed", "whole"), "a repeated terminal frame emits nothing")

	text.Add("item", "Hel")
	text.Add("item", "lo")
	text.Add("item", "")
	require.Equal(t, "Hello", text.Text("item"))
	require.Equal(t, ", world", text.Suffix("item", "Hello, world"))
	require.Equal(t, "Hello, world", text.Text("item"))
	require.Empty(t, text.Suffix("item", "Goodbye"), "a final text that does not extend the stream yields nothing")

	text.Forget("item")
	require.Empty(t, text.Text("item"))
	require.Equal(t, "again", text.Suffix("item", "again"))

	text.Reset()
	require.Empty(t, text.Text("unstreamed"))
	require.Empty(t, text.Text("other"))
}

//nolint:paralleltest // AllocsPerRun refuses to run in a parallel test.
func TestStreamedTextGrowsLinearly(t *testing.T) {
	var text StreamedText

	allocations := testing.AllocsPerRun(1, func() {
		text.Reset()

		for range 4096 {
			text.Add("item", "token ")
		}
	})
	require.Less(t, allocations, 64.0, "deltas append to one builder rather than copying the whole text each time")
	require.Equal(t, strings.Repeat("token ", 4096), text.Text("item"))
}
