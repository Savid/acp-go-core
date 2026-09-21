package lifecycle

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewIncarnationDoesNotReuseAResumedSessionStream(t *testing.T) {
	t.Parallel()

	const session = "21ca471c-5e11-4f17-a3cf-6eefbc2acdcf"

	names := []string{NewIncarnation(session), NewIncarnation(session), NewIncarnation(session)}
	for _, name := range names {
		require.True(t, strings.HasPrefix(name, session+":"), "an incarnation names its session")
	}
	require.Len(t, map[string]struct{}{names[0]: {}, names[1]: {}, names[2]: {}}, len(names))
}
