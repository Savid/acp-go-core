package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/usage/gateway"
	"github.com/savid/acp-go-core/wire"
	"github.com/stretchr/testify/require"
)

func gatewayServer(t *testing.T) *httptest.Server {
	t.Helper()

	report, err := os.ReadFile("testdata/report.json")
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gateway-key" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		if r.URL.Path != "/v1/usage" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(report)
	}))
	t.Cleanup(server.Close)

	return server
}

func TestEndpointDerivesTheReportAddressFromTheAPIRoot(t *testing.T) {
	t.Parallel()

	for base, want := range map[string]string{
		"http://127.0.0.1:4000":            "http://127.0.0.1:4000/v1/usage",
		"http://127.0.0.1:4000/":           "http://127.0.0.1:4000/v1/usage",
		"http://127.0.0.1:4000/v1":         "http://127.0.0.1:4000/v1/usage",
		"http://127.0.0.1:4000/v1/":        "http://127.0.0.1:4000/v1/usage",
		"https://gateway.example/proxy/v1": "https://gateway.example/proxy/v1/usage",
	} {
		got, err := gateway.Endpoint(base)
		require.NoError(t, err, base)
		require.Equal(t, want, got, base)
	}

	for _, base := range []string{"", "gateway.example", "ftp://gateway.example", "http://user:pw@gateway.example", "http://gateway.example/v1?x=1"} {
		_, err := gateway.Endpoint(base)
		require.Error(t, err, base)
	}
}

func TestReadProjectsOneProviderSection(t *testing.T) {
	t.Parallel()

	server := gatewayServer(t)
	credential := usage.Credential{Token: "gateway-key", BaseURL: server.URL + "/v1"}

	anthropic, err := gateway.Reader{ProviderID: "anthropic"}.Read(t.Context(), credential)
	require.NoError(t, err)
	require.NoError(t, anthropic.Validate())
	require.True(t, anthropic.Available)
	require.Nil(t, anthropic.UsageAllowed)
	require.Equal(t, []wire.AccountUsageLimit{
		{ObservedAt: "2026-09-19T08:40:37Z", ID: "5h", Label: "Claude 5 Hour", WindowSeconds: 18000, UsedPercent: 0, UsageAllowed: new(true), ResetsAt: "2026-09-19T11:29:59Z"},
		{ObservedAt: "2026-09-19T08:40:37Z", ID: "7d", Label: "Claude 7 Day", WindowSeconds: 604800, UsedPercent: 0, UsageAllowed: new(true), ResetsAt: "2026-09-26T07:59:59Z"},
		{ObservedAt: "2026-09-19T08:40:37Z", ID: "7d/fable", Label: "Claude 7 Day (Fable)", WindowSeconds: 604800, UsedPercent: 0, UsageAllowed: new(true), ResetsAt: "2026-09-26T07:59:59Z"},
	}, anthropic.Limits)

	codex, err := gateway.Reader{ProviderID: "openai-codex"}.Read(t.Context(), credential)
	require.NoError(t, err)
	require.NoError(t, codex.Validate())
	require.Equal(t, "plus", codex.Plan)
	require.Equal(t, new(true), codex.UsageAllowed)
	require.Len(t, codex.Limits, 3)
	require.Equal(t, "secondary", codex.Limits[1].ID)
	require.InDelta(t, 8, codex.Limits[1].UsedPercent, 1e-9)
	require.Equal(t, "base-model-inference/primary", codex.Limits[2].ID)

	openrouter, err := gateway.Reader{ProviderID: "openrouter"}.Read(t.Context(), credential)
	require.NoError(t, err)
	require.NoError(t, openrouter.Validate())
	require.Empty(t, openrouter.Limits, "a token count has no wire form and is left out")
	require.Equal(t, []wire.AccountUsageBalance{{ID: "credits", Label: "Credits", ObservedAt: "2026-09-19T08:40:37Z",
		Used: &wire.AccountUsageMoney{Amount: 40.81, Currency: "USD"}, Limit: &wire.AccountUsageMoney{Amount: 50, Currency: "USD"}, Remaining: &wire.AccountUsageMoney{Amount: 9.19, Currency: "USD"}}}, openrouter.Balances)
	require.Equal(t, []wire.AccountUsageRequestLimit{{ID: "free-daily", Label: "Free requests", ObservedAt: "2026-09-19T08:40:37Z", Used: 12, Limit: 1000, Remaining: 988, ResetsAt: "2026-09-19T16:00:00Z"}}, openrouter.RequestLimits)

	_, err = gateway.Reader{ProviderID: "opencode-go"}.Read(t.Context(), credential)
	require.Error(t, err, "a section the gateway could not fetch is a failed read")

	missing, err := gateway.Reader{ProviderID: "xai"}.Read(t.Context(), credential)
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), missing)
}

func TestReadDistinguishesProxiesAndBadBearers(t *testing.T) {
	t.Parallel()

	server := gatewayServer(t)

	refused, err := gateway.Reader{ProviderID: "anthropic"}.Read(t.Context(), usage.Credential{Token: "wrong", BaseURL: server.URL})
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), refused)

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	t.Cleanup(proxy.Close)

	plain, err := gateway.Reader{ProviderID: "anthropic"}.Read(t.Context(), usage.Credential{Token: "gateway-key", BaseURL: proxy.URL + "/v1"})
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), plain)

	_, err = gateway.Reader{ProviderID: "anthropic"}.Read(t.Context(), usage.Credential{Token: "gateway-key", BaseURL: "not a url"})
	require.Error(t, err, "a base that is not an http origin is the caller's mistake")

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(broken.Close)

	_, err = gateway.Reader{ProviderID: "anthropic"}.Read(t.Context(), usage.Credential{Token: "gateway-key", BaseURL: broken.URL})
	var failure *usage.HTTPError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, http.StatusBadGateway, failure.StatusCode)
}

func TestReadRoutesAnswersWithTheFirstCoveringRoute(t *testing.T) {
	t.Parallel()

	server := gatewayServer(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	t.Cleanup(proxy.Close)

	fallback := wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated)
	routes := []gateway.Route{
		{Provider: "broken", BaseURL: "gateway.example", Token: "x"},
		{Provider: "empty", BaseURL: proxy.URL, Token: " "},
		{Provider: "proxy", BaseURL: proxy.URL + "/v1", Token: "proxy-key"},
		{Provider: "omp", BaseURL: server.URL + "/v1", Token: "gateway-key"},
	}

	response, err := gateway.ReadRoutes(t.Context(), nil, routes, "anthropic", fallback)
	require.NoError(t, err)
	require.True(t, response.Available)
	require.Len(t, response.Limits, 3)

	response, err = gateway.ReadRoutes(t.Context(), nil, routes, "xai", fallback)
	require.NoError(t, err)
	require.Equal(t, fallback, response, "a provider no route covers keeps the fallback")

	_, err = gateway.ReadRoutes(t.Context(), nil, routes, "opencode-go", fallback)
	require.Error(t, err, "a gateway that could not fetch the provider is a failed read")
}
