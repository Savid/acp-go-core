// Package gateway reads the aggregate usage report a forwarding gateway
// publishes for the upstream accounts it brokers.
package gateway

import (
	"context"
	"crypto/sha256"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/usage/anthropic"
	"github.com/savid/acp-go-core/usage/internal/usagehttp"
	"github.com/savid/acp-go-core/usage/openaicodex"
	"github.com/savid/acp-go-core/usage/opencodego"
	"github.com/savid/acp-go-core/wire"
)

// Path is where a gateway publishes its report, beneath the API root the
// harness is configured with.
const Path = "/v1/usage"

// The gateway's window ids for the windows every provider shares.
const (
	windowFiveHour = "5h"
	windowSevenDay = "7d"
	windowMonthly  = "monthly"
)

// Reader reads one upstream provider's section of the report published at the
// credential's base. It uses the supplied transport or http.DefaultTransport
// and never acquires credentials.
type Reader struct {
	Transport  http.RoundTripper
	ProviderID string
}

// Endpoint is the report address beneath base: the API root the harness sends
// requests to, with or without its trailing /v1 segment.
func Endpoint(base string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("gateway base is not an http origin")
	}

	parsed.Path = strings.TrimSuffix(strings.TrimSuffix(parsed.Path, "/"), "/v1") + Path
	parsed.RawPath = ""

	return parsed.String(), nil
}

// Read observes the provider's section of the report at credential.BaseURL
// with the bearer the harness sends there. A base that publishes no report is
// a plain proxy and answers not_reported.
func (r Reader) Read(ctx context.Context, credential usage.Credential) (wire.AccountUsageResponse, error) {
	endpoint, err := Endpoint(credential.BaseURL)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	response, err := usagehttp.Get(ctx, r.Transport, endpoint, credential.Token, nil)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	switch response.StatusCode {
	case http.StatusUnauthorized:
		return wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), nil
	case http.StatusNotFound:
		return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
	case http.StatusOK:
		return r.decode(response)
	}

	return wire.AccountUsageResponse{}, &usage.HTTPError{StatusCode: response.StatusCode, RetryAt: response.RetryAt}
}

type report struct {
	Reports []providerReport `json:"reports"`
}

type providerReport struct {
	Provider  string  `json:"provider"`
	FetchedAt int64   `json:"fetchedAt"`
	Error     string  `json:"error"`
	Limits    []limit `json:"limits"`
	Metadata  struct {
		PlanType string `json:"planType"`
		Allowed  *bool  `json:"allowed"`
	} `json:"metadata"`
}

type limit struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Scope  struct {
		Tier    string `json:"tier"`
		ModelID string `json:"modelId"`
	} `json:"scope"`
	Window struct {
		ID         string `json:"id"`
		DurationMs int64  `json:"durationMs"`
		ResetsAt   int64  `json:"resetsAt"`
	} `json:"window"`
	Amount struct {
		Used         *float64 `json:"used"`
		Limit        *float64 `json:"limit"`
		Remaining    *float64 `json:"remaining"`
		UsedFraction *float64 `json:"usedFraction"`
		Unit         string   `json:"unit"`
	} `json:"amount"`
}

