package acpcore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	acpcore "github.com/savid/acp-go-core"
	"github.com/savid/acp-go-core/storetest"
)

func TestInMemorySessionStoreContract(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(*testing.T) acpcore.SessionStore { return acpcore.NewInMemorySessionStore() })
}

func TestInMemorySessionStoreCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store := acpcore.NewInMemorySessionStore()
	key := acpcore.SessionKey{SessionID: "s"}

	require.ErrorIs(t, store.Append(ctx, key, []acpcore.SessionStoreEntry{acpcore.SessionStoreEntry(`{}`)}), context.Canceled)

	_, err := store.Load(ctx, key)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, store.Delete(ctx, key), context.Canceled)

	_, err = store.ListSessions(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestInMemorySessionStoreLoadReturnsCopies(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := acpcore.NewInMemorySessionStore()
	key := acpcore.SessionKey{SessionID: "s"}
	original := acpcore.SessionStoreEntry(`{"a":1}`)

	require.NoError(t, store.Append(ctx, key, []acpcore.SessionStoreEntry{original}))
	original[2] = 'z'

	got, err := store.Load(ctx, key)
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(got[0]))

	got[0][2] = 'z'

	again, err := store.Load(ctx, key)
	require.NoError(t, err)
	require.Equal(t, `{"a":1}`, string(again[0]))
}
