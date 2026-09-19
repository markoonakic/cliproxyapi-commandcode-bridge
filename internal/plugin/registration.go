package plugin

import (
	"context"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/abi"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/quota"
)

// registration is the capability declaration returned at plugin.register and
// plugin.reconfigure. Metadata uses the SDK's Go field names because the host
// decodes pluginapi.Metadata directly; capability keys are snake_case.
func registration(version, commit string) ([]byte, error) {
	meta := pluginapi.Metadata{
		Name:             "Command Code Bridge",
		Version:          version,
		Author:           "CommandCode Bridge contributors",
		GitHubRepository: "https://github.com/markoonakic/cliproxyapi-commandcode-bridge",
		ConfigFields:     []pluginapi.ConfigField{},
	}
	_ = commit

	payload := struct {
		SchemaVersion uint32             `json:"schema_version"`
		Metadata      pluginapi.Metadata `json:"metadata"`
		Capabilities  map[string]any     `json:"capabilities"`
	}{
		SchemaVersion: abi.SchemaVersion,
		Metadata:      meta,
		Capabilities: map[string]any{
			"quota_provider": true,
		},
	}
	return abi.OK(payload)
}

// contextBackground returns a context for a host-initiated call.
func contextBackground() context.Context {
	return context.Background()
}

// quotaProvider adapts the quota implementation to the SDK interface.
type quotaProvider struct {
	inner *quota.Provider
}

func newQuotaProvider() *quotaProvider {
	return &quotaProvider{inner: quota.NewProvider()}
}

func (q *quotaProvider) Identifier() string { return quota.ProviderID }

func (q *quotaProvider) DescribeQuota(ctx context.Context, req pluginapi.QuotaDescribeRequest) (pluginapi.QuotaDescribeResponse, error) {
	return q.inner.DescribeQuota(ctx, req)
}

func (q *quotaProvider) FetchQuota(ctx context.Context, req pluginapi.QuotaFetchRequest) (pluginapi.QuotaFetchResponse, error) {
	return q.inner.FetchQuota(ctx, req)
}

func (q *quotaProvider) ResetQuota(ctx context.Context, req pluginapi.QuotaResetRequest) (pluginapi.QuotaResetResponse, error) {
	return q.inner.ResetQuota(ctx, req)
}

var _ = time.Second
