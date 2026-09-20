// Package auth implements the CLIProxyAPI AuthProvider capability for
// Command Code.
//
// Command Code authenticates with a static "user_" API key. There is no OAuth
// or device-code flow, so login parity is explicitly impossible; enrollment is
// performed through the plugin's Management Center page instead. This package
// therefore owns credential parsing, identity enrichment, and periodic refresh.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/host"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/identity"
	"github.com/markoonakic/cliproxyapi-commandcode-bridge/internal/quota"
)

// ProviderID is the stable provider key, shared with every other capability.
const ProviderID = identity.ProviderKey

// Credential is the persisted auth file shape.
//
// The first seven fields match the already-deployed file, so an existing
// credential parses unchanged. The identity fields are optional additions
// written when the account is enrolled or refreshed.
type Credential struct {
	Type         string          `json:"type"`
	APIKey       string          `json:"api_key"`
	Label        string          `json:"label,omitempty"`
	Plan         string          `json:"plan,omitempty"`
	Priority     int             `json:"priority,omitempty"`
	PriorityOver int             `json:"priority_override,omitempty"`
	Models       []CredentialMod `json:"models,omitempty"`
	UserID       string          `json:"user_id,omitempty"`
	Email        string          `json:"email,omitempty"`
	UserName     string          `json:"user_name,omitempty"`
	SubID        string          `json:"subscription_id,omitempty"`
}

// CredentialMod is one selected model, optionally exposing a client alias.
type CredentialMod struct {
	Name  string `json:"name"`
	Alias string `json:"alias,omitempty"`
}

// PlanPriorities maps a Command Code plan to its default routing priority.
// These presets match the published plan tiers.
var PlanPriorities = map[string]int{
	"go":          7,
	"goat":        6,
	"pro":         5,
	"team":        4,
	"team-pro":    4,
	"max-10x":     3,
	"max-20x":     2,
	"provider":    1,
	"unspecified": 0,
}

// NormalizePlan lowercases a plan and resolves its priority.
// Unknown plans resolve to priority 0, matching the reference behaviour.
func NormalizePlan(raw string) (string, int) {
	plan := strings.ToLower(strings.TrimSpace(raw))
	if plan == "" {
		return "unspecified", 0
	}
	if priority, ok := PlanPriorities[plan]; ok {
		return plan, priority
	}
	return plan, 0
}

// Fingerprint derives the auth file identity from the API key. It is a hash,
// never the key, so it is safe to persist in the file name.
func Fingerprint(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])[:12]
}

// FileName returns the auth file name for an API key.
func FileName(apiKey string) string {
	return identity.FileNamePrefix + "-" + Fingerprint(apiKey) + ".json"
}

// Validate checks the key shape. Command Code keys begin with "user_" and must
// carry a body after the prefix.
func Validate(apiKey string) error {
	trimmed := strings.TrimSpace(apiKey)
	if !strings.HasPrefix(trimmed, "user_") {
		return fmt.Errorf("Command Code API key must start with user_")
	}
	if len(strings.TrimPrefix(trimmed, "user_")) == 0 {
		return fmt.Errorf("Command Code API key is missing its body after user_")
	}
	return nil
}

// Parse decodes and validates a credential payload.
func Parse(raw []byte) (Credential, error) {
	if len(raw) == 0 {
		return Credential{}, fmt.Errorf("credential payload is empty")
	}
	var cred Credential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return Credential{}, fmt.Errorf("parse credential: %w", err)
	}
	if err := Validate(cred.APIKey); err != nil {
		return Credential{}, err
	}
	return cred, nil
}

// EffectivePriority returns the priority override when set, otherwise the plan preset.
func EffectivePriority(cred Credential) int {
	if cred.PriorityOver > 0 {
		return cred.PriorityOver
	}
	if cred.Priority > 0 {
		return cred.Priority
	}
	_, preset := NormalizePlan(cred.Plan)
	return preset
}

// DisplayLabel chooses the user-facing label for an account, preferring an
// explicit label, then the email, then the user name, then the fingerprint.
func DisplayLabel(cred Credential) string {
	for _, candidate := range []string{cred.Label, cred.Email, cred.UserName} {
		if trimmedValue := strings.TrimSpace(candidate); trimmedValue != "" {
			return trimmedValue
		}
	}
	return Fingerprint(cred.APIKey)
}

// Provider implements pluginapi.AuthProvider.
type Provider struct {
	client *quota.Client
}

// NewProvider builds an auth provider.
func NewProvider() *Provider {
	return &Provider{client: quota.NewClient("")}
}

// Identifier returns the provider key handled by this auth provider.
func (p *Provider) Identifier() string { return ProviderID }

