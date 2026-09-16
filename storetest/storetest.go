// Package storetest is the session store contract battery. A host store passes
// it by construction: every rule in the contract is a case here.
package storetest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	acpcore "github.com/savid/acp-go-core"
)

// Run drives the contract battery against a fresh store from newStore for each
// case.
func Run(t *testing.T, newStore func(t *testing.T) acpcore.SessionStore) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, store acpcore.SessionStore)
	}{
		{"replace publishes and load reads the whole generation", replaceAndLoad},
		{"load of a missing session is nil", loadMissing},
		{"load keeps a live empty main record present", loadEmptyMain},
		{"load returns caller-owned copies", loadReturnsCopies},
		{"replace owns the entries it receives", replaceOwnsEntries},
		{"load reads one complete generation during replacements", concurrentGeneration},
		{"replace tombstones unlisted subpaths", replaceGeneration},
		{"replace keeps a listed empty subpath live", replaceEmptySubpath},
		{"replace clears a subpath tombstone it lists", replaceRevivesSubpath},
		{"replace refuses a foreign or duplicate key before writing", replaceRefusesBadKeys},
		{"replace refuses a non-main key", replaceRefusesNonMain},
		{"delete main cascades and is final", deleteMainIsFinal},
		{"delete subpath removes only that subpath", deleteSubpath},
		{"delete of a missing key succeeds", deleteMissing},
		{"list sessions orders newest first then by id", listSessions},
		{"empty session id", emptySessionID},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, newStore(t))
		})
	}
}

func entry(s string) acpcore.SessionStoreEntry { return acpcore.SessionStoreEntry(s) }

func main(id string) acpcore.SessionKey { return acpcore.SessionKey{SessionID: id} }

func sub(id, path string) acpcore.SessionKey { return acpcore.SessionKey{SessionID: id, Subpath: path} }

func entries(values ...string) []acpcore.SessionStoreEntry {
	result := make([]acpcore.SessionStoreEntry, 0, len(values))
	for _, value := range values {
		result = append(result, entry(value))
	}

	return result
}

// publish writes one generation whose main record holds mainEntries and whose
// subpaths are the given replacements.
func publish(ctx context.Context, store acpcore.SessionStore, id string, mainEntries []acpcore.SessionStoreEntry, subpaths ...acpcore.SessionStoreReplacement) error {
	replacements := append([]acpcore.SessionStoreReplacement{{Key: main(id), Entries: mainEntries}}, subpaths...)

	return store.Replace(ctx, main(id), replacements)
}

func loadEntries(ctx context.Context, store acpcore.SessionStore, key acpcore.SessionKey) ([]acpcore.SessionStoreEntry, error) {
	generation, err := store.Load(ctx, key.SessionID)

	return generation[key.Subpath], err
}

func replaceAndLoad(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{"a":1}`, `{"b":2}`), acpcore.SessionStoreReplacement{Key: sub("s", "config"), Entries: entries(`{"c":3}`)}))

	generation, err := store.Load(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, map[string][]acpcore.SessionStoreEntry{
		"":       entries(`{"a":1}`, `{"b":2}`),
		"config": entries(`{"c":3}`),
	}, generation)
}

func loadMissing(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()

	generation, err := store.Load(ctx, "absent")
	require.NoError(t, err)
	require.Nil(t, generation, "a never-written session has no generation")

	require.NoError(t, publish(ctx, store, "s", entries(`{}`)))
	require.NoError(t, store.Delete(ctx, main("s")))

	generation, err = store.Load(ctx, "s")
	require.NoError(t, err)
	require.Nil(t, generation, "a tombstoned session has no generation")
}

func loadEmptyMain(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", nil, acpcore.SessionStoreReplacement{Key: sub("s", "config"), Entries: entries(`{"model":"m"}`)}))

	generation, err := store.Load(ctx, "s")
	require.NoError(t, err)
	require.NotNil(t, generation)

	rows, present := generation[acpcore.SessionStoreMainSubpath]
	require.True(t, present, "a committed empty main record is present under the empty subpath")
	require.Empty(t, rows)
	require.Equal(t, entries(`{"model":"m"}`), generation["config"])

	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.Equal(t, "s", sessions[0].SessionID)
}

func loadReturnsCopies(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{"a":1}`), acpcore.SessionStoreReplacement{Key: sub("s", "config"), Entries: entries(`{"c":1}`)}))

	generation, err := store.Load(ctx, "s")
	require.NoError(t, err)

	generation[""][0][2] = 'z'
	generation[""] = append(generation[""], entry(`{"extra":true}`))
	delete(generation, "config")

	again, err := store.Load(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, map[string][]acpcore.SessionStoreEntry{
		"":       entries(`{"a":1}`),
		"config": entries(`{"c":1}`),
	}, again, "mutating a loaded generation must not change the store")
}

func replaceOwnsEntries(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	rows := entries(`{"a":1}`)
	require.NoError(t, publish(ctx, store, "s", rows))

	rows[0][2] = 'z'

	got, err := loadEntries(ctx, store, main("s"))
	require.NoError(t, err)
	require.Equal(t, entries(`{"a":1}`), got, "mutating entries after Replace must not change the store")
}

