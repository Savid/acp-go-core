package wire

import (
	"encoding/base64"
	"slices"
	"strconv"
	"strings"

	"github.com/coder/acp-go-sdk"
)

const sessionsPageSize = 50

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
