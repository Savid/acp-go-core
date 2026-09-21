package process

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileLockHasOneWriter(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, err := LockFile(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	second, err := LockFile(path)
	require.Error(t, err)
	require.Nil(t, second)
	require.NoError(t, first.Close())
	second, err = LockFile(path)
	require.NoError(t, err)
	require.NoError(t, second.Close())
}

func TestFileUnlockReleasesInheritedDescriptor(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, err := LockFile(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	inherited, err := syscall.Dup(int(first.file.Fd()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Close(inherited) })
	require.NoError(t, first.Close())
	second, err := LockFile(path)
	require.NoError(t, err)
	require.NoError(t, second.Close())
}
