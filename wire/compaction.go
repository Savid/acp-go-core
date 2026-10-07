package wire

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"

	"github.com/coder/acp-go-sdk"
)

const (
	CompactionInProgress    = "in_progress"
	CompactionCompleted     = "completed"
	CompactionFailed        = "failed"
	CompactionCancelled     = "cancelled"
	CompactionTriggerAuto   = "auto"
	CompactionTriggerManual = "manual"
)

// Compaction describes one top-level context compaction attempt.
type Compaction struct {
	CompactionID  string `json:"compactionId"`
	Status        string `json:"status"`
	Trigger       string `json:"trigger,omitempty"`
	ContextBefore *int   `json:"contextBefore,omitempty"`
	ContextAfter  *int   `json:"contextAfter,omitempty"`
}

// CompactionCarrier carries compaction without changing session info or usage.
func CompactionCarrier(sessionID acp.SessionId, value Compaction) acp.SessionNotification {
	return acp.SessionNotification{
		SessionId: sessionID,
		Update:    acp.SessionUpdate{SessionInfoUpdate: &acp.SessionSessionInfoUpdate{}},
		Meta:      map[string]any{CompactionKey: value},
	}
}

// Compactions correlates native attempt keys and publishes each transition once.
// Its zero value is ready to use. Callers supply events in native order and keep
// keys distinct across native runs. Empty keys represent uncorrelated outcomes.
type Compactions struct {
	mu       sync.Mutex
	attempts map[string]Compaction
}

// Publish assigns an opaque ID and retains reported facts across transitions.
// Publication does not inherit request cancellation. Failed delivery is not retried.
func (c *Compactions) Publish(ctx context.Context, client interface {
	SessionUpdate(context.Context, acp.SessionNotification) error
}, sessionID acp.SessionId, key string, value Compaction) error {
	switch value.Status {
	case CompactionInProgress, CompactionCompleted, CompactionFailed, CompactionCancelled:
	default:
		return errors.New("invalid compaction status")
	}

	if value.Status == CompactionInProgress && key == "" {
		return errors.New("compaction start requires an attempt key")
	}

	if value.Trigger != "" && value.Trigger != CompactionTriggerAuto && value.Trigger != CompactionTriggerManual {
		return errors.New("invalid compaction trigger")
	}

	for _, tokens := range []*int{value.ContextBefore, value.ContextAfter} {
		if tokens != nil && *tokens < 0 {
			return errors.New("negative compaction context")
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	previous, exists := c.attempts[key]
	if key != "" && exists && (previous.Status != CompactionInProgress || value.Status == CompactionInProgress) {
		return nil
	}

	value.CompactionID = previous.CompactionID
	if value.CompactionID == "" {
		value.CompactionID = rand.Text()
	}

	if value.Trigger == "" {
		value.Trigger = previous.Trigger
	}

	value.ContextBefore = compactionTokens(value.ContextBefore, previous.ContextBefore)
	value.ContextAfter = compactionTokens(value.ContextAfter, previous.ContextAfter)

	if key != "" {
		if c.attempts == nil {
			c.attempts = make(map[string]Compaction)
		}

		saved := value
		saved.ContextBefore = compactionTokens(value.ContextBefore, nil)
		saved.ContextAfter = compactionTokens(value.ContextAfter, nil)
		c.attempts[key] = saved
	}

	if client == nil {
		return nil
	}

	return client.SessionUpdate(context.WithoutCancel(ctx), CompactionCarrier(sessionID, value))
}

func compactionTokens(current, previous *int) *int {
	if current != nil {
		return new(*current)
	}

	if previous != nil {
		return new(*previous)
	}

	return nil
}
