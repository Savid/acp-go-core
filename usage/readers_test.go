package usage_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/usage/anthropic"
	"github.com/savid/acp-go-core/usage/openaicodex"
	"github.com/savid/acp-go-core/usage/opencodego"
	"github.com/savid/acp-go-core/usage/openrouter"
	"github.com/savid/acp-go-core/wire"
	"github.com/stretchr/testify/require"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureTransport(t *testing.T, host string, handler http.HandlerFunc) http.RoundTripper {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	base, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	transport := base.Clone()
	t.Cleanup(transport.CloseIdleConnections)

	return roundTrip(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, host, r.URL.Host)
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer sk-ant-oat01-fixture-key", r.Header.Get("Authorization"))
		request := r.Clone(r.Context())
		request.URL.Scheme, request.URL.Host = target.Scheme, target.Host

		return transport.RoundTrip(request)
	})
}

func TestOpenRouterKeepsKeyCapLifetimeSpendAndAccountBalanceSeparate(t *testing.T) {
	var calls []string
	reader := openrouter.Reader{Transport: fixtureTransport(t, "openrouter.ai", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/key":
			_, err := io.WriteString(w, `{"data":{"limit":20,"limit_remaining":7,"limit_reset":"monthly","usage":103.25,"free_model_daily_requests":{"used":12,"limit":50,"remaining":38}}}`)
			require.NoError(t, err)
		case "/api/v1/credits":
			_, err := io.WriteString(w, `{"data":{"total_credits":50,"total_usage":40.812047315}}`)
			require.NoError(t, err)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})}
	response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
	require.NoError(t, err)
	require.NoError(t, response.Validate())
	require.Equal(t, []string{"/api/v1/key", "/api/v1/credits"}, calls)
	require.True(t, response.Available)
	require.Nil(t, response.UsageAllowed)
	require.Empty(t, response.Limits)
	require.Len(t, response.Balances, 3)
	keyCap, lifetime, account := response.Balances[0], response.Balances[1], response.Balances[2]
	require.Equal(t, "key_spending", keyCap.ID)
	require.Equal(t, &wire.AccountUsageMoney{Amount: 20, Currency: "USD"}, keyCap.Limit)
	require.Equal(t, &wire.AccountUsageMoney{Amount: 7, Currency: "USD"}, keyCap.Remaining)
	require.Nil(t, keyCap.Used, "all-time spend is not spend in the key cap's reset period")
	require.Equal(t, "monthly", keyCap.ResetInterval)
	require.Equal(t, &wire.AccountUsageMoney{Amount: 103.25, Currency: "USD"}, lifetime.Used)
	require.Equal(t, "account_credits", account.ID)
	require.Equal(t, &wire.AccountUsageMoney{Amount: 40.812047315, Currency: "USD"}, account.Used)
	require.InDelta(t, 9.187952685, account.Remaining.Amount, 1e-9)
	require.Nil(t, account.Limit, "credits purchased are not a spending cap")
	require.False(t, account.Uncapped)
	require.Empty(t, account.ResetInterval)
	require.Len(t, response.RequestLimits, 1)
	requests := response.RequestLimits[0]
	require.Equal(t, int64(12), requests.Used)
	require.Equal(t, int64(50), requests.Limit)
	require.Equal(t, int64(38), requests.Remaining)
	require.Equal(t, "daily", requests.ResetInterval)
	for _, balance := range response.Balances {
		_, parseErr := time.Parse(time.RFC3339, balance.ObservedAt)
		require.NoError(t, parseErr)
	}
}

func TestOpenCodeGoEntitlementRefusalIsNotReported(t *testing.T) {
	reader := opencodego.Reader{Transport: fixtureTransport(t, "opencode.ai", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, err := io.WriteString(w, `{"error":{"type":"EntitlementError","message":"no subscription"}}`)
		require.NoError(t, err)
	})}
	response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key"})
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), response)

	reader = opencodego.Reader{Transport: fixtureTransport(t, "opencode.ai", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, writeErr := io.WriteString(w, `{"error":{"type":"Forbidden"}}`)
		require.NoError(t, writeErr)
	})}
	_, err = reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key"})
	var httpErr *usage.HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusForbidden, httpErr.StatusCode)
}

