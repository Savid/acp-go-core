package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/savid/acp-go-core/usage"
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
		"http://127.0.0.1:4000":     "http://127.0.0.1:4000/v1/usage",
		"http://127.0.0.1:4000/":    "http://127.0.0.1:4000/v1/usage",
		"http://127.0.0.1:4000/v1":  "http://127.0.0.1:4000/v1/usage",
		"http://127.0.0.1:4000/v1/": "http://127.0.0.1:4000/v1/usage",
		"https://example/proxy/v1":  "https://example/proxy/v1/usage",
	} {
		got, err := endpoint(base)
		require.NoError(t, err, base)
		require.Equal(t, want, got, base)
	}

	for _, base := range []string{"", "example", "ftp://example", "http://user:pw@example", "http://example/v1?x=1"} {
		_, err := endpoint(base)
		require.Error(t, err, base)
	}
}

func TestReadProjectsOneProviderSection(t *testing.T) {
	t.Parallel()

	server := gatewayServer(t)
	credential := usage.Credential{Token: "gateway-key", BaseURL: server.URL + "/v1"}

	anthropic, err := readSection("anthropic", t.Context(), credential)
	require.NoError(t, err)
	require.NoError(t, anthropic.Validate())
	require.True(t, anthropic.Available)
	require.Nil(t, anthropic.UsageAllowed)
	require.Equal(t, []wire.AccountUsageLimit{
		{ObservedAt: "2026-09-19T08:40:37Z", ID: "session", UsedPercent: 0, UsageAllowed: new(true), ResetsAt: "2026-09-19T11:29:59Z"},
		{ObservedAt: "2026-09-19T08:40:37Z", ID: "weekly_all", UsedPercent: 0, UsageAllowed: new(true), ResetsAt: "2026-09-26T07:59:59Z"},
		{ObservedAt: "2026-09-19T08:40:37Z", ID: "weekly_scoped/Fable", Label: "Fable", UsedPercent: 0, UsageAllowed: new(true), ResetsAt: "2026-09-26T07:59:59Z"},
	}, anthropic.Limits, "windows carry the names and shape Anthropic's own reader gives them")

	codex, err := readSection("openai-codex", t.Context(), credential)
	require.NoError(t, err)
	require.NoError(t, codex.Validate())
	require.Equal(t, "plus", codex.Plan)
	require.Equal(t, new(true), codex.UsageAllowed)
	require.Len(t, codex.Limits, 3)
	require.Equal(t, "codex/primary", codex.Limits[0].ID)
	require.Equal(t, "codex/secondary", codex.Limits[1].ID)
	require.InDelta(t, 8, codex.Limits[1].UsedPercent, 1e-9)
	require.Equal(t, "base_model_inference/primary", codex.Limits[2].ID)
	require.Equal(t, "gpt-reserve", codex.Limits[2].Label, "ChatGPT windows are labelled by model, as natively")
	require.Empty(t, codex.Limits[0].Label)
	require.Equal(t, int64(18000), codex.Limits[0].WindowSeconds, "only ChatGPT windows carry a length")

	openrouter, err := readSection("openrouter", t.Context(), credential)
	require.NoError(t, err)
	require.NoError(t, openrouter.Validate())
	require.Empty(t, openrouter.Limits, "a token count has no wire form and is left out")

	goWindows, err := readSection("opencode-go", t.Context(), usage.Credential{Token: "gateway-key", BaseURL: server.URL + "/v1"})
	require.Error(t, err)
	require.Nil(t, goWindows.Limits)
	require.Equal(t, []wire.AccountUsageBalance{{ID: "credits", Label: "Credits", ObservedAt: "2026-09-19T08:40:37Z",
		Used: &wire.AccountUsageMoney{Amount: 40.81, Currency: currencyUSD}, Remaining: &wire.AccountUsageMoney{Amount: 9.19, Currency: currencyUSD}}}, openrouter.Balances)
	require.Equal(t, []wire.AccountUsageRequestLimit{{ID: "free-daily", Label: "Free requests", ObservedAt: "2026-09-19T08:40:37Z", Used: 12, Limit: 1000, Remaining: 988, ResetsAt: "2026-09-19T16:00:00Z"}}, openrouter.RequestLimits)

	_, err = readSection("opencode-go", t.Context(), credential)
	require.Error(t, err, "a section the gateway could not fetch is a failed read")

	missing, err := readSection("xai", t.Context(), credential)
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), missing)
}

