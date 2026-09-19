package wire

import (
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestSessionRequestBuilders(t *testing.T) {
	t.Parallel()

	dirs := []string{"/a"}
	env := map[string]string{"KEY": "value"}
	meta := map[string]any{"host": map[string]any{"env": env}}

	req := NewSessionRequest("/w", WithSessionAdditionalDirectories(dirs...), WithSessionMeta(meta), WithSessionMetaValue(map[string]any{"acme": map[string]any{"options": map[string]any{"model": "m"}}}))
	require.Equal(t, "/w", req.Cwd)
	require.Equal(t, []acp.McpServer{}, req.McpServers)
	require.Equal(t, []string{"/a"}, req.AdditionalDirectories)
	require.Equal(t, "m", nested(t, req.Meta, "acme", "options")["model"])

	dirs[0] = "/changed"
	env["KEY"] = "changed"
	meta["late"] = true
	require.Equal(t, []string{"/a"}, req.AdditionalDirectories)
	hostEnv, ok := nested(t, req.Meta, "host")["env"].(map[string]string)
	require.True(t, ok)
	require.Equal(t, "value", hostEnv["KEY"])
	require.NotContains(t, req.Meta, "late")

	load := LoadSessionRequest("s", "/w", WithSessionMeta(map[string]any{"k": 1}))
	require.Equal(t, acp.SessionId("s"), load.SessionId)
	require.Equal(t, []acp.McpServer{}, load.McpServers)
	require.Equal(t, 1, load.Meta["k"])

	resume := ResumeSessionRequest("s", "/w")
	require.Equal(t, acp.SessionId("s"), resume.SessionId)
	require.Nil(t, resume.Meta)

	require.Equal(t, acp.SessionId("s"), DeleteSessionRequest("s").SessionId)
	require.Equal(t, acp.SessionId("s"), CancelRequest("s").SessionId)

	prompt := TextPromptRequest("s", "hello")
	require.Len(t, prompt.Prompt, 1)
	require.Equal(t, "hello", prompt.Prompt[0].Text.Text)

	option := SetConfigOptionRequest("s", "model", "gpt")
	require.Equal(t, acp.SessionConfigValueId("gpt"), option.ValueId.Value)

	list := ListSessionsRequest(WithListSessionsCwd("/w"), WithListSessionsCursor("c"), WithListSessionsMeta(map[string]any{"k": 1}))
	require.Equal(t, "/w", *list.Cwd)
	require.Equal(t, "c", *list.Cursor)
	require.Equal(t, 1, list.Meta["k"])
}

func TestSessionRequestBuildersRefuseReservedMeta(t *testing.T) {
	t.Parallel()

	for _, key := range ReservedLiterals {
		require.Panics(t, func() { WithSessionMeta(map[string]any{key: map[string]any{}}) }, key)
		require.Panics(t, func() { WithListSessionsMeta(map[string]any{key: map[string]any{}}) }, key)
	}
}

func nested(t *testing.T, meta map[string]any, keys ...string) map[string]any {
	t.Helper()

	current := meta
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		require.True(t, ok, key)
		current = next
	}

	return current
}