func TestOpenRouterRetainsUncappedUsageWhenAccountBalanceIsUnavailable(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			reader := openrouter.Reader{Transport: fixtureTransport(t, "openrouter.ai", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/key" {
					_, err := io.WriteString(w, `{"data":{"limit":null,"limit_remaining":null,"limit_reset":null,"usage":0}}`)
					require.NoError(t, err)

					return
				}
				require.Equal(t, "/api/v1/credits", r.URL.Path)
				w.WriteHeader(status)
			})}
			response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
			require.NoError(t, err)
			require.True(t, response.Available)
			require.Len(t, response.Balances, 1)
			require.True(t, response.Balances[0].Uncapped)
			require.Equal(t, &wire.AccountUsageMoney{Currency: "USD"}, response.Balances[0].Used)
			require.Nil(t, response.Balances[0].Remaining)
			require.Nil(t, response.Balances[0].Limit)
		})
	}
}

func TestOpenRouterReportsZeroAndOverdrawnAccountBalances(t *testing.T) {
	for _, total := range []float64{0, 5} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			reader := openrouter.Reader{Transport: fixtureTransport(t, "openrouter.ai", func(w http.ResponseWriter, r *http.Request) {
				body := `{"data":{"limit":10,"limit_remaining":-2.5,"usage":12.5}}`
				if r.URL.Path == "/api/v1/credits" {
					body = fmt.Sprintf(`{"data":{"total_credits":%v,"total_usage":%v}}`, total, total*1.5)
				}
				_, err := io.WriteString(w, body)
				require.NoError(t, err)
			})}
			response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
			require.NoError(t, err)
			require.Len(t, response.Balances, 3)
			require.Equal(t, -2.5, response.Balances[0].Remaining.Amount)
			require.Equal(t, -total*.5, response.Balances[2].Remaining.Amount)
		})
	}
}

func TestOpenCodeGoPreservesWindowMeasurementsAndStatus(t *testing.T) {
	reader := opencodego.Reader{Transport: fixtureTransport(t, "opencode.ai", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/zen/go/v1/usage", r.URL.Path)
		_, err := io.WriteString(w, `{"usage":{"rolling":{"status":"ok","percent":0,"resetsAt":"2026-09-18T10:00:00.123Z"},"weekly":{"status":"rate-limited","percent":103.5,"resetsAt":"2026-09-21T00:00:00Z"},"monthly":{"status":"ok","percent":34.5,"resetsAt":"2026-10-01T00:00:00Z"}}}`)
		require.NoError(t, err)
	})}
	response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
	require.NoError(t, err)
	require.NoError(t, response.Validate())
	require.Len(t, response.Limits, 3)
	require.Equal(t, 0.0, response.Limits[0].UsedPercent)
	require.True(t, *response.Limits[0].UsageAllowed)
	require.Equal(t, 103.5, response.Limits[1].UsedPercent)
	require.False(t, *response.Limits[1].UsageAllowed)
	require.Equal(t, "2026-09-18T10:00:00Z", response.Limits[0].ResetsAt)
	require.Empty(t, response.Balances, "percentages do not establish dollar amounts")
	require.Nil(t, response.UsageAllowed, "window status does not establish account-wide permission")
}

func TestProviderReadersRejectMalformedObservations(t *testing.T) {
	cases := []struct {
		name, body string
		makeReader func(http.RoundTripper) usage.Reader
	}{
		{"claude missing percent", `{"limits":[{"kind":"session"}]}`, func(t http.RoundTripper) usage.Reader { return anthropic.Reader{Transport: t} }},
		{"claude missing monetary unit", `{"spend":{"enabled":true,"used":{"amount_minor":120,"currency":"USD"}}}`, func(t http.RoundTripper) usage.Reader { return anthropic.Reader{Transport: t} }},
		{"codex wrong account", `{"account_id":"other","plan_type":"plus","rate_limit":{"primary_window":{"used_percent":10}}}`, func(t http.RoundTripper) usage.Reader { return openaicodex.Reader{Transport: t} }},
		{"codex missing percentage", `{"account_id":"fixture-account","plan_type":"plus","rate_limit":{"primary_window":{}}}`, func(t http.RoundTripper) usage.Reader { return openaicodex.Reader{Transport: t} }},
		{"router missing cap", `{"data":{"usage":10}}`, func(t http.RoundTripper) usage.Reader { return openrouter.Reader{Transport: t} }},
		{"router missing remaining", `{"data":{"usage":10,"limit":20}}`, func(t http.RoundTripper) usage.Reader { return openrouter.Reader{Transport: t} }},
		{"router malformed request count", `{"data":{"usage":10,"limit":null,"limit_remaining":null,"free_model_daily_requests":{"used":1.5,"limit":50,"remaining":48.5}}}`, func(t http.RoundTripper) usage.Reader { return openrouter.Reader{Transport: t} }},
		{"go incomplete windows", `{"usage":{"rolling":{"status":"ok","percent":0,"resetsAt":"2026-10-01T00:00:00Z"}}}`, func(t http.RoundTripper) usage.Reader { return opencodego.Reader{Transport: t} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			reader := tc.makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++

				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			}))
			response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
			require.Error(t, err)
			require.Equal(t, wire.AccountUsageResponse{}, response)
			require.Equal(t, 1, calls)
		})
	}
}