func TestReadDistinguishesProxiesAndBadBearers(t *testing.T) {
	t.Parallel()

	server := gatewayServer(t)

	refused, err := readSection("anthropic", t.Context(), usage.Credential{Token: "wrong", BaseURL: server.URL})
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), refused)

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	t.Cleanup(proxy.Close)

	plain, err := readSection("anthropic", t.Context(), usage.Credential{Token: "gateway-key", BaseURL: proxy.URL + "/v1"})
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), plain)

	_, err = readSection("anthropic", t.Context(), usage.Credential{Token: "gateway-key", BaseURL: "not a url"})
	require.Error(t, err, "a base that is not an http origin is the caller's mistake")

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(broken.Close)

	_, err = readSection("anthropic", t.Context(), usage.Credential{Token: "gateway-key", BaseURL: broken.URL})
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
	routes := []Route{
		{Provider: "broken", BaseURL: "example", Token: "x"},
		{Provider: "empty", BaseURL: proxy.URL, Token: " "},
		{Provider: "proxy", BaseURL: proxy.URL + "/v1", Token: "proxy-key"},
		{Provider: "gateway", BaseURL: server.URL + "/v1", Token: "gateway-key"},
	}

	response, err := ReadRoutes(t.Context(), nil, func(context.Context) ([]Route, error) { return routes, nil }, "anthropic", fallback)
	require.NoError(t, err)
	require.True(t, response.Available)
	require.Len(t, response.Limits, 3)

	response, err = ReadRoutes(t.Context(), nil, func(context.Context) ([]Route, error) { return routes, nil }, "xai", fallback)
	require.NoError(t, err)
	require.Equal(t, fallback, response, "a provider no route covers keeps the fallback")

	_, err = ReadRoutes(t.Context(), nil, func(context.Context) ([]Route, error) { return routes, nil }, "opencode-go", fallback)
	require.Error(t, err, "a gateway that could not fetch the provider is a failed read")
}

func TestReadNamesOpenCodeGoWindowsNatively(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"generatedAt":1,"reports":[{"provider":"opencode-go","fetchedAt":1789807238149,"limits":[
			{"id":"rolling-5h","label":"5 Hour limit","window":{"id":"5h","durationMs":18000000},"amount":{"usedFraction":0.1,"unit":"percent"},"status":"ok"},
			{"id":"weekly","label":"Weekly limit","window":{"id":"7d","durationMs":604800000},"amount":{"usedFraction":0.2,"unit":"percent"},"status":"warning"},
			{"id":"monthly","label":"Monthly limit","window":{"id":"monthly"},"amount":{"usedFraction":1,"unit":"percent"},"status":"exhausted"},
			{"id":"bonus","label":"Bonus","window":{"id":"promo"},"amount":{"usedFraction":0,"unit":"percent"},"status":"unknown"}],"metadata":{"planType":"OpenCode Go"}}]}`))
	}))
	t.Cleanup(server.Close)

	response, err := readSection("opencode-go", t.Context(), usage.Credential{Token: "k", BaseURL: server.URL})
	require.NoError(t, err)
	require.Equal(t, "OpenCode Go", response.Plan)
	require.Equal(t, []wire.AccountUsageLimit{
		{ObservedAt: "2026-09-19T08:40:38Z", ID: "rolling", Label: "Rolling", UsedPercent: 10, UsageAllowed: new(true)},
		{ObservedAt: "2026-09-19T08:40:38Z", ID: "weekly", Label: "Weekly", UsedPercent: 20, UsageAllowed: new(true)},
		{ObservedAt: "2026-09-19T08:40:38Z", ID: "monthly", Label: "Monthly", UsedPercent: 100, UsageAllowed: new(false)},
		{ObservedAt: "2026-09-19T08:40:38Z", ID: "bonus", Label: "Bonus", UsedPercent: 0},
	}, response.Limits, "known windows take the native names; an unknown window keeps the gateway's")
}

func TestModelsReadsTheGatewayList(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gateway-key" {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)

			return
		}

		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"openai-codex/gpt-5.6-luna","object":"model","owned_by":"openai-codex","display_name":"GPT-5.6-Luna","context_length":400000,"max_output_tokens":128000,"input_modalities":["text","image"]},
			{"id":"opencode-go/qwen3.8-flash","object":"model","owned_by":"opencode-go"},
			{"id":"  ","object":"model"}]}`))
	}))
	t.Cleanup(server.Close)

	endpoint, err := modelsEndpoint(server.URL + "/v1")
	require.NoError(t, err)
	require.Equal(t, server.URL+"/v1/models", endpoint)

	models, err := Models(t.Context(), nil, Route{Provider: "gateway", BaseURL: server.URL + "/v1", Token: "gateway-key"})
	require.NoError(t, err)
	require.Equal(t, []Model{
		{ID: "openai-codex/gpt-5.6-luna", Name: "GPT-5.6-Luna", ContextWindow: 400000, MaxTokens: 128000, Inputs: []string{"text", "image"}},
		{ID: "opencode-go/qwen3.8-flash", Name: "opencode-go/qwen3.8-flash"},
	}, models, "an entry without an id is left out; a missing display name falls back to the id")

	none, err := Models(t.Context(), nil, Route{BaseURL: server.URL + "/v1", Token: "wrong"})
	require.NoError(t, err)
	require.Nil(t, none, "a refused bearer publishes no list to this caller")

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	t.Cleanup(proxy.Close)

	none, err = Models(t.Context(), nil, Route{BaseURL: proxy.URL, Token: "k"})
	require.NoError(t, err)
	require.Nil(t, none)
}

