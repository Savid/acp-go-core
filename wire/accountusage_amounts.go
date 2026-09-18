package wire

import (
	"errors"
	"math"
	"strings"
)

const (
	accountResetDaily   = "daily"
	accountResetWeekly  = "weekly"
	accountResetMonthly = "monthly"
)

// AccountUsageMoney is an observed amount in major units of its uppercase currency.
type AccountUsageMoney struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// AccountUsageBalance reports one spending cap or account credit balance.
// Uncapped explicitly describes a key without a spending cap; an account credit
// balance has neither Uncapped nor Limit. Remaining may be negative.
type AccountUsageBalance struct {
	ID            string             `json:"id"`
	Label         string             `json:"label,omitempty"`
	ObservedAt    string             `json:"observedAt"`
	StaleAt       string             `json:"staleAt"`
	Used          *AccountUsageMoney `json:"used,omitempty"`
	Limit         *AccountUsageMoney `json:"limit,omitempty"`
	Remaining     *AccountUsageMoney `json:"remaining,omitempty"`
	Uncapped      bool               `json:"uncapped,omitempty"`
	ResetInterval string             `json:"resetInterval,omitempty"`
	ResetsAt      string             `json:"resetsAt,omitempty"`
}

// AccountUsageRequestLimit reports the provider's count and ceiling for a request class.
type AccountUsageRequestLimit struct {
	ID            string `json:"id"`
	Label         string `json:"label,omitempty"`
	ObservedAt    string `json:"observedAt"`
	StaleAt       string `json:"staleAt"`
	Used          int64  `json:"used"`
	Limit         int64  `json:"limit"`
	Remaining     int64  `json:"remaining"`
	ResetInterval string `json:"resetInterval,omitempty"`
	ResetsAt      string `json:"resetsAt,omitempty"`
}

func validateAccountMeasurement(id, label, observedAt, staleAt, interval, resetsAt string) error {
	if id == "" || id != strings.TrimSpace(id) || label != strings.TrimSpace(label) {
		return errors.New("measurement identity is empty or carries surrounding whitespace")
	}

	if err := validateAccountUsageTime(observedAt, "observedAt"); err != nil {
		return err
	}

	if err := validateAccountUsageTime(staleAt, "staleAt"); err != nil {
		return err
	}

	if interval != "" && interval != accountResetDaily && interval != accountResetWeekly && interval != accountResetMonthly {
		return errors.New("resetInterval is unknown")
	}

	if resetsAt != "" {
		return validateAccountUsageTime(resetsAt, "resetsAt")
	}

	return nil
}

func (b AccountUsageBalance) validate() error {
	if err := validateAccountMeasurement(b.ID, b.Label, b.ObservedAt, b.StaleAt, b.ResetInterval, b.ResetsAt); err != nil {
		return err
	}

	if b.Used == nil && b.Remaining == nil {
		return errors.New("balance requires used or remaining")
	}

	if b.Uncapped && (b.Limit != nil || b.Remaining != nil || b.ResetInterval != "" || b.ResetsAt != "") {
		return errors.New("uncapped spending has no limit, remaining, or reset")
	}

	currency := ""

	for _, amount := range []*AccountUsageMoney{b.Used, b.Limit, b.Remaining} {
		if amount == nil {
			continue
		}

		if len(amount.Currency) != 3 || strings.IndexFunc(amount.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) != -1 {
			return errors.New("currency must be a three-letter uppercase code")
		}

		if currency != "" && currency != amount.Currency {
			return errors.New("one balance must use one currency")
		}

		currency = amount.Currency
		if math.IsNaN(amount.Amount) || math.IsInf(amount.Amount, 0) {
			return errors.New("monetary amount must be finite")
		}
	}

	if b.Used != nil && b.Used.Amount < 0 || b.Limit != nil && b.Limit.Amount < 0 {
		return errors.New("used and limit must be nonnegative")
	}

	return nil
}

func (l AccountUsageRequestLimit) validate() error {
	if err := validateAccountMeasurement(l.ID, l.Label, l.ObservedAt, l.StaleAt, l.ResetInterval, l.ResetsAt); err != nil {
		return err
	}

	if l.Used < 0 || l.Limit < 0 || l.Remaining < 0 {
		return errors.New("request counts must be nonnegative")
	}

	return nil
}
