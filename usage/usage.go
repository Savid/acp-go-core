// Package usage supplies shared provider account readers without acquiring credentials.
package usage

import (
	"context"
	"fmt"
	"time"

	"github.com/savid/acp-go-core/wire"
)

// Freshness is the maximum age of a provider HTTP observation.
const Freshness = time.Minute

// Reader reads the account addressed by the caller's effective provider credential.
type Reader interface {
	Read(context.Context, string) (wire.AccountUsageResponse, error)
}

// HTTPError reports an unsuccessful provider read without exposing credentials or bodies.
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider usage HTTP status %d", e.StatusCode)
}