func replaceGeneration(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{"one":1}`), acpcore.SessionStoreReplacement{Key: sub("s", "a"), Entries: entries(`{"sub":"a"}`)}))
	require.NoError(t, publish(ctx, store, "s", entries(`{"two":2}`), acpcore.SessionStoreReplacement{Key: sub("s", "b"), Entries: entries(`{"sub":"b"}`)}))

	generation, err := store.Load(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, map[string][]acpcore.SessionStoreEntry{
		"":  entries(`{"two":2}`),
		"b": entries(`{"sub":"b"}`),
	}, generation, "an unlisted subpath is tombstoned by the replacement")
}

func replaceEmptySubpath(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{}`), acpcore.SessionStoreReplacement{Key: sub("s", "empty")}))

	generation, err := store.Load(ctx, "s")
	require.NoError(t, err)

	rows, present := generation["empty"]
	require.True(t, present)
	require.Empty(t, rows)
}

func replaceRevivesSubpath(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{}`), acpcore.SessionStoreReplacement{Key: sub("s", "a"), Entries: entries(`{"v":1}`)}))
	require.NoError(t, store.Delete(ctx, sub("s", "a")))

	gone, err := loadEntries(ctx, store, sub("s", "a"))
	require.NoError(t, err)
	require.Empty(t, gone)

	require.NoError(t, publish(ctx, store, "s", entries(`{}`), acpcore.SessionStoreReplacement{Key: sub("s", "a"), Entries: entries(`{"v":2}`)}))

	got, err := loadEntries(ctx, store, sub("s", "a"))
	require.NoError(t, err)
	require.Equal(t, entries(`{"v":2}`), got)
}

func replaceRefusesBadKeys(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{"keep":true}`)))

	err := store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: entries(`{}`)},
		{Key: sub("other", "x")},
	})
	require.Error(t, err, "a key naming another session is refused")

	err = store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: entries(`{}`)},
		{Key: sub("s", "x")},
		{Key: sub("s", "x")},
	})
	require.Error(t, err, "a duplicate key is refused")

	err = store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{{Key: sub("s", "x")}})
	require.Error(t, err, "a set without the main key is refused")

	got, err := loadEntries(ctx, store, main("s"))
	require.NoError(t, err)
	require.Equal(t, entries(`{"keep":true}`), got, "a refused replacement writes nothing")
}

func replaceRefusesNonMain(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	err := store.Replace(context.Background(), sub("s", "a"), []acpcore.SessionStoreReplacement{{Key: sub("s", "a")}})
	require.Error(t, err)
}

func deleteMainIsFinal(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{}`), acpcore.SessionStoreReplacement{Key: sub("s", "a"), Entries: entries(`{}`)}))
	require.NoError(t, store.Delete(ctx, main("s")))

	generation, err := store.Load(ctx, "s")
	require.NoError(t, err)
	require.Nil(t, generation)

	require.NoError(t, publish(ctx, store, "s", entries(`{"revive":true}`), acpcore.SessionStoreReplacement{Key: sub("s", "b"), Entries: entries(`{"late":true}`)}))

	generation, err = store.Load(ctx, "s")
	require.NoError(t, err)
	require.Nil(t, generation, "a tombstoned main never revives")

	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	require.Empty(t, sessions)
}

func deleteSubpath(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "s", entries(`{}`),
		acpcore.SessionStoreReplacement{Key: sub("s", "a"), Entries: entries(`{}`)},
		acpcore.SessionStoreReplacement{Key: sub("s", "b"), Entries: entries(`{}`)},
	))
	require.NoError(t, store.Delete(ctx, sub("s", "a")))

	generation, err := store.Load(ctx, "s")
	require.NoError(t, err)
	require.Equal(t, map[string][]acpcore.SessionStoreEntry{
		"":  entries(`{}`),
		"b": entries(`{}`),
	}, generation)
}

func deleteMissing(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	require.NoError(t, store.Delete(context.Background(), main("never")))
}

func listSessions(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, publish(ctx, store, "b", entries(`{}`)))
	require.NoError(t, publish(ctx, store, "a", entries(`{}`), acpcore.SessionStoreReplacement{Key: sub("a", "x"), Entries: entries(`{}`)}))

	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	require.Len(t, sessions, 2)

	for _, session := range sessions {
		require.NotZero(t, session.UpdatedAtUnixMilli)
	}

	for index := 1; index < len(sessions); index++ {
		previous, current := sessions[index-1], sessions[index]
		require.GreaterOrEqual(t, previous.UpdatedAtUnixMilli, current.UpdatedAtUnixMilli)

		if previous.UpdatedAtUnixMilli == current.UpdatedAtUnixMilli {
			require.Less(t, previous.SessionID, current.SessionID)
		}
	}
}

func emptySessionID(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Delete(ctx, main("")))
	require.ErrorIs(t, store.Replace(ctx, main(""), []acpcore.SessionStoreReplacement{{Key: main("")}}), acpcore.ErrSessionIDRequired)

	generation, err := store.Load(ctx, "")
	require.NoError(t, err)
	require.Nil(t, generation)
}

func concurrentGeneration(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := t.Context()
	replace := func(value string) error {
		rows := entries(value)

		return store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{{Key: main("s"), Entries: rows}, {Key: sub("s", "config"), Entries: rows}})
	}
	require.NoError(t, replace(`{"generation":0}`))

	done := make(chan error, 1)
	defer func() { require.NoError(t, <-done) }()

	go func() {
		for index := range 500 {
			value := `{"generation":1}`
			if index%2 == 0 {
				value = `{"generation":2}`
			}

			if err := replace(value); err != nil {
				done <- err

				return
			}
		}

		done <- nil
	}()

	for range 500 {
		generation, err := store.Load(ctx, "s")
		require.NoError(t, err)
		require.Equal(t, generation[""], generation["config"], "a reader observed a mixed generation")
	}
}
