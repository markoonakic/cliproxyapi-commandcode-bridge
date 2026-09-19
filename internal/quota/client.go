package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/host"
)

// Command Code endpoints. The alpha routes are undocumented; their existence
// and response shapes were verified live against a "goat" plan account.
// Parsing is fail-closed: on drift the plugin reports "unavailable" rather
// than fabricating a zero.
const (
	defaultBaseURL = "https://api.commandcode.ai"
	pathWhoami     = "/alpha/whoami"
	pathSubs       = "/alpha/billing/subscriptions"
	pathCredits    = "/alpha/billing/credits"
	pathUsage      = "/alpha/usage/summary"
	pathModels     = "/provider/v1/models"
	userAgent      = "cliproxyapi-commandcode-bridge"
)

// Client talks to Command Code through the host HTTP callback so the host's
// transport policy and request logging apply.
type Client struct {
	baseURL string
}

// NewClient builds a client. An empty baseURL selects the public API.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{baseURL: baseURL}
}

// Identity is the account identity reported by /alpha/whoami.
type Identity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	UserName string `json:"userName"`
}

type whoamiResponse struct {
	Success bool     `json:"success"`
	User    Identity `json:"user"`
}

// Subscription describes the plan and billing period.
type Subscription struct {
	ID                 string `json:"id"`
	PlanID             string `json:"planId"`
	Status             string `json:"status"`
	CurrentPeriodStart string `json:"currentPeriodStart"`
	CurrentPeriodEnd   string `json:"currentPeriodEnd"`
	CancelAtPeriodEnd  bool   `json:"cancelAtPeriodEnd"`
}

type subscriptionsResponse struct {
	Success bool         `json:"success"`
	Data    Subscription `json:"data"`
}

