package wire

import (
	"fmt"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestSessionPagesHaveStableOrder(t *testing.T) {
	t.Parallel()
	stamp := "2026-09-14T00:00:00Z"
	sessions := make([]acp.SessionInfo, 51)
	for index := range sessions {
		sessions[index] = acp.SessionInfo{SessionId: acp.SessionId(fmt.Sprintf("s%02d", 50-index)), UpdatedAt: &stamp}
	}
	page, cursor, err := PaginateSessions(sessions, nil)
	require.NoError(t, err)
	require.Len(t, page, 50)
	require.Equal(t, acp.SessionId("s00"), page[0].SessionId)
	require.Equal(t, acp.SessionId("s49"), page[49].SessionId)
	require.NotNil(t, cursor)
	page, cursor, err = PaginateSessions(sessions, cursor)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.Equal(t, acp.SessionId("s50"), page[0].SessionId)
	require.Nil(t, cursor)
	require.Equal(t, acp.SessionId("s50"), sessions[0].SessionId)
}
