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
		{"append and load preserve order", appendAndLoad},
		{"empty append is a no-op", emptyAppend},
		{"replace publishes one generation and tombstones unlisted subpaths", replaceGeneration},
		{"replace keeps a listed empty subpath live", replaceEmptySubpath},
		{"replace clears a subpath tombstone it lists", replaceRevivesSubpath},
		{"replace refuses a foreign or duplicate key before writing", replaceRefusesBadKeys},
		{"replace refuses a non-main key", replaceRefusesNonMain},
		{"delete main cascades and is final", deleteMainIsFinal},
		{"delete subpath removes only that subpath", deleteSubpath},
		{"delete of a missing key succeeds", deleteMissing},
		{"list sessions orders newest first then by id", listSessions},
		{"list subkeys is sorted and excludes main", listSubkeys},
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

func appendAndLoad(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{"a":1}`), entry(`{"b":2}`)}))
	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{"c":3}`)}))

	got, err := store.Load(ctx, main("s"))
	require.NoError(t, err)
	require.Equal(t, []acpcore.SessionStoreEntry{entry(`{"a":1}`), entry(`{"b":2}`), entry(`{"c":3}`)}, got)

	missing, err := store.Load(ctx, main("absent"))
	require.NoError(t, err)
	require.Empty(t, missing)
}

func emptyAppend(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main("s"), nil))

	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	require.Empty(t, sessions)
}

func replaceGeneration(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{"one":1}`)}))
	require.NoError(t, store.Append(ctx, sub("s", "a"), []acpcore.SessionStoreEntry{entry(`{"sub":"a"}`)}))

	require.NoError(t, store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: []acpcore.SessionStoreEntry{entry(`{"two":2}`)}},
		{Key: sub("s", "b"), Entries: []acpcore.SessionStoreEntry{entry(`{"sub":"b"}`)}},
	}))

	got, err := store.Load(ctx, main("s"))
	require.NoError(t, err)
	require.Equal(t, []acpcore.SessionStoreEntry{entry(`{"two":2}`)}, got)

	old, err := store.Load(ctx, sub("s", "a"))
	require.NoError(t, err)
	require.Empty(t, old, "an unlisted subpath is tombstoned by the replacement")

	subkeys, err := store.ListSubkeys(ctx, main("s"))
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, subkeys)
}

func replaceEmptySubpath(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: []acpcore.SessionStoreEntry{entry(`{}`)}},
		{Key: sub("s", "empty")},
	}))

	subkeys, err := store.ListSubkeys(ctx, main("s"))
	require.NoError(t, err)
	require.Equal(t, []string{"empty"}, subkeys)
}

func replaceRevivesSubpath(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, sub("s", "a"), []acpcore.SessionStoreEntry{entry(`{"v":1}`)}))
	require.NoError(t, store.Delete(ctx, sub("s", "a")))
	require.NoError(t, store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: []acpcore.SessionStoreEntry{entry(`{}`)}},
		{Key: sub("s", "a"), Entries: []acpcore.SessionStoreEntry{entry(`{"v":2}`)}},
	}))

	got, err := store.Load(ctx, sub("s", "a"))
	require.NoError(t, err)
	require.Equal(t, []acpcore.SessionStoreEntry{entry(`{"v":2}`)}, got)
}

func replaceRefusesBadKeys(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{"keep":true}`)}))

	err := store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: []acpcore.SessionStoreEntry{entry(`{}`)}},
		{Key: sub("other", "x")},
	})
	require.Error(t, err, "a key naming another session is refused")

	err = store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: []acpcore.SessionStoreEntry{entry(`{}`)}},
		{Key: sub("s", "x")},
		{Key: sub("s", "x")},
	})
	require.Error(t, err, "a duplicate key is refused")

	err = store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{{Key: sub("s", "x")}})
	require.Error(t, err, "a set without the main key is refused")

	got, err := store.Load(ctx, main("s"))
	require.NoError(t, err)
	require.Equal(t, []acpcore.SessionStoreEntry{entry(`{"keep":true}`)}, got, "a refused replacement writes nothing")
}

