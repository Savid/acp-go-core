package wire

import (
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/acp-go-sdk"
)

const sessionsPageSize = 50

// SessionRequests reserves session identities while an establishing request is
// in flight. Its zero value is ready to use.
type SessionRequests struct {
	mu      sync.Mutex
	pending map[acp.SessionId]struct{}
}

// Acquire refuses a concurrent restore of the same session before either
// request can hydrate or bind its native state. The caller releases the
// reservation after publication has been arranged or the request has failed.
func (r *SessionRequests) Acquire(id acp.SessionId) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.pending[id]; exists {
		return nil, Backpressure("session_restore")
	}

	if r.pending == nil {
		r.pending = make(map[acp.SessionId]struct{})
	}

	r.pending[id] = struct{}{}

	return sync.OnceFunc(func() {
		r.mu.Lock()
		delete(r.pending, id)
		r.mu.Unlock()
	}), nil
}

// PaginateSessions orders sessions newest first, then by id, and returns the
// page selected by a raw URL-base64 offset cursor.
func PaginateSessions(sessions []acp.SessionInfo, cursor *string) ([]acp.SessionInfo, *string, error) {
	sessions = slices.Clone(sessions)
	slices.SortFunc(sessions, func(left, right acp.SessionInfo) int {
		var leftStamp, rightStamp string
		if left.UpdatedAt != nil {
			leftStamp = *left.UpdatedAt
		}

		if right.UpdatedAt != nil {
			rightStamp = *right.UpdatedAt
		}

		if order := strings.Compare(rightStamp, leftStamp); order != 0 {
			return order
		}

		return strings.Compare(string(left.SessionId), string(right.SessionId))
	})

	offset := 0

	if cursor != nil && *cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(*cursor)
		if err != nil {
			return nil, nil, Unsupported("cursor")
		}

		offset, err = strconv.Atoi(string(data))
		if err != nil || offset < 0 || offset > len(sessions) {
			return nil, nil, Unsupported("cursor")
		}
	}

	end := offset + sessionsPageSize
	if end >= len(sessions) {
		return sessions[offset:], nil, nil
	}

	next := base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))

	return sessions[offset:end], &next, nil
}
