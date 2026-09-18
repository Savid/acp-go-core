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
		require.Equal(t, "Bearer fixture-key", r.Header.Get("Authorization"))
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
	response, err := reader.Read(t.Context(), "fixture-key")
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
		observed, parseErr := time.Parse(time.RFC3339, balance.ObservedAt)
		require.NoError(t, parseErr)
		stale, parseErr := time.Parse(time.RFC3339, balance.StaleAt)
		require.NoError(t, parseErr)
		require.Equal(t, usage.Freshness, stale.Sub(observed))
	}
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
			response, err := reader.Read(t.Context(), "fixture-key")
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
			response, err := reader.Read(t.Context(), "fixture-key")
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
	response, err := reader.Read(t.Context(), "fixture-key")
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
			response, err := reader.Read(t.Context(), "fixture-key")
			require.Error(t, err)
			require.Equal(t, wire.AccountUsageResponse{}, response)
			require.Equal(t, 1, calls)
		})
	}
}

func TestProviderHTTPBoundary(t *testing.T) {
	for _, kind := range []string{"go", "router"} {
		t.Run(kind, func(t *testing.T) {
			makeReader := func(transport http.RoundTripper) usage.Reader {
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
				response, err := reader.Read(t.Context(), "")
				require.NoError(t, err)
				require.Equal(t, wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), response)
			})
			t.Run("redirect", func(t *testing.T) {
				var calls atomic.Int64
				reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
					calls.Add(1)

					return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other.example/usage"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}))
				_, err := reader.Read(t.Context(), "fixture-key")
				var readErr *usage.HTTPError
				require.ErrorAs(t, err, &readErr)
				require.Equal(t, 302, readErr.StatusCode)
				require.Equal(t, int64(1), calls.Load())
			})
			t.Run("rate limited", func(t *testing.T) {
				reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
				}))
				_, err := reader.Read(t.Context(), "fixture-key")
				var readErr *usage.HTTPError
				require.ErrorAs(t, err, &readErr)
				require.Equal(t, http.StatusTooManyRequests, readErr.StatusCode)
			})
			t.Run("cancellation", func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
					cancel()
					<-r.Context().Done()

					return nil, r.Context().Err()
				}))
				_, err := reader.Read(ctx, "fixture-key")
				require.ErrorIs(t, err, context.Canceled)
			})
			t.Run("bounded body", func(t *testing.T) {
				reader := makeReader(roundTrip(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", 65537))), Request: r}, nil
				}))
				_, err := reader.Read(t.Context(), "fixture-key")
				require.ErrorContains(t, err, "read bound")
			})
			t.Run("redacted error", func(t *testing.T) {
				reader := makeReader(roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture-key response payload") }))
				_, err := reader.Read(t.Context(), "fixture-key")
				require.Error(t, err)
				require.NotContains(t, err.Error(), "fixture-key")
				require.NotContains(t, err.Error(), "payload")
			})
		})
	}
}
