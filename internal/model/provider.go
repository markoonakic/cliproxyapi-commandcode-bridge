// Package model implements the CLIProxyAPI ModelProvider capability for
// Command Code.
//
// It fixes two defects in the community plugin: StaticModels returned an empty
// list despite a usable catalogue being embedded, and per-auth models were
// published with the raw upstream id as the display name instead of a
// human-readable name. Native channels (for example antigravity) return
// readable names such as "Claude Opus 4.6 (Thinking)".
package model

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/auth"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/quota"
)

//go:embed catalog/snapshot.json
var snapshotJSON []byte

// catalogModel is one entry of the embedded catalogue and of a live
// /provider/v1/models response.
type catalogModel struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	Name          string `json:"name"`
	ContextLength int64  `json:"context_length"`
}

type catalogResponse struct {
	Object string         `json:"object"`
	Data   []catalogModel `json:"data"`
}

// Provider implements pluginapi.ModelProvider.
type Provider struct {
	client *quota.Client
}

// NewProvider builds a model provider.
func NewProvider() *Provider {
	return &Provider{client: quota.NewClient("")}
}

// StaticCatalog returns the embedded catalogue. It never fails: a malformed
// snapshot yields an empty slice rather than an error, because the host treats
// a model-provider error as a capability failure.
func StaticCatalog() []pluginapi.ModelInfo {
	var items []catalogModel
	if err := json.Unmarshal(snapshotJSON, &items); err != nil {
		return nil
	}
	if len(items) == 0 {
		return nil
	}
	// Also accept a wrapper object shape.
	if items[0].ID == "" {
		var wrapped catalogResponse
		if err := json.Unmarshal(snapshotJSON, &wrapped); err != nil {
			return nil
		}
		items = wrapped.Data
	}
	return toModelInfos(items)
}

// StaticModels returns the full catalogue to the host.
func (p *Provider) StaticModels(_ context.Context, _ pluginapi.StaticModelRequest) (pluginapi.ModelResponse, error) {
	return pluginapi.ModelResponse{Provider: auth.ProviderID, Models: StaticCatalog()}, nil
}

// ModelsForAuth returns the models available to one credential.
//
// When the credential selects explicit models, exactly those are published and
// an alias replaces the client-visible id. Otherwise the live catalogue is
// fetched, falling back to the embedded snapshot when discovery fails.
func (p *Provider) ModelsForAuth(ctx context.Context, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	cred, err := auth.Parse(req.StorageJSON)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}

	contexts := map[string]int64{}
	if catalog, errCatalog := p.client.Catalog(ctx, cred.APIKey); errCatalog == nil {
		for _, item := range catalog {
			if id := strings.TrimSpace(item.ID); id != "" {
				contexts[id] = item.ContextLength
			}
		}
	}
	if len(contexts) == 0 {
		for _, info := range StaticCatalog() {
			contexts[info.ID] = info.ContextLength
		}
	}

	if len(cred.Models) == 0 {
		return pluginapi.ModelResponse{Provider: auth.ProviderID, Models: modelsFromContexts(contexts)}, nil
	}

	models := make([]pluginapi.ModelInfo, 0, len(cred.Models))
	for _, selected := range cred.Models {
		name := strings.TrimSpace(selected.Name)
		if name == "" {
			continue
		}
		// An alias becomes the client-visible id; the upstream id is preserved
		// in Name so the executor still addresses the right model.
		id := name
		if alias := strings.TrimSpace(selected.Alias); alias != "" {
			id = alias
		}
		models = append(models, pluginapi.ModelInfo{
			ID:                         id,
			Name:                       name,
			DisplayName:                DisplayNameFor(name),
			ContextLength:              contexts[name],
			SupportedGenerationMethods: []string{"chat"},
			SupportedInputModalities:   []string{"text"},
			SupportedOutputModalities:  []string{"text"},
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return pluginapi.ModelResponse{Provider: auth.ProviderID, Models: models}, nil
}

// DisplayNameFor returns a human-readable name for a model id.
//
// It uses the catalogue's name when known, and otherwise derives a readable
// label from the id rather than echoing the raw id. This is the parity fix for
// the management UI, which renders display_name.
func DisplayNameFor(id string) string {
	for _, info := range StaticCatalog() {
		if info.ID == id {
			return info.DisplayName
		}
	}
	return readableFromID(id)
}

// readableFromID turns "deepseek/deepseek-v4.1-flash" into "DeepSeek V4.1 Flash".
func readableFromID(id string) string {
	base := id
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return id
	}
	for i, part := range parts {
		parts[i] = titleToken(part)
	}
	return strings.Join(parts, " ")
}

// titleToken applies brand casing and known acronyms, otherwise capitalizes the
// first rune ("v4.1" -> "V4.1", "120b" -> "120B").
func titleToken(token string) string {
	if token == "" {
		return token
	}
	lower := strings.ToLower(token)
	if brand, ok := brandTokens[lower]; ok {
		return brand
	}
	if acronyms[lower] {
		return strings.ToUpper(token)
	}
	runes := []rune(token)
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	// A trailing size suffix such as 120b reads better upper-cased.
	if len(runes) > 1 && runes[len(runes)-1] == 'b' {
		runes[len(runes)-1] = 'B'
	}
	return string(runes)
}

// brandTokens preserves vendor casing that simple title-casing would mangle.
var brandTokens = map[string]string{
	"deepseek": "DeepSeek",
	"muse":     "Muse",
	"qwen":     "Qwen",
	"kimi":     "Kimi",
	"llama":    "Llama",
	"mistral":  "Mistral",
	"gemini":   "Gemini",
}

// acronyms are tokens rendered fully upper-case.
var acronyms = map[string]bool{
	"glm": true, "gpt": true, "oss": true, "ai": true, "xl": true, "vl": true,
}

func toModelInfos(items []catalogModel) []pluginapi.ModelInfo {
	seen := make(map[string]struct{}, len(items))
	models := make([]pluginapi.ModelInfo, 0, len(items))
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		models = append(models, pluginapi.ModelInfo{
			ID:                         id,
			Object:                     strings.TrimSpace(item.Object),
			Created:                    item.Created,
			OwnedBy:                    strings.TrimSpace(item.OwnedBy),
			Name:                       strings.TrimSpace(item.Name),
			DisplayName:                firstNonEmpty(item.Name, readableFromID(id)),
			ContextLength:              item.ContextLength,
			SupportedGenerationMethods: []string{"chat"},
			SupportedInputModalities:   []string{"text"},
			SupportedOutputModalities:  []string{"text"},
		})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models
}

func modelsFromContexts(contexts map[string]int64) []pluginapi.ModelInfo {
	ids := make([]string, 0, len(contexts))
	for id := range contexts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	models := make([]pluginapi.ModelInfo, 0, len(ids))
	for _, id := range ids {
		models = append(models, pluginapi.ModelInfo{
			ID:                         id,
			Name:                       id,
			DisplayName:                DisplayNameFor(id),
			ContextLength:              contexts[id],
			SupportedGenerationMethods: []string{"chat"},
			SupportedInputModalities:   []string{"text"},
			SupportedOutputModalities:  []string{"text"},
		})
	}
	return models
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

var _ = fmt.Sprintf
