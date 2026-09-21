package wire

import (
	"github.com/coder/acp-go-sdk"
)

// LifecycleCarrier builds the notification one lifecycle envelope rides: an
// identity-only session_info_update with the envelope on its _meta.
func LifecycleCarrier(sessionID acp.SessionId, envelope map[string]any) acp.SessionNotification {
	return acp.SessionNotification{
		Meta:      map[string]any{LifecycleKey: envelope},
		SessionId: sessionID,
		Update:    acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{}},
	}
}
