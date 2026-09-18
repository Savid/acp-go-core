// Package usage supplies shared provider account readers without acquiring credentials.
package usage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/savid/acp-go-core/wire"
)

// Credential identifies the effective provider account resolved by the native harness.
type Credential struct {
	Token     string
	AccountID string
}

// Reader reads the account addressed by the caller's effective provider credential.
type Reader interface {
	Read(context.Context, Credential) (wire.AccountUsageResponse, error)
}

// HTTPError reports an unsuccessful provider read without exposing credentials or bodies.
type HTTPError struct {
	StatusCode int
	RetryAt    time.Time
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider usage HTTP status %d", e.StatusCode)
}

// RequestError exposes provider status and retry timing without response bodies or credentials.
func RequestError(vendor string, err error) *acp.RequestError {
	failure := wire.InternalFailure(vendor, "account_usage")

	var provider *HTTPError
	if errors.As(err, &provider) {
		data, _ := failure.Data.(map[string]any)

		data["statusCode"] = provider.StatusCode
		if !provider.RetryAt.IsZero() {
			data["retryAt"] = wire.AccountUsageTime(provider.RetryAt)
		}
	}

	return failure
}
