package wire

import (
	"maps"
	"slices"

	"github.com/coder/acp-go-sdk"
)

// SessionMetaKey is the host-owned _meta namespace a session/new carries.
const SessionMetaKey = "acp-go.dev/session"

const sessionMetaFieldEphemeral = "ephemeral"

// SessionMeta is what a host states about a session it opens.
type SessionMeta struct {
	// Ephemeral marks a session the host deletes without ever needing it back.
	// The sibling never writes it to the store and never lists it.
	Ephemeral bool
}

// DecodeSessionMeta reads the host-owned namespace from a session/new _meta.
// An absent namespace is the zero value; a namespace that is not an object, a
// field it does not define, or a non-boolean ephemeral is unsupported, naming
// its path.
func DecodeSessionMeta(meta map[string]any) (SessionMeta, *acp.RequestError) {
	raw, present := meta[SessionMetaKey]
	if !present {
		return SessionMeta{}, nil
	}

	fields, ok := raw.(map[string]any)
	if !ok {
		return SessionMeta{}, Unsupported("_meta." + SessionMetaKey)
	}

	var decoded SessionMeta

	for _, name := range slices.Sorted(maps.Keys(fields)) {
		path := "_meta." + SessionMetaKey + "." + name
		if name != sessionMetaFieldEphemeral {
			return SessionMeta{}, Unsupported(path)
		}

		value, isBool := fields[name].(bool)
		if !isBool {
			return SessionMeta{}, Unsupported(path)
		}

		decoded.Ephemeral = value
	}

	return decoded, nil
}

// RefuseSessionMeta refuses the namespace on a session/load or session/resume:
// those name a session the store holds, which an ephemeral session never is,
// so the host has nothing to state there.
func RefuseSessionMeta(meta map[string]any) *acp.RequestError {
	if _, present := meta[SessionMetaKey]; present {
		return Unsupported("_meta." + SessionMetaKey)
	}

	return nil
}

// Apply writes the namespace onto meta, allocating it when nil, and returns
// the map. A zero SessionMeta writes nothing.
func (m SessionMeta) Apply(meta map[string]any) map[string]any {
	if !m.Ephemeral {
		return meta
	}

	if meta == nil {
		meta = make(map[string]any, 1)
	}

	meta[SessionMetaKey] = map[string]any{sessionMetaFieldEphemeral: true}

	return meta
}
