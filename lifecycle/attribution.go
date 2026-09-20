package lifecycle

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// contentUpdates are the session update variants that carry a turn's content.
// Session-scoped variants such as commands, modes, and config options are not
// attributed to a turn.
var contentUpdates = map[string]struct{}{
	updateAgentMessageChunk: {},
	updateAgentThoughtChunk: {},
	updateUserMessageChunk:  {},
	updateToolCall:          {},
	updateToolCallUpdate:    {},
	updatePlan:              {},
	updateUsage:             {},
}

// The sessionUpdate discriminators of the content variants.
const (
	updateAgentMessageChunk = "agent_message_chunk"
	updateAgentThoughtChunk = "agent_thought_chunk"
	updateUserMessageChunk  = "user_message_chunk"
	updateToolCall          = "tool_call"
	updateToolCallUpdate    = "tool_call_update"
	updatePlan              = "plan"
	updateUsage             = "usage_update"
)

// reservedPrefix names the family's own _meta keys, which carry no vendor
// content hints.
const reservedPrefix = "acp-go.dev/"

// vendorHintFields are the members no vendor namespace may carry on a
// notification: content is attributed by the foreground, never by a hint.
var vendorHintFields = []string{"turnId", "messageId"}

// CheckAttribution proves, over one session's session/update notification
// payloads in delivery order, that every content update belongs to the running
// foreground: it arrives while the foreground is live, and no vendor namespace
// under its _meta names a turnId or messageId. Envelopes are reduced as a host
// reduces them, so a refused stream is reported as the reducer's refusal.
func CheckAttribution(negotiated Negotiated, notifications []json.RawMessage) error {
	reducer := NewReducer(Options{Negotiated: negotiated})

	for index, params := range notifications {
		if err := checkVendorHints(params); err != nil {
			return fmt.Errorf("notification %d: %w", index, err)
		}

		err := reducer.ReduceSessionUpdate(params)
		if err == nil {
			continue
		}

		if !errors.Is(err, ErrNoEnvelope) {
			return fmt.Errorf("notification %d: %w", index, err)
		}

		kind, content := contentUpdate(params)
		if !content {
			continue
		}

		foreground := reducer.State().Foreground
		if foreground == nil || foreground.State == ForegroundIdle {
			return fmt.Errorf("notification %d: %s arrived with no live foreground", index, kind)
		}
	}

	return nil
}

func contentUpdate(params json.RawMessage) (string, bool) {
	notification, ok := jsonObject(params)
	if !ok {
		return "", false
	}

	update, ok := jsonObject(notification[updateField])
	if !ok {
		return "", false
	}

	kind, ok := jsonString(update[sessionUpdateField])
	if !ok {
		return "", false
	}

	_, content := contentUpdates[kind]

	return kind, content
}

func checkVendorHints(params json.RawMessage) error {
	notification, ok := jsonObject(params)
	if !ok {
		return nil
	}

	meta, ok := jsonObject(notification[metaField])
	if !ok {
		return nil
	}

	for namespace, raw := range meta {
		if strings.HasPrefix(namespace, reservedPrefix) {
			continue
		}

		fields, ok := jsonObject(raw)
		if !ok {
			continue
		}

		for _, field := range vendorHintFields {
			if _, present := fields[field]; present {
				return fmt.Errorf("_meta.%s.%s correlates content by hint", namespace, field)
			}
		}
	}

	return nil
}
