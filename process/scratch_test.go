package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScratchDirCreatesUnderTheParent(t *testing.T) {
	t.Parallel()

	parent := filepath.Join(t.TempDir(), "scratch")
	dir, err := ScratchDir(parent, "pi", "ext")
	require.NoError(t, err)
	require.Equal(t, parent, filepath.Dir(dir))
	require.True(t, strings.HasPrefix(filepath.Base(dir), "acp-go-pi-ext-"))

	info, err := os.Stat(parent)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestScratchPathNamesWithoutCreating(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	path, err := ScratchPath(parent, "pi", "ext", "digest")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(parent, "acp-go-pi-ext-digest"), path)
	require.NoFileExists(t, path)
}
