// Package usage supplies shared provider account readers without acquiring credentials.
package usage

import (
	"context"
	"fmt"

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
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider usage HTTP status %d", e.StatusCode)
}
