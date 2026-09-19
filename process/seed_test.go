package process

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteSeedFiles(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, WriteSeedFiles(dir, nil))

	files := map[string]string{"config.toml": "a = 1\n", "skills/x.md": "# x"}
	require.NoError(t, WriteSeedFiles(dir, files))

	settings, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	require.NoError(t, err)
	require.Equal(t, "a = 1\n", string(settings))

	manifest, err := os.ReadFile(filepath.Join(dir, seedManifestFileName))
	require.NoError(t, err)
	require.JSONEq(t, `["config.toml","skills/x.md"]`, string(manifest))

	files["config.toml"] = "a = 2\n"
	require.NoError(t, WriteSeedFiles(dir, files))

	settings, err = os.ReadFile(filepath.Join(dir, "config.toml"))
	require.NoError(t, err)
	require.Equal(t, "a = 2\n", string(settings), "a managed file follows the seed")

	blocked := map[string]string{"first.txt": "1", "nested/second.txt": "2"}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nested"), []byte("not a directory"), 0o600))
	require.Error(t, WriteSeedFiles(dir, blocked), "the second file cannot be written under a regular file")
	require.NoError(t, os.Remove(filepath.Join(dir, "nested")))
	require.NoError(t, WriteSeedFiles(dir, blocked), "the first file was recorded as managed before it was written")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "operator.json"), []byte("{}"), 0o600))

	var seedErr *SeedFileError

	require.ErrorAs(t, WriteSeedFiles(dir, map[string]string{"operator.json": "x"}), &seedErr)
	require.Equal(t, "operator.json", seedErr.Name)
	require.EqualError(t, seedErr, `invalid seed file "operator.json"`)

	for _, bad := range []string{"", "/abs", "../up", "a/../b", "./x", ".seed-manifest.json", "a\x00b"} {
		require.ErrorAs(t, WriteSeedFiles(dir, map[string]string{bad: "x"}), &seedErr, bad)
	}
}
