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

	require.ErrorIs(t, store.Replace(ctx, key, []acpcore.SessionStoreReplacement{{Key: key}}), context.Canceled)

	_, err := store.Load(ctx, key.SessionID)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, store.Delete(ctx, key), context.Canceled)

	_, err = store.ListSessions(ctx)
	require.ErrorIs(t, err, context.Canceled)
}
