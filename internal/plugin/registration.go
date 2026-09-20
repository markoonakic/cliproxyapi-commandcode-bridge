package plugin

import (
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/abi"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/identity"
)

// registration is the capability declaration returned at plugin.register and
// plugin.reconfigure.
//
// Metadata uses the SDK's Go field names because the host decodes
// pluginapi.Metadata directly. Capability keys are snake_case, matching the
// host's rpcCapabilities decoding.
func registration(version, _ string) ([]byte, error) {
	payload := struct {
		SchemaVersion uint32             `json:"schema_version"`
		Metadata      pluginapi.Metadata `json:"metadata"`
		Capabilities  map[string]any     `json:"capabilities"`
	}{
		SchemaVersion: abi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             identity.Name,
			Version:          version,
			Author:           identity.Author,
			GitHubRepository: identity.Repository,
			ConfigFields:     []pluginapi.ConfigField{},
		},
		Capabilities: map[string]any{
			// Quota and limits parity with the native channels.
			"quota_provider": true,
			// Credential parsing, identity enrichment and plan discovery.
			"auth_provider": true,
			// Model catalogue, with human-readable display names.
			"model_provider": true,
			// Inference: OpenAI chat-completions translated to Command Code.
			"executor":                true,
			"executor_model_scope":    "oauth",
			"executor_input_formats":  []string{"chat-completions"},
			"executor_output_formats": []string{"chat-completions"},
			// Priority-aware, quota-aware multi-account selection.
			"scheduler": true,
			// Enrollment and quota dashboard under /v0/resource/plugins/.
			"management_api": true,
		},
	}
	return abi.OK(payload)
}
