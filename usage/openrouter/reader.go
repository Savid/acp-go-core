// Package openrouter reads OpenRouter key allowances and account credit balances.
package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/usage/internal/usagehttp"
	"github.com/savid/acp-go-core/wire"
)

const (
	ProviderID      = "openrouter"
	Endpoint        = "https://openrouter.ai/api/v1/key"
	balanceEndpoint = "https://openrouter.ai/api/v1/credits"
)

// Reader uses the supplied transport or http.DefaultTransport. It never acquires credentials.
type Reader struct{ Transport http.RoundTripper }

// Read observes the key allowance and any account balance that the same credential can read.
func (r Reader) Read(ctx context.Context, apiKey string) (wire.AccountUsageResponse, error) {
	response, err := usagehttp.Get(ctx, r.Transport, Endpoint, apiKey)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if response.StatusCode == http.StatusUnauthorized {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), nil
	}

	if response.StatusCode != http.StatusOK {
		return wire.AccountUsageResponse{}, &usage.HTTPError{StatusCode: response.StatusCode}
	}

	result, err := decode(response)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if balance := r.credits(ctx, apiKey); balance != nil {
		result.Balances = append(result.Balances, *balance)
	}

	if ctx.Err() != nil {
		return wire.AccountUsageResponse{}, ctx.Err()
	}

	return result, result.Validate()
}

//nolint:tagliatelle // Provider API members use snake_case.
type keyLimits struct {
	Limit      json.RawMessage `json:"limit"`
	Remaining  *float64        `json:"limit_remaining"`
	Reset      *string         `json:"limit_reset"`
	Usage      *float64        `json:"usage"`
	FreeModels *requestLimit   `json:"free_model_daily_requests"`
}

type requestLimit struct {
	Used      *int64 `json:"used"`
	Limit     *int64 `json:"limit"`
	Remaining *int64 `json:"remaining"`
}

func decode(response usagehttp.Response) (wire.AccountUsageResponse, error) {
	var body struct {
		Data *keyLimits `json:"data"`
	}
	if err := response.Decode(&body); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if body.Data == nil || len(body.Data.Limit) == 0 || body.Data.Usage == nil || !finiteNonnegative(*body.Data.Usage) {
		return wire.AccountUsageResponse{}, errors.New("OpenRouter key usage is incomplete")
	}

	var limit *float64
	if json.Unmarshal(body.Data.Limit, &limit) != nil || (limit == nil) != (body.Data.Remaining == nil) {
		return wire.AccountUsageResponse{}, errors.New("OpenRouter key limit is incomplete")
	}

	observedAt := wire.AccountUsageTime(response.ObservedAt)
	staleAt := wire.AccountUsageTime(response.ObservedAt.Add(usage.Freshness))

	budget := wire.AccountUsageBalance{ID: "key_spending", Label: "Key spending", ObservedAt: observedAt, StaleAt: staleAt}
	if limit == nil {
		budget.Uncapped = true
		budget.Used = money(*body.Data.Usage)
	} else {
		budget.Limit = money(*limit)

		budget.Remaining = money(*body.Data.Remaining)
		if body.Data.Reset != nil {
			budget.ResetInterval = *body.Data.Reset
		}
	}

	result := wire.AccountUsageResponse{Available: true, Balances: []wire.AccountUsageBalance{budget}}
	if limit != nil {
		result.Balances = append(result.Balances, wire.AccountUsageBalance{
			ID: "key_usage", Label: "Key spend (all time)", ObservedAt: observedAt, StaleAt: staleAt, Used: money(*body.Data.Usage),
		})
	}

	if requests := body.Data.FreeModels; requests != nil {
		if requests.Used == nil || requests.Limit == nil || requests.Remaining == nil {
			return wire.AccountUsageResponse{}, errors.New("OpenRouter free-model request limit is incomplete")
		}

		result.RequestLimits = append(result.RequestLimits, wire.AccountUsageRequestLimit{
			ID: "free_model_daily_requests", Label: "Free-model requests", ObservedAt: observedAt, StaleAt: staleAt,
			Used: *requests.Used, Limit: *requests.Limit, Remaining: *requests.Remaining, ResetInterval: "daily",
		})
	}

	if err := result.Validate(); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	return result, nil
}

func (r Reader) credits(ctx context.Context, apiKey string) *wire.AccountUsageBalance {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	response, err := usagehttp.Get(ctx, r.Transport, balanceEndpoint, apiKey)
	if err != nil || response.StatusCode != http.StatusOK {
		return nil
	}

	var body struct {
		Data *struct {
			TotalCredits *float64 `json:"total_credits"` //nolint:tagliatelle // Provider API member.
			TotalUsage   *float64 `json:"total_usage"`   //nolint:tagliatelle // Provider API member.
		} `json:"data"`
	}
	if response.Decode(&body) != nil || body.Data == nil || body.Data.TotalCredits == nil || body.Data.TotalUsage == nil {
		return nil
	}

	purchased, used := *body.Data.TotalCredits, *body.Data.TotalUsage
	if !finiteNonnegative(purchased) || !finiteNonnegative(used) {
		return nil
	}

	return &wire.AccountUsageBalance{
		ID: "account_credits", Label: "Account credits", ObservedAt: wire.AccountUsageTime(response.ObservedAt),
		StaleAt: wire.AccountUsageTime(response.ObservedAt.Add(usage.Freshness)), Used: money(used), Remaining: money(purchased - used),
	}
}

func money(amount float64) *wire.AccountUsageMoney {
	return &wire.AccountUsageMoney{Amount: amount, Currency: "USD"}
}
func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
