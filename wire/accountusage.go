package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/coder/acp-go-sdk"

	"github.com/savid/acp-go-core/lifecycle"
)

// AccountUsageCapabilityKey is the _meta.<vendor> member advertising the
// account-usage read.
const AccountUsageCapabilityKey = "accountUsage"

// AccountUsageReadTimeout bounds the native reads behind one account-usage
// request.
const AccountUsageReadTimeout = 30 * time.Second

// AccountUsageScope names what a sibling's account-usage read is bound to.
type AccountUsageScope string

// The two account-usage scopes.
const (
	// AccountUsageScopeSession reads through a loaded session's native process.
	AccountUsageScopeSession AccountUsageScope = "session"
	// AccountUsageScopeAgent reads through the shared native runtime.
	AccountUsageScopeAgent AccountUsageScope = "agent"
)

// Reasons an account-usage read answers unavailable.
const (
	// AccountUsageNotAuthenticated means the harness reports no authenticated
	// account.
	AccountUsageNotAuthenticated = "not_authenticated"
	// AccountUsageNotReported means the harness reports no allowance window for
	// its current credential.
	AccountUsageNotReported = "not_reported"
)

// Account-usage request and advertisement members.
const (
	accountUsageFieldParams = "params"
	accountUsageFieldMeta   = "_meta"
	accountUsageFieldMethod = "method"
	accountUsageFieldScope  = "scope"
)

// AccountUsageAdvertisement is the _meta.<vendor>.accountUsage value a sibling
// that implements the read advertises at initialize.
func AccountUsageAdvertisement(method string, scope AccountUsageScope) map[string]any {
	return map[string]any{accountUsageFieldMethod: method, accountUsageFieldScope: string(scope)}
}

// AccountUsageRequest is one decoded _<vendor>/accountUsage request.
type AccountUsageRequest struct {
	// SessionID is empty only on an agent-scoped read that named no session.
	SessionID acp.SessionId
	// Meta is the request's _meta as sent; the sibling opens its span with it
	// so the host's trace keys propagate as on every request.
	Meta map[string]any
}

// DecodeAccountUsageRequest reads the params of one account-usage request. The
// only members are sessionId and _meta; anything else, and any repeated
// member, is refused naming the member. Only the agent scope accepts a request
// without sessionId; every other scope value, including an unset one, requires
// it. _meta is decoded first, so every refusal after it returns the decoded
// Meta beside the error and the sibling still opens its span with the host's
// trace keys.
func DecodeAccountUsageRequest(params json.RawMessage, scope AccountUsageScope) (AccountUsageRequest, *acp.RequestError) {
	members, refusal := decodeAccountUsageMembers(params)
	if refusal != nil {
		return AccountUsageRequest{}, refusal
	}

	var request AccountUsageRequest

	if raw, present := members[accountUsageFieldMeta]; present {
		if err := json.Unmarshal(raw, &request.Meta); err != nil {
			return AccountUsageRequest{}, Unsupported(accountUsageFieldMeta)
		}

		if paramErr := lifecycle.RejectKey(request.Meta); paramErr != nil {
			return request, ParamRefusal(paramErr)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(members)) {
		if name != fieldSessionID && name != accountUsageFieldMeta {
			return request, Unsupported(name)
		}
	}

	raw, present := members[fieldSessionID]
	if !present {
		if scope != AccountUsageScopeAgent {
			return request, Missing(fieldSessionID)
		}

		return request, nil
	}

	var id string
	if err := json.Unmarshal(raw, &id); err != nil || id == "" {
		return request, Unsupported(fieldSessionID)
	}

	request.SessionID = acp.SessionId(id)

	return request, nil
}

// decodeAccountUsageMembers reads the request object member by member. Absent
// or null params are the empty object; anything that is not exactly one object
// is refused naming params, and a repeated member is refused by its name so a
// second copy can never hide the first from the checks that follow.
func decodeAccountUsageMembers(params json.RawMessage) (map[string]json.RawMessage, *acp.RequestError) {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return map[string]json.RawMessage{}, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if opening, err := decoder.Token(); err != nil || opening != json.Delim('{') {
		return nil, Unsupported(accountUsageFieldParams)
	}

	members := map[string]json.RawMessage{}

	for decoder.More() {
		token, err := decoder.Token()

		name, isName := token.(string)
		if err != nil || !isName {
			return nil, Unsupported(accountUsageFieldParams)
		}

		if _, repeated := members[name]; repeated {
			return nil, Unsupported(name)
		}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, Unsupported(accountUsageFieldParams)
		}

		members[name] = value
	}

	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return nil, Unsupported(accountUsageFieldParams)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, Unsupported(accountUsageFieldParams)
	}

	return members, nil
}

