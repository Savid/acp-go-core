package wire

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestDecodeAccountUsageRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		params  string
		scope   AccountUsageScope
		session acp.SessionId
		meta    map[string]any
		code    int
		data    map[string]any
	}{
		{name: "session id", params: `{"sessionId":"s-1"}`, scope: AccountUsageScopeSession, session: "s-1"},
		{name: "agent scope without session", params: `{}`, scope: AccountUsageScopeAgent},
		{name: "agent scope absent params", params: ``, scope: AccountUsageScopeAgent},
		{name: "agent scope null params", params: `null`, scope: AccountUsageScopeAgent},
		{name: "agent scope with session", params: `{"sessionId":"s-2"}`, scope: AccountUsageScopeAgent, session: "s-2"},
		{name: "meta kept for propagation", params: `{"sessionId":"s-3","_meta":{"other":{"x":1},"traceparent":"00-1-2-01"}}`, scope: AccountUsageScopeSession, session: "s-3", meta: map[string]any{"other": map[string]any{"x": float64(1)}, "traceparent": "00-1-2-01"}},
		{name: "session scope missing", params: `{}`, scope: AccountUsageScopeSession, code: -32602, data: map[string]any{FieldError: VerdictMissing, FieldField: "sessionId"}},
		{name: "missing session keeps meta", params: `{"_meta":{"traceparent":"00-1-2-01"}}`, scope: AccountUsageScopeSession, meta: map[string]any{"traceparent": "00-1-2-01"}, code: -32602, data: map[string]any{FieldError: VerdictMissing, FieldField: "sessionId"}},
		{name: "unknown member keeps meta", params: `{"zeta":1,"_meta":{"traceparent":"00-1-2-01"}}`, scope: AccountUsageScopeAgent, meta: map[string]any{"traceparent": "00-1-2-01"}, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "zeta"}},
		{name: "bad session id keeps meta", params: `{"sessionId":"","_meta":{"traceparent":"00-1-2-01"}}`, scope: AccountUsageScopeAgent, meta: map[string]any{"traceparent": "00-1-2-01"}, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "sessionId"}},
		{name: "empty session id", params: `{"sessionId":""}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "sessionId"}},
		{name: "null session id", params: `{"sessionId":null}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "sessionId"}},
		{name: "numeric session id", params: `{"sessionId":7}`, scope: AccountUsageScopeSession, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "sessionId"}},
		{name: "unknown member", params: `{"sessionId":"s","accountId":"x"}`, scope: AccountUsageScopeSession, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "accountId"}},
		{name: "first unknown member by name", params: `{"zeta":1,"alpha":2}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "alpha"}},
		{name: "array params", params: `[]`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "params"}},
		{name: "trailing input", params: `{} {}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "params"}},
		{name: "malformed", params: `{"sessionId":`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "params"}},
		{name: "unclosed object", params: `{"sessionId":"s"`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "params"}},
		{name: "meta not an object", params: `{"_meta":1}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "_meta"}},
		{name: "unset scope requires a session", params: `{}`, scope: "", code: -32602, data: map[string]any{FieldError: VerdictMissing, FieldField: "sessionId"}},
		{name: "repeated session id", params: `{"sessionId":"a","sessionId":"b"}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "sessionId"}},
		{name: "repeated meta cannot hide the lifecycle key", params: `{"_meta":{"` + LifecycleKey + `":{"version":1}},"_meta":{}}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "_meta"}},
		{name: "non-string member name", params: `{1:2}`, scope: AccountUsageScopeAgent, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: "params"}},
		{name: "lifecycle key refused", params: `{"sessionId":"s","_meta":{"` + LifecycleKey + `":{"version":1}}}`, scope: AccountUsageScopeSession, meta: map[string]any{LifecycleKey: map[string]any{"version": float64(1)}}, code: -32602, data: map[string]any{FieldError: VerdictUnsupported, FieldField: `_meta["` + LifecycleKey + `"]`}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			request, refusal := DecodeAccountUsageRequest(json.RawMessage(tc.params), tc.scope)
			if tc.code == 0 {
				require.Nil(t, refusal)
				require.Equal(t, tc.session, request.SessionID)
				require.Equal(t, tc.meta, request.Meta)

				return
			}

			require.NotNil(t, refusal)
			require.Equal(t, tc.code, refusal.Code)
			require.Equal(t, tc.data, refusal.Data)
			require.Equal(t, tc.meta, request.Meta, "a refusal after the _meta decode still carries the trace keys")
		})
	}
}

func TestAccountUsageResponseValidate(t *testing.T) {
	t.Parallel()

	observed := AccountUsageTime(time.Date(2026, 9, 17, 2, 41, 3, 500, time.FixedZone("x", 3600)))
	require.Equal(t, "2026-09-17T01:41:03Z", observed)
	require.Equal(t, "9999-12-31T23:59:59Z", AccountUsageTime(time.Unix(253402300799, 0)))
	require.Empty(t, AccountUsageTime(time.Unix(253402300800, 0)), "a five-digit year has no fixed form")
	require.Empty(t, AccountUsageTime(time.Unix(-62135596801, 0)), "year zero has no fixed form")

	valid := AccountUsageResponse{Available: true, Plan: "pro", Limits: []AccountUsageLimit{
		{ObservedAt: observed, StaleAt: "2026-09-17T01:42:03Z", ID: "codex/primary", WindowSeconds: 604800, UsedPercent: 23, ResetsAt: "2026-09-21T12:02:18Z"},
		{ObservedAt: observed, StaleAt: "2026-09-17T01:42:03Z", ID: "codex_spark/primary", Label: "Spark", UsedPercent: 0},
	}}
	require.NoError(t, valid.Validate())
	require.NoError(t, AccountUsageUnavailable(AccountUsageNotAuthenticated).Validate())
	require.NoError(t, AccountUsageUnavailable(AccountUsageNotReported).Validate())

	mutate := func(change func(*AccountUsageResponse)) AccountUsageResponse {
		copied := valid
		copied.Limits = append([]AccountUsageLimit(nil), valid.Limits...)
		change(&copied)

		return copied
	}

	cases := map[string]AccountUsageResponse{
		"unavailable without reason":     {},
		"unavailable unknown reason":     AccountUsageUnavailable("read_failed"),
		"unavailable with limits":        {Reason: AccountUsageNotReported, Limits: valid.Limits},
		"unavailable with plan":          {Reason: AccountUsageNotReported, Plan: "pro"},
		"unavailable with usage allowed": {Reason: AccountUsageNotReported, UsageAllowed: new(false)},
		"available with reason":          mutate(func(r *AccountUsageResponse) { r.Reason = AccountUsageNotReported }),
		"window without observed at":     mutate(func(r *AccountUsageResponse) { r.Limits[0].ObservedAt = "" }),
		"observed at with offset":        mutate(func(r *AccountUsageResponse) { r.Limits[0].ObservedAt = "2026-09-17T03:41:03+02:00" }),
		"observed at with fraction":      mutate(func(r *AccountUsageResponse) { r.Limits[0].ObservedAt = "2026-09-17T01:41:03.5Z" }),
		"window without expiry":          mutate(func(r *AccountUsageResponse) { r.Limits[0].StaleAt = "" }),
		"available without limits":       mutate(func(r *AccountUsageResponse) { r.Limits = nil }),
		"plan with whitespace":           mutate(func(r *AccountUsageResponse) { r.Plan = " pro" }),
		"empty limit id":                 mutate(func(r *AccountUsageResponse) { r.Limits[0].ID = "" }),
		"limit id with whitespace":       mutate(func(r *AccountUsageResponse) { r.Limits[0].ID = "a " }),
		"duplicate limit id":             mutate(func(r *AccountUsageResponse) { r.Limits[1].ID = r.Limits[0].ID }),
		"label with whitespace":          mutate(func(r *AccountUsageResponse) { r.Limits[1].Label = "Spark " }),
		"negative window":                mutate(func(r *AccountUsageResponse) { r.Limits[0].WindowSeconds = -1 }),
		"negative percent":               mutate(func(r *AccountUsageResponse) { r.Limits[0].UsedPercent = -0.5 }),
		"nan percent":                    mutate(func(r *AccountUsageResponse) { r.Limits[0].UsedPercent = math.NaN() }),
		"infinite percent":               mutate(func(r *AccountUsageResponse) { r.Limits[0].UsedPercent = math.Inf(1) }),
		"resets at not rfc3339":          mutate(func(r *AccountUsageResponse) { r.Limits[0].ResetsAt = "1789960938" }),
		"resets at with offset":          mutate(func(r *AccountUsageResponse) { r.Limits[0].ResetsAt = "2026-09-21T14:02:18+02:00" }),
		"resets at with fractional part": mutate(func(r *AccountUsageResponse) { r.Limits[0].ResetsAt = "2026-09-21T12:02:18.1Z" }),
	}

	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, response.Validate())
		})
	}

	above := mutate(func(r *AccountUsageResponse) { r.Limits[0].UsedPercent = 130 })
	require.NoError(t, above.Validate(), "utilization above 100 is the harness's own figure")
}

func TestAccountUsageResponseWire(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(AccountUsageUnavailable(AccountUsageNotAuthenticated))
	require.NoError(t, err)
	require.JSONEq(t, `{"available":false,"reason":"not_authenticated"}`, string(encoded))

	encoded, err = json.Marshal(AccountUsageResponse{Available: true, Limits: []AccountUsageLimit{{ObservedAt: "2026-09-17T01:41:03Z", StaleAt: "2026-09-17T01:42:03Z", ID: "session", UsedPercent: 4}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"available":true,"limits":[{"observedAt":"2026-09-17T01:41:03Z","staleAt":"2026-09-17T01:42:03Z","id":"session","usedPercent":4}]}`, string(encoded))

	encoded, err = json.Marshal(AccountUsageResponse{Available: true, Plan: "pro", Limits: []AccountUsageLimit{{ObservedAt: "2026-09-17T01:41:03Z", StaleAt: "2026-09-17T01:42:03Z", ID: "weekly", UsedPercent: 0}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"available":true,"plan":"pro","limits":[{"observedAt":"2026-09-17T01:41:03Z","staleAt":"2026-09-17T01:42:03Z","id":"weekly","usedPercent":0}]}`, string(encoded), "a zero utilization is carried and an unstated usageAllowed is omitted")

	encoded, err = json.Marshal(AccountUsageResponse{Available: true, UsageAllowed: new(false), Limits: []AccountUsageLimit{{ObservedAt: "2026-09-17T01:41:03Z", StaleAt: "2026-09-17T01:42:03Z", ID: "weekly", UsedPercent: 100}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"available":true,"usageAllowed":false,"limits":[{"observedAt":"2026-09-17T01:41:03Z","staleAt":"2026-09-17T01:42:03Z","id":"weekly","usedPercent":100}]}`, string(encoded), "a stated false is carried, not omitted")

	require.Equal(t, map[string]any{"method": "_x/accountUsage", "scope": "session"}, AccountUsageAdvertisement("_x/accountUsage", AccountUsageScopeSession))
	require.Equal(t, map[string]any{"method": "_x/accountUsage", "scope": "agent"}, AccountUsageAdvertisement("_x/accountUsage", AccountUsageScopeAgent))
}

