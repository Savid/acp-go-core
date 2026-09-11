package wire

import (
	"context"
	"encoding/json"
	"fmt"
)

// RawEventMaxBytes bounds one raw-event notification payload.
const RawEventMaxBytes = 64 * 1024

// Raw-event payload and marker members.
const (
	rawFieldSequence = "sequence"
	rawFieldSource   = "source"
	rawFieldEvent    = "event"
	rawFieldMeta     = "_meta"

	rawMarkerTruncated      = "truncated"
	rawMarkerReason         = "reason"
	rawMarkerMaxBytes       = "maxBytes"
	rawMarkerSizeBytes      = "sizeBytes"
	rawReasonOversize       = "oversize"
	rawReasonUnserializable = "unserializable"
)

// RawEventMethod names a sibling's raw-event notification.
func RawEventMethod(vendor string) string { return "_" + vendor + "/rawEvent" }

// RawEventsEnabled reads the per-session opt-in from a session lifecycle
// request's _meta.
func RawEventsEnabled(meta map[string]any, vendor string) bool {
	vendorMeta, _ := meta[vendor].(map[string]any)
	rawEvent, _ := vendorMeta["rawEvent"].(map[string]any)
	enabled, _ := rawEvent["enabled"].(bool)

	return enabled
}

// Notifier delivers one extension notification.
type Notifier func(ctx context.Context, method string, params map[string]any) error

// RawEvents emits one session's raw events with a per-session sequence that
// starts at 1 and advances only on successful delivery.
type RawEvents struct {
	method    string
	sessionID string
	source    string
	enabled   bool
	sequence  uint64
}

// NewRawEvents builds the emitter for one session.
func NewRawEvents(vendor, sessionID, source string, enabled bool) *RawEvents {
	return &RawEvents{method: RawEventMethod(vendor), sessionID: sessionID, source: source, enabled: enabled}
}

// Enabled reports whether the session opted in.
func (r *RawEvents) Enabled() bool { return r != nil && r.enabled }

// Emit delivers event as the next raw-event notification. A nil event emits
// nothing. An oversize or unserializable event is replaced by the fixed marker.
// A delivery error is returned and does not consume the sequence.
func (r *RawEvents) Emit(ctx context.Context, notify Notifier, event map[string]any) error {
	if !r.Enabled() || event == nil {
		return nil
	}

	payload, err := CapRawEvent(map[string]any{
		fieldSessionID:   r.sessionID,
		rawFieldSequence: r.sequence + 1,
		rawFieldSource:   r.source,
		rawFieldEvent:    event,
	})
	if err != nil {
		return err
	}

	if err := notify(ctx, r.method, payload); err != nil {
		return err
	}

	r.sequence++

	return nil
}

// CapRawEvent bounds one raw-event payload. A payload whose encoding exceeds
// RawEventMaxBytes, or cannot be encoded, keeps its identity members and
// replaces event with the fixed truncation marker.
func CapRawEvent(payload map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(payload)
	if err == nil && len(encoded) <= RawEventMaxBytes {
		return payload, nil
	}

	marker := map[string]any{
		rawMarkerTruncated: true,
		rawMarkerReason:    rawReasonOversize,
		rawMarkerMaxBytes:  RawEventMaxBytes,
		rawMarkerSizeBytes: len(encoded),
	}
	if err != nil {
		marker = map[string]any{
			rawMarkerTruncated: true,
			rawMarkerReason:    rawReasonUnserializable,
			rawMarkerMaxBytes:  RawEventMaxBytes,
		}
	}

	capped := map[string]any{
		fieldSessionID:   payload[fieldSessionID],
		rawFieldSequence: payload[rawFieldSequence],
		rawFieldSource:   payload[rawFieldSource],
		rawFieldEvent:    marker,
	}
	if meta, ok := payload[rawFieldMeta]; ok {
		capped[rawFieldMeta] = meta
	}

	final, err := json.Marshal(capped)
	if err != nil {
		return nil, fmt.Errorf("marshal capped raw event payload: %w", err)
	}

	if len(final) > RawEventMaxBytes {
		return nil, fmt.Errorf("capped raw event payload is %d bytes, exceeds %d", len(final), RawEventMaxBytes)
	}

	return capped, nil
}
