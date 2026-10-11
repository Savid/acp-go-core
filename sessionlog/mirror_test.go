package sessionlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	acpcore "github.com/savid/acp-go-core"
)

// countingStore records the writes a mirror makes.
type countingStore struct {
	*acpcore.InMemorySessionStore

	mu       sync.Mutex
	replaces int
	appends  int
	appended int
	configs  int
	fail     bool
}

func newCountingStore() *countingStore {
	return &countingStore{InMemorySessionStore: acpcore.NewInMemorySessionStore()}
}

func (s *countingStore) Replace(ctx context.Context, main acpcore.SessionKey, replacements []acpcore.SessionStoreReplacement) error {
	s.mu.Lock()
	s.replaces++
	fail := s.fail
	s.mu.Unlock()

	if fail {
		return errors.New("store unavailable")
	}

	return s.InMemorySessionStore.Replace(ctx, main, replacements)
}

func (s *countingStore) Append(ctx context.Context, main acpcore.SessionKey, base int, entries []acpcore.SessionStoreEntry, replacements []acpcore.SessionStoreReplacement) error {
	s.mu.Lock()
	s.appends++
	s.appended += len(entries)
	s.configs += len(replacements)
	fail := s.fail
	s.mu.Unlock()

	if fail {
		return errors.New("store unavailable")
	}

	return s.InMemorySessionStore.Append(ctx, main, base, entries, replacements)
}

func (s *countingStore) counts() (replaces, appends, appended, configs int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.replaces, s.appends, s.appended, s.configs
}

func requireStored(t *testing.T, store acpcore.SessionStore, rows []string, model string) {
	t.Helper()

	var record testRecord

	loaded, found, err := Load(t.Context(), store, "s", &record)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, rows, rowStrings(loaded))
	require.Equal(t, model, record.Model)
}

func TestMirrorAppendsOnlyNewRowsAndChangedConfiguration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	mirror := NewMirror(store, "s")

	appendFile(t, path, "{\"a\":1}\n{\"b\":2}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))

	replaces, appends, _, _ := store.counts()
	require.Equal(t, 1, replaces, "the first commit publishes the whole generation")
	require.Zero(t, appends)

	appendFile(t, path, "{\"c\":3}\n{\"d\":")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}, "one")

	replaces, appends, appended, configs := store.counts()
	require.Equal(t, 1, replaces)
	require.Equal(t, 1, appends)
	require.Equal(t, 1, appended)
	require.Zero(t, configs, "an unchanged configuration is not rewritten")

	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "two"})))
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}, "two")

	_, appends, appended, configs = store.counts()
	require.Equal(t, 2, appends, "a configuration change appends no rows")
	require.Equal(t, 1, appended)
	require.Equal(t, 1, configs)

	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "two"})))

	replaces, appends, _, _ = store.counts()
	require.Equal(t, 1, replaces)
	require.Equal(t, 2, appends, "an unchanged generation is not written")

	appendFile(t, path, "4}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "two"})))
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`, `{"c":3}`, `{"d":4}`}, "two")
}

func TestMirrorRepublishesARewrittenHistory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	mirror := NewMirror(store, "s")

	appendFile(t, path, "{\"a\":1}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))

	_, err := WriteJSONL(path, [][]byte{[]byte(`{"x":1}`), []byte(`{"y":2}`)})
	require.NoError(t, err)
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))
	requireStored(t, store, []string{`{"x":1}`, `{"y":2}`}, "one")

	replaces, appends, _, _ := store.counts()
	require.Equal(t, 2, replaces, "a history that disagrees with the committed rows is republished")
	require.Zero(t, appends)

	_, err = WriteJSONL(path, [][]byte{[]byte(`{"x":1}`), []byte(`{"y":2}`), []byte(`{"z":3}`)})
	require.NoError(t, err)
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))
	requireStored(t, store, []string{`{"x":1}`, `{"y":2}`, `{"z":3}`}, "one")

	replaces, appends, appended, _ := store.counts()
	require.Equal(t, 2, replaces, "a replaced file that extends the committed rows appends")
	require.Equal(t, 1, appends)
	require.Equal(t, 1, appended)
}

func TestMirrorRefusesAShorterHistoryUntilReset(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	mirror := NewMirror(store, "s")

	appendFile(t, path, "{\"a\":1}\n{\"b\":2}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))
	require.NoError(t, os.Remove(path))

	require.ErrorContains(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})), "shrank")
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`}, "one")

	mirror.Reset()
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "rebound"})))
	requireStored(t, store, []string{}, "rebound")
}

