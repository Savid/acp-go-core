// Package openaicodex reads ChatGPT subscription allowance windows.
package openaicodex

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/usage/internal/usagehttp"
	"github.com/savid/acp-go-core/wire"
)

const (
	ProviderID = "openai-codex"
	Endpoint   = "https://chatgpt.com/backend-api/wham/usage"
)

// Reader uses the supplied transport or http.DefaultTransport. It never acquires credentials.
type Reader struct{ Transport http.RoundTripper }

// Read observes the subscription bound to the caller's native access token and account ID.
func (r Reader) Read(ctx context.Context, credential usage.Credential) (wire.AccountUsageResponse, error) {
	if strings.TrimSpace(credential.Token) != "" && strings.TrimSpace(credential.AccountID) == "" {
		return wire.AccountUsageResponse{}, errors.New("ChatGPT usage requires an account ID")
	}

	headers := http.Header{"Chatgpt-Account-Id": {credential.AccountID}}

	response, err := usagehttp.Get(ctx, r.Transport, Endpoint, credential.Token, headers)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if response.StatusCode == http.StatusUnauthorized {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), nil
	}

	if response.StatusCode != http.StatusOK {
		return wire.AccountUsageResponse{}, &usage.HTTPError{StatusCode: response.StatusCode, RetryAt: response.RetryAt}
	}

	return decode(response, credential.AccountID)
}

//nolint:tagliatelle // Provider API members use snake_case.
type window struct {
	UsedPercent *float64 `json:"used_percent"`
	Seconds     int64    `json:"limit_window_seconds"`
	ResetsAt    int64    `json:"reset_at"`
}

//nolint:tagliatelle // Provider API members use snake_case.
type rateLimit struct {
	Allowed   *bool   `json:"allowed"`
	Primary   *window `json:"primary_window"`
	Secondary *window `json:"secondary_window"`
}

//nolint:tagliatelle // Provider API members use snake_case.
type allowance struct {
	Name    string     `json:"limit_name"`
	Feature string     `json:"metered_feature"`
	Limit   *rateLimit `json:"rate_limit"`
}

//nolint:tagliatelle // Provider API members use snake_case.
type observation struct {
	AccountID  string      `json:"account_id"`
	Plan       string      `json:"plan_type"`
	Limit      *rateLimit  `json:"rate_limit"`
	CodeReview *rateLimit  `json:"code_review_rate_limit"`
	Additional []allowance `json:"additional_rate_limits"`
}

func decode(response usagehttp.Response, accountID string) (wire.AccountUsageResponse, error) {
	var body observation
	if err := response.Decode(&body); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if body.AccountID != accountID || strings.TrimSpace(body.Plan) == "" {
		return wire.AccountUsageResponse{}, errors.New("ChatGPT usage account binding is incomplete or changed")
	}

	result := wire.AccountUsageResponse{Available: true, Plan: body.Plan}

	limits := append([]allowance{{Feature: "codex", Limit: body.Limit}, {Feature: "code_review", Limit: body.CodeReview}}, body.Additional...)
	for _, limit := range limits {
		if limit.Limit == nil {
			continue
		}

		if limit.Feature == "" || limit.Feature != strings.TrimSpace(limit.Feature) {
			return wire.AccountUsageResponse{}, errors.New("ChatGPT usage limit identity is missing")
		}

		for _, item := range []struct {
			suffix string
			window *window
		}{{"primary", limit.Limit.Primary}, {"secondary", limit.Limit.Secondary}} {
			w := item.window
			if w == nil {
				continue
			}

			if w.UsedPercent == nil || w.Seconds < 0 {
				return wire.AccountUsageResponse{}, errors.New("ChatGPT usage window is incomplete")
			}

			entry := wire.AccountUsageLimit{
				ID: limit.Feature + "/" + item.suffix, Label: strings.TrimSpace(limit.Name),
				UsedPercent: *w.UsedPercent, WindowSeconds: w.Seconds, UsageAllowed: limit.Limit.Allowed,
				ObservedAt: wire.AccountUsageTime(response.ObservedAt),
			}
			if w.ResetsAt > 0 {
				entry.ResetsAt = wire.AccountUsageTime(time.Unix(w.ResetsAt, 0))
			}

			result.Limits = append(result.Limits, entry)
		}
	}

	if len(result.Limits) == 0 {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
	}

	if err := result.Validate(); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	return result, nil
}
