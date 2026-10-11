package sessionlog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func appendFile(t *testing.T, path, data string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, err = file.WriteString(data)
	require.NoError(t, err)
	require.NoError(t, file.Close())
}

func rowStrings(rows [][]byte) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, string(row))
	}

	return out
}

func TestReadJSONLReadsCompleteLinesThenOnlyAppendedOnes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	appendFile(t, path, "{\"a\":1}\n\n  {\"b\":2} \r\n{\"c\":")

	tail, err := ReadJSONL(path, Cursor{})
	require.NoError(t, err)
	require.True(t, tail.Full)
	require.Equal(t, []string{`{"a":1}`, `{"b":2}`}, rowStrings(tail.Rows))
	require.Equal(t, int64(len("{\"a\":1}\n\n  {\"b\":2} \r\n")), tail.Next.Offset(), "an unterminated line is left for a later read")

	appendFile(t, path, "3}\n{\"d\":4}\n")
	tail, err = ReadJSONL(path, tail.Next)
	require.NoError(t, err)
	require.False(t, tail.Full)
	require.Equal(t, []string{`{"c":3}`, `{"d":4}`}, rowStrings(tail.Rows))

	again, err := ReadJSONL(path, tail.Next)
	require.NoError(t, err)
	require.False(t, again.Full)
	require.Empty(t, again.Rows)
	require.Equal(t, tail.Next.Offset(), again.Next.Offset())
}

func TestReadJSONLRereadsAReplacedTruncatedOrRewrittenFile(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(t *testing.T, path string){
		"truncated": func(t *testing.T, path string) {
			t.Helper()
			require.NoError(t, os.Truncate(path, 8))
			appendFile(t, path, "{\"x\":1}\n{\"y\":2}\n")
		},
		"rewritten in place": func(t *testing.T, path string) {
			t.Helper()
			file, err := os.OpenFile(path, os.O_WRONLY, 0)
			require.NoError(t, err)
			_, err = file.WriteAt([]byte(`{"b":9}`), 8)
			require.NoError(t, err)
			require.NoError(t, file.Close())
			appendFile(t, path, "{\"c\":3}\n")
		},
		"replaced": func(t *testing.T, path string) {
			t.Helper()
			_, err := WriteJSONL(path, [][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`), []byte(`{"c":3}`)})
			require.NoError(t, err)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "log.jsonl")
			appendFile(t, path, "{\"a\":1}\n{\"b\":2}\n")
			first, err := ReadJSONL(path, Cursor{})
			require.NoError(t, err)

			change(t, path)

			tail, err := ReadJSONL(path, first.Next)
			require.NoError(t, err)
			require.True(t, tail.Full, "a file that no longer extends the cursor is read in full")

			whole, err := ReadJSONL(path, Cursor{})
			require.NoError(t, err)
			require.Equal(t, rowStrings(whole.Rows), rowStrings(tail.Rows))
		})
	}
}

func TestReadJSONLMissingFileIsEmpty(t *testing.T) {
	t.Parallel()
	tail, err := ReadJSONL(filepath.Join(t.TempDir(), "absent.jsonl"), Cursor{})
	require.NoError(t, err)
	require.True(t, tail.Full)
	require.Empty(t, tail.Rows)
}

func TestReadJSONLFileStopsAtSize(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	appendFile(t, path, "{\"a\":1}\n{\"b\":2}\n")

	file, err := os.Open(path)
	require.NoError(t, err)

	defer file.Close()

	tail, err := ReadJSONLFile(file, Cursor{}, 8)
	require.NoError(t, err)
	require.Equal(t, []string{`{"a":1}`}, rowStrings(tail.Rows))

	tail, err = ReadJSONLFile(file, tail.Next, -1)
	require.NoError(t, err)
	require.False(t, tail.Full)
	require.Equal(t, []string{`{"b":2}`}, rowStrings(tail.Rows))

	_, err = ReadJSONLFile(file, Cursor{}, 1<<20)
	require.Error(t, err)
}

func TestWriteJSONLPublishesRowsAndContinuesFromItsCursor(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "log.jsonl")
	cursor, err := WriteJSONL(path, [][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`)})
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{\"a\":1}\n{\"b\":2}\n", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	appendFile(t, path, "{\"c\":3}\n")
	tail, err := ReadJSONL(path, cursor)
	require.NoError(t, err)
	require.False(t, tail.Full)
	require.Equal(t, []string{`{"c":3}`}, rowStrings(tail.Rows))

	_, err = WriteJSONL(path, [][]byte{[]byte("{\"a\":\n1}")})
	require.Error(t, err)

	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1, "a failed write leaves no staging file and keeps the previous file")
}