func TestProviderHTTPBoundary(t *testing.T) {
	for _, kind := range []string{"go", "router", "codex", "anthropic"} {
		t.Run(kind, func(t *testing.T) {
			makeReader := func(transport http.RoundTripper) usage.Reader {
				if kind == "anthropic" {
					return anthropic.Reader{Transport: transport}
				}
				if kind == "codex" {
					return openaicodex.Reader{Transport: transport}
				}
				if kind == "go" {
					return opencodego.Reader{Transport: transport}
				}

				return openrouter.Reader{Transport: transport}
			}
			t.Run("missing credential", func(t *testing.T) {
				reader := makeReader(roundTrip(func(*http.Request) (*http.Response, error) {
					t.Error("unauthenticated request sent")

					return nil, errors.New("unexpected")
				}))
				response, err := reader.Read(t.Context(), usage.Credential{})
				require.NoError(t, err)
				require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), response)
			})
			t.Run("redirect", func(t *testing.T) {
				var calls atomic.Int64
				reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
					calls.Add(1)

					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/usage"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}))
				_, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
				var readErr *usage.HTTPError
				require.ErrorAs(t, err, &readErr)
				require.Equal(t, 302, readErr.StatusCode)
				require.Equal(t, int64(1), calls.Load())
			})
			for _, retry := range []struct {
				name, header string
				delay        time.Duration
			}{
				{"retry seconds", "120", 120 * time.Second},
				{"retry date", time.Now().UTC().Add(10 * time.Minute).Format(http.TimeFormat), 0},
				{"invalid retry", "not-a-date", 0},
				{"negative retry", "-1", 0},
			} {
				t.Run(retry.name, func(t *testing.T) {
					var calls int
					reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
						calls++

						return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {retry.header}}, Body: io.NopCloser(strings.NewReader("private provider body")), Request: r}, nil
					}))
					started := time.Now().UTC()
					response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
					var readErr *usage.HTTPError
					require.ErrorAs(t, err, &readErr)
					require.Equal(t, http.StatusTooManyRequests, readErr.StatusCode)
					require.Equal(t, wire.AccountUsageResponse{}, response)
					require.Equal(t, 1, calls)
					data, ok := usage.RequestError("fixture", fmt.Errorf("wrapped: %w", err)).Data.(map[string]any)
					require.True(t, ok)
					want := map[string]any{"error": "fixture_internal_failure", "class": "account_usage", "statusCode": 429}
					switch retry.name {
					case "retry seconds":
						require.False(t, readErr.RetryAt.Before(started.Add(retry.delay)))
						require.WithinDuration(t, started.Add(retry.delay), readErr.RetryAt, 2*time.Second)
						want["retryAt"] = wire.AccountUsageTime(readErr.RetryAt)
					case "retry date":
						expected, parseErr := http.ParseTime(retry.header)
						require.NoError(t, parseErr)
						require.Equal(t, expected, readErr.RetryAt)
						want["retryAt"] = wire.AccountUsageTime(expected)
					default:
						require.True(t, readErr.RetryAt.IsZero())
					}
					require.Equal(t, want, data)
				})
			}
			t.Run("cancellation", func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
					cancel()
					<-r.Context().Done()

					return nil, r.Context().Err()
				}))
				_, err := reader.Read(ctx, usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
				require.ErrorIs(t, err, context.Canceled)
			})
			t.Run("bounded body", func(t *testing.T) {
				reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", 65537))), Request: r}, nil
				}))
				_, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
				require.ErrorContains(t, err, "read bound")
			})
			t.Run("redacted error", func(t *testing.T) {
				reader := makeReader(roundTrip(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("sk-ant-oat01-fixture-key response payload")
				}))
				_, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
				require.Error(t, err)
				require.NotContains(t, err.Error(), "sk-ant-oat01-fixture-key")
				require.NotContains(t, err.Error(), "payload")
			})
		})
	}
}