func TestMirrorRepairsAGenerationWrittenAroundIt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	mirror := NewMirror(store, "s")

	appendFile(t, path, "{\"a\":1}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))
	require.NoError(t, commitRows(ctx, store, "s", [][]byte{[]byte(`{"other":1}`), []byte(`{"other":2}`)}, testRecord{Model: "other"}))

	appendFile(t, path, "{\"b\":2}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`}, "one")
}

func TestMirrorFailureKeepsThePreviousGeneration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	mirror := NewMirror(store, "s")

	appendFile(t, path, "{\"a\":1}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))

	appendFile(t, path, "{\"b\":2}\n")
	store.mu.Lock()
	store.fail = true
	store.mu.Unlock()
	require.Error(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "two"})))
	requireStored(t, store, []string{`{"a":1}`}, "one")

	store.mu.Lock()
	store.fail = false
	store.mu.Unlock()
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "two"})))
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`}, "two")

	appendFile(t, path, "not json\n")
	require.Error(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "two"})))
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`}, "two")
}

func TestMirrorHydrateMaterializesStoredRowsThenAppends(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	require.NoError(t, commitRows(ctx, store, "s", [][]byte{[]byte(`{"a":1}`), []byte(`{"b":2}`)}, testRecord{Model: "one"}))

	var record testRecord
	stored, _, err := Load(ctx, store, "s", &record)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "log.jsonl")
	mirror := NewMirror(store, "s")

	var validated int

	rows, err := mirror.Hydrate(ctx, path, stored, record, func(rows [][]byte) error {
		validated = len(rows)

		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, validated)
	require.Equal(t, []string{`{"a":1}`, `{"b":2}`}, rowStrings(rows))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{\"a\":1}\n{\"b\":2}\n", string(data))

	appendFile(t, path, "{\"c\":3}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(record)))
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}, "one")

	replaces, appends, appended, configs := store.counts()
	require.Equal(t, 1, replaces, "hydration and the following turn write no whole generation")
	require.Equal(t, 1, appends)
	require.Equal(t, 1, appended)
	require.Zero(t, configs)
}

func TestMirrorHydrateAdoptsLongerNativeRows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	require.NoError(t, commitRows(ctx, store, "s", [][]byte{[]byte(`{"a":1}`)}, testRecord{Model: "one"}))

	path := filepath.Join(t.TempDir(), "log.jsonl")
	appendFile(t, path, "{\"a\":1}\n{\"native\":2}\n")

	var record testRecord
	stored, _, err := Load(ctx, store, "s", &record)
	require.NoError(t, err)

	mirror := NewMirror(store, "s")
	rows, err := mirror.Hydrate(ctx, path, stored, record, nil)
	require.NoError(t, err)
	require.Equal(t, []string{`{"a":1}`, `{"native":2}`}, rowStrings(rows))
	requireStored(t, store, []string{`{"a":1}`, `{"native":2}`}, "one")

	_, appends, appended, _ := store.counts()
	require.Equal(t, 1, appends)
	require.Equal(t, 1, appended)

	_, err = WriteJSONL(path, [][]byte{[]byte(`{"different":1}`)})
	require.NoError(t, err)
	_, err = NewMirror(store, "s").Hydrate(ctx, path, rows, record, nil)
	require.Error(t, err, "a disagreement at a shared position fails")

	refused := errors.New("not this session's rows")
	_, err = NewMirror(store, "s").Hydrate(ctx, filepath.Join(t.TempDir(), "absent.jsonl"), rows, record, func([][]byte) error { return refused })
	require.ErrorIs(t, err, refused)
}

// messageSource serves a growing history by index, continuing after the
// position a cursor carries.
type messageSource struct {
	rows  [][]byte
	reads []any
}

func (s *messageSource) read(from Cursor) (Tail, error) {
	s.reads = append(s.reads, from.Position())

	start, continued := from.Position().(int)
	if !continued {
		return Tail{Rows: s.rows, Next: Position(len(s.rows)), Full: true}, nil
	}

	return Tail{Rows: s.rows[start:], Next: Position(len(s.rows))}, nil
}

func TestMirrorContinuesANonFileSourceFromItsPosition(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	source := &messageSource{rows: [][]byte{[]byte(`{"m":1}`)}}
	mirror := NewMirror(store, "s")

	var seen []int

	record := func(tail Tail) (any, error) {
		seen = append(seen, len(tail.Rows))

		return testRecord{Model: "one"}, nil
	}

	require.NoError(t, mirror.Commit(ctx, source.read, record))
	source.rows = append(source.rows, []byte(`{"m":2}`), []byte(`{"m":3}`))
	require.NoError(t, mirror.Commit(ctx, source.read, record))
	requireStored(t, store, []string{`{"m":1}`, `{"m":2}`, `{"m":3}`}, "one")

	require.Equal(t, []any{nil, 1}, source.reads, "the second read continues from the committed position")
	require.Equal(t, []int{1, 2}, seen, "the configuration is computed for the rows each read returned")

	replaces, appends, appended, _ := store.counts()
	require.Equal(t, 1, replaces)
	require.Equal(t, 1, appends)
	require.Equal(t, 2, appended)
}