func replaceRefusesNonMain(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	err := store.Replace(context.Background(), sub("s", "a"), []acpcore.SessionStoreReplacement{{Key: sub("s", "a")}})
	require.Error(t, err)
}

func deleteMainIsFinal(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Append(ctx, sub("s", "a"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Delete(ctx, main("s")))

	for _, key := range []acpcore.SessionKey{main("s"), sub("s", "a")} {
		got, err := store.Load(ctx, key)
		require.NoError(t, err)
		require.Empty(t, got)
	}

	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{"late":true}`)}))
	require.NoError(t, store.Append(ctx, sub("s", "b"), []acpcore.SessionStoreEntry{entry(`{"late":true}`)}))
	require.NoError(t, store.Replace(ctx, main("s"), []acpcore.SessionStoreReplacement{
		{Key: main("s"), Entries: []acpcore.SessionStoreEntry{entry(`{"revive":true}`)}},
	}))

	for _, key := range []acpcore.SessionKey{main("s"), sub("s", "b")} {
		got, err := store.Load(ctx, key)
		require.NoError(t, err)
		require.Empty(t, got, "a tombstoned main never revives")
	}

	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	require.Empty(t, sessions)

	subkeys, err := store.ListSubkeys(ctx, main("s"))
	require.NoError(t, err)
	require.Empty(t, subkeys)
}

func deleteSubpath(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Append(ctx, sub("s", "a"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Append(ctx, sub("s", "b"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Delete(ctx, sub("s", "a")))

	got, err := store.Load(ctx, main("s"))
	require.NoError(t, err)
	require.Len(t, got, 1)

	subkeys, err := store.ListSubkeys(ctx, main("s"))
	require.NoError(t, err)
	require.Equal(t, []string{"b"}, subkeys)

	require.NoError(t, store.Append(ctx, sub("s", "a"), []acpcore.SessionStoreEntry{entry(`{"late":true}`)}))

	late, err := store.Load(ctx, sub("s", "a"))
	require.NoError(t, err)
	require.Empty(t, late, "a tombstoned subpath refuses appends")
}

func deleteMissing(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	require.NoError(t, store.Delete(context.Background(), main("never")))
}

func listSessions(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main("b"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Append(ctx, main("a"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Append(ctx, sub("a", "x"), []acpcore.SessionStoreEntry{entry(`{}`)}))

	sessions, err := store.ListSessions(ctx)
	require.NoError(t, err)
	require.Len(t, sessions, 2)

	for index := 1; index < len(sessions); index++ {
		previous, current := sessions[index-1], sessions[index]
		require.GreaterOrEqual(t, previous.UpdatedAtUnixMilli, current.UpdatedAtUnixMilli)

		if previous.UpdatedAtUnixMilli == current.UpdatedAtUnixMilli {
			require.Less(t, previous.SessionID, current.SessionID)
		}
	}
}

func listSubkeys(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, sub("s", "b"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Append(ctx, sub("s", "a"), []acpcore.SessionStoreEntry{entry(`{}`)}))
	require.NoError(t, store.Append(ctx, main("s"), []acpcore.SessionStoreEntry{entry(`{}`)}))

	subkeys, err := store.ListSubkeys(ctx, main("s"))
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, subkeys)
}

func emptySessionID(t *testing.T, store acpcore.SessionStore) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, store.Append(ctx, main(""), nil))
	require.NoError(t, store.Delete(ctx, main("")))
	require.ErrorIs(t, store.Append(ctx, main(""), []acpcore.SessionStoreEntry{entry(`{}`)}), acpcore.ErrSessionIDRequired)
	require.ErrorIs(t, store.Replace(ctx, main(""), []acpcore.SessionStoreReplacement{{Key: main("")}}), acpcore.ErrSessionIDRequired)
}
