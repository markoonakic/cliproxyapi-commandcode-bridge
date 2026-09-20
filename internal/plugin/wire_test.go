package plugin

import (
	"encoding/json"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/abi"
)

// TestEveryResponseTypeIsJSONEncodable guards a class of bug that only appears
// on a live host.
//
// The SDK stream response carries a channel of chunks. Returning it directly
// made the RPC layer fail with "unsupported type: <-chan ..." and the request
// failed only at runtime. Each response type the plugin emits must marshal.
func TestEveryResponseTypeIsJSONEncodable(t *testing.T) {
	cases := map[string]any{
		"executor stream wire":     executorStreamWire{},
		"management route wire":    managementRouteWire{},
		"management resource wire": managementResourceWire{},
		"quota describe":           pluginapi.QuotaDescribeResponse{},
		"quota fetch":              pluginapi.QuotaFetchResponse{},
		"quota reset":              pluginapi.QuotaResetResponse{},
		"executor response":        pluginapi.ExecutorResponse{},
		"auth parse":               pluginapi.AuthParseResponse{},
		"auth refresh":             pluginapi.AuthRefreshResponse{},
		"auth login poll":          pluginapi.AuthLoginPollResponse{},
		"model response":           pluginapi.ModelResponse{},
		"scheduler pick":           pluginapi.SchedulerPickResponse{},
		"management response":      pluginapi.ManagementResponse{},
		"registration wire":        managementRegistrationWire(pluginapi.ManagementRegistrationResponse{}),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := json.Marshal(value); err != nil {
				t.Fatalf("%s is not JSON-encodable: %v", name, err)
			}
		})
	}
}

// TestStreamDispatchReturnsHeadersOnly verifies the streamed method returns a
// decodable envelope rather than failing to marshal.
func TestStreamDispatchReturnsHeadersOnly(t *testing.T) {
	raw := []byte(`{"stream_id":"stream-1","model":"m","storage_json":{"type":"command-code","api_key":"user_x"}}`)
	out, err := Dispatch(abi.MethodExecutorExecuteStream, raw, "1.0.0", "test")
	if err != nil {
		t.Fatalf("Dispatch returned a transport error: %v", err)
	}
	var envelope abi.Envelope
	if errUnmarshal := json.Unmarshal(out, &envelope); errUnmarshal != nil {
		t.Fatalf("envelope is not JSON: %v", errUnmarshal)
	}
	// Either a handled failure or a successful headers payload is acceptable;
	// a marshal failure is not.
	if envelope.Error != nil && envelope.Error.Message == "" {
		t.Error("error envelope has no message")
	}
}

// TestQuotaFetchWithoutCredentialFailsCleanly verifies a missing credential
// produces a failed envelope rather than a panic or an empty success.
func TestQuotaFetchWithoutCredentialFailsCleanly(t *testing.T) {
	out, err := Dispatch(abi.MethodQuotaFetch, []byte(`{}`), "1.0.0", "test")
	if err != nil {
		t.Fatalf("Dispatch returned a transport error: %v", err)
	}
	var envelope abi.Envelope
	if errUnmarshal := json.Unmarshal(out, &envelope); errUnmarshal != nil {
		t.Fatalf("envelope is not JSON: %v", errUnmarshal)
	}
	if envelope.OK {
		t.Error("expected a failed envelope without a credential")
	}
}

// TestManagementRegistrationOmitsHandlers verifies route handlers never reach
// the wire, since they are interfaces the host replaces.
func TestManagementRegistrationOmitsHandlers(t *testing.T) {
	wire := managementRegistrationWire(pluginapi.ManagementRegistrationResponse{
		Routes:    []pluginapi.ManagementRoute{{Method: "GET", Path: "/quota"}},
		Resources: []pluginapi.ResourceRoute{{Path: "/accounts", Menu: "Accounts"}},
	})
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{"handler", "Handler"} {
		if contains(text, forbidden) {
			t.Errorf("wire payload mentions %q: %s", forbidden, text)
		}
	}
	if !contains(text, "/accounts") || !contains(text, "/quota") {
		t.Errorf("wire payload is missing routes: %s", text)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
