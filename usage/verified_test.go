package usage_test

import (
	"context"
	"testing"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/wire"
	"github.com/stretchr/testify/require"
)

type usageReader func(context.Context, usage.Credential) (wire.AccountUsageResponse, error)

func (f usageReader) Read(ctx context.Context, credential usage.Credential) (wire.AccountUsageResponse, error) {
	return f(ctx, credential)
}

func TestReadVerifiedRejectsChangedAccess(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"token", "account", "route", "availability", "unchanged"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			access := usage.Access{APIKey: "token", AccountID: "account"}
			reader := usageReader(func(_ context.Context, credential usage.Credential) (wire.AccountUsageResponse, error) {
				require.Equal(t, usage.Credential{Token: "token", AccountID: "account"}, credential)
				switch field {
				case "token":
					access.APIKey = "changed"
				case "account":
					access.AccountID = "changed"
				case "route":
					access.Fingerprint[0] = 1
				case "availability":
					access.Reason = wire.AccountUsageNotReported
				}

				return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
			})
			response, err := usage.ReadVerified(t.Context(), func(context.Context) (usage.Access, error) { return access, nil }, reader)
			if field == "unchanged" {
				require.NoError(t, err)
				require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), response)
			} else {
				require.EqualError(t, err, "provider credentials or route changed")
				require.Equal(t, wire.AccountUsageResponse{}, response)
			}
		})
	}
}

func TestReadVerifiedUnavailableSkipsProvider(t *testing.T) {
	t.Parallel()
	response, err := usage.ReadVerified(t.Context(), func(context.Context) (usage.Access, error) {
		return usage.Access{Reason: wire.AccountUsageNotAuthenticated}, nil
	}, nil)
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), response)
}
