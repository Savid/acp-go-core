package sessionlog

import (
	"context"
	"errors"
	"testing"

	acpcore "github.com/savid/acp-go-core"
	"github.com/stretchr/testify/require"
)

type testRecord struct {
	Model string `json:"model"`
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
	loaded, err := Load(ctx, store, "session", &record)
	require.NoError(t, err)
	require.Equal(t, rows, loaded)
	require.Equal(t, "one", record.Model)
	store.fail = false
	require.NoError(t, Commit(ctx, store, "session", rows, testRecord{Model: "two"}))
	loaded, err = Load(ctx, store, "session", &record)
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
			require.NoError(t, store.Append(t.Context(), acpcore.SessionKey{SessionID: "s"}, []acpcore.SessionStoreEntry{[]byte(`{"type":"message"}`)}))
			require.NoError(t, store.Append(t.Context(), acpcore.SessionKey{SessionID: "s", Subpath: ConfigSubpath}, []acpcore.SessionStoreEntry{[]byte(data)}))
			var record testRecord
			_, err := Load(t.Context(), store, "s", &record)
			require.Error(t, err)
		})
	}
}

func TestReconcileNativeContinuation(t *testing.T) {
	t.Parallel()
	rows := [][]byte{[]byte(`{"text":"ACP"}`), []byte(`{"text":"native"}`)}
	merged, err := Reconcile(rows, rows[:1])
	require.NoError(t, err)
	require.Equal(t, rows, merged)
	merged, err = Reconcile(rows[:1], rows)
	require.NoError(t, err)
	require.Equal(t, rows, merged)
	_, err = Reconcile([][]byte{[]byte(`{"text":"different"}`)}, rows)
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
			loaded, err := Load(t.Context(), store, "s", &record)
			require.NoError(t, err)
			require.Equal(t, rows, loaded)
			require.Equal(t, "one", record.Model)
			require.NoError(t, store.Append(t.Context(), acpcore.SessionKey{SessionID: "s"}, []acpcore.SessionStoreEntry{[]byte(invalid)}))
			_, err = Load(t.Context(), store, "s", &record)
			require.Error(t, err)
		})
	}
}