// AccountUsageLimit is one allowance window as the harness reports it.
type AccountUsageLimit struct {
	// ObservedAt is when the harness supplied this window.
	ObservedAt string `json:"observedAt"`
	// StaleAt is when this observation expires or was invalidated.
	StaleAt string `json:"staleAt"`
	// ID is unique within one response and stable across reads of the same
	// account.
	ID string `json:"id"`
	// Label is the harness's human name for the window, when it has one.
	Label string `json:"label,omitempty"`
	// WindowSeconds is the window length; zero means the harness did not say.
	WindowSeconds int64 `json:"windowSeconds,omitempty"`
	// UsedPercent is the harness's own utilization figure and may exceed 100.
	UsedPercent float64 `json:"usedPercent"`
	// ResetsAt is an RFC 3339 UTC instant with whole seconds, when known.
	ResetsAt string `json:"resetsAt,omitempty"`
}

// AccountUsageResponse is one _<vendor>/accountUsage result.
type AccountUsageResponse struct {
	Available bool `json:"available"`
	// Reason is present only when Available is false.
	Reason string `json:"reason,omitempty"`
	// Plan is the harness's plan or subscription name, when it reports one.
	Plan string `json:"plan,omitempty"`
	// UsageAllowed is the harness's own statement of whether the account may
	// run ordinary inference now; nil when it makes none. It is never derived
	// from the windows.
	UsageAllowed *bool `json:"usageAllowed,omitempty"`
	// Limits is non-empty when Available is true and absent otherwise.
	Limits []AccountUsageLimit `json:"limits,omitempty"`
}

// AccountUsageUnavailable answers a read that completed with nothing to report.
func AccountUsageUnavailable(reason string) AccountUsageResponse {
	return AccountUsageResponse{Reason: reason}
}

// AccountUsageTime renders one instant in the response's fixed form. An instant
// outside years 0001 through 9999 has no such form and renders empty, so a
// caller omits the member.
func AccountUsageTime(at time.Time) string {
	at = at.UTC().Truncate(time.Second)
	if year := at.Year(); year < 1 || year > 9999 {
		return ""
	}

	return at.Format(time.RFC3339)
}

// Validate reports the first way the response departs from the contract shape.
func (r AccountUsageResponse) Validate() error {
	if !r.Available {
		return r.validateUnavailable()
	}

	if r.Reason != "" {
		return errors.New("an available response carries no reason")
	}

	if r.Plan != strings.TrimSpace(r.Plan) {
		return errors.New("plan carries surrounding whitespace")
	}

	if len(r.Limits) == 0 {
		return errors.New("an available response carries at least one limit")
	}

	seen := make(map[string]struct{}, len(r.Limits))

	for index, limit := range r.Limits {
		if err := limit.validate(); err != nil {
			return fmt.Errorf("limits[%d]: %w", index, err)
		}

		if _, duplicate := seen[limit.ID]; duplicate {
			return fmt.Errorf("limits[%d]: id %q repeats", index, limit.ID)
		}

		seen[limit.ID] = struct{}{}
	}

	return nil
}

func (r AccountUsageResponse) validateUnavailable() error {
	switch r.Reason {
	case AccountUsageNotAuthenticated, AccountUsageNotReported:
	default:
		return fmt.Errorf("reason %q is not a contract token", r.Reason)
	}

	if r.Plan != "" || r.UsageAllowed != nil || len(r.Limits) != 0 {
		return errors.New("an unavailable response carries only its reason")
	}

	return nil
}

func (l AccountUsageLimit) validate() error {
	if err := validateAccountUsageTime(l.ObservedAt, "observedAt"); err != nil {
		return err
	}

	if err := validateAccountUsageTime(l.StaleAt, "staleAt"); err != nil {
		return err
	}

	if l.ID == "" || l.ID != strings.TrimSpace(l.ID) {
		return errors.New("id is empty or carries surrounding whitespace")
	}

	if l.Label != strings.TrimSpace(l.Label) {
		return errors.New("label carries surrounding whitespace")
	}

	if l.WindowSeconds < 0 {
		return errors.New("windowSeconds is negative")
	}

	if math.IsNaN(l.UsedPercent) || math.IsInf(l.UsedPercent, 0) || l.UsedPercent < 0 {
		return errors.New("usedPercent is not a finite non-negative number")
	}

	if l.ResetsAt == "" {
		return nil
	}

	return validateAccountUsageTime(l.ResetsAt, "resetsAt")
}

// validateAccountUsageTime accepts only the exact rendering AccountUsageTime
// produces: UTC, whole seconds, Z suffix.
func validateAccountUsageTime(value, field string) error {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || AccountUsageTime(parsed) != value {
		return fmt.Errorf("%s %q is not an RFC 3339 UTC instant with whole seconds", field, value)
	}

	return nil
}
