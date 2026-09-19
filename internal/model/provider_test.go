package model

import (
	"testing"
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
