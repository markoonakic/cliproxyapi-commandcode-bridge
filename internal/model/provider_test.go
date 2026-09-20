package model

import (
	"sort"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/auth"
)

// TestStaticCatalogIsNotEmpty guards the community plugin's defect where
// StaticModels returned an empty list despite an embedded catalogue.
func TestStaticCatalogIsNotEmpty(t *testing.T) {
	catalog := StaticCatalog()
	if len(catalog) == 0 {
		t.Fatal("static catalogue is empty; the embedded snapshot did not load")
	}
	if len(catalog) < 50 {
		t.Errorf("static catalogue has %d models, expected the full snapshot", len(catalog))
	}
}

// TestStaticCatalogHasHumanReadableDisplayNames guards the second community
// plugin defect: display names were the raw upstream id.
func TestStaticCatalogHasHumanReadableDisplayNames(t *testing.T) {
	for _, info := range StaticCatalog() {
		if info.DisplayName == "" {
			t.Fatalf("model %q has no display name", info.ID)
		}
		if info.DisplayName == info.ID {
			t.Errorf("model %q uses its raw id as the display name", info.ID)
		}
	}
}

func TestDisplayNameForKnownModel(t *testing.T) {
	// A model present in the snapshot must use the catalogue's readable name.
	first := StaticCatalog()[0]
	if got := DisplayNameFor(first.ID); got != first.DisplayName {
		t.Errorf("DisplayNameFor(%q) = %q, want %q", first.ID, got, first.DisplayName)
	}
}

// TestDisplayNameForUnknownModelIsReadable verifies a model absent from the
// catalogue still gets a readable label rather than the raw id.
func TestDisplayNameForUnknownModelIsReadable(t *testing.T) {
	cases := map[string]string{
		"deepseek/deepseek-v4.1-flash":    "DeepSeek V4.1 Flash",
		"z-ai/glm-5.3-flash":              "GLM 5.3 Flash",
		"meta/muse-spark-1.3-contributor": "Muse Spark 1.3 Contributor",
		"gpt-oss-120b-medium":             "GPT OSS 120B Medium",
	}
	for id, want := range cases {
		if got := DisplayNameFor(id); got != want {
			t.Errorf("DisplayNameFor(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestReadableFromIDStripsProviderPrefix(t *testing.T) {
	if got := readableFromID("anthropic/claude-sonnet-5"); got != "Claude Sonnet 5" {
		t.Errorf("readableFromID = %q", got)
	}
	if got := readableFromID("plain-model"); got != "Plain Model" {
		t.Errorf("readableFromID(no prefix) = %q", got)
	}
}

// credentialWith builds a credential whose selected models carry aliases.
func credentialWith(entries ...auth.CredentialMod) auth.Credential {
	return auth.Credential{
		Type: "commandcode-bridge", APIKey: "user_testkey123", Plan: "goat", Priority: 6,
		Models: entries,
	}
}

// TestModelsForAuthPublishesAliasAsID is the contract side-by-side testing
// depends on: an alias becomes the client-visible model id, while the upstream
// model name is preserved so the executor still addresses the right model.
func TestModelsForAuthPublishesAliasAsID(t *testing.T) {
	cred := credentialWith(
		auth.CredentialMod{Name: "deepseek/deepseek-v4.1-flash"},
		auth.CredentialMod{Name: "z-ai/glm-5.3-flash", Alias: "cc/glm-5.3-flash"},
	)
	models, err := modelsForCredential(cred, nil)
	if err != nil {
		t.Fatalf("modelsForCredential failed: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}

	byID := map[string]pluginapi.ModelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}

	// The aliased model must be advertised under its alias, with the upstream
	// name retained for execution.
	aliased, ok := byID["cc/glm-5.3-flash"]
	if !ok {
		t.Fatalf("alias id not published; got ids %v", keysOf(byID))
	}
	if aliased.Name != "z-ai/glm-5.3-flash" {
		t.Errorf("Name = %q, want the upstream model preserved", aliased.Name)
	}
	if aliased.DisplayName != "GLM 5.3 Flash" {
		t.Errorf("DisplayName = %q, want a human-readable name", aliased.DisplayName)
	}
	// The upstream id must NOT also appear, or both would collide.
	if _, clash := byID["z-ai/glm-5.3-flash"]; clash {
		t.Error("upstream id must not be published alongside the alias")
	}

	// An entry without an alias is published under its upstream name.
	if _, ok := byID["deepseek/deepseek-v4.1-flash"]; !ok {
		t.Errorf("unaliased model missing; got ids %v", keysOf(byID))
	}
}

// TestModelsForAuthWithoutAliasesMatchesUpstreamIDs verifies the default case
// used by the already-deployed credential.
func TestModelsForAuthWithoutAliasesMatchesUpstreamIDs(t *testing.T) {
	cred := credentialWith(
		auth.CredentialMod{Name: "deepseek/deepseek-v4.1-flash"},
		auth.CredentialMod{Name: "z-ai/glm-5.3-flash"},
		auth.CredentialMod{Name: "meta/muse-spark-1.3-contributor"},
	)
	models, err := modelsForCredential(cred, nil)
	if err != nil {
		t.Fatalf("modelsForCredential failed: %v", err)
	}
	want := []string{
		"deepseek/deepseek-v4.1-flash",
		"meta/muse-spark-1.3-contributor",
		"z-ai/glm-5.3-flash",
	}
	got := keysOfIDs(models)
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ids = %v, want %v", got, want)
		}
	}
}

// TestModelsForAuthAliasRoundTripsThroughExecutor is the end-to-end contract:
// the id the plugin advertises must resolve back to the upstream model.
func TestModelsForAuthAliasRoundTripsThroughExecutor(t *testing.T) {
	cred := credentialWith(
		auth.CredentialMod{Name: "z-ai/glm-5.3-flash", Alias: "cc/glm-5.3-flash"},
	)
	models, err := modelsForCredential(cred, nil)
	if err != nil {
		t.Fatalf("modelsForCredential failed: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("models = %d, want 1", len(models))
	}

	// Feed the advertised id back in, as the host would.
	resolved := auth.ResolveUpstreamModel(models[0].ID, cred)
	if resolved != "z-ai/glm-5.3-flash" {
		t.Errorf("advertised id %q resolved to %q, want the upstream model", models[0].ID, resolved)
	}
}

func keysOf(m map[string]pluginapi.ModelInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysOfIDs(models []pluginapi.ModelInfo) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	sort.Strings(out)
	return out
}
