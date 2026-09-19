package wire_test

import (
	"testing"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"github.com/savid/acp-go-core/wire"
)

func TestModelSelectOptionsOrdersNativeConfiguredCurrent(t *testing.T) {
	t.Parallel()

	native := []wire.ModelRow{
		{ID: "one", Name: "One", Description: "first", Meta: map[string]any{"contextWindow": 10, "modelId": "wrong"}},
		{ID: "one"},
		{ID: ""},
		{ID: "two"},
	}

	values := wire.ModelSelectOptions("v", "current", native, []string{"one", "listed", "", "listed"})
	require.Len(t, values, 4)

	require.Equal(t, acp.SessionConfigValueId("one"), values[0].Value)
	require.Equal(t, "One", values[0].Name)
	require.Equal(t, "first", *values[0].Description)
	require.Equal(t, map[string]any{"v": map[string]any{"modelId": "one", "contextWindow": 10}}, values[0].Meta)

	require.Equal(t, "two", values[1].Name)
	require.Nil(t, values[1].Description)
	require.Equal(t, map[string]any{"v": map[string]any{"modelId": "two"}}, values[1].Meta)

	require.Equal(t, acp.SessionConfigValueId("listed"), values[2].Value)
	require.Nil(t, values[2].Meta)
	require.Equal(t, acp.SessionConfigValueId("current"), values[3].Value)
	require.Nil(t, values[3].Meta)
}

func TestModelSelectOptionsOmitsCurrentTheCatalogNames(t *testing.T) {
	t.Parallel()

	values := wire.ModelSelectOptions("v", "one", []wire.ModelRow{{ID: "one"}}, nil)
	require.Len(t, values, 1)
	require.Empty(t, wire.ModelSelectOptions("v", "", nil, nil))
}
