package wire

import (
	"strings"
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestSelectPositionEncoding(t *testing.T) {
	t.Parallel()

	require.Equal(t, acp.PositionEncodingKindUtf8, SelectPositionEncoding([]acp.PositionEncodingKind{acp.PositionEncodingKindUtf16, acp.PositionEncodingKindUtf8}))
	require.Equal(t, acp.PositionEncodingKindUtf16, SelectPositionEncoding([]acp.PositionEncodingKind{acp.PositionEncodingKindUtf32}))
	require.Equal(t, acp.PositionEncodingKindUtf16, SelectPositionEncoding(nil))
}

func TestPromptTitle(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", PromptTitle(nil))
	require.Equal(t, "", PromptTitle([]acp.ContentBlock{acp.TextBlock("   \n\t ")}))
	require.Equal(t, "second line", PromptTitle([]acp.ContentBlock{acp.TextBlock(" "), acp.TextBlock(" second\n line ")}))

	long := strings.Repeat("word ", 100)
	title := NormalizeTitle(long)
	require.Len(t, []rune(title), sessionTitleMaxRunes)
	require.True(t, strings.HasSuffix(title, "..."))
	require.Equal(t, "a b", NormalizeTitle("a \n\tb"))
}

func TestValidCommandName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"review", "fix-bug", "日本語", "a_b.c"} {
		require.True(t, ValidCommandName(name), name)
	}

	for _, name := range []string{"", "a/b", "a b", "a\tb", "a b", "a\u200bb", "a\x07b", "a\x0bb", "\xff"} {
		require.False(t, ValidCommandName(name), "%q", name)
	}
}

func TestUnstreamedSuffix(t *testing.T) {
	t.Parallel()

	require.Equal(t, "full", UnstreamedSuffix("", "full"))
	require.Equal(t, " tail", UnstreamedSuffix("head", "head tail"))
	require.Equal(t, "", UnstreamedSuffix("head", "different"))
	require.Equal(t, "", UnstreamedSuffix("head", "head"))
}

func TestContextResourceText(t *testing.T) {
	t.Parallel()

	require.Equal(t, "\n<context ref=\"file:///a&amp;b\">\n&lt;x&gt; &quot;q&quot; &apos;s\n</context>", ContextResourceText("file:///a&b", `<x> "q" 's`))
}

func TestAudienceIsUserOnly(t *testing.T) {
	t.Parallel()

	require.False(t, AudienceIsUserOnly(nil))
	require.False(t, AudienceIsUserOnly(&acp.Annotations{}))
	require.False(t, AudienceIsUserOnly(&acp.Annotations{Audience: []acp.Role{acp.RoleUser, acp.RoleAssistant}}))
	require.True(t, AudienceIsUserOnly(&acp.Annotations{Audience: []acp.Role{acp.RoleUser}}))
}

func TestMetaOptionPath(t *testing.T) {
	t.Parallel()

	require.Equal(t, "_meta.acme.options", MetaOptionPath("acme", ""))
	require.Equal(t, "_meta.acme.options.model", MetaOptionPath("acme", "model"))
}
