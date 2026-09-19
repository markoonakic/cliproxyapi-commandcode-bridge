package executor

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/auth"
)

func testCredential() auth.Credential {
	return auth.Credential{
		Type: "commandcode-bridge", APIKey: "user_testkey", Plan: "goat", Priority: 6,
		Models: []auth.CredentialMod{
			{Name: "deepseek/deepseek-v4.1-flash"},
			{Name: "z-ai/glm-5.3-flash", Alias: "glm"},
		},
	}
}

func TestTranslateRequestBuildsCommandCodeEnvelope(t *testing.T) {
	inbound := `{"model":"deepseek/deepseek-v4.1-flash","messages":[{"role":"system","content":"be terse"},{"role":"user","content":"hi"}],"max_tokens":64,"temperature":0.2,"stream":true}`
	body, options, err := translateRequest([]byte(inbound), "", "thread-1", testCredential(), time.Now())
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}
	if !options.Stream {
		t.Error("expected stream option to be set")
	}

	var envelope struct {
		PermissionMode string `json:"permissionMode"`
		ThreadID       string `json:"threadId"`
		Config         struct {
			Date string `json:"date"`
		} `json:"config"`
		Params struct {
			Model       string           `json:"model"`
			Stream      bool             `json:"stream"`
			System      string           `json:"system"`
			MaxTokens   int64            `json:"max_tokens"`
			Temperature float64          `json:"temperature"`
			Messages    []map[string]any `json:"messages"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	if envelope.Params.Model != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("model = %q", envelope.Params.Model)
	}
	if envelope.Params.System != "be terse" {
		t.Errorf("system = %q, want the system message hoisted into params", envelope.Params.System)
	}
	if envelope.Params.MaxTokens != 64 || envelope.Params.Temperature != 0.2 {
		t.Errorf("sampling params = %+v", envelope.Params)
	}
	// The upstream request is always streamed; the client's stream flag only
	// decides which executor path the host calls.
	if !envelope.Params.Stream {
		t.Error("upstream stream must always be true")
	}
	if len(envelope.Params.Messages) != 1 || envelope.Params.Messages[0]["role"] != "user" {
		t.Errorf("messages = %+v", envelope.Params.Messages)
	}
	if envelope.PermissionMode != permissionMode {
		t.Errorf("permissionMode = %q", envelope.PermissionMode)
	}
}

// TestTranslateRequestMapsAliasToUpstreamModel verifies a client-visible alias
// reaches the correct upstream model.
func TestTranslateRequestMapsAliasToUpstreamModel(t *testing.T) {
	inbound := `{"model":"glm","messages":[{"role":"user","content":"hi"}]}`
	body, _, err := translateRequest([]byte(inbound), "", "t", testCredential(), time.Now())
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}
	var envelope struct {
		Params struct {
			Model string `json:"model"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Params.Model != "z-ai/glm-5.3-flash" {
		t.Errorf("alias resolved to %q, want the upstream model", envelope.Params.Model)
	}
}

// TestTranslateRequestIsPermissive verifies a model outside the credential's
// selection is forwarded rather than rejected, which is the chosen policy.
func TestTranslateRequestIsPermissive(t *testing.T) {
	inbound := `{"model":"some/new-model","messages":[{"role":"user","content":"hi"}]}`
	body, _, err := translateRequest([]byte(inbound), "", "t", testCredential(), time.Now())
	if err != nil {
		t.Fatalf("a model outside the selection must not be rejected: %v", err)
	}
	var envelope struct {
		Params struct {
			Model string `json:"model"`
		} `json:"params"`
	}
	_ = json.Unmarshal(body, &envelope)
	if envelope.Params.Model != "some/new-model" {
		t.Errorf("model = %q, want passthrough", envelope.Params.Model)
	}
}

func TestTranslateRequestToolCallRoundTrip(t *testing.T) {
	inbound := `{"model":"m","messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},
		{"role":"tool","tool_call_id":"call_1","name":"get_weather","content":"sunny"}
	]}`
	body, _, err := translateRequest([]byte(inbound), "", "t", testCredential(), time.Now())
	if err != nil {
		t.Fatalf("translateRequest failed: %v", err)
	}
	var envelope struct {
		Params struct {
			Messages []map[string]any `json:"messages"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Params.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(envelope.Params.Messages))
	}
	assistant := envelope.Params.Messages[1]
	blocks, _ := assistant["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("assistant blocks = %+v", assistant["content"])
	}
	block, _ := blocks[0].(map[string]any)
	if block["type"] != "tool-call" || block["toolName"] != "get_weather" {
		t.Errorf("tool-call block = %+v", block)
	}
	// The arguments must be a parsed object, not a JSON string.
	input, _ := block["input"].(map[string]any)
	if input["city"] != "Paris" {
		t.Errorf("tool input = %+v", block["input"])
	}
}

func TestTranslateRequestRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"not json":                  `{`,
		"no messages":               `{"model":"m","messages":[]}`,
		"unsupported role":          `{"model":"m","messages":[{"role":"robot","content":"x"}]}`,
		"non-function tool":         `{"model":"m","messages":[{"role":"user","content":"x"}],"tools":[{"type":"web_search"}]}`,
		"assistant without content": `{"model":"m","messages":[{"role":"user","content":"x"},{"role":"assistant","content":null}]}`,
	}
	for name, inbound := range cases {
		if _, _, err := translateRequest([]byte(inbound), "", "t", testCredential(), time.Now()); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestTranslateRequestRequiresModel(t *testing.T) {
	if _, _, err := translateRequest([]byte(`{"messages":[{"role":"user","content":"x"}]}`), "", "t", testCredential(), time.Now()); err == nil {
		t.Error("expected an error when no model is resolvable")
	}
	// An explicitly selected model can supply the model when the body omits it.
	if _, _, err := translateRequest([]byte(`{"messages":[{"role":"user","content":"x"}]}`), "z-ai/glm-5.3-flash", "t", testCredential(), time.Now()); err != nil {
		t.Errorf("executor-provided model should be usable: %v", err)
	}
}

func TestResolveModelPrecedence(t *testing.T) {
	cred := testCredential()
	if got := resolveModel("z-ai/glm-5.3-flash", "deepseek/deepseek-v4.1-flash", cred); got != "z-ai/glm-5.3-flash" {
		t.Errorf("executor model should win, got %q", got)
	}
	if got := resolveModel("", "glm", cred); got != "z-ai/glm-5.3-flash" {
		t.Errorf("alias should resolve to upstream, got %q", got)
	}
	if got := resolveModel("", "unknown/model", cred); got != "unknown/model" {
		t.Errorf("unknown model should pass through, got %q", got)
	}
	if got := resolveModel("", "", cred); got != "" {
		t.Errorf("empty model = %q", got)
	}
}