// decode projects the provider's section: percent limits become windows, usd
// amounts balances, request counts request limits, and any other unit is left
// out. A section the gateway could not fetch is a failed read.
func (r Reader) decode(response usagehttp.Response) (wire.AccountUsageResponse, error) {
	var body report

	published := response.Decode(&body) == nil && body.Reports != nil
	if !published {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
	}

	for _, section := range body.Reports {
		if section.Provider != r.ProviderID {
			continue
		}

		if section.Error != "" && len(section.Limits) == 0 {
			return wire.AccountUsageResponse{}, errors.New("gateway could not read the upstream account")
		}

		result := wire.AccountUsageResponse{Available: true, Plan: strings.TrimSpace(section.Metadata.PlanType), UsageAllowed: section.Metadata.Allowed}
		observed := wire.AccountUsageTime(time.UnixMilli(section.FetchedAt))

		for index := range section.Limits {
			entry := &section.Limits[index]
			id, label := nativeWindow(section.Provider, entry)

			resets := ""
			if entry.Window.ResetsAt > 0 {
				resets = wire.AccountUsageTime(time.UnixMilli(entry.Window.ResetsAt))
			}

			switch entry.Amount.Unit {
			case "percent":
				percent, ok := percentOf(entry)
				if !ok {
					continue
				}

				window := wire.AccountUsageLimit{ID: id, Label: label, ObservedAt: observed, UsedPercent: percent, ResetsAt: resets, UsageAllowed: allowed(entry.Status)}
				if entry.Window.DurationMs > 0 && section.Provider == openaicodex.ProviderID {
					window.WindowSeconds = entry.Window.DurationMs / 1000
				}

				result.Limits = append(result.Limits, window)
			case "usd":
				if entry.Amount.Used == nil {
					continue
				}

				result.Balances = append(result.Balances, wire.AccountUsageBalance{
					ID: id, Label: label, ObservedAt: observed, ResetsAt: resets,
					Used: money(entry.Amount.Used), Limit: money(entry.Amount.Limit), Remaining: money(entry.Amount.Remaining),
				})
			case "requests":
				if entry.Amount.Used == nil || entry.Amount.Limit == nil {
					continue
				}

				remaining := *entry.Amount.Limit - *entry.Amount.Used
				if entry.Amount.Remaining != nil {
					remaining = *entry.Amount.Remaining
				}

				result.RequestLimits = append(result.RequestLimits, wire.AccountUsageRequestLimit{
					ID: id, Label: label, ObservedAt: observed, ResetsAt: resets,
					Used: int64(*entry.Amount.Used), Limit: int64(*entry.Amount.Limit), Remaining: int64(remaining),
				})
			}
		}

		if len(result.Limits)+len(result.Balances)+len(result.RequestLimits) == 0 {
			return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
		}

		if err := result.Validate(); err != nil {
			return wire.AccountUsageResponse{}, err
		}

		return result, nil
	}

	return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
}

// nativeWindow names a gateway limit as the provider's own reader names the
// same window, so an account reads the same whichever route carried it. The
// gateway's window and tier identify the window; a provider without a
// mapping keeps the gateway's names. Only ChatGPT's reader reports window
// lengths, so only its windows carry one.
func nativeWindow(provider string, entry *limit) (id, label string) {
	switch provider {
	case anthropic.ProviderID:
		switch {
		case entry.Window.ID == windowFiveHour:
			return "session", ""
		case entry.Window.ID == windowSevenDay && entry.Scope.Tier == "":
			return "weekly_all", ""
		case entry.Window.ID == windowSevenDay:
			model := titled(entry.Scope.Tier)

			return "weekly_scoped/" + model, model
		}
	case openaicodex.ProviderID:
		feature := "codex"
		if entry.Scope.Tier != "" {
			feature = strings.ReplaceAll(entry.Scope.Tier, "-", "_")
		}

		if suffix := entry.ID[strings.LastIndex(entry.ID, ":")+1:]; suffix == "primary" || suffix == "secondary" {
			return feature + "/" + suffix, strings.TrimSpace(entry.Scope.ModelID)
		}
	case opencodego.ProviderID:
		switch entry.Window.ID {
		case windowFiveHour:
			return "rolling", "Rolling"
		case windowSevenDay:
			return "weekly", "Weekly"
		case windowMonthly:
			return "monthly", "Monthly"
		}
	}

	return strings.ReplaceAll(strings.TrimPrefix(entry.ID, provider+":"), ":", "/"), strings.TrimSpace(entry.Label)
}

// titled renders a gateway tier as the provider's model display name: the
// first letter raised, the rest as given.
func titled(tier string) string {
	if tier == "" {
		return ""
	}

	return strings.ToUpper(tier[:1]) + tier[1:]
}