// WindowLimit is one rolling usage window (5-hour or weekly).
type WindowLimit struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"resetAt"` // epoch milliseconds
}

// Credits is the credit state reported for the billing period.
type Credits struct {
	BelowThreshold   bool    `json:"belowThreshold"`
	CreditThreshold  float64 `json:"creditThreshold"`
	MonthlyCredits   float64 `json:"monthlyCredits"`
	PurchasedCredits float64 `json:"purchasedCredits"`
	FreeCredits      float64 `json:"freeCredits"`
}

// WindowLimits groups the metered windows.
type WindowLimits struct {
	Limited  bool         `json:"limited"`
	Exceeded *bool        `json:"exceeded"`
	FiveHour *WindowLimit `json:"fiveHour"`
	Weekly   *WindowLimit `json:"weekly"`
}

// CreditsResponse is the /alpha/billing/credits payload.
type CreditsResponse struct {
	Credits      Credits      `json:"credits"`
	WindowLimits WindowLimits `json:"windowLimits"`
}

// UsageSummary is the /alpha/usage/summary payload.
type UsageSummary struct {
	TotalCount       int64   `json:"totalCount"`
	CompletedCount   int64   `json:"completedCount"`
	FailedCount      int64   `json:"failedCount"`
	SuccessRate      float64 `json:"successRate"`
	TotalCost        float64 `json:"totalCost"`
	AverageCost      float64 `json:"averageCost"`
	TotalTokens      int64   `json:"totalTokens"`
	TotalTokensIn    int64   `json:"totalTokensIn"`
	TotalTokensOut   int64   `json:"totalTokensOut"`
	TotalCredits     float64 `json:"totalCredits"`
	TotalFreeCredits float64 `json:"totalFreeCredits"`
	PeriodBasis      string  `json:"periodBasis"`
}

// Error is a classified upstream failure.
type Error struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("command code: %s (http %d): %s", e.Code, e.StatusCode, e.Message)
}

// hostHTTPRequest is the wire shape of a host.http.do callback request. The
// callback id is required so the host can resolve request-scoped context.
type hostHTTPRequest struct {
	HostCallbackID string      `json:"host_callback_id,omitempty"`
	Method         string      `json:"method,omitempty"`
	URL            string      `json:"url,omitempty"`
	Headers        http.Header `json:"headers,omitempty"`
	Body           []byte      `json:"body,omitempty"`
}

// get performs an authenticated GET through the host and decodes the JSON body.
func (c *Client) get(ctx context.Context, apiKey, path string, out any) (http.Header, error) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+apiKey)
	headers.Set("Accept", "application/json")
	headers.Set("User-Agent", userAgent)

	var resp pluginapi.HTTPResponse
	errCall := host.Call(pluginabi.MethodHostHTTPDo, hostHTTPRequest{
		HostCallbackID: host.CallbackFrom(ctx),
		Method:         http.MethodGet,
		URL:            c.baseURL + path,
		Headers:        headers,
	}, &resp)
	if errCall != nil {
		return nil, &Error{Code: "transport_error", Message: errCall.Error(), Retryable: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.Headers, classify(resp.StatusCode, resp.Body)
	}
	if out != nil {
		if errUnmarshal := json.Unmarshal(resp.Body, out); errUnmarshal != nil {
			return resp.Headers, &Error{
				Code:       "invalid_response",
				Message:    "upstream returned an unparseable body",
				StatusCode: resp.StatusCode,
			}
		}
	}
	return resp.Headers, nil
}

// classify maps an upstream status to a structured error.
func classify(status int, body []byte) *Error {
	message := truncate(string(body), 512)
	switch status {
	case http.StatusUnauthorized:
		return &Error{StatusCode: status, Code: "invalid_credentials", Message: message}
	case http.StatusForbidden:
		return &Error{StatusCode: status, Code: "upstream_forbidden", Message: message}
	case http.StatusTooManyRequests:
		return &Error{StatusCode: status, Code: "upstream_rate_limited", Message: message, Retryable: true}
	}
	if status >= 500 {
		return &Error{StatusCode: status, Code: "upstream_error", Message: message, Retryable: true}
	}
	return &Error{StatusCode: status, Code: "upstream_rejected", Message: message}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Whoami returns the account identity.
func (c *Client) Whoami(ctx context.Context, apiKey string) (Identity, error) {
	var out whoamiResponse
	if _, err := c.get(ctx, apiKey, pathWhoami, &out); err != nil {
		return Identity{}, err
	}
	return out.User, nil
}

// Subscription fetches the plan and billing period.
func (c *Client) Subscription(ctx context.Context, apiKey string) (Subscription, error) {
	var out subscriptionsResponse
	if _, err := c.get(ctx, apiKey, pathSubs, &out); err != nil {
		return Subscription{}, err
	}
	return out.Data, nil
}

// Credits fetches the window limits and credit balances, returning the response
// headers so the caller can compute a server time offset.
func (c *Client) Credits(ctx context.Context, apiKey string) (CreditsResponse, http.Header, error) {
	var out CreditsResponse
	headers, err := c.get(ctx, apiKey, pathCredits, &out)
	if err != nil {
		return CreditsResponse{}, headers, err
	}
	return out, headers, nil
}

// Usage fetches the billing-period usage summary.
func (c *Client) Usage(ctx context.Context, apiKey string) (UsageSummary, error) {
	var out UsageSummary
	if _, err := c.get(ctx, apiKey, pathUsage, &out); err != nil {
		return UsageSummary{}, err
	}
	return out, nil
}

// CatalogModel is one entry of the provider model catalogue.
type CatalogModel struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	Name          string `json:"name"`
	ContextLength int64  `json:"context_length"`
}

type catalogResponse struct {
	Object string         `json:"object"`
	Data   []CatalogModel `json:"data"`
}

// Catalog fetches the documented provider model catalogue.
func (c *Client) Catalog(ctx context.Context, apiKey string) ([]CatalogModel, error) {
	var out catalogResponse
	if _, err := c.get(ctx, apiKey, pathModels, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// RemainingFraction converts a window into a clamped remaining fraction. It
// reports ok=false when the window cannot be trusted, so callers never render a
// fabricated percentage.
func RemainingFraction(limit *WindowLimit) (float64, bool) {
	if limit == nil {
		return 0, false
	}
	if math.IsNaN(limit.Cap) || math.IsInf(limit.Cap, 0) || limit.Cap <= 0 {
		return 0, false
	}
	if math.IsNaN(limit.Used) || math.IsInf(limit.Used, 0) || limit.Used < 0 {
		return 0, false
	}
	fraction := 1 - (limit.Used / limit.Cap)
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	return fraction, true
}

// ResetTime converts an epoch-millisecond reset stamp to UTC RFC3339. It
// returns an empty string when the value is absent or implausible.
func ResetTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).UTC()
	if t.Year() < 2000 || t.Year() > 2200 {
		return ""
	}
	return t.Format(time.RFC3339)
}
