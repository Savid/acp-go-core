package wire

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeSessionMetaReadsTheHostNamespace(t *testing.T) {
	t.Parallel()

	decoded, refusal := DecodeSessionMeta(nil)
	require.Nil(t, refusal)
	require.Equal(t, SessionMeta{}, decoded)

	decoded, refusal = DecodeSessionMeta(map[string]any{"claude": map[string]any{"options": map[string]any{}}})
	require.Nil(t, refusal)
	require.Equal(t, SessionMeta{}, decoded)

	decoded, refusal = DecodeSessionMeta(map[string]any{SessionMetaKey: map[string]any{"ephemeral": true}})
	require.Nil(t, refusal)
	require.Equal(t, SessionMeta{Ephemeral: true}, decoded)

	decoded, refusal = DecodeSessionMeta(map[string]any{SessionMetaKey: map[string]any{"ephemeral": false}})
	require.Nil(t, refusal)
	require.Equal(t, SessionMeta{}, decoded)
}

func TestDecodeSessionMetaRefusesWhatItDoesNotDefine(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		meta map[string]any
		path string
	}{
		"not an object":    {map[string]any{SessionMetaKey: true}, "_meta." + SessionMetaKey},
		"unknown field":    {map[string]any{SessionMetaKey: map[string]any{"persist": false}}, "_meta." + SessionMetaKey + ".persist"},
		"non-boolean flag": {map[string]any{SessionMetaKey: map[string]any{"ephemeral": "yes"}}, "_meta." + SessionMetaKey + ".ephemeral"},
	} {
		_, refusal := DecodeSessionMeta(tc.meta)
		require.Equal(t, Unsupported(tc.path), refusal, name)
	}
}

func TestRefuseSessionMetaNamesTheKey(t *testing.T) {
	t.Parallel()

	require.Nil(t, RefuseSessionMeta(nil))
	require.Nil(t, RefuseSessionMeta(map[string]any{"claude": map[string]any{}}))
	require.Equal(t, Unsupported("_meta."+SessionMetaKey), RefuseSessionMeta(SessionMeta{Ephemeral: true}.Apply(nil)))
	require.Equal(t, Unsupported("_meta."+SessionMetaKey), RefuseSessionMeta(map[string]any{SessionMetaKey: map[string]any{}}))
}

func TestSessionMetaApplyRoundTrips(t *testing.T) {
	t.Parallel()

	require.Nil(t, SessionMeta{}.Apply(nil))
	meta := SessionMeta{Ephemeral: true}.Apply(map[string]any{"traceparent": "00-x"})
	require.Equal(t, "00-x", meta["traceparent"])
	decoded, refusal := DecodeSessionMeta(meta)
	require.Nil(t, refusal)
	require.True(t, decoded.Ephemeral)
	decoded, refusal = DecodeSessionMeta(SessionMeta{Ephemeral: true}.Apply(nil))
	require.Nil(t, refusal)
	require.True(t, decoded.Ephemeral)
}