func TestAccountUsageAmounts(t *testing.T) {
	t.Parallel()
	balance := AccountUsageBalance{ID: "key", ObservedAt: "2026-09-18T00:00:00Z", StaleAt: "2026-09-18T00:01:00Z", Remaining: &AccountUsageMoney{Amount: -0.001, Currency: "USD"}}
	response := AccountUsageResponse{Available: true, Balances: []AccountUsageBalance{balance}}
	require.NoError(t, response.Validate())
	for name, change := range map[string]func(*AccountUsageBalance){
		"mixed currency":          func(b *AccountUsageBalance) { b.Limit = &AccountUsageMoney{Amount: 2, Currency: "EUR"} },
		"invalid currency":        func(b *AccountUsageBalance) { b.Remaining = &AccountUsageMoney{Amount: 0, Currency: "usd"} },
		"nonfinite amount":        func(b *AccountUsageBalance) { b.Used = &AccountUsageMoney{Amount: math.NaN(), Currency: "USD"} },
		"uncapped with remaining": func(b *AccountUsageBalance) { b.Uncapped = true },
		"missing observation":     func(b *AccountUsageBalance) { b.ObservedAt = "" },
		"unknown interval":        func(b *AccountUsageBalance) { b.ResetInterval = "hourly" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := balance
			change(&b)
			require.Error(t, (AccountUsageResponse{Available: true, Balances: []AccountUsageBalance{b}}).Validate())
		})
	}
	response.RequestLimits = []AccountUsageRequestLimit{{ID: "key", ObservedAt: balance.ObservedAt, StaleAt: balance.StaleAt, Used: 1, Limit: 2, Remaining: 1}}
	require.Error(t, response.Validate(), "IDs are unique across measurement types")
	response.RequestLimits[0].ID = "requests"
	require.NoError(t, response.Validate())
	response.RequestLimits[0].Used = -1
	require.Error(t, response.Validate())
}

func TestAccountUsageProviderSelection(t *testing.T) {
	t.Parallel()
	request, refusal := DecodeAccountUsageRequest(json.RawMessage(`{"sessionId":"s","providerId":"openrouter"}`), AccountUsageScopeSession)
	require.Nil(t, refusal)
	require.Equal(t, "openrouter", request.ProviderID)
	for _, raw := range []string{`null`, `""`, `" openrouter"`, `5`} {
		_, refusal = DecodeAccountUsageRequest(json.RawMessage(`{"sessionId":"s","providerId":`+raw+`}`), AccountUsageScopeSession)
		require.NotNil(t, refusal)
		require.Equal(t, map[string]any{FieldError: VerdictUnsupported, FieldField: "providerId"}, refusal.Data)
	}
	advertisement := AccountUsageAdvertisement("_test/accountUsage", AccountUsageScopeSession, "openrouter")
	require.Equal(t, []string{"openrouter"}, advertisement["providers"])
}

func TestStructuredOutputAdvertisement(t *testing.T) {
	t.Parallel()

	require.Equal(t, map[string]any{
		"config": "_meta.v.options.outputSchema",
		"result": "_meta.v.structuredOutput",
		"schema": "json_schema",
	}, StructuredOutputAdvertisement("v"))
}