func percentOf(entry *limit) (float64, bool) {
	switch {
	case entry.Amount.UsedFraction != nil:
		return *entry.Amount.UsedFraction * 100, finite(*entry.Amount.UsedFraction)
	case entry.Amount.Used != nil:
		return *entry.Amount.Used, finite(*entry.Amount.Used)
	default:
		return 0, false
	}
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func allowed(status string) *bool {
	switch status {
	case "ok", "warning":
		return new(true)
	case "exhausted":
		return new(false)
	default:
		return nil
	}
}

func money(amount *float64) *wire.AccountUsageMoney {
	if amount == nil {
		return nil
	}

	return &wire.AccountUsageMoney{Amount: *amount, Currency: "USD"}
}

// Route is one custom provider route a harness is configured with: the base
// it sends requests to and the bearer it sends there.
type Route struct {
	Provider string
	BaseURL  string
	Token    string
}

// ReadRoutes asks each route for the provider's section in order and answers
// with the first that covers it. A route without an http base or a bearer is
// skipped. When no route covers the provider, fallback answers.
func ReadRoutes(ctx context.Context, transport http.RoundTripper, routes []Route, providerID string, fallback wire.AccountUsageResponse) (wire.AccountUsageResponse, error) {
	for _, route := range routes {
		if _, err := Endpoint(route.BaseURL); err != nil || strings.TrimSpace(route.Token) == "" {
			continue
		}

		access := usage.Access{APIKey: route.Token, BaseURL: route.BaseURL, Fingerprint: sha256.Sum256([]byte(route.Provider + "\x00" + route.BaseURL))}

		response, err := usage.ReadVerified(ctx, func(context.Context) (usage.Access, error) { return access, nil }, Reader{Transport: transport, ProviderID: providerID})
		if err != nil {
			return wire.AccountUsageResponse{}, err
		}

		if response.Available {
			return response, nil
		}
	}

	return fallback, nil
}

// ModelsPath is where a gateway publishes the models it routes to, beneath
// the API root the harness is configured with.
const ModelsPath = "/v1/models"

// Model is one entry of a gateway's model list.
type Model struct {
	ID            string
	Name          string
	ContextWindow int64
	MaxTokens     int64
	// Inputs is the gateway's own list of input modalities; nil means it
	// reported none.
	Inputs []string
}

// ModelsEndpoint is the model list address beneath base, with or without its
// trailing /v1 segment.
func ModelsEndpoint(base string) (string, error) {
	endpoint, err := Endpoint(base)
	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(endpoint, Path) + ModelsPath, nil
}

// Models reads the model list a gateway publishes at the route's base with the
// bearer the harness sends there, in the gateway's order. A base that
// publishes no list answers none.
func Models(ctx context.Context, transport http.RoundTripper, route Route) ([]Model, error) {
	endpoint, err := ModelsEndpoint(route.BaseURL)
	if err != nil {
		return nil, err
	}

	response, err := usagehttp.Get(ctx, transport, endpoint, route.Token, nil)
	if err != nil {
		return nil, err
	}

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusUnauthorized:
		return nil, nil
	default:
		return nil, &usage.HTTPError{StatusCode: response.StatusCode, RetryAt: response.RetryAt}
	}

	var body struct {
		Data []struct {
			ID            string   `json:"id"`
			DisplayName   string   `json:"display_name"`      //nolint:tagliatelle // Gateway API member.
			ContextLength int64    `json:"context_length"`    //nolint:tagliatelle // Gateway API member.
			MaxOutput     int64    `json:"max_output_tokens"` //nolint:tagliatelle // Gateway API member.
			Inputs        []string `json:"input_modalities"`  //nolint:tagliatelle // Gateway API member.
		} `json:"data"`
	}

	published := response.Decode(&body) == nil && body.Data != nil
	if !published {
		return nil, nil
	}

	models := make([]Model, 0, len(body.Data))

	for _, entry := range body.Data {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}

		name := strings.TrimSpace(entry.DisplayName)
		if name == "" {
			name = id
		}

		models = append(models, Model{ID: id, Name: name, ContextWindow: max(entry.ContextLength, 0), MaxTokens: max(entry.MaxOutput, 0), Inputs: entry.Inputs})
	}

	return models, nil
}