// ParseAuth recognizes and enriches a credential file.
func (p *Provider) ParseAuth(_ context.Context, req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	cred, err := Parse(req.RawJSON)
	if err != nil {
		// Unrecognized material is not an error; the host tries other parsers.
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: ToAuthData(cred, req.FileName)}, nil
}

// StartLogin reports the honest boundary: Command Code has no OAuth flow, so
// enrollment happens on the plugin's Management Center page instead.
func (p *Provider) StartLogin(_ context.Context, _ pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	return pluginapi.AuthLoginStartResponse{}, fmt.Errorf(
		"Command Code uses static API keys and has no OAuth login; enroll the account on the Command Code Bridge page")
}

// PollLogin reports that no login flow exists to poll.
func (p *Provider) PollLogin(_ context.Context, _ pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	return pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusError,
		Message: "Command Code uses static API keys; use the Command Code Bridge enrollment page",
	}, nil
}

// RefreshAuth re-reads identity and plan from upstream so the panel stays
// accurate. It never writes or rotates a token: the API key is the credential.
func (p *Provider) RefreshAuth(ctx context.Context, req pluginapi.AuthRefreshRequest) (pluginapi.AuthRefreshResponse, error) {
	cred, err := Parse(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	ctx = host.WithCallbackID(ctx, host.CallbackFrom(ctx))

	updated := cred
	if identity, errWhoami := p.client.Whoami(ctx, cred.APIKey); errWhoami == nil {
		updated.UserID = identity.ID
		updated.Email = identity.Email
		updated.UserName = identity.Name
		if updated.Label == "" {
			updated.Label = DisplayLabel(updated)
		}
	}
	if sub, errSub := p.client.Subscription(ctx, cred.APIKey); errSub == nil {
		updated.SubID = sub.ID
		if plan, priority := NormalizePlan(sub.PlanID); plan != "unspecified" {
			updated.Plan = plan
			if updated.PriorityOver == 0 {
				updated.Priority = priority
			}
		}
	}

	return pluginapi.AuthRefreshResponse{
		Auth:             ToAuthData(updated, reqFileName(req)),
		NextRefreshAfter: time.Now().Add(24 * time.Hour),
	}, nil
}

// reqFileName resolves the auth file name for a refresh request, deriving it
// from the key when the host did not supply one.
func reqFileName(req pluginapi.AuthRefreshRequest) string {
	if name := strings.TrimSpace(req.AuthID); strings.HasSuffix(name, ".json") {
		return name
	}
	return ""
}

// ToAuthData projects a credential into the host's auth record.
//
// Identity travels in Metadata (mutable, host-managed) and routing priority in
// Attributes (immutable provider attributes), mirroring how the native channels
// expose email and plan.
func ToAuthData(cred Credential, fileName string) pluginapi.AuthData {
	if fileName == "" {
		fileName = FileName(cred.APIKey)
	}
	raw, _ := json.Marshal(cred)

	priority := EffectivePriority(cred)
	metadata := map[string]any{
		"email":        cred.Email,
		"account_type": cred.Plan,
		"user_id":      cred.UserID,
		"user_name":    cred.UserName,
	}
	attributes := map[string]string{
		"priority": fmt.Sprintf("%d", priority),
	}
	if cred.Email != "" {
		attributes["email"] = cred.Email
	}

	return pluginapi.AuthData{
		Provider:    ProviderID,
		ID:          Fingerprint(cred.APIKey),
		FileName:    fileName,
		Label:       DisplayLabel(cred),
		StorageJSON: raw,
		Metadata:    metadata,
		Attributes:  attributes,
	}
}

// ClientModelID returns the id clients use for a selected model: the alias when
// one is set, otherwise the upstream model name.
//
// The host registers this id, so it is the id a client must request. The
// upstream name is preserved separately so execution still addresses the right
// model.
func ClientModelID(selected CredentialMod) string {
	if alias := strings.TrimSpace(selected.Alias); alias != "" {
		return alias
	}
	return strings.TrimSpace(selected.Name)
}

// ResolveUpstreamModel maps a client-visible model id back to the upstream
// model name for a credential.
//
// It accepts either an alias or a plain upstream name, because a credential may
// expose some models with aliases and others without. An id that matches
// nothing is returned unchanged, so execution stays permissive rather than
// rejecting a model the account may still be entitled to use.
func ResolveUpstreamModel(candidate string, cred Credential) string {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return ""
	}
	for _, selected := range cred.Models {
		name := strings.TrimSpace(selected.Name)
		if name == "" {
			continue
		}
		if alias := strings.TrimSpace(selected.Alias); alias != "" && alias == trimmed {
			return name
		}
		if name == trimmed {
			return trimmed
		}
	}
	return trimmed
}
