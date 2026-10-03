package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestWriteSeedFilesRefusesSymlinksAndStagesTheManifest(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "agent")
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(dir, 0o700))

	var seedErr *SeedFileError

	require.NoError(t, os.Symlink(filepath.Join(outside, "escaped.conf"), filepath.Join(dir, "dangling.conf")))
	require.ErrorAs(t, WriteSeedFiles(dir, map[string]string{"dangling.conf": "x"}), &seedErr, "a dangling symlink at a seed name is refused")
	require.NoFileExists(t, filepath.Join(outside, "escaped.conf"))

	require.NoError(t, os.WriteFile(filepath.Join(outside, "target.conf"), []byte("operator"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "target.conf"), filepath.Join(dir, "linked.conf")))
	require.ErrorAs(t, WriteSeedFiles(dir, map[string]string{"linked.conf": "x"}), &seedErr, "a symlink at a seed name is refused")
	target, err := os.ReadFile(filepath.Join(outside, "target.conf"))
	require.NoError(t, err)
	require.Equal(t, "operator", string(target))

	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "away")))
	require.Error(t, WriteSeedFiles(dir, map[string]string{"away/escaped.conf": "x"}), "a symlinked directory leaving the root is refused")
	require.NoFileExists(t, filepath.Join(outside, "escaped.conf"))

	require.NoError(t, WriteSeedFiles(dir, map[string]string{"config.toml": "a = 1\n"}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	require.ElementsMatch(t, []string{seedManifestFileName, seedManifestFileName + ".lock", "away", "config.toml", "dangling.conf", "linked.conf"}, names, "no manifest staging file is left behind")
	require.ErrorAs(t, WriteSeedFiles(dir, map[string]string{seedManifestFileName + ".staging": "x"}), &seedErr, "manifest staging names are reserved")
}

func TestWriteSeedFilesConcurrentLaunches(t *testing.T) {
	t.Parallel()
	contents := strings.Repeat("shared seed contents\n", 4096)
	for range 20 {
		dir := t.TempDir()
		start := make(chan struct{})
		var workers sync.WaitGroup
		for index := range 8 {
			workers.Go(func() {
				<-start
				err := WriteSeedFiles(dir, map[string]string{
					"shared.conf":                        contents,
					fmt.Sprintf("worker-%d.conf", index): "worker",
				})
				if !assert.NoError(t, err) {
					return
				}
				data, err := os.ReadFile(filepath.Join(dir, "shared.conf"))
				assert.NoError(t, err)
				assert.Equal(t, contents, string(data))
			})
		}
		close(start)
		workers.Wait()
		root, err := os.OpenRoot(dir)
		require.NoError(t, err)
		manifest, err := loadSeedManifest(root)
		require.NoError(t, root.Close())
		require.NoError(t, err)
		require.Len(t, manifest, 9)
	}
}

func TestWriteSeedFilesPublishesCompleteFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first, second := strings.Repeat("a", 1<<18), strings.Repeat("b", 1<<18)
	require.NoError(t, WriteSeedFiles(dir, map[string]string{"config": first}))
	done := make(chan struct{})
	var readers sync.WaitGroup
	readers.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
				data, err := os.ReadFile(filepath.Join(dir, "config"))
				if !assert.NoError(t, err) || !assert.True(t, string(data) == first || string(data) == second, "reader saw a partial seed file") {
					return
				}
			}
		}
	})
	for range 20 {
		assert.NoError(t, WriteSeedFiles(dir, map[string]string{"config": second}))
		assert.NoError(t, WriteSeedFiles(dir, map[string]string{"config": first}))
	}
	close(done)
	readers.Wait()
}
