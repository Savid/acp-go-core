package sessionlog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	acpcore "github.com/savid/acp-go-core"
)

type testRecord struct {
	Model string `json:"model"`
}

func TestLoadDistinguishesEmptyConversationFromMissing(t *testing.T) {
	t.Parallel()
	store := acpcore.NewInMemorySessionStore()
	require.NoError(t, Commit(t.Context(), store, "empty", nil, testRecord{Model: "selected"}))
	var record testRecord
	rows, found, err := Load(t.Context(), store, "empty", &record)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, rows)
	require.Equal(t, "selected", record.Model)
	rows, found, err = Load(t.Context(), store, "missing", &record)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, rows)
}

type failingStore struct {
	acpcore.SessionStore
	fail bool
}

func (s *failingStore) Replace(ctx context.Context, main acpcore.SessionKey, replacements []acpcore.SessionStoreReplacement) error {
	if s.fail {
		return errors.New("store unavailable")
	}

	return s.SessionStore.Replace(ctx, main, replacements)
}

func TestCommitPublishesRowsAndConfigurationTogether(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := &failingStore{SessionStore: acpcore.NewInMemorySessionStore()}
	rows := [][]byte{[]byte(`{"type":"message","text":"hello"}`)}
	require.NoError(t, Commit(ctx, store, "session", rows, testRecord{Model: "one"}))
	store.fail = true
	require.Error(t, Commit(ctx, store, "session", [][]byte{rows[0], []byte(`{"text":"next"}`)}, testRecord{Model: "two"}))
	var record testRecord
	loaded, _, err := Load(ctx, store, "session", &record)
	require.NoError(t, err)
	require.Equal(t, rows, loaded)
	require.Equal(t, "one", record.Model)
	store.fail = false
	require.NoError(t, Commit(ctx, store, "session", rows, testRecord{Model: "two"}))
	loaded, _, err = Load(ctx, store, "session", &record)
	require.NoError(t, err)
	require.Equal(t, rows, loaded)
	require.Equal(t, "two", record.Model)
}

func TestLoadRejectsInvalidCurrentRecords(t *testing.T) {
	t.Parallel()
	for _, data := range []string{`{"model":"x","extra":1}`, `{"model":"x","model":"y"}`, `{"model":"x","mo\u0064el":"y"}`, `{"model":2}`, `{"model":`, `{"model":"x"} {}`} {
		t.Run(data, func(t *testing.T) {
			t.Parallel()
			store := acpcore.NewInMemorySessionStore()
			main := acpcore.SessionKey{SessionID: "s"}
			require.NoError(t, store.Replace(t.Context(), main, []acpcore.SessionStoreReplacement{
				{Key: main, Entries: []acpcore.SessionStoreEntry{[]byte(`{"type":"message"}`)}},
				{Key: acpcore.SessionKey{SessionID: "s", Subpath: ConfigSubpath}, Entries: []acpcore.SessionStoreEntry{[]byte(data)}},
			}))
			var record testRecord
			_, _, err := Load(t.Context(), store, "s", &record)
			require.Error(t, err)
		})
	}
}

func TestReconcileNativeContinuation(t *testing.T) {
	t.Parallel()
	rows := [][]byte{[]byte(`{"text":"ACP"}`), []byte(`{"text":"native"}`)}
	merged, nativeWins, err := Reconcile(rows, rows[:1])
	require.NoError(t, err)
	require.True(t, nativeWins)
	require.Equal(t, rows, merged)
	merged, nativeWins, err = Reconcile(rows[:1], rows)
	require.NoError(t, err)
	require.False(t, nativeWins)
	require.Equal(t, rows, merged)
	_, _, err = Reconcile([][]byte{[]byte(`{"text":"different"}`)}, rows)
	require.Error(t, err)
}

func TestInvalidNativeRowsNeverReplaceCommittedState(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{"null", "[]", "false", "{}{}", "{"} {
		t.Run(invalid, func(t *testing.T) {
			t.Parallel()
			store := acpcore.NewInMemorySessionStore()
			rows := [][]byte{[]byte(`{"text":"saved"}`)}
			require.NoError(t, Commit(t.Context(), store, "s", rows, testRecord{Model: "one"}))
			require.Error(t, Commit(t.Context(), store, "s", [][]byte{[]byte(invalid)}, testRecord{Model: "two"}))
			var record testRecord
			loaded, _, err := Load(t.Context(), store, "s", &record)
			require.NoError(t, err)
			require.Equal(t, rows, loaded)
			require.Equal(t, "one", record.Model)
			main := acpcore.SessionKey{SessionID: "s"}
			require.NoError(t, store.Replace(t.Context(), main, []acpcore.SessionStoreReplacement{
				{Key: main, Entries: []acpcore.SessionStoreEntry{[]byte(invalid)}},
				{Key: acpcore.SessionKey{SessionID: "s", Subpath: ConfigSubpath}, Entries: []acpcore.SessionStoreEntry{[]byte(`{"model":"one"}`)}},
			}))
			_, _, err = Load(t.Context(), store, "s", &record)
			require.Error(t, err)
		})
	}
}

type interleavingStore struct {
	acpcore.SessionStore
	afterLoad func()
}

func (s *interleavingStore) Load(ctx context.Context, sessionID string) (map[string][]acpcore.SessionStoreEntry, error) {
	rows, err := s.SessionStore.Load(ctx, sessionID)
	if s.afterLoad != nil {
		fn := s.afterLoad
		s.afterLoad = nil
		fn()
	}

	return rows, err
}

func TestLoadKeepsGenerationTogether(t *testing.T) {
	t.Parallel()
	store := &interleavingStore{SessionStore: acpcore.NewInMemorySessionStore()}
	require.NoError(t, Commit(t.Context(), store, "s", [][]byte{[]byte(`{"model":"one"}`)}, testRecord{Model: "one"}))
	store.afterLoad = func() {
		require.NoError(t, Commit(t.Context(), store, "s", [][]byte{[]byte(`{"model":"two"}`)}, testRecord{Model: "two"}))
	}
	var record testRecord
	rows, _, err := Load(t.Context(), store, "s", &record)
	require.NoError(t, err)
	var row testRecord
	require.NoError(t, json.Unmarshal(rows[0], &row))
	require.Equal(t, row.Model, record.Model, "Load returned native rows and configuration from different committed generations")
}
