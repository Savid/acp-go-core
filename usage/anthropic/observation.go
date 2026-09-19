package anthropic

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/savid/acp-go-core/wire"
)

// Observation is the provider's allowance list and monetary spending observation.
type Observation struct {
	Limits []Limit `json:"limits"`
	Spend  *Spend  `json:"spend"`
}

// Limit is one allowance window, optionally scoped to a model.
type Limit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent"`
	ResetsAt string   `json:"resets_at"` //nolint:tagliatelle // Provider API member.
	Scope    *Scope   `json:"scope"`
}

type Scope struct {
	Model *Model `json:"model"`
}

type Model struct {
	DisplayName string `json:"display_name"` //nolint:tagliatelle // Provider API member.
}

// Spend keeps reported spending separate from subscription percentage windows.
type Spend struct {
	Enabled bool   `json:"enabled"`
	Used    *Money `json:"used"`
	Limit   *Money `json:"limit"`
}

// Money carries its unit exponent; no currency or scale is inferred.
type Money struct {
	AmountMinor *float64 `json:"amount_minor"` //nolint:tagliatelle // Provider API member.
	Currency    string   `json:"currency"`
	Exponent    *int     `json:"exponent"`
}

// Response projects one native observation without renewing its timestamps.
func (o Observation) Response(plan string, observedAt time.Time) (wire.AccountUsageResponse, error) {
	result := wire.AccountUsageResponse{Available: true, Plan: strings.TrimSpace(plan)}
	observed := wire.AccountUsageTime(observedAt)

	for _, limit := range o.Limits {
		if limit.Percent == nil {
			return wire.AccountUsageResponse{}, errors.New("anthropic usage percentage is missing")
		}

		entry := wire.AccountUsageLimit{ID: strings.TrimSpace(limit.Kind), UsedPercent: *limit.Percent, ObservedAt: observed}
		if limit.Scope != nil && limit.Scope.Model != nil {
			if entry.Label = strings.TrimSpace(limit.Scope.Model.DisplayName); entry.Label != "" {
				entry.ID += "/" + entry.Label
			}
		}

		if limit.ResetsAt != "" {
			at, err := time.Parse(time.RFC3339Nano, limit.ResetsAt)
			if err != nil {
				return wire.AccountUsageResponse{}, errors.New("anthropic usage reset time is invalid")
			}

			entry.ResetsAt = wire.AccountUsageTime(at)
		}

		result.Limits = append(result.Limits, entry)
	}

	if spend := o.Spend; spend != nil && spend.Enabled && spend.Used != nil {
		used, err := spend.Used.money()
		if err != nil {
			return wire.AccountUsageResponse{}, err
		}

		limit, err := spend.Limit.money()
		if err != nil {
			return wire.AccountUsageResponse{}, err
		}

		result.Balances = append(result.Balances, wire.AccountUsageBalance{
			ID: "usage_credits", Used: used, Limit: limit, ObservedAt: observed,
		})
	}

	if len(result.Limits) == 0 && len(result.Balances) == 0 {
		return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
	}

	if err := result.Validate(); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	return result, nil
}

//nolint:nilnil // An absent monetary field remains absent.
func (m *Money) money() (*wire.AccountUsageMoney, error) {
	if m == nil {
		return nil, nil
	}

	if m.AmountMinor == nil || m.Exponent == nil || *m.Exponent < 0 || *m.Exponent > 9 {
		return nil, errors.New("anthropic usage money unit is incomplete")
	}

	return &wire.AccountUsageMoney{Amount: *m.AmountMinor / math.Pow10(*m.Exponent), Currency: m.Currency}, nil
}
