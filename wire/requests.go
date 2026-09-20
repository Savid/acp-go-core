package wire

import (
	"slices"

	"github.com/coder/acp-go-sdk"
)

// SessionRequestConfig accumulates the members a session lifecycle request
// builder sets. A sibling's vendor option constructors merge into Meta.
type SessionRequestConfig struct {
	AdditionalDirectories []string
	Meta                  map[string]any
}

// SessionRequestOption configures an embedded-Go session lifecycle request.
type SessionRequestOption func(*SessionRequestConfig)

func newSessionRequestConfig(opts ...SessionRequestOption) SessionRequestConfig {
	config := SessionRequestConfig{}
	for _, opt := range opts {
		opt(&config)
	}

	return config
}

// NewSessionRequest constructs a session/new request. It always carries an
// empty mcpServers array: MCP is not supported.
func NewSessionRequest(cwd string, opts ...SessionRequestOption) acp.NewSessionRequest {
	config := newSessionRequestConfig(opts...)

	return acp.NewSessionRequest{
		Cwd:                   cwd,
		McpServers:            []acp.McpServer{},
		AdditionalDirectories: slices.Clone(config.AdditionalDirectories),
		Meta:                  CloneMap(config.Meta),
	}
}

// LoadSessionRequest constructs a session/load request.
func LoadSessionRequest(sessionID acp.SessionId, cwd string, opts ...SessionRequestOption) acp.LoadSessionRequest {
	config := newSessionRequestConfig(opts...)

	return acp.LoadSessionRequest{
		SessionId:             sessionID,
		Cwd:                   cwd,
		McpServers:            []acp.McpServer{},
		AdditionalDirectories: slices.Clone(config.AdditionalDirectories),
		Meta:                  CloneMap(config.Meta),
	}
}

// ResumeSessionRequest constructs a session/resume request.
func ResumeSessionRequest(sessionID acp.SessionId, cwd string, opts ...SessionRequestOption) acp.ResumeSessionRequest {
	config := newSessionRequestConfig(opts...)

	return acp.ResumeSessionRequest{
		SessionId:             sessionID,
		Cwd:                   cwd,
		McpServers:            []acp.McpServer{},
		AdditionalDirectories: slices.Clone(config.AdditionalDirectories),
		Meta:                  CloneMap(config.Meta),
	}
}

// DeleteSessionRequest constructs a session/delete request.
func DeleteSessionRequest(sessionID acp.SessionId) acp.UnstableDeleteSessionRequest {
	return acp.UnstableDeleteSessionRequest{SessionId: sessionID}
}

// WithSessionAdditionalDirectories sets additional workspace directories.
func WithSessionAdditionalDirectories(paths ...string) SessionRequestOption {
	cloned := slices.Clone(paths)

	return func(config *SessionRequestConfig) {
		config.AdditionalDirectories = slices.Clone(cloned)
	}
}

// WithSessionMeta merges host metadata into a session lifecycle request. A
// key matching a family-reserved literal panics rather than merging: those
// namespaces are stamped by the sibling alone.
func WithSessionMeta(meta map[string]any) SessionRequestOption {
	rejectReservedMeta("WithSessionMeta", meta)

	cloned := CloneMap(meta)

	return func(config *SessionRequestConfig) {
		config.Meta = MergeMap(config.Meta, cloned)
	}
}

// WithSessionMetaValue merges one already-validated vendor metadata object.
// Sibling option constructors use it to add their own namespace.
func WithSessionMetaValue(meta map[string]any) SessionRequestOption {
	cloned := CloneMap(meta)

	return func(config *SessionRequestConfig) {
		config.Meta = MergeMap(config.Meta, cloned)
	}
}

// PromptRequest constructs a session/prompt request.
func PromptRequest(sessionID acp.SessionId, blocks ...acp.ContentBlock) acp.PromptRequest {
	return acp.PromptRequest{
		SessionId: sessionID,
		Prompt:    append([]acp.ContentBlock{}, blocks...),
	}
}

// TextPromptRequest constructs a session/prompt request with one text block.
func TextPromptRequest(sessionID acp.SessionId, text string) acp.PromptRequest {
	return PromptRequest(sessionID, acp.TextBlock(text))
}

// CancelRequest constructs a session/cancel notification.
func CancelRequest(sessionID acp.SessionId) acp.CancelNotification {
	return acp.CancelNotification{SessionId: sessionID}
}

// SetConfigOptionRequest constructs a value-id session/set_config_option request.
func SetConfigOptionRequest(
	sessionID acp.SessionId,
	configID acp.SessionConfigId,
	value acp.SessionConfigValueId,
) acp.SetSessionConfigOptionRequest {
	return acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{
			SessionId: sessionID,
			ConfigId:  configID,
			Value:     value,
		},
	}
}

// ListSessionsRequestOption configures an embedded-Go session/list request.
type ListSessionsRequestOption func(*acp.ListSessionsRequest)

// ListSessionsRequest constructs a session/list request.
func ListSessionsRequest(opts ...ListSessionsRequestOption) acp.ListSessionsRequest {
	var req acp.ListSessionsRequest
	for _, opt := range opts {
		opt(&req)
	}

	return req
}

// WithListSessionsCwd filters session/list by cwd.
func WithListSessionsCwd(cwd string) ListSessionsRequestOption {
	return func(req *acp.ListSessionsRequest) { req.Cwd = &cwd }
}

// WithListSessionsCursor sets the pagination cursor.
func WithListSessionsCursor(cursor string) ListSessionsRequestOption {
	return func(req *acp.ListSessionsRequest) { req.Cursor = &cursor }
}

// WithListSessionsMeta sets host metadata on a session/list request, refusing
// reserved literals like WithSessionMeta.
func WithListSessionsMeta(meta map[string]any) ListSessionsRequestOption {
	rejectReservedMeta("WithListSessionsMeta", meta)

	cloned := CloneMap(meta)

	return func(req *acp.ListSessionsRequest) { req.Meta = MergeMap(req.Meta, cloned) }
}

// rejectReservedMeta panics: a reserved key in a builder's meta is a
// programming error, not a request a host can be refused.
func rejectReservedMeta(builder string, meta map[string]any) {
	if err := CheckReservedMeta(meta); err != nil {
		panic(builder + ": " + err.Error())
	}
}