func TestCodexUsagePreservesAccountAndIndependentWindows(t *testing.T) {
	reader := openaicodex.Reader{Transport: fixtureTransport(t, "chatgpt.com", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/backend-api/wham/usage", r.URL.Path)
		require.Equal(t, "fixture-account", r.Header.Get("ChatGPT-Account-ID"))
		_, err := io.WriteString(w, `{"account_id":"fixture-account","plan_type":"plus","rate_limit":{"allowed":false,"primary_window":{"used_percent":103.5,"limit_window_seconds":18000,"reset_at":1790000000},"secondary_window":{"used_percent":8,"limit_window_seconds":604800,"reset_at":1790500000}},"additional_rate_limits":[{"limit_name":"Reserve","metered_feature":"base_model_inference","rate_limit":{"allowed":true,"primary_window":{"used_percent":0,"limit_window_seconds":604800,"reset_at":1790600000},"secondary_window":null}}],"credits":{"balance":"42"}}`)
		require.NoError(t, err)
	})}
	response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key", AccountID: "fixture-account"})
	require.NoError(t, err)
	require.Equal(t, "plus", response.Plan)
	require.Len(t, response.Limits, 3)
	require.Equal(t, "codex/primary", response.Limits[0].ID)
	require.Equal(t, 103.5, response.Limits[0].UsedPercent)
	require.Equal(t, int64(18000), response.Limits[0].WindowSeconds)
	require.False(t, *response.Limits[0].UsageAllowed)
	require.Equal(t, "base_model_inference/primary", response.Limits[2].ID)
	require.Equal(t, "Reserve", response.Limits[2].Label)
	require.Equal(t, 0.0, response.Limits[2].UsedPercent)
	require.True(t, *response.Limits[2].UsageAllowed)
	require.Nil(t, response.UsageAllowed)
	require.Empty(t, response.Balances, "subscription credits do not establish a dollar balance")
	for _, window := range response.Limits {
		_, parseErr := time.Parse(time.RFC3339, window.ObservedAt)
		require.NoError(t, parseErr)
	}
}

func TestAnthropicUsagePreservesPercentagesAndMonetaryUnits(t *testing.T) {
	reader := anthropic.Reader{Transport: fixtureTransport(t, "api.anthropic.com", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/oauth/usage", r.URL.Path)
		require.Equal(t, "oauth-2025-04-20", r.Header.Get("Anthropic-Beta"))
		_, err := io.WriteString(w, `{"limits":[{"kind":"session","percent":0.5,"resets_at":"2026-09-18T10:00:00.123Z"},{"kind":"weekly_scoped","percent":101.5,"scope":{"model":{"display_name":"Fable"}}}],"spend":{"enabled":true,"used":{"amount_minor":125,"currency":"USD","exponent":2},"limit":{"amount_minor":5000,"currency":"USD","exponent":2}}}`)
		require.NoError(t, err)
	})}
	response, err := reader.Read(t.Context(), usage.Credential{Token: "sk-ant-oat01-fixture-key"})
	require.NoError(t, err)
	require.Len(t, response.Limits, 2)
	require.Equal(t, 0.5, response.Limits[0].UsedPercent)
	require.Equal(t, "weekly_scoped/Fable", response.Limits[1].ID)
	require.Equal(t, 101.5, response.Limits[1].UsedPercent)
	require.Len(t, response.Balances, 1)
	require.Equal(t, &wire.AccountUsageMoney{Amount: 1.25, Currency: "USD"}, response.Balances[0].Used)
	require.Equal(t, &wire.AccountUsageMoney{Amount: 50, Currency: "USD"}, response.Balances[0].Limit)
	require.Nil(t, response.Balances[0].Remaining)
	response, err = reader.Read(t.Context(), usage.Credential{Token: "api-key"})
	require.NoError(t, err)
	require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotReported), response)
}

func TestUsageRequestErrorHidesUnknownFailureDetails(t *testing.T) {
	require.Equal(t, wire.InternalFailure("fixture", "account_usage"), usage.RequestError("fixture", errors.New("credential-shaped sensitive detail")))
}
