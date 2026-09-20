// Package management implements the CLIProxyAPI ManagementAPI capability for
// Command Code: an enrollment and quota dashboard served from a plugin-owned
// resource route, plus authenticated management routes.
//
// The built-in Quota page hardcodes its provider adapters and cannot render a
// plugin card, so this page is how quota becomes visible in the Management
// Center today. The QuotaProvider ABI is implemented in parallel so the API
// surface is also at parity and any future dynamic UI works unchanged.
package management

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/auth"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/host"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/quota"
)

//go:embed web/accounts.html
var accountsPage []byte

// ProviderID is the stable provider key.
const ProviderID = auth.ProviderID

// Route paths, relative to their respective prefixes.
const (
	pathAccounts = "/accounts"
	pathValidate = "/validate"
	pathQuota    = "/quota"
)

// Provider implements pluginapi.ManagementAPI.
type Provider struct {
	quota *quota.Provider
}

// NewProvider builds the management provider.
func NewProvider(quotaProvider *quota.Provider) *Provider {
	return &Provider{quota: quotaProvider}
}

// RegisterManagement declares the plugin's routes.
//
// Handlers are filled in by the host, so only the route metadata is returned.
func (p *Provider) RegisterManagement(_ context.Context, _ pluginapi.ManagementRegistrationRequest) (pluginapi.ManagementRegistrationResponse, error) {
	return pluginapi.ManagementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{
				Method:      http.MethodGet,
				Path:        pathQuota,
				Description: "Fetch normalized quota for one Command Code credential",
			},
			{
				Method:      http.MethodPost,
				Path:        pathValidate,
				Description: "Validate a Command Code API key without persisting it",
			},
		},
		Resources: []pluginapi.ResourceRoute{
			{
				Path:        pathAccounts,
				Menu:        "CommandCode Bridge Accounts",
				Description: "Enroll and inspect Command Code accounts, quota and usage",
			},
		},
	}, nil
}

// HandleManagement serves one plugin-owned route.
//
// The host passes the full request path, for example
// "/v0/resource/plugins/command-code/accounts", but a route is declared relative
// ("/accounts"). Both spellings are therefore matched, which is what the
// reference plugin does; matching only the declared path makes every page 404.
func (p *Provider) HandleManagement(ctx context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	path := strings.TrimRight(strings.TrimSpace(req.Path), "/")
	if path == "" {
		path = "/"
	}
	switch {
	case req.Method == http.MethodGet && (path == pathAccounts || path == resourcePath(pathAccounts)):
		return htmlResponse(accountsPage), nil
	case req.Method == http.MethodGet && (path == pathQuota || path == managementPath(pathQuota)):
		return p.handleQuota(ctx, req)
	case req.Method == http.MethodPost && (path == pathValidate || path == managementPath(pathValidate)):
		return p.handleValidate(ctx, req)
	default:
		return jsonError(http.StatusNotFound, "unknown route"), nil
	}
}

// handleQuota resolves normalized quota for the requested credential so the
// dashboard renders exactly what the host's quota API returns.
func (p *Provider) handleQuota(ctx context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	authIndex := firstQuery(req.Query, "auth_index", "authIndex")
	if authIndex == "" {
		return jsonError(http.StatusBadRequest, "auth_index is required"), nil
	}
	ctx = host.WithCallbackID(ctx, host.CallbackFrom(ctx))

	resp, err := p.quota.FetchQuota(ctx, pluginapi.QuotaFetchRequest{AuthIndex: authIndex})
	if err != nil {
		// Fail closed: report the failure rather than an empty success.
		return jsonError(http.StatusBadGateway, fmt.Sprintf("failed to fetch quota: %v", err)), nil
	}
	return jsonOK(resp), nil
}

// handleValidate performs a dry-run key check: it resolves identity and plan but
// never persists anything.
func (p *Provider) handleValidate(ctx context.Context, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return jsonError(http.StatusBadRequest, "invalid request body"), nil
	}
	apiKey := strings.TrimSpace(body.APIKey)
	if err := auth.Validate(apiKey); err != nil {
		return jsonError(http.StatusBadRequest, err.Error()), nil
	}
	ctx = host.WithCallbackID(ctx, host.CallbackFrom(ctx))

	client := quota.NewClient("")
	identity, errWhoami := client.Whoami(ctx, apiKey)
	if errWhoami != nil {
		return jsonError(http.StatusBadGateway, fmt.Sprintf("key validation failed: %v", errWhoami)), nil
	}
	subscription, errSub := client.Subscription(ctx, apiKey)

	result := map[string]any{
		"valid":       true,
		"fingerprint": auth.Fingerprint(apiKey),
		"file_name":   auth.FileName(apiKey),
		"identity":    identity,
	}
	if errSub == nil {
		plan, priority := auth.NormalizePlan(subscription.PlanID)
		result["subscription"] = subscription
		result["plan"] = plan
		result["default_priority"] = priority
	}
	return jsonOK(result), nil
}

// resourcePath returns the full path the host uses for a resource route.
func resourcePath(relative string) string {
	return "/v0/resource/plugins/" + ProviderID + relative
}

// managementPath returns the full path the host uses for a management route.
//
// Management routes are mounted directly under /v0/management; unlike resource
// routes they are not namespaced by plugin id.
func managementPath(relative string) string {
	return "/v0/management" + relative
}

func firstQuery(values url.Values, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func jsonOK(payload any) pluginapi.ManagementResponse {
	body, err := json.Marshal(payload)
	if err != nil {
		return jsonError(http.StatusInternalServerError, "encode response failed")
	}
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	}
}

func jsonError(status int, message string) pluginapi.ManagementResponse {
	body, _ := json.Marshal(map[string]string{"error": message})
	return pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	}
}

func htmlResponse(body []byte) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       body,
	}
}

var _ = pluginabi.MethodManagementRegister
