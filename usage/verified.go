package usage

import (
	"context"
	"errors"

	"github.com/savid/acp-go-core/wire"
)

// Access identifies the credential and route selected by the native runtime.
// BaseURL is where the runtime sends the key; Fingerprint covers native
// routing facts beyond the credential itself.
type Access struct {
	APIKey      string
	AccountID   string
	BaseURL     string
	Reason      string
	Fingerprint [32]byte
}

// ReadVerified publishes usage only while the native credential and route
// remain the same throughout the provider read.
func ReadVerified(ctx context.Context, access func(context.Context) (Access, error), reader Reader) (wire.AccountUsageResponse, error) {
	before, err := access(ctx)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if before.Reason != "" {
		return wire.AccountUsageUnavailable(before.Reason), nil
	}

	response, err := reader.Read(ctx, Credential{Token: before.APIKey, AccountID: before.AccountID, BaseURL: before.BaseURL})
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	after, err := access(ctx)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if before != after {
		return wire.AccountUsageResponse{}, errors.New("provider credentials or route changed")
	}

	return response, response.Validate()
}
