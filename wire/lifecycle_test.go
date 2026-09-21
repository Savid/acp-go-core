package wire

import (
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestLifecycleCarrierIsIdentityOnly(t *testing.T) {
	t.Parallel()

	carrier := LifecycleCarrier("s", map[string]any{"version": 1})
	require.Equal(t, acp.SessionId("s"), carrier.SessionId)
	require.Equal(t, map[string]any{"version": 1}, carrier.Meta[LifecycleKey])
	require.NotNil(t, carrier.Update.SessionInfoUpdate)
	require.Nil(t, carrier.Update.SessionInfoUpdate.Title)
	require.Nil(t, carrier.Update.SessionInfoUpdate.UpdatedAt)
}

func TestCheckSessionIDRefusesAnOverlongID(t *testing.T) {
	t.Parallel()

	require.Nil(t, CheckSessionID("session"))
	require.Nil(t, CheckSessionID(acp.SessionId(make([]byte, SessionIDMaxBytes))))
	require.Equal(t, UnknownSession(), CheckSessionID(acp.SessionId(make([]byte, SessionIDMaxBytes+1))))
}
