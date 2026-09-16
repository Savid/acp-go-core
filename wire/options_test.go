package wire

import (
	"errors"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"github.com/savid/acp-go-core/lifecycle"
	"github.com/savid/acp-go-core/process"
)

func TestStringMapOption(t *testing.T) {
	t.Parallel()

	typed := map[string]string{"A": "1"}
	got, refusal := StringMapOption(typed, "env")
	require.Nil(t, refusal)
	require.Equal(t, typed, got)

	typed["A"] = "changed"
	require.Equal(t, "1", got["A"], "the option copies a typed map")

	got, refusal = StringMapOption(map[string]any{"A": "1"}, "env")
	require.Nil(t, refusal)
	require.Equal(t, map[string]string{"A": "1"}, got)

	_, refusal = StringMapOption(map[string]any{"A": 1}, "env")
	require.Equal(t, Unsupported("env.A"), refusal)

	_, refusal = StringMapOption("text", "env")
	require.Equal(t, Unsupported("env"), refusal)
}

func TestStringSliceOption(t *testing.T) {
	t.Parallel()

	typed := []string{"/a"}
	got, refusal := StringSliceOption(typed, "dirs")
	require.Nil(t, refusal)
	require.Equal(t, typed, got)

	typed[0] = "/changed"
	require.Equal(t, "/a", got[0], "the option copies a typed slice")

	got, refusal = StringSliceOption([]any{"/a", "/b"}, "dirs")
	require.Nil(t, refusal)
	require.Equal(t, []string{"/a", "/b"}, got)

	_, refusal = StringSliceOption([]any{"/a", 1}, "dirs")
	require.Equal(t, Unsupported("dirs[1]"), refusal)

	_, refusal = StringSliceOption(1, "dirs")
	require.Equal(t, Unsupported("dirs"), refusal)
}

func TestCloneAndMergeMap(t *testing.T) {
	t.Parallel()

	require.Nil(t, CloneMap(nil))

	env := map[string]string{"A": "1"}
	list := []string{"x"}
	nestedValue := map[string]any{"n": 1}
	values := []any{nestedValue}
	source := map[string]any{"env": env, "list": list, "values": values, "scalar": 1}

	cloned := CloneMap(source)
	env["A"] = "changed"
	list[0] = "changed"
	nestedValue["n"] = 2

	require.Equal(t, map[string]string{"A": "1"}, cloned["env"])
	require.Equal(t, []string{"x"}, cloned["list"])
	clonedValues, ok := cloned["values"].([]any)
	require.True(t, ok)
	first, ok := clonedValues[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 1, first["n"])

	base := map[string]any{"a": map[string]any{"keep": 1, "over": 1}, "b": 1}
	overlay := map[string]any{"a": map[string]any{"over": 2, "new": 3}, "c": env}
	merged := MergeMap(base, overlay)
	require.Equal(t, map[string]any{"keep": 1, "over": 2, "new": 3}, merged["a"])
	require.Equal(t, 1, merged["b"])

	env["A"] = "later"
	mergedEnv, ok := merged["c"].(map[string]string)
	require.True(t, ok)
	require.Equal(t, "changed", mergedEnv["A"], "merge copies the overlay's typed map")
	baseNested, ok := base["a"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 1, baseNested["over"], "merge leaves the base untouched")

	require.Equal(t, map[string]any{"k": 1}, MergeMap(nil, map[string]any{"k": 1}))
}

func TestValidateSessionEnvironment(t *testing.T) {
	t.Parallel()

	require.Nil(t, ValidateSessionEnvironment(map[string]string{"A": "1"}, []string{"/bin"}, "options"))
	require.Equal(t, Unsupported("options.env.A=B"), ValidateSessionEnvironment(map[string]string{"A=B": "1"}, nil, "options"))
	require.Equal(t, Unsupported("options.extraPathDirs[1]"), ValidateSessionEnvironment(nil, []string{"/ok", "relative"}, "options"))
}

func TestAcquireSessionGate(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{}, 1)

	release, err := AcquireSessionGate(gate, "session_prompt")
	require.NoError(t, err)

	_, err = AcquireSessionGate(gate, "session_prompt")
	require.Equal(t, Backpressure("session_prompt"), err)

	release()
	release()

	release, err = AcquireSessionGate(gate, "session_prompt")
	require.NoError(t, err)
	release()
}

func TestRefusalHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, acp.NewInvalidRequest(map[string]any{FieldError: "agent closed"}), AgentClosed())
	require.Equal(t, Missing("_meta"), ParamRefusal(&lifecycle.ParamError{Field: "_meta", Verdict: lifecycle.VerdictMissing}))
	require.Equal(t, Unsupported("_meta.x"), ParamRefusal(&lifecycle.ParamError{Field: "_meta.x", Verdict: lifecycle.VerdictUnsupported}))
	require.Equal(t, Unsupported("seedFiles"), SeedFileRefusal(&process.SeedFileError{Name: "../x"}))
	require.Nil(t, SeedFileRefusal(errors.New("disk full")))

	response := CancelledResponse(acp.PromptRequest{MessageId: new("m1")})
	require.Equal(t, acp.StopReasonCancelled, response.StopReason)
	require.Equal(t, "m1", *response.UserMessageId)
}

func TestTurnFailedBoundsTheNativeCause(t *testing.T) {
	t.Parallel()

	long := "  " + string(make([]byte, nativeCauseMaxBytes+10)) + "\xff tail"
	failure := TurnFailed("pi", TurnFailure{Cause: CauseProvider, Message: long})
	data, ok := failure.Data.(map[string]any)
	require.True(t, ok)
	message, ok := data[FieldMessage].(string)
	require.True(t, ok)
	require.LessOrEqual(t, len(message), nativeCauseMaxBytes)
	require.NotContains(t, message, "\xff")
	trimmed, ok := TurnFailed("pi", TurnFailure{Message: " trimmed \n"}).Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "trimmed", trimmed[FieldMessage])
}