func TestModelsReadsAListLargerThanAUsageReport(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[`))
		for i := range 1500 {
			if i > 0 {
				_, _ = w.Write([]byte(","))
			}

			_, _ = fmt.Fprintf(w, `{"id":"provider-%d/model-%d","object":"model","owned_by":"provider-%d","display_name":"Model %d","context_length":128000,"max_output_tokens":8192,"input_modalities":["text"]}`, i%9, i, i%9, i)
		}
		_, _ = w.Write([]byte(`]}`))
	}))
	t.Cleanup(server.Close)

	models, err := Models(t.Context(), nil, Route{BaseURL: server.URL + "/v1", Token: "k"})
	require.NoError(t, err)
	require.Len(t, models, 1500)
}

func TestGatewayRejectsErroredSectionWithMeasurements(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"reports":[{"provider":"anthropic","fetchedAt":1789807237000,"error":"upstream read failed after first window","limits":[{"id":"anthropic:5h","window":{"id":"5h"},"amount":{"unit":"percent","used":25}}]}]}`)
	}))
	t.Cleanup(server.Close)
	response, err := readSection("anthropic", t.Context(), usage.Credential{Token: "key", BaseURL: server.URL})
	require.Error(t, err, "explicit upstream failure must not turn its partial measurements into success: %+v", response)
}
func TestGatewayRejectsNonIntegerRequestCounts(t *testing.T) {
	for _, used := range []string{"1.9", "-0.5", "9223372036854775808"} {
		t.Run(used, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"reports":[{"provider":"openrouter","fetchedAt":1789807237000,"limits":[{"id":"requests","amount":{"unit":"requests","used":%s,"limit":10,"remaining":8}}]}]}`, used)
			}))
			t.Cleanup(server.Close)
			response, err := readSection("openrouter", t.Context(), usage.Credential{Token: "key", BaseURL: server.URL})
			require.Error(t, err, "malformed source count must not be rounded into a valid observation: %+v", response)
		})
	}
}
func TestGatewayPreservesIntegerRequestCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"reports":[{"provider":"openrouter","fetchedAt":1789807237000,"limits":[{"id":"requests","amount":{"unit":"requests","used":9007199254740993,"limit":9007199254740993,"remaining":0}}]}]}`)
	}))
	t.Cleanup(server.Close)
	response, err := readSection("openrouter", t.Context(), usage.Credential{Token: "key", BaseURL: server.URL})
	require.NoError(t, err)
	require.Len(t, response.RequestLimits, 1)
	require.Equal(t, int64(9007199254740993), response.RequestLimits[0].Used)
	require.Equal(t, int64(9007199254740993), response.RequestLimits[0].Limit)
}

func TestReadRoutesRevalidatesNativeBinding(t *testing.T) {
	t.Parallel()
	server := gatewayServer(t)
	calls := 0
	resolve := func(context.Context) ([]Route, error) {
		calls++
		token := "gateway-key"
		if calls > 1 {
			token = "rotated-key"
		}

		return []Route{{Provider: "gateway", BaseURL: server.URL, Token: token}}, nil
	}
	response, err := ReadRoutes(t.Context(), nil, resolve, "anthropic", wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated))
	require.Error(t, err)
	require.False(t, response.Available)
	require.Equal(t, 2, calls)
}

func TestReadRoutesPreservesConnectedUnreportedAccount(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"reports":[{"provider":"anthropic","limits":[]}]}`)
	}))
	t.Cleanup(server.Close)
	response, err := ReadRoutes(t.Context(), nil, func(context.Context) ([]Route, error) {
		return []Route{{BaseURL: server.URL, Token: "key"}}, nil
	}, "anthropic", wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated))
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), response)
}

func TestGatewayPreservesUnmappedWindowNames(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"opencode-go", "unmapped-provider"} {
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			id := provider + ":promo:bonus"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"reports":[{"provider":%q,"fetchedAt":1789807237000,"limits":[{"id":%q,"label":"Bonus","window":{"id":"promo"},"amount":{"unit":"percent","used":25}}]}]}`, provider, id)
			}))
			t.Cleanup(server.Close)
			response, err := readSection(provider, t.Context(), usage.Credential{BaseURL: server.URL, Token: "key"})
			require.NoError(t, err)
			require.Len(t, response.Limits, 1)
			require.Equal(t, id, response.Limits[0].ID)
			require.Equal(t, "Bonus", response.Limits[0].Label)
		})
	}
}

// readSection reads one provider's section the way ReadRoutes does, without
// the route revalidation.
func readSection(providerID string, ctx context.Context, credential usage.Credential) (wire.AccountUsageResponse, error) {
	response, _, err := (reader{ProviderID: providerID}).read(ctx, credential)

	return response, err
}
