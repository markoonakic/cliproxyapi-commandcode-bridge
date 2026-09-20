package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// TestHandleManagementMatchesFullAndRelativePaths guards the routing contract.
//
// A route is declared relative ("/accounts"), but the host passes the full
// request path ("/v0/resource/plugins/<id>/accounts"). Matching only one of the
// two makes the page 404 in the Management Center, which is exactly what
// happened before this test existed.
func TestHandleManagementMatchesFullAndRelativePaths(t *testing.T) {
	p := NewProvider(nil)
	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"resource page via full path", http.MethodGet, "/v0/resource/plugins/" + ProviderID + "/accounts", http.StatusOK},
		{"resource page via relative path", http.MethodGet, pathAccounts, http.StatusOK},
		{"resource page with trailing slash", http.MethodGet, "/v0/resource/plugins/" + ProviderID + "/accounts/", http.StatusOK},
		{"quota via full path", http.MethodGet, "/v0/management" + pathQuota, http.StatusBadRequest},
		{"quota via relative path", http.MethodGet, pathQuota, http.StatusBadRequest},
		{"validate via full path", http.MethodPost, "/v0/management" + pathValidate, http.StatusBadRequest},
		{"validate via relative path", http.MethodPost, pathValidate, http.StatusBadRequest},
		{"unknown route", http.MethodGet, "/nope", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := p.HandleManagement(context.Background(), pluginapi.ManagementRequest{
				Method: tc.method, Path: tc.path, Query: url.Values{},
			})
			if err != nil {
				t.Fatalf("HandleManagement returned error: %v", err)
			}
			if resp.StatusCode != tc.want {
				t.Errorf("%s %s: status = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

// TestAccountsPageIsHTML verifies the dashboard body is served, not an error.
func TestAccountsPageIsHTML(t *testing.T) {
	p := NewProvider(nil)
	resp, err := p.HandleManagement(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet, Path: resourcePath(pathAccounts), Query: url.Values{},
	})
	if err != nil {
		t.Fatalf("HandleManagement returned error: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "<!DOCTYPE html>") {
		t.Error("expected an HTML dashboard body")
	}
}

// TestQuotaWithoutAuthIndexFailsClosed verifies the quota route reports a clear
// error rather than an empty success when the credential is not identified.
func TestQuotaWithoutAuthIndexFailsClosed(t *testing.T) {
	p := NewProvider(nil)
	resp, err := p.HandleManagement(context.Background(), pluginapi.ManagementRequest{
		Method: http.MethodGet, Path: managementPath(pathQuota), Query: url.Values{},
	})
	if err != nil {
		t.Fatalf("HandleManagement returned error: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body map[string]string
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("error body is not JSON: %v", err)
	}
	if body["error"] == "" {
		t.Error("expected an error message")
	}
}

// TestRegisterManagementDeclaresBothRouteKinds verifies the declaration matches
// what the handler serves.
func TestRegisterManagementDeclaresBothRouteKinds(t *testing.T) {
	p := NewProvider(nil)
	resp, err := p.RegisterManagement(context.Background(), pluginapi.ManagementRegistrationRequest{})
	if err != nil {
		t.Fatalf("RegisterManagement returned error: %v", err)
	}
	if len(resp.Resources) != 1 || resp.Resources[0].Path != pathAccounts {
		t.Errorf("resources = %+v, want one %s route", resp.Resources, pathAccounts)
	}
	if len(resp.Routes) != 2 {
		t.Errorf("routes = %d, want 2", len(resp.Routes))
	}
	// Every declared route must be servable, in both spellings.
	for _, route := range resp.Routes {
		for _, candidate := range []string{route.Path, managementPath(route.Path)} {
			got, errCall := p.HandleManagement(context.Background(), pluginapi.ManagementRequest{
				Method: route.Method, Path: candidate, Query: url.Values{},
			})
			if errCall != nil {
				t.Fatalf("%s %s returned error: %v", route.Method, candidate, errCall)
			}
			if got.StatusCode == http.StatusNotFound {
				t.Errorf("declared route %s %s is not served", route.Method, candidate)
			}
		}
	}
}
