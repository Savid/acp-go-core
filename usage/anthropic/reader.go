// Package anthropic reads Claude subscription allowances and usage credits.
package anthropic

import (
	"context"
	"net/http"
	"strings"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/usage/internal/usagehttp"
	"github.com/savid/acp-go-core/wire"
)

const (
	ProviderID = "anthropic"
	Endpoint   = "https://api.anthropic.com/api/oauth/usage"
)

// Reader uses the supplied transport or http.DefaultTransport. It never acquires credentials.
type Reader struct{ Transport http.RoundTripper }

// Read observes allowances for the caller's effective Claude OAuth token.
func (r Reader) Read(ctx context.Context, credential usage.Credential) (wire.AccountUsageResponse, error) {
	if ctx.Err() != nil {
		return wire.AccountUsageResponse{}, ctx.Err()
	}

	if strings.TrimSpace(credential.Token) == "" {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), nil
	}

	if !strings.HasPrefix(credential.Token, "sk-ant-oat01-") {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
	}

	response, err := usagehttp.Get(ctx, r.Transport, Endpoint, credential.Token, http.Header{"Anthropic-Beta": {"oauth-2025-04-20"}})
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if response.StatusCode == http.StatusUnauthorized {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), nil
	}

	if response.StatusCode == http.StatusForbidden {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
	}

	if response.StatusCode != http.StatusOK {
		return wire.AccountUsageResponse{}, &usage.HTTPError{StatusCode: response.StatusCode}
	}

	var observation Observation
	if err := response.Decode(&observation); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	return observation.Response("", response.ObservedAt)
}
