package quota

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func jsonUnmarshal(raw string, out any) error {
	return json.Unmarshal([]byte(raw), out)
}

func quotaResponseStub() pluginapi.QuotaFetchResponse {
	return pluginapi.QuotaFetchResponse{Subscription: &pluginapi.QuotaSubscription{Plan: "individual-goat"}}
}

// liveCredits is the payload captured from a live Command Code account on the
// "goat" plan. It is the authoritative fixture for the quota mapping.
const liveCredits = `{
  "credits": {
    "belowThreshold": false,
    "creditThreshold": 0,
    "monthlyCredits": 62.474168526,
    "purchasedCredits": 0,
    "freeCredits": 0
  },
  "windowLimits": {
    "limited": true,
    "exceeded": null,
    "fiveHour": {"used": 1.69041425, "cap": 14, "exceeded": false, "resetAt": 1789821304634},
    "weekly": {"used": 7.525831474, "cap": 35, "exceeded": false, "resetAt": 1790328928004}
  }
}`

// liveExceededCredits is the payload captured from a live Command Code account
// while its weekly window was exceeded. Upstream changed windowLimits.exceeded
// from null to the name of the exceeded window, which a strict bool could not
// decode, so this fixture pins the shape that broke every quota read.
const liveExceededCredits = `{
  "credits": {
    "belowThreshold": false,
    "creditThreshold": 0,
    "monthlyCredits": 34.9994455693,
    "purchasedCredits": 0,
    "freeCredits": 0
  },
  "windowLimits": {
    "limited": true,
    "exceeded": "weekly",
    "fiveHour": {"used": 0.691363631, "cap": 14, "exceeded": false, "resetAt": 1790203351870},
    "weekly": {"used": 35.0005544307, "cap": 35, "exceeded": true, "resetAt": 1790328928004}
  }
}`

const liveSubscription = `{"success":true,"data":{"planId":"individual-goat","status":"active","currentPeriodStart":"2026-09-18T06:45:49.000Z","currentPeriodEnd":"2026-10-18T06:45:49.000Z","cancelAtPeriodEnd":false}}`

func decodeLive(t *testing.T) (CreditsResponse, Subscription) {
	t.Helper()
	var credits CreditsResponse
	if err := jsonUnmarshal(liveCredits, &credits); err != nil {
		t.Fatalf("decode credits fixture: %v", err)
	}
	var sub subscriptionsResponse
	if err := jsonUnmarshal(liveSubscription, &sub); err != nil {
		t.Fatalf("decode subscription fixture: %v", err)
	}
	return credits, sub.Data
}

func TestMapQuotaLivePayload(t *testing.T) {
	credits, sub := decodeLive(t)
	resp, err := MapQuota(credits, sub, nil, time.Now())
	if err != nil {
		t.Fatalf("MapQuota returned error: %v", err)
	}

	if resp.Subscription == nil || resp.Subscription.Plan != "individual-goat" {
		t.Fatalf("subscription plan = %+v, want individual-goat", resp.Subscription)
	}

	if len(resp.Groups) != 1 {
		t.Fatalf("groups = %d, want 1 (buckets must be nested in a group)", len(resp.Groups))
	}
	buckets := resp.Groups[0].Buckets
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(buckets))
	}

	byWindow := map[string]float64{}
	reset := map[string]string{}
	for _, b := range buckets {
		byWindow[b.Window] = b.RemainingFraction
		reset[b.Window] = b.ResetTime
	}

	const wantFiveHour = 0.879256125
	const wantWeekly = 0.7849762436
	if math.Abs(byWindow["5h"]-wantFiveHour) > 1e-6 {
		t.Errorf("5h remainingFraction = %v, want %v", byWindow["5h"], wantFiveHour)
	}
	if math.Abs(byWindow["weekly"]-wantWeekly) > 1e-6 {
		t.Errorf("weekly remainingFraction = %v, want %v", byWindow["weekly"], wantWeekly)
	}

	if reset["5h"] != "2026-09-19T12:35:04Z" {
		t.Errorf("5h resetTime = %q, want 2026-09-19T12:35:04Z", reset["5h"])
	}
	if reset["weekly"] != "2026-09-25T09:35:28Z" {
		t.Errorf("weekly resetTime = %q, want 2026-09-25T09:35:28Z", reset["weekly"])
	}

	if len(resp.Summary) == 0 {
		t.Fatal("expected at least one summary metric")
	}
	metric := resp.Summary[0]
	if metric.Key != "credits_remaining" || metric.Format != "currency" || metric.Currency != "USD" {
		t.Errorf("summary metric = %+v, want credits_remaining/USD", metric)
	}
	if math.Abs(metric.Value-62.474168526) > 1e-9 {
		t.Errorf("credits value = %v, want 62.474168526", metric.Value)
	}
}

