package auth

import (
	"encoding/json"
	"testing"
)

// deployedCredential is the credential file shape already present on the host.
// It must keep parsing so the live account survives switchover.
const deployedCredential = `{
  "type": "commandcode-bridge",
  "api_key": "user_examplekey123",
  "label": "1",
  "plan": "goat",
  "priority": 6,
  "models": [
    {"name": "deepseek/deepseek-v4.1-flash", "alias": ""},
    {"name": "z-ai/glm-5.3-flash", "alias": ""},
    {"name": "meta/muse-spark-1.3-contributor", "alias": ""}
  ]
}`

func TestParseAcceptsDeployedCredential(t *testing.T) {
	cred, err := Parse([]byte(deployedCredential))
	if err != nil {
		t.Fatalf("deployed credential rejected: %v", err)
	}
	if cred.Plan != "goat" || cred.Priority != 6 || cred.Label != "1" {
		t.Errorf("credential = %+v", cred)
	}
	if len(cred.Models) != 3 {
		t.Fatalf("models = %d, want 3", len(cred.Models))
	}
}

func TestParseAcceptsIdentityAdditions(t *testing.T) {
	enriched := `{"type":"commandcode-bridge","api_key":"user_abc123","label":"acct","plan":"individual-goat","priority":6,"user_id":"u-1","email":"a@b.c","user_name":"abc","subscription_id":"sub_1","models":[]}`
	cred, err := Parse([]byte(enriched))
	if err != nil {
		t.Fatalf("enriched credential rejected: %v", err)
	}
	if cred.Email != "a@b.c" || cred.UserID != "u-1" || cred.SubID != "sub_1" {
		t.Errorf("identity fields not parsed: %+v", cred)
	}
}

func TestParseRejectsInvalidKeys(t *testing.T) {
	for _, raw := range []string{
		``,
		`{}`,
		`{"api_key":""}`,
		`{"api_key":"sk-abc"}`,
		`{"api_key":"user_"}`,
		`not json`,
	} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("expected rejection for %q", raw)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("user_abc"); err != nil {
		t.Errorf("valid key rejected: %v", err)
	}
	for _, bad := range []string{"", "user_", "user", "abc", "USER_abc"} {
		if err := Validate(bad); err == nil {
			t.Errorf("expected %q to be invalid", bad)
		}
	}
}

func TestNormalizePlanPriorityPresets(t *testing.T) {
	cases := map[string]int{
		"goat": 6, "go": 7, "pro": 5, "team": 4,
		"max-10x": 3, "max-20x": 2, "provider": 1,
	}
	for plan, want := range cases {
		gotPlan, gotPriority := NormalizePlan(plan)
		if gotPriority != want {
			t.Errorf("NormalizePlan(%q) priority = %d, want %d", plan, gotPriority, want)
		}
		if gotPlan != plan {
			t.Errorf("NormalizePlan(%q) plan = %q", plan, gotPlan)
		}
	}
	// Case and whitespace are normalized; unknown plans are not errors.
	if plan, priority := NormalizePlan("  GOAT  "); plan != "goat" || priority != 6 {
		t.Errorf("NormalizePlan(trim/upper) = %q/%d", plan, priority)
	}
	if _, priority := NormalizePlan("mystery"); priority != 0 {
		t.Errorf("unknown plan priority = %d, want 0", priority)
	}
	if plan, _ := NormalizePlan(""); plan != "unspecified" {
		t.Errorf("empty plan = %q, want unspecified", plan)
	}
}

func TestEffectivePriorityPrefersOverride(t *testing.T) {
	if got := EffectivePriority(Credential{Plan: "goat", Priority: 6}); got != 6 {
		t.Errorf("plan preset priority = %d, want 6", got)
	}
	if got := EffectivePriority(Credential{Plan: "goat", Priority: 6, PriorityOver: 9}); got != 9 {
		t.Errorf("override priority = %d, want 9", got)
	}
	if got := EffectivePriority(Credential{Plan: "mystery"}); got != 0 {
		t.Errorf("unknown plan priority = %d, want 0", got)
	}
}

func TestFingerprintIsStableAndNotTheKey(t *testing.T) {
	const key = "user_supersecretvalue"
	fp := Fingerprint(key)
	if fp == key || len(fp) != 12 {
		t.Fatalf("fingerprint = %q, want a 12-char hash", fp)
	}
	if Fingerprint(key) != fp {
		t.Error("fingerprint must be stable")
	}
	if Fingerprint(key+"x") == fp {
		t.Error("different keys must not collide here")
	}
	if fileName := FileName(key); fileName != "commandcode-bridge-"+fp+".json" {
		t.Errorf("FileName = %q", fileName)
	}
}

func TestDisplayLabelFallbacks(t *testing.T) {
	if got := DisplayLabel(Credential{APIKey: "user_x", Label: "L"}); got != "L" {
		t.Errorf("label = %q, want L", got)
	}
	if got := DisplayLabel(Credential{APIKey: "user_x", Email: "a@b.c"}); got != "a@b.c" {
		t.Errorf("email fallback = %q", got)
	}
	if got := DisplayLabel(Credential{APIKey: "user_x", UserName: "abc"}); got != "abc" {
		t.Errorf("username fallback = %q", got)
	}
	if got := DisplayLabel(Credential{APIKey: "user_x"}); got != Fingerprint("user_x") {
		t.Errorf("fingerprint fallback = %q", got)
	}
}

// TestToAuthDataPopulatesIdentity verifies the parity fields the native
// channels expose (email, plan) are present on the auth record, and that the
// hosted storage JSON round-trips.
func TestToAuthDataPopulatesIdentity(t *testing.T) {
	cred := Credential{
		Type: "commandcode-bridge", APIKey: "user_abc123", Label: "acct",
		Plan: "goat", Priority: 6, Email: "a@b.c", UserID: "u-1", UserName: "abc",
	}
	data := ToAuthData(cred, "")

	if data.Provider != ProviderID {
		t.Errorf("provider = %q", data.Provider)
	}
	if data.Label != "acct" {
		t.Errorf("label = %q", data.Label)
	}
	if data.Metadata["email"] != "a@b.c" || data.Metadata["account_type"] != "goat" {
		t.Errorf("metadata = %+v", data.Metadata)
	}
	if data.Attributes["priority"] != "6" {
		t.Errorf("priority attribute = %q, want 6", data.Attributes["priority"])
	}
	var roundTrip Credential
	if err := json.Unmarshal(data.StorageJSON, &roundTrip); err != nil {
		t.Fatalf("storage json not decodable: %v", err)
	}
	if roundTrip.APIKey != cred.APIKey {
		t.Error("storage json lost the api key")
	}
}
