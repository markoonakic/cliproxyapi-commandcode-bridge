// Package quota implements the CLIProxyAPI QuotaProvider capability for
// Command Code, plus the account-health model the dashboard and scheduler use.
package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/identity"
)

// ProviderID is the stable provider key, shared with every other capability.
const ProviderID = identity.ProviderKey

// DisplayName is the user-facing label for the Management API.
const DisplayName = identity.DisplayName

// credential mirrors the persisted Command Code auth file. The identity fields
// are optional additions, so the pre-existing seven-key file still parses.
type credential struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key"`
	Label        string `json:"label,omitempty"`
	Plan         string `json:"plan,omitempty"`
	Priority     int    `json:"priority,omitempty"`
	PriorityOver int    `json:"priority_override,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	Email        string `json:"email,omitempty"`
	UserName     string `json:"user_name,omitempty"`
	SubID        string `json:"subscription_id,omitempty"`
	Models       []struct {
		Name  string `json:"name"`
		Alias string `json:"alias,omitempty"`
	} `json:"models,omitempty"`
}

// Provider implements pluginapi.QuotaProvider.
type Provider struct {
	cache *Cache
}

// NewProvider builds a quota provider with an in-memory cache.
func NewProvider() *Provider {
	return &Provider{cache: NewCache()}
}

// Identifier returns the provider key handled by this quota provider.
func (p *Provider) Identifier() string { return ProviderID }

// DescribeQuota reports capability metadata without network access. The host
// calls this while scanning auth files, so it must stay cheap.
func (p *Provider) DescribeQuota(_ context.Context, _ pluginapi.QuotaDescribeRequest) (pluginapi.QuotaDescribeResponse, error) {
	return pluginapi.QuotaDescribeResponse{
		SupportedProviders: []string{ProviderID},
		DisplayName:        DisplayName,
		// Command Code exposes no reset endpoint for credits or windows.
		SupportsReset: false,
	}, nil
}

// FetchQuota retrieves and normalizes quota for one credential.
//
// The SDK's HTTPClient field is json:"-" and never crosses the ABI, so upstream
// calls are issued through the host.http.do callback using the host callback id
// carried on the request envelope.
func (p *Provider) FetchQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
	cred, err := parseCredential(req.StorageJSON)
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}

	key := cacheKey(req.AuthIndex, req.AuthID, cred.APIKey)
	if cached, fresh := p.cache.Get(key); fresh {
		return cached, nil
	}

	// The credential may override the API base URL; otherwise use the public API.
	client := NewClient("")
	credits, headers, errCredits := client.Credits(ctx, cred.APIKey)
	if errCredits != nil {
		p.cache.PutError(key)
		return pluginapi.QuotaFetchResponse{}, errCredits
	}

	sub, errSub := client.Subscription(ctx, cred.APIKey)
	if errSub != nil {
		// A missing subscription is not fatal: windows and credits are the
		// primary payload. Fall back to the plan recorded on the credential.
		sub = Subscription{PlanID: cred.Plan}
	}

	resp, errMap := MapQuota(credits, sub, headers, time.Now())
	if errMap != nil {
		p.cache.PutError(key)
		return pluginapi.QuotaFetchResponse{}, errMap
	}
	p.cache.Put(key, resp)
	// Record exhaustion so the scheduler can skip this account until reset.
	p.cache.SetExhausted(req.AuthID, exhaustionUntil(credits, time.Now()))
	return resp, nil
}

// exhaustionUntil returns the earliest reset time among windows that are used
// up, or the zero time when the account still has quota.
//
// The host learns about exhaustion only after upstream returns a rate-limit
// error; recording it here lets the scheduler avoid the account proactively.
func exhaustionUntil(credits CreditsResponse, now time.Time) time.Time {
	if credits.WindowLimits.Exceeded != nil && !*credits.WindowLimits.Exceeded {
		return time.Time{}
	}
	var earliest time.Time
	for _, window := range []*WindowLimit{credits.WindowLimits.FiveHour, credits.WindowLimits.Weekly} {
		if window == nil || !window.Exceeded {
			continue
		}
		reset := time.UnixMilli(window.ResetAt).UTC()
		if window.ResetAt <= 0 || reset.Year() < 2000 || reset.Year() > 2200 {
			continue
		}
		if reset.Before(now) {
			continue
		}
		if earliest.IsZero() || reset.Before(earliest) {
			earliest = reset
		}
	}
	return earliest
}

// Exhausted reports whether the credential's cached quota is used up.
// It satisfies the scheduler's availability contract.
func (p *Provider) Exhausted(authID string) bool {
	if p == nil {
		return false
	}
	return p.cache.Exhausted(authID)
}

// ResetQuota always reports failure. Returning success would make the host
// clear local cooldown state even though nothing was reset upstream.
func (p *Provider) ResetQuota(_ context.Context, _ pluginapi.QuotaResetRequest) (pluginapi.QuotaResetResponse, error) {
	return pluginapi.QuotaResetResponse{
		Success: false,
		Message: "Command Code does not support quota reset",
	}, nil
}

// MapQuota converts a verified Command Code payload into the host's normalized
// quota shape. It is fail-closed: a window is emitted only when its cap and used
// values are usable, so the UI never shows a fabricated percentage.
func MapQuota(credits CreditsResponse, sub Subscription, headers http.Header, now time.Time) (pluginapi.QuotaFetchResponse, error) {
	out := pluginapi.QuotaFetchResponse{}

	plan := sub.PlanID
	if plan == "" {
		plan = "unknown"
	}
	subscription := pluginapi.QuotaSubscription{Plan: plan, TierID: sub.PlanID}
	out.Subscription = &subscription

	// Command Code credits are USD-equivalent: the live usage summary reported
	// totalCost == totalCredits for the same period.
	out.Summary = append(out.Summary, pluginapi.QuotaMetric{
		Key:      "credits_remaining",
		Label:    "Monthly credits remaining",
		Value:    credits.Credits.MonthlyCredits,
		Format:   "currency",
		Currency: "USD",
	})
	if credits.Credits.PurchasedCredits > 0 {
		out.Summary = append(out.Summary, pluginapi.QuotaMetric{
			Key:      "purchased_credits_remaining",
			Label:    "Purchased credits remaining",
			Value:    credits.Credits.PurchasedCredits,
			Format:   "currency",
			Currency: "USD",
		})
	}
	if credits.Credits.FreeCredits > 0 {
		out.Summary = append(out.Summary, pluginapi.QuotaMetric{
			Key:      "free_credits_remaining",
			Label:    "Free credits remaining",
			Value:    credits.Credits.FreeCredits,
			Format:   "currency",
			Currency: "USD",
		})
	}

	// QuotaFetchResponse has no top-level bucket field, so buckets must be
	// nested inside a group.
	buckets := make([]pluginapi.QuotaBucket, 0, 2)
	if bucket, okBucket := mapBucket("5h", "Five hour limit", credits.WindowLimits.FiveHour); okBucket {
		buckets = append(buckets, bucket)
	}
	if bucket, okBucket := mapBucket("weekly", "Weekly limit", credits.WindowLimits.Weekly); okBucket {
		buckets = append(buckets, bucket)
	}
	if len(buckets) > 0 {
		out.Groups = append(out.Groups, pluginapi.QuotaGroup{
			DisplayName: "Command Code limits",
			Buckets:     buckets,
		})
	}

	if offset, okOffset := serverTimeOffset(headers, now); okOffset {
		out.ServerTimeOffsetMs = offset
	}

	if len(buckets) == 0 && len(out.Summary) == 0 {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("command code returned no usable quota data")
	}
	return out, nil
}

func mapBucket(window, label string, limit *WindowLimit) (pluginapi.QuotaBucket, bool) {
	fraction, ok := RemainingFraction(limit)
	if !ok {
		return pluginapi.QuotaBucket{}, false
	}
	description := fmt.Sprintf("%s: $%.2f of $%.2f used", label, limit.Used, limit.Cap)
	if limit.Exceeded {
		description += " (exceeded)"
	}
	return pluginapi.QuotaBucket{
		Window:            window,
		RemainingFraction: fraction,
		ResetTime:         ResetTime(limit.ResetAt),
		Description:       description,
	}, true
}

// serverTimeOffset compares the upstream Date header with local time.
func serverTimeOffset(headers http.Header, now time.Time) (int64, bool) {
	if headers == nil {
		return 0, false
	}
	raw := headers.Get("Date")
	if raw == "" {
		return 0, false
	}
	parsed, err := http.ParseTime(raw)
	if err != nil {
		return 0, false
	}
	return parsed.Sub(now).Milliseconds(), true
}

func parseCredential(raw []byte) (credential, error) {
	if len(raw) == 0 {
		return credential{}, fmt.Errorf("credential payload is empty")
	}
	var cred credential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return credential{}, fmt.Errorf("parse credential: %w", err)
	}
	if !strings.HasPrefix(cred.APIKey, "user_") || len(strings.TrimPrefix(cred.APIKey, "user_")) == 0 {
		return credential{}, fmt.Errorf("Command Code API key must start with user_ and have a body")
	}
	return cred, nil
}

// Cache is a small keyed cache with separate success and error TTLs.
//
// It also tracks per-credential exhaustion so the scheduler can skip an account
// whose quota window is used up before upstream has to reject a request.
type Cache struct {
	mu        sync.Mutex
	entries   map[string]cacheEntry
	exhausted map[string]time.Time
	now       func() time.Time
}

type cacheEntry struct {
	value     pluginapi.QuotaFetchResponse
	failed    bool
	expiresAt time.Time
}

// NewCache builds an empty cache.
func NewCache() *Cache {
	return &Cache{
		entries:   make(map[string]cacheEntry),
		exhausted: make(map[string]time.Time),
		now:       time.Now,
	}
}

// SetExhausted records that a credential's quota is used up until the given
// reset time. A zero reset time clears the mark, because the window has no
// known reset and the account must not stay blocked forever.
func (c *Cache) SetExhausted(authID string, until time.Time) {
	if c == nil || authID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if until.IsZero() || c.now().After(until) {
		delete(c.exhausted, authID)
		return
	}
	c.exhausted[authID] = until
}

// Exhausted reports whether the credential's quota is currently used up.
// An unknown credential is not exhausted, so missing data never blocks a pick.
func (c *Cache) Exhausted(authID string) bool {
	if c == nil || authID == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.exhausted[authID]
	if !ok {
		return false
	}
	if c.now().After(until) {
		// The window has reset, so the account is eligible again with no
		// network call and no background timer.
		delete(c.exhausted, authID)
		return false
	}
	return true
}

// Get returns a cached success value if it is still fresh.
func (c *Cache) Get(key string) (pluginapi.QuotaFetchResponse, bool) {
	if c == nil {
		return pluginapi.QuotaFetchResponse{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return pluginapi.QuotaFetchResponse{}, false
	}
	if c.now().After(entry.expiresAt) {
		delete(c.entries, key)
		return pluginapi.QuotaFetchResponse{}, false
	}
	if entry.failed {
		return pluginapi.QuotaFetchResponse{}, false
	}
	return entry.value, true
}

// Failed reports whether the key is in a fresh negative-cache state.
func (c *Cache) Failed(key string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	return ok && entry.failed && c.now().Before(entry.expiresAt)
}

// Put stores a successful result.
func (c *Cache) Put(key string, value pluginapi.QuotaFetchResponse) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{value: value, expiresAt: c.now().Add(successTTL)}
}

// PutError records a short-lived negative entry so a failing upstream is not hammered.
func (c *Cache) PutError(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = cacheEntry{failed: true, expiresAt: c.now().Add(errorTTL)}
}

const (
	// successTTL follows the click-to-refresh cadence of the native quota UI.
	successTTL = 60 * time.Second
	// errorTTL is a short negative cache.
	errorTTL = 15 * time.Second
)

func cacheKey(authIndex, authID, apiKey string) string {
	return authIndex + "\x00" + authID + "\x00" + apiKey
}
