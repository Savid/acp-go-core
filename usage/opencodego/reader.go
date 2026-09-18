// Package opencodego reads OpenCode Go subscription windows.
package opencodego

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/savid/acp-go-core/usage"
	"github.com/savid/acp-go-core/usage/internal/usagehttp"
	"github.com/savid/acp-go-core/wire"
)

const (
	ProviderID = "opencode-go"
	Endpoint   = "https://opencode.ai/zen/go/v1/usage"
)

// Reader uses the supplied transport or http.DefaultTransport. It never acquires credentials.
type Reader struct{ Transport http.RoundTripper }

// Read observes the subscription addressed by the caller's effective API key.
func (r Reader) Read(ctx context.Context, apiKey string) (wire.AccountUsageResponse, error) {
	response, err := usagehttp.Get(ctx, r.Transport, Endpoint, apiKey)
	if err != nil {
		return wire.AccountUsageResponse{}, err
	}

	switch response.StatusCode {
	case http.StatusUnauthorized:
		return wire.AccountUsageUnavailable(wire.AccountUsageNotAuthenticated), nil
	case http.StatusForbidden:
		var refusal struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if response.Decode(&refusal) == nil && refusal.Error.Type == "EntitlementError" {
			return wire.AccountUsageUnavailable(wire.AccountUsageNotReported), nil
		}
	case http.StatusOK:
		return decode(response)
	}

	return wire.AccountUsageResponse{}, &usage.HTTPError{StatusCode: response.StatusCode}
}

type window struct {
	Status   string    `json:"status"`
	Percent  *float64  `json:"percent"`
	ResetsAt time.Time `json:"resetsAt"`
}

func decode(response usagehttp.Response) (wire.AccountUsageResponse, error) {
	var body struct {
		Usage *struct {
			Rolling *window `json:"rolling"`
			Weekly  *window `json:"weekly"`
			Monthly *window `json:"monthly"`
		} `json:"usage"`
	}
	if err := response.Decode(&body); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	if body.Usage == nil {
		return wire.AccountUsageResponse{}, errors.New("OpenCode Go usage windows are missing")
	}

	result := wire.AccountUsageResponse{Available: true}

	for _, item := range []struct {
		id, label string
		window    *window
	}{
		{"rolling", "Rolling", body.Usage.Rolling},
		{"weekly", "Weekly", body.Usage.Weekly},
		{"monthly", "Monthly", body.Usage.Monthly},
	} {
		w := item.window
		if w == nil || w.Percent == nil || w.ResetsAt.IsZero() || (w.Status != "ok" && w.Status != "rate-limited") {
			return wire.AccountUsageResponse{}, errors.New("OpenCode Go usage window is incomplete")
		}

		result.Limits = append(result.Limits, wire.AccountUsageLimit{
			ID: item.id, Label: item.label, UsedPercent: *w.Percent,
			ObservedAt: wire.AccountUsageTime(response.ObservedAt),
			StaleAt:    wire.AccountUsageTime(response.ObservedAt.Add(usage.Freshness)),
			ResetsAt:   wire.AccountUsageTime(w.ResetsAt), UsageAllowed: new(w.Status == "ok"),
		})
	}

	if err := result.Validate(); err != nil {
		return wire.AccountUsageResponse{}, err
	}

	return result, nil
}
