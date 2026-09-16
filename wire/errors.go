// Package wire holds the uniform error shapes, raw-event framing, and reserved
// literal rules every sibling answers with.
package wire

import (
	"errors"
	"strings"

	"github.com/coder/acp-go-sdk"

	"github.com/savid/acp-go-core/lifecycle"
	"github.com/savid/acp-go-core/process"
)

// nativeCauseMaxBytes bounds the native cause text a turn failure carries.
const nativeCauseMaxBytes = 2048

// Data member names shared by every uniform error.
const (
	FieldError   = "error"
	FieldField   = "field"
	FieldCause   = "cause"
	FieldMessage = "message"
	FieldClass   = "class"
	FieldLimit   = "limit"
)

// Invalid-params verdicts.
const (
	VerdictUnsupported    = "unsupported"
	VerdictMissing        = "missing"
	verdictUnknownSession = "unknown session"
	verdictBackpressure   = "backpressure"
	verdictAgentClosed    = "agent closed"
	fieldSessionID        = "sessionId"
	fieldSeedFiles        = "seedFiles"
)

// Turn failure causes.
const (
	CauseProcessExit = "process_exit"
	CauseTransport   = "transport"
	CauseProvider    = "provider"
	CauseTimeout     = "timeout"
)

// Off-prompt -32603 token suffixes. A sibling prefixes each with its vendor key.
const (
	TokenInvalidOptions     = "invalid_options"
	TokenRestoreFailed      = "restore_failed"
	TokenRuntimeUnavailable = "runtime_unavailable"
	TokenSessionPoisoned    = "session_poisoned"
	TokenInternalFailure    = "internal_failure"
	TokenTurnFailed         = "turn_failed"
)

// Unsupported refuses a value that is present and refused, naming its path.
func Unsupported(field string) *acp.RequestError {
	return acp.NewInvalidParams(map[string]any{FieldError: VerdictUnsupported, FieldField: field})
}

// Missing refuses a request that omitted a value the contract requires.
func Missing(field string) *acp.RequestError {
	return acp.NewInvalidParams(map[string]any{FieldError: VerdictMissing, FieldField: field})
}

// UnknownSession answers a session-scoped request that found no eligible
// session.
func UnknownSession() *acp.RequestError {
	return acp.NewInvalidParams(map[string]any{FieldError: verdictUnknownSession, FieldField: fieldSessionID})
}

// Backpressure refuses admission at a concurrency limit.
func Backpressure(limit string) *acp.RequestError {
	return acp.NewInvalidRequest(map[string]any{FieldError: verdictBackpressure, FieldLimit: limit})
}

// AgentClosed refuses a request that arrived after the agent stopped serving.
func AgentClosed() *acp.RequestError {
	return acp.NewInvalidRequest(map[string]any{FieldError: verdictAgentClosed})
}

// ParamRefusal maps a lifecycle negotiation or correlation refusal to the
// uniform invalid-params shape.
func ParamRefusal(err *lifecycle.ParamError) *acp.RequestError {
	if err.Verdict == lifecycle.VerdictMissing {
		return Missing(err.Field)
	}

	return Unsupported(err.Field)
}

// SeedFileRefusal maps an invalid seed file to the uniform refusal naming
// seedFiles. Any other error yields nil so the caller classifies it.
func SeedFileRefusal(err error) *acp.RequestError {
	var seedErr *process.SeedFileError
	if errors.As(err, &seedErr) {
		return Unsupported(fieldSeedFiles)
	}

	return nil
}

// CancelledResponse answers a prompt whose turn ended by cancellation.
func CancelledResponse(params acp.PromptRequest) acp.PromptResponse {
	return acp.PromptResponse{StopReason: acp.StopReasonCancelled, UserMessageId: params.MessageId}
}

// boundNativeCause is the one gate every native cause text passes through
// before it reaches a client.
func boundNativeCause(message string) string {
	if len(message) > nativeCauseMaxBytes {
		message = message[:nativeCauseMaxBytes]
	}

	return strings.TrimSpace(strings.ToValidUTF8(message, ""))
}

// TurnFailure describes a native turn that ended without a stop reason.
type TurnFailure struct {
	Cause        string
	Message      string
	StatusCode   int
	ProviderCode string
	// Stage, Reason, SizeBytes, and MaxBytes appear only on image output
	// failures.
	Stage     string
	Reason    string
	SizeBytes int64
	MaxBytes  int64
}

// TurnFailed renders the <vendor>_turn_failed error. Message carries the real
// native cause, bounded to nativeCauseMaxBytes of valid UTF-8.
func TurnFailed(vendor string, failure TurnFailure) *acp.RequestError {
	data := map[string]any{
		FieldError:   vendor + "_" + TokenTurnFailed,
		FieldCause:   failure.Cause,
		FieldMessage: boundNativeCause(failure.Message),
	}

	if failure.StatusCode > 0 {
		data["statusCode"] = failure.StatusCode
	}

	if failure.ProviderCode != "" {
		data["providerCode"] = failure.ProviderCode
	}

	if failure.Stage != "" {
		data["stage"] = failure.Stage
	}

	if failure.Reason != "" {
		data["reason"] = failure.Reason
	}

	if failure.SizeBytes > 0 {
		data["sizeBytes"] = failure.SizeBytes
	}

	if failure.MaxBytes > 0 {
		data["maxBytes"] = failure.MaxBytes
	}

	return acp.NewInternalError(data)
}

// InvalidOptions reports a construction verdict. field names the refused option
// when the sibling refuses one at a time and is empty otherwise.
func InvalidOptions(vendor, field string) *acp.RequestError {
	data := map[string]any{FieldError: vendor + "_" + TokenInvalidOptions}
	if field != "" {
		data[FieldField] = field
	}

	return acp.NewInternalError(data)
}

// RestoreFailed reports a store entry that could not be restored.
func RestoreFailed(vendor string) *acp.RequestError {
	return acp.NewInternalError(map[string]any{FieldError: vendor + "_" + TokenRestoreFailed})
}

// RuntimeUnavailable reports a shared runtime that is gone and could not be
// replaced.
func RuntimeUnavailable(vendor string) *acp.RequestError {
	return acp.NewInternalError(map[string]any{FieldError: vendor + "_" + TokenRuntimeUnavailable})
}

// SessionPoisoned reports a session that refuses every operation but close and
// delete. cause is a closed token the sibling documents.
func SessionPoisoned(vendor, cause string) *acp.RequestError {
	return acp.NewInternalError(map[string]any{FieldError: vendor + "_" + TokenSessionPoisoned, FieldCause: cause})
}

// InternalFailure reports a failure the sibling cannot classify. class is an
// optional closed token the sibling documents.
func InternalFailure(vendor, class string) *acp.RequestError {
	data := map[string]any{FieldError: vendor + "_" + TokenInternalFailure}
	if class != "" {
		data[FieldClass] = class
	}

	return acp.NewInternalError(data)
}
