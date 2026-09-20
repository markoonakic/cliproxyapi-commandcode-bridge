package plugin

import (
	"encoding/json"
	"testing"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/abi"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/identity"
)

// decodeResult unwraps a successful RPC envelope and decodes its result.
func decodeResult(t *testing.T, raw []byte, out any) {
	t.Helper()
	var envelope abi.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("envelope not ok: %+v", envelope.Error)
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
}

func TestRegistrationDeclaresQuotaProvider(t *testing.T) {
	raw, err := Dispatch(abi.MethodPluginRegister, nil, "1.0.0", "test")
	if err != nil {
		t.Fatalf("register returned error: %v", err)
	}
	var payload struct {
		SchemaVersion uint32 `json:"schema_version"`
		Metadata      struct {
			Name    string `json:"Name"`
			Version string `json:"Version"`
		} `json:"metadata"`
		Capabilities map[string]any `json:"capabilities"`
	}
	decodeResult(t, raw, &payload)

	if payload.SchemaVersion != abi.SchemaVersion {
		t.Errorf("schema_version = %d, want %d", payload.SchemaVersion, abi.SchemaVersion)
	}
	if payload.Metadata.Version != "1.0.0" {
		t.Errorf("metadata version = %q, want 1.0.0", payload.Metadata.Version)
	}
	if declared, ok := payload.Capabilities["quota_provider"].(bool); !ok || !declared {
		t.Errorf("capabilities = %+v, want quota_provider=true", payload.Capabilities)
	}
}

func TestReconfigureMatchesRegistration(t *testing.T) {
	registerRaw, _ := Dispatch(abi.MethodPluginRegister, nil, "1.0.0", "test")
	reconfigureRaw, _ := Dispatch(abi.MethodPluginReconfigure, nil, "1.0.0", "test")
	if string(registerRaw) != string(reconfigureRaw) {
		t.Error("reconfigure must return the same registration payload")
	}
}

func TestQuotaDescribeReportsNoResetSupport(t *testing.T) {
	raw, err := Dispatch(abi.MethodQuotaDescribe, nil, "1.0.0", "test")
	if err != nil {
		t.Fatalf("describe returned error: %v", err)
	}
	var resp struct {
		SupportedProviders []string `json:"supported_providers"`
		SupportsReset      bool     `json:"supports_reset"`
		DisplayName        string   `json:"display_name"`
	}
	decodeResult(t, raw, &resp)

	if len(resp.SupportedProviders) != 1 || resp.SupportedProviders[0] != identity.ProviderKey {
		t.Errorf("supported_providers = %v", resp.SupportedProviders)
	}
	if resp.SupportsReset {
		t.Error("supports_reset must be false: Command Code has no reset endpoint")
	}
}

// TestQuotaResetNeverReportsSuccess guards the host cooldown state: a true
// success would clear local cooldowns despite no upstream reset.
func TestQuotaResetNeverReportsSuccess(t *testing.T) {
	raw, err := Dispatch(abi.MethodQuotaReset, []byte(`{"auth_index":"x"}`), "1.0.0", "test")
	if err != nil {
		t.Fatalf("reset returned error: %v", err)
	}
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	decodeResult(t, raw, &resp)
	if resp.Success {
		t.Fatal("ResetQuota must never report success")
	}
}

func TestQuotaIdentifierMatchesProviderKey(t *testing.T) {
	raw, err := Dispatch(abi.MethodQuotaIdentifier, nil, "1.0.0", "test")
	if err != nil {
		t.Fatalf("identifier returned error: %v", err)
	}
	var resp struct {
		Identifier string `json:"identifier"`
	}
	decodeResult(t, raw, &resp)
	if resp.Identifier != identity.ProviderKey {
		t.Errorf("identifier = %q, want %q", resp.Identifier, identity.ProviderKey)
	}
}

func TestUnknownMethodIsRejected(t *testing.T) {
	raw, err := Dispatch("does.not.exist", nil, "1.0.0", "test")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	var envelope abi.Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.OK {
		t.Fatal("unknown method must return a failed envelope")
	}
	if envelope.Error == nil || envelope.Error.Code != "unknown_method" {
		t.Errorf("error = %+v, want code unknown_method", envelope.Error)
	}
}