func TestMirrorAdoptWritesNothingAndContinues(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	rows := [][]byte{[]byte(`{"m":1}`), []byte(`{"m":2}`)}
	require.NoError(t, commitRows(ctx, store, "s", rows, testRecord{Model: "one"}))

	var record testRecord
	stored, _, err := Load(ctx, store, "s", &record)
	require.NoError(t, err)

	source := &messageSource{rows: append(stored, []byte(`{"m":3}`))}
	mirror := NewMirror(store, "s")
	require.NoError(t, mirror.Adopt(stored, record, Position(len(stored))))

	replaces, appends, _, _ := store.counts()
	require.Equal(t, 1, replaces, "adopting writes nothing")
	require.Zero(t, appends)

	require.NoError(t, mirror.Commit(ctx, source.read, fixed(record)))
	requireStored(t, store, []string{`{"m":1}`, `{"m":2}`, `{"m":3}`}, "one")
	require.Equal(t, []any{2}, source.reads)

	replaces, appends, appended, configs := store.counts()
	require.Equal(t, 1, replaces)
	require.Equal(t, 1, appends)
	require.Equal(t, 1, appended)
	require.Zero(t, configs, "the adopted configuration is not rewritten")
}

func TestMirrorRecordFailureWritesNothing(t *testing.T) {
	t.Parallel()
	store := newCountingStore()
	refused := errors.New("session is closing")

	err := NewMirror(store, "s").Commit(t.Context(), Rows([][]byte{[]byte(`{"a":1}`)}), func(Tail) (any, error) { return nil, refused })
	require.ErrorIs(t, err, refused)

	replaces, appends, _, _ := store.counts()
	require.Zero(t, replaces)
	require.Zero(t, appends)
}

func TestMirrorValidatesOnlyRowsPastTheCommittedPrefix(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	held := [][]byte{[]byte(`{"a":1}`), []byte(`not json the store already holds`)}
	mirror := NewMirror(store, "s")
	require.NoError(t, mirror.Adopt(held, testRecord{Model: "one"}, Cursor{}))
	require.NoError(t, store.Replace(ctx, acpcore.SessionKey{SessionID: "s"}, []acpcore.SessionStoreReplacement{
		{Key: acpcore.SessionKey{SessionID: "s"}, Entries: entries(held)},
	}))

	require.NoError(t, mirror.Commit(ctx, Rows(append(held, []byte(`{"c":3}`))), fixed(testRecord{Model: "one"})))
	require.Error(t, mirror.Commit(ctx, Rows(append(held, []byte(`{"c":3}`), []byte(`{`))), fixed(testRecord{Model: "one"})))
}

func TestMirrorHydrateCutsATornNativeTail(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	require.NoError(t, commitRows(ctx, store, "s", [][]byte{[]byte(`{"a":1}`)}, testRecord{Model: "one"}))

	path := filepath.Join(t.TempDir(), "log.jsonl")
	appendFile(t, path, "{\"a\":1}\n{\"b\":2}\n{\"torn\":")

	var record testRecord
	stored, _, err := Load(ctx, store, "s", &record)
	require.NoError(t, err)

	mirror := NewMirror(store, "s")
	rows, err := mirror.Hydrate(ctx, path, stored, record, nil)
	require.NoError(t, err)
	require.Equal(t, []string{`{"a":1}`, `{"b":2}`}, rowStrings(rows))

	appendFile(t, path, "{\"c\":3}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(record)), "the next native line starts on a line of its own")
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`, `{"c":3}`}, "one")
}

func TestMirrorRereadsSourceAndRecordAfterAFailedAppend(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newCountingStore()
	path := filepath.Join(t.TempDir(), "log.jsonl")
	mirror := NewMirror(store, "s")

	appendFile(t, path, "{\"a\":1}\n")
	require.NoError(t, mirror.Commit(ctx, File(path), fixed(testRecord{Model: "one"})))
	require.NoError(t, commitRows(ctx, store, "s", [][]byte{[]byte(`{"other":1}`), []byte(`{"other":2}`)}, testRecord{Model: "other"}))
	appendFile(t, path, "{\"b\":2}\n")

	var reads, records int

	source := func(from Cursor) (Tail, error) {
		reads++

		return ReadJSONL(path, from)
	}
	record := func(Tail) (any, error) {
		records++

		return testRecord{Model: "one"}, nil
	}

	require.NoError(t, mirror.Commit(ctx, source, record))
	require.Equal(t, 2, reads, "the refused append rereads the whole history")
	require.Equal(t, 2, records, "the configuration is computed for each read")
	requireStored(t, store, []string{`{"a":1}`, `{"b":2}`}, "one")
}