// TestExceededFieldAcceptsEveryUpstreamShape pins the field that broke quota
// reads: null, a boolean, and the name of the exceeded window must all decode.
func TestExceededFieldAcceptsEveryUpstreamShape(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"null", `{"windowLimits":{"exceeded":null}}`, false},
		{"absent", `{"windowLimits":{}}`, false},
		{"false", `{"windowLimits":{"exceeded":false}}`, true},
		{"true", `{"windowLimits":{"exceeded":true}}`, false},
		{"window name", `{"windowLimits":{"exceeded":"weekly"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var payload struct {
				WindowLimits WindowLimits `json:"windowLimits"`
			}
			if err := jsonUnmarshal(tc.raw, &payload); err != nil {
				t.Fatalf("decode %s: %v", tc.raw, err)
			}
			if got := payload.WindowLimits.ExceededIsFalse(); got != tc.want {
				t.Errorf("ExceededIsFalse() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMapQuotaLiveExceededPayload verifies the exceeded payload still produces
// both bars, with the exceeded window clamped to zero remaining.
func TestMapQuotaLiveExceededPayload(t *testing.T) {
	var credits CreditsResponse
	if err := jsonUnmarshal(liveExceededCredits, &credits); err != nil {
		t.Fatalf("decode exceeded fixture: %v", err)
	}
	resp, err := MapQuota(credits, Subscription{PlanID: "individual-goat"}, nil, time.Now())
	if err != nil {
		t.Fatalf("MapQuota returned error: %v", err)
	}
	if len(resp.Groups) != 1 || len(resp.Groups[0].Buckets) != 2 {
		t.Fatalf("groups = %+v, want one group with two buckets", resp.Groups)
	}
	byWindow := map[string]pluginapi.QuotaBucket{}
	for _, bucket := range resp.Groups[0].Buckets {
		byWindow[bucket.Window] = bucket
	}
	if got := byWindow["weekly"].RemainingFraction; got != 0 {
		t.Errorf("weekly remainingFraction = %v, want 0 (usage over the cap is clamped)", got)
	}
	if got := byWindow["5h"].RemainingFraction; math.Abs(got-0.9506169) > 1e-6 {
		t.Errorf("5h remainingFraction = %v, want 0.9506169", got)
	}
	if !strings.Contains(byWindow["weekly"].Description, "exceeded") {
		t.Errorf("weekly description = %q, want it marked exceeded", byWindow["weekly"].Description)
	}
}

// TestMapQuotaOmitsZeroCreditMetrics verifies that absent credit balances are
// not rendered as $0.00 rows.
func TestMapQuotaOmitsZeroCreditMetrics(t *testing.T) {
	credits, sub := decodeLive(t)
	resp, err := MapQuota(credits, sub, nil, time.Now())
	if err != nil {
		t.Fatalf("MapQuota returned error: %v", err)
	}
	for _, metric := range resp.Summary {
		if metric.Key == "purchased_credits_remaining" || metric.Key == "free_credits_remaining" {
			t.Errorf("zero-valued credit metric %q should be omitted", metric.Key)
		}
	}
}

// TestMapQuotaFailsClosedOnBadCaps verifies that an untrustworthy window never
// produces a percentage.
func TestMapQuotaFailsClosedOnBadCaps(t *testing.T) {
	cases := []struct {
		name      string
		fiveHour  *WindowLimit
		wantNoFix bool
	}{
		{"zero cap", &WindowLimit{Used: 1, Cap: 0, ResetAt: 1789821304634}, true},
		{"negative cap", &WindowLimit{Used: 1, Cap: -5, ResetAt: 1789821304634}, true},
		{"nan cap", &WindowLimit{Used: 1, Cap: math.NaN(), ResetAt: 1789821304634}, true},
		{"negative used", &WindowLimit{Used: -1, Cap: 14, ResetAt: 1789821304634}, true},
		{"missing window", nil, true},
		{"valid", &WindowLimit{Used: 7, Cap: 14, ResetAt: 1789821304634}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			credits := CreditsResponse{
				Credits:      Credits{MonthlyCredits: 5},
				WindowLimits: WindowLimits{FiveHour: tc.fiveHour},
			}
			resp, err := MapQuota(credits, Subscription{PlanID: "individual-goat"}, nil, time.Now())
			if err != nil {
				t.Fatalf("MapQuota error: %v", err)
			}
			hasBucket := len(resp.Groups) > 0 && len(resp.Groups[0].Buckets) > 0
			if tc.wantNoFix && hasBucket {
				t.Errorf("expected no bucket for untrustworthy window, got %+v", resp.Groups[0].Buckets)
			}
			if !tc.wantNoFix {
				if !hasBucket {
					t.Fatal("expected a bucket for a valid window")
				}
				got := resp.Groups[0].Buckets[0].RemainingFraction
				if math.Abs(got-0.5) > 1e-9 {
					t.Errorf("remainingFraction = %v, want 0.5", got)
				}
			}
		})
	}
}

// TestRemainingFractionClamps verifies clamping and rejects unusable inputs.
func TestRemainingFractionClamps(t *testing.T) {
	if _, ok := RemainingFraction(nil); ok {
		t.Error("nil window should not be usable")
	}
	if _, ok := RemainingFraction(&WindowLimit{Used: 1, Cap: 0}); ok {
		t.Error("zero cap should not be usable")
	}
	if got, ok := RemainingFraction(&WindowLimit{Used: 20, Cap: 10}); !ok || got != 0 {
		t.Errorf("over-cap remaining = %v (ok=%v), want 0", got, ok)
	}
	if got, ok := RemainingFraction(&WindowLimit{Used: -1, Cap: 10}); ok {
		t.Errorf("negative used should be unusable, got %v (ok=%v)", got, ok)
	}
}

// TestResetTimeRejectsImplausibleValues verifies epoch-ms conversion guards.
func TestResetTimeRejectsImplausibleValues(t *testing.T) {
	if got := ResetTime(0); got != "" {
		t.Errorf("ResetTime(0) = %q, want empty", got)
	}
	if got := ResetTime(-1); got != "" {
		t.Errorf("ResetTime(-1) = %q, want empty", got)
	}
	// A value far outside a plausible range must not render.
	if got := ResetTime(1); got != "" {
		t.Errorf("ResetTime(1) = %q, want empty", got)
	}
	if got := ResetTime(1789821304634); got != "2026-09-19T12:35:04Z" {
		t.Errorf("ResetTime(live) = %q", got)
	}
}

// TestParseCredentialAcceptsPreexistingShape verifies backward compatibility
// with the credential file already deployed, so the account survives switchover.
func TestParseCredentialAcceptsPreexistingShape(t *testing.T) {
	deployed := `{"type":"commandcode-bridge","api_key":"user_examplekey","label":"1","plan":"goat","priority":6,"models":[{"name":"deepseek/deepseek-v4.1-flash","alias":""}]}`
	cred, err := parseCredential([]byte(deployed))
	if err != nil {
		t.Fatalf("deployed credential shape rejected: %v", err)
	}
	if cred.Plan != "goat" || cred.Priority != 6 {
		t.Errorf("credential = %+v, want plan goat priority 6", cred)
	}
	if len(cred.Models) != 1 || cred.Models[0].Name != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("models = %+v", cred.Models)
	}
}

func TestParseCredentialRejectsBadKeys(t *testing.T) {
	for _, raw := range []string{
		`{}`,
		`{"api_key":""}`,
		`{"api_key":"sk-abc"}`,
		`{"api_key":"user_"}`,
		`not json`,
	} {
		if _, err := parseCredential([]byte(raw)); err == nil {
			t.Errorf("expected rejection for %q", raw)
		}
	}
}

func TestClassifyUpstreamStatuses(t *testing.T) {
	cases := map[int]struct {
		code      string
		retryable bool
	}{
		http.StatusUnauthorized:        {"invalid_credentials", false},
		http.StatusForbidden:           {"upstream_forbidden", false},
		http.StatusTooManyRequests:     {"upstream_rate_limited", true},
		http.StatusInternalServerError: {"upstream_error", true},
		http.StatusBadRequest:          {"upstream_rejected", false},
	}
	for status, want := range cases {
		got := classify(status, []byte("body"))
		if got.Code != want.code || got.Retryable != want.retryable {
			t.Errorf("classify(%d) = %+v, want %s retryable=%v", status, got, want.code, want.retryable)
		}
	}
}

func TestCacheSeparatesSuccessAndError(t *testing.T) {
	cache := NewCache()
	cache.Put("k", quotaResponseStub())
	if _, fresh := cache.Get("k"); !fresh {
		t.Fatal("expected a fresh success entry")
	}
	cache.PutError("e")
	if cache.Failed("e") != true {
		t.Error("expected a fresh negative entry")
	}
	if _, fresh := cache.Get("e"); fresh {
		t.Error("a failed entry must not be served as a value")
	}
}

// TestExhaustionUntil verifies the scheduler's quota signal is derived from the
// windows the host cannot see before upstream rejects a request.
func TestExhaustionUntil(t *testing.T) {
	now := time.UnixMilli(1789820000000).UTC()
	future := now.Add(2 * time.Hour).UnixMilli()
	past := now.Add(-2 * time.Hour).UnixMilli()

	cases := []struct {
		name    string
		credits CreditsResponse
		want    bool
	}{
		{"not exceeded", CreditsResponse{WindowLimits: WindowLimits{
			FiveHour: &WindowLimit{Used: 1, Cap: 14, Exceeded: false, ResetAt: future},
		}}, false},
		{"five hour exceeded", CreditsResponse{WindowLimits: WindowLimits{
			FiveHour: &WindowLimit{Used: 14, Cap: 14, Exceeded: true, ResetAt: future},
		}}, true},
		{"weekly exceeded", CreditsResponse{WindowLimits: WindowLimits{
			Weekly: &WindowLimit{Used: 35, Cap: 35, Exceeded: true, ResetAt: future},
		}}, true},
		{"exceeded but already reset", CreditsResponse{WindowLimits: WindowLimits{
			FiveHour: &WindowLimit{Used: 14, Cap: 14, Exceeded: true, ResetAt: past},
		}}, false},
		{"exceeded without a reset time", CreditsResponse{WindowLimits: WindowLimits{
			FiveHour: &WindowLimit{Used: 14, Cap: 14, Exceeded: true, ResetAt: 0},
		}}, false},
		{"explicitly not limited", CreditsResponse{WindowLimits: WindowLimits{
			Exceeded: json.RawMessage("false"),
			FiveHour: &WindowLimit{Used: 14, Cap: 14, Exceeded: true, ResetAt: future},
		}}, false},
		{"weekly named by the exceeded field", CreditsResponse{WindowLimits: WindowLimits{
			Exceeded: json.RawMessage(`"weekly"`),
			Weekly:   &WindowLimit{Used: 35, Cap: 35, Exceeded: true, ResetAt: future},
		}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := exhaustionUntil(tc.credits, now)
			if tc.want && got.IsZero() {
				t.Error("expected an exhaustion time")
			}
			if !tc.want && !got.IsZero() {
				t.Errorf("expected no exhaustion, got %v", got)
			}
		})
	}
}

// TestCacheTracksExhaustionPerCredential verifies the scheduler reads per-account
// state and that a passed reset window clears itself with no network call.
func TestCacheTracksExhaustionPerCredential(t *testing.T) {
	cache := NewCache()
	if cache.Exhausted("a") {
		t.Error("unknown credential must not be reported as exhausted")
	}

	cache.SetExhausted("a", time.Now().Add(time.Hour))
	if !cache.Exhausted("a") {
		t.Error("expected a to be exhausted")
	}
	if cache.Exhausted("b") {
		t.Error("exhaustion must be per credential, not global")
	}

	// A window that has already reset clears itself.
	cache.SetExhausted("c", time.Now().Add(-time.Minute))
	if cache.Exhausted("c") {
		t.Error("an elapsed reset must clear exhaustion")
	}

	// A zero reset clears the mark rather than blocking forever.
	cache.SetExhausted("a", time.Time{})
	if cache.Exhausted("a") {
		t.Error("a zero reset must clear exhaustion")
	}
}
